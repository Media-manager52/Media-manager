package acquisition

import (
	"context"
	"fmt"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/postgres"
)

// statedFeedXML is feedXML with the indexer's own usage counters, as the
// measured indexers state them in every answer.
func statedFeedXML(used int, titles ...string) string {
	items := ""
	for _, title := range titles {
		items += fmt.Sprintf(`<item><title>%s</title><guid>feed-%s</guid><newznab:attr name="resolution" value="1920x1080"/></item>`, title, title)
	}
	return fmt.Sprintf(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><newznab:apilimits apicurrent="%d" apimax="1000" grabmax="30" apioldesttime="Sat, 14 Nov 2026 07:04:29 +0000" />`, used) + items + `</channel></rss>`
}

// TestSplitFeedReadsMoviesAndSeriesWithTheirOwnProgress reads one indexer's
// newest uploads as a movie and a series request. Each request keeps its own
// progress, known uploads and gap count; both draw on the indexer's one
// pacing lane and query allowance; the single-request state is left as it
// was. It takes about a minute: the gap part sends ten paced requests.
func TestSplitFeedReadsMoviesAndSeriesWithTheirOwnProgress(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	var flood atomic.Bool
	var answers atomic.Int32
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		used := int(answers.Add(1))
		switch query.Get("t") {
		case "movie":
			return statedFeedXML(used, "New.Movie.2019.1080p.WEB-DL", "Known.Movie.2019.1080p.WEB-DL")
		case "tvsearch":
			if flood.Load() {
				// Every page holds only uploads never seen before.
				offset := query.Get("offset")
				return statedFeedXML(used, "Flood.Show.S01E01.p"+offset+".1080p.WEB-DL", "Flood.Show.S01E02.p"+offset+".1080p.WEB-DL")
			}
			return statedFeedXML(used, "New.Show.S01E02.1080p.WEB-DL", "Known.Show.S01E01.1080p.WEB-DL")
		}
		t.Errorf("unexpected request %v", query)
		return emptySearchXML()
	})
	defer server.Close()
	indexer := configureMovieSearchIndexer(t, store, server.URL)
	// What the single request saw last, before the indexer was read as two.
	past := time.Now().UTC().Add(-time.Minute)
	if _, err := store.RecordIndexerFeedRead(ctx, postgres.IndexerFeedState{IndexerInstanceID: indexer.ID, LastReadAt: &past, NextReadAt: &past, IntervalSeconds: 600, LastOutcome: "ok",
		RecentGUIDs: []string{"feed-Known.Movie.2019.1080p.WEB-DL", "feed-Known.Show.S01E01.1080p.WEB-DL"}, ReadsTotal: 7, LastReadComplete: true}, nil, past); err != nil {
		t.Fatal(err)
	}
	watcher := &FeedWatcher{Search: movieSearchService(store, server), Streams: func(string) []newznab.FeedStream {
		return newznab.FeedStreams("https://abnzb.com/api")
	}}
	streamState := func(stream string) postgres.IndexerFeedState {
		t.Helper()
		states, err := store.IndexerFeedStreamStates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state, found := states[postgres.FeedStreamKey{IndexerInstanceID: indexer.ID, Stream: stream}]
		if !found {
			t.Fatalf("no reader state for the %s stream", stream)
		}
		return state
	}

	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	requests := captured()
	if len(requests) != 2 {
		t.Fatalf("the first pass sent %d requests, want one per stream: %v", len(requests), requests)
	}
	for index, want := range []struct{ mode, categories string }{{"movie", "2000"}, {"tvsearch", "5000"}} {
		request := requests[index]
		if request.Get("t") != want.mode || request.Get("cat") != want.categories || request.Has("q") || request.Get("extended") != "1" || request.Get("limit") != "100" {
			t.Fatalf("request %d=%v, want t=%s cat=%s", index, request, want.mode, want.categories)
		}
	}
	// Each stream went on from the uploads the single request had seen: only
	// the upload that is new counts as new, and one page was enough.
	for _, stream := range []string{"movie", "series"} {
		if state := streamState(stream); state.LastOutcome != "ok" || state.LastItemCount != 2 || state.LastNewCount != 1 || state.ReadsTotal != 1 || state.GapReadsTotal != 0 || !state.LastReadComplete || state.NextReadAt == nil {
			t.Fatalf("%s stream state=%+v", stream, state)
		}
	}
	// The single-request state is untouched, so going back to one request
	// continues from it.
	states, err := store.IndexerFeedStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if single := states[indexer.ID]; single.ReadsTotal != 7 || len(single.RecentGUIDs) != 2 || single.Stream != "" {
		t.Fatalf("single-request state changed: %+v", single)
	}
	// The owner's view shows the two streams as one reader.
	summaries, err := store.IndexerFeedSummaries(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if summary := summaries[indexer.ID]; summary.Reads != 2 || summary.GapReads != 0 || summary.LastOutcome != "ok" {
		t.Fatalf("summary=%+v", summary)
	}
	// Both requests were charged to the indexer's one query allowance, and
	// the counters the indexer stated in its answers were kept.
	var reserved, lanes int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family='query'),
		(SELECT count(*) FROM indexer_budget_state WHERE indexer_instance_id=$1)`, indexer.ID.String()).Scan(&reserved, &lanes); err != nil {
		t.Fatal(err)
	}
	if reserved != 2 || lanes != 1 {
		t.Fatalf("reservations=%d pacing rows=%d, want 2 and 1", reserved, lanes)
	}
	if stated, found, err := store.LoadProviderLimits(ctx, indexer.ID.String()); err != nil || !found || stated.QueryUsed != 2 || stated.QueryMax != 1000 || stated.GrabUsed != -1 || stated.GrabMax != 30 {
		t.Fatalf("stated limits=%+v found=%v err=%v", stated, found, err)
	}

	// Not due: nothing is sent.
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := len(captured()); sent != 2 {
		t.Fatalf("a pass before its time sent %d request(s)", sent-2)
	}

	// Only the series stream is due, and it never reaches an upload seen
	// before: its gap is counted, the movie stream's is not, and the movie
	// stream's schedule does not move.
	movieBefore := streamState("movie")
	flood.Store(true)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_feed_stream_state SET next_read_at=now()-interval '1 second' WHERE stream='series'`); err != nil {
		t.Fatal(err)
	}
	if err := watcher.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	later := captured()[2:]
	if len(later) != feedMaxPages {
		t.Fatalf("the series catch-up sent %d requests, want %d", len(later), feedMaxPages)
	}
	for _, request := range later {
		if request.Get("t") != "tvsearch" {
			t.Fatalf("the movie stream was read before its time: %v", request)
		}
	}
	series, movie := streamState("series"), streamState("movie")
	if series.GapReadsTotal != 1 || series.LastReadComplete || series.ReadsTotal != 2 || series.LastNewCount != 2*feedMaxPages {
		t.Fatalf("series stream after the flood=%+v", series)
	}
	if movie.GapReadsTotal != 0 || !movie.LastReadComplete || movie.ReadsTotal != 1 || !movie.NextReadAt.Equal(*movieBefore.NextReadAt) {
		t.Fatalf("movie stream changed with the series gap: %+v", movie)
	}
	if summaries, err = store.IndexerFeedSummaries(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if summary := summaries[indexer.ID]; summary.Reads != 3 || summary.GapReads != 1 {
		t.Fatalf("summary after the gap=%+v", summary)
	}
}
