package platform

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

// A long idle poll must never delay already-ready work, including work after
// a paced deferral. Use real claims and acknowledgements to cover the Run loop.
func TestWorkerDrainsReadyWorkWithoutWaitingForIdlePoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := newWorkerTestStore(t, ctx)
	for _, kind := range []string{"workflow.transitioned", "workflow.search_requested", "workflow.search_requested"} {
		_, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,$3,'{}',now(),now())`, uuid.NewV7().String(), uuid.NewV7().String(), kind)
		if err != nil {
			t.Fatal(err)
		}
	}
	retryAt := time.Now().Add(time.Hour)
	handled := make(chan string, 3)
	searches := 0
	worker := Worker{Store: store, Owner: "drain", PollInterval: time.Hour,
		Handlers: map[string]Handler{
			"workflow.transitioned": func(context.Context, postgres.OutboxEvent) error {
				handled <- "transition"
				return nil
			},
			"workflow.search_requested": func(context.Context, postgres.OutboxEvent) error {
				searches++
				handled <- "search"
				if searches == 1 {
					return deferredTestError{at: retryAt}
				}
				return nil
			},
		}}
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	for range 3 {
		select {
		case <-handled:
		case <-ctx.Done():
			t.Fatal("ready work waited for the idle polling interval")
		}
	}
	// The deferred request remains scheduled; it is never executed early.
	// Wait for the acknowledgement before stopping the worker.
	for {
		var completed, deferred int
		err := store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE completed_at IS NOT NULL),count(*) FILTER (WHERE completed_at IS NULL AND available_at=$1 AND lease_owner IS NULL AND attempts=0) FROM outbox_events`, retryAt.UTC().Truncate(time.Microsecond)).Scan(&completed, &deferred)
		if err != nil {
			t.Fatal(err)
		}
		if completed == 2 && deferred == 1 {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("acknowledgements missing: completed=%d deferred=%d", completed, deferred)
		}
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("idle cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("idle polling did not respond to cancellation")
	}
}
