package acquisition

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestEmptySearchesBackOff proves repeated searches that select nothing are
// paced with a growing delay instead of re-querying every indexer every 30
// minutes.
func TestEmptySearchesBackOff(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, func(url.Values) string { return emptySearchXML() })
	defer server.Close()
	configureMovieSearchIndexer(t, store, server.URL, true)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)

	nextSearch := func() (uint64, time.Duration) {
		t.Helper()
		var revision int64
		var availableAt time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT aggregate_revision,available_at FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL ORDER BY occurred_at DESC LIMIT 1`, workflow.ID.String()).Scan(&revision, &availableAt); err != nil {
			t.Fatal(err)
		}
		return uint64(revision), time.Until(availableAt)
	}
	run := func(revision uint64) {
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
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}

	run(workflow.Revision)
	revision, delay := nextSearch()
	if delay < 29*time.Minute || delay > 31*time.Minute {
		t.Fatalf("first empty search retry in %s, want 30m", delay)
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateWaiting {
		t.Fatalf("workflow=%+v err=%v", current, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	run(revision)
	if _, delay = nextSearch(); delay < 59*time.Minute || delay > 61*time.Minute {
		t.Fatalf("second empty search retry in %s, want 1h", delay)
	}
	if requests := captured(); len(requests) != 2 {
		t.Fatalf("provider requests=%d, want 2", len(requests))
	}
}
