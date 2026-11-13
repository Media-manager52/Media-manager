package acquisition

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// TestFeedReaderGoesOnAfterARestart covers a restart: a new reader (nothing
// in memory) continues from the upload IDs the last read saved, so only the
// uploads since then are new, and it counts its reads.
func TestFeedReaderGoesOnAfterARestart(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	var later atomic.Bool
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("q") != "" {
			return emptySearchXML()
		}
		if later.Load() {
			return feedXML("Newer.Upload.2019.1080p.WEB-DL", "First.Upload.2019.1080p.WEB-DL", "Second.Upload.2019.1080p.WEB-DL")
		}
		return feedXML("First.Upload.2019.1080p.WEB-DL", "Second.Upload.2019.1080p.WEB-DL")
	})
	defer server.Close()
	indexer := configureMovieSearchIndexer(t, store, server.URL)
	if err := (&FeedWatcher{Search: movieSearchService(store, server)}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	states, err := store.IndexerFeedStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state := states[indexer.ID]; len(state.RecentGUIDs) != 2 || state.ReadsTotal != 1 || state.GapReadsTotal != 0 || !state.LastReadComplete {
		t.Fatalf("first read state=%+v", state)
	}

	// A restart: a new reader with nothing in memory.
	later.Store(true)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_feed_state SET next_read_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	before := len(captured())
	if err := (&FeedWatcher{Search: movieSearchService(store, server)}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if requests := len(captured()) - before; requests != 1 {
		t.Fatalf("read after the restart sent %d requests, want 1", requests)
	}
	states, err = store.IndexerFeedStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state := states[indexer.ID]; state.LastNewCount != 1 || state.LastItemCount != 3 || len(state.RecentGUIDs) != 3 || state.ReadsTotal != 2 || state.GapReadsTotal != 0 {
		t.Fatalf("read after the restart state=%+v", state)
	}
	summaries, err := store.IndexerFeedSummaries(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if summary := summaries[indexer.ID]; summary.Reads != 2 || summary.GapReads != 0 {
		t.Fatalf("summary=%+v", summary)
	}
}

// TestFeedWatcherBringsANewEpisodesFirstSearchForward covers a workflow that
// waits for its first scheduled search (monitored): a fitting upload brings
// that search forward like a waiting title's.
func TestFeedWatcherBringsANewEpisodesFirstSearchForward(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, _ := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("q") == "" {
			return feedXML("Klaus.2019.1080p.WEB-DL")
		}
		return emptySearchXML()
	})
	defer server.Close()
	configureMovieSearchIndexer(t, store, server.URL)
	workflow := createMovieSearchWorkflow(t, store)
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='monitored' WHERE id=$1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=now()+interval '2 hours' WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := (&FeedWatcher{Search: movieSearchService(store, server)}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var outcome string
	var due bool
	if err := store.Pool.QueryRow(ctx, `SELECT hit.outcome,(SELECT available_at<=now() FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL)
		FROM indexer_feed_matches hit WHERE hit.workflow_id=$1`, workflow.ID.String()).Scan(&outcome, &due); err != nil {
		t.Fatal(err)
	}
	if outcome != "search_advanced" || !due {
		t.Fatalf("match outcome=%q search due=%v", outcome, due)
	}
}
