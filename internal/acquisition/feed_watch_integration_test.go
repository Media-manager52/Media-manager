package acquisition

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func feedXML(titles ...string) string {
	items := ""
	for _, title := range titles {
		// The provider's id belongs to the upload, not to its place in the list.
		items += fmt.Sprintf(`<item><title>%s</title><guid>feed-%s</guid><newznab:attr name="resolution" value="1920x1080"/></item>`, title, title)
	}
	return `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>` + items + `</channel></rss>`
}

// TestFeedWatcherBringsAWaitingTitlesSearchForward runs the whole chain: a
// title was searched without a result and waits; its release then appears in
// the indexer's newest uploads; the watcher brings the title's own search
// forward; that search asks the indexer as always and selects the release
// under the profile's rules. The watcher itself selects nothing.
func TestFeedWatcherBringsAWaitingTitlesSearchForward(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	var uploaded atomic.Bool
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		switch {
		case query.Get("q") == "" && uploaded.Load():
			return feedXML("Klaus.2019.1080p.WEB-DL", "Something.Else.2019.1080p.WEB-DL")
		case query.Get("q") == "":
			return feedXML("Something.Else.2019.1080p.WEB-DL")
		case uploaded.Load():
			return movieSearchReleaseXML("Klaus.2019.1080p.WEB-DL")
		}
		return emptySearchXML()
	})
	defer server.Close()
	indexer := configureMovieSearchIndexer(t, store, server.URL)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)
	watcher := &FeedWatcher{Search: service}

	handle := func(revision uint64) {
		t.Helper()
		event := postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: revision}
		err := service.Handle(ctx, event)
		var deferred interface{ RetryAt() time.Time }
		if errors.As(err, &deferred) {
			time.Sleep(time.Until(deferred.RetryAt()) + 50*time.Millisecond)
			err = service.Handle(ctx, event)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	nextSearch := func() (uint64, time.Time) {
		t.Helper()
		var revision int64
		var availableAt time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT aggregate_revision,available_at FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL ORDER BY occurred_at DESC LIMIT 1`, workflow.ID.String()).Scan(&revision, &availableAt); err != nil {
			t.Fatal(err)
		}
		return uint64(revision), availableAt
	}
	matches := func(outcome string) int {
		t.Helper()
		var count int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_feed_matches WHERE workflow_id=$1 AND outcome=$2`, workflow.ID.String(), outcome).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	// The first search finds nothing; the next one is half an hour away.
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	handle(workflow.Revision)
	revision, availableAt := nextSearch()
	if time.Until(availableAt) < 29*time.Minute {
		t.Fatalf("next search in %s, want about 30m", time.Until(availableAt))
	}

	// Nothing for the title among the newest uploads: nothing changes.
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	requests := captured()
	feed := requests[len(requests)-1]
	if feed.Get("t") != "search" || feed.Has("q") || feed.Get("cat") != "2000,5000" || feed.Get("extended") != "1" || feed.Get("limit") != "100" || feed.Has("offset") && feed.Get("offset") != "0" {
		t.Fatalf("newest-uploads request=%v", feed)
	}
	if _, at := nextSearch(); !at.Equal(availableAt) || matches("pending")+matches("search_advanced") != 0 {
		t.Fatalf("an unrelated upload moved the search to %s", at)
	}
	states, err := store.IndexerFeedStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := states[indexer.ID]
	if state.LastOutcome != "ok" || state.NextReadAt == nil || time.Until(*state.NextReadAt) < 9*time.Minute || state.LastItemCount != 1 {
		t.Fatalf("reader state=%+v", state)
	}
	// Not due yet: the next pass sends no request.
	before := len(captured())
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if after := len(captured()); after != before {
		t.Fatalf("a read before its time sent %d request(s)", after-before)
	}

	// The release is uploaded. The title was searched moments ago, so the
	// match waits out the quiet period first.
	uploaded.Store(true)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_feed_state SET next_read_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if matches("pending") != 1 {
		t.Fatalf("pending matches=%d, want 1", matches("pending"))
	}
	if _, at := nextSearch(); !at.Equal(availableAt) {
		t.Fatalf("the search was brought forward inside the quiet period: %s", at)
	}

	// After the quiet period the title's own search is due at once.
	if _, err := store.Pool.Exec(ctx, `UPDATE search_runs SET started_at=started_at-interval '2 hours',completed_at=completed_at-interval '2 hours' WHERE workflow_id=$1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	before = len(captured())
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if after := len(captured()); after != before {
		t.Fatalf("bringing a search forward sent %d provider request(s)", after-before)
	}
	advancedRevision, at := nextSearch()
	if advancedRevision != revision || at.After(time.Now()) || matches("search_advanced") != 1 {
		t.Fatalf("search revision=%d (want %d) available_at=%s advanced=%d", advancedRevision, revision, at, matches("search_advanced"))
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateWaiting || current.SelectedRelease != nil {
		t.Fatalf("the watcher changed the workflow: %+v err=%v", current, err)
	}

	// The search that was brought forward selects the release itself.
	handle(revision)
	current, err = store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateCandidateSelected || current.SelectedRelease == nil {
		t.Fatalf("after the search workflow=%+v err=%v", current, err)
	}
}

// TestFeedWatcherBacksOffWhenAnIndexerCannotListItsNewestUploads proves a
// provider without a usable list is asked rarely, and that searching on that
// indexer goes on.
func TestFeedWatcherBacksOffWhenAnIndexerCannotListItsNewestUploads(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("q") == "" {
			return ""
		}
		return emptySearchXML()
	})
	defer server.Close()
	indexer := configureMovieSearchIndexer(t, store, server.URL)
	createMovieSearchWorkflow(t, store)
	watcher := &FeedWatcher{Search: movieSearchService(store, server)}

	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	states, err := store.IndexerFeedStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := states[indexer.ID]
	if state.ConsecutiveFailures != 1 || state.LastOutcome == "ok" || state.LastOutcome == "" || state.NextReadAt == nil || time.Until(*state.NextReadAt) < 59*time.Minute {
		t.Fatalf("reader state after a failed read=%+v", state)
	}
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if requests := captured(); len(requests) != 1 {
		t.Fatalf("a failing list was asked %d times, want 1", len(requests))
	}
}
