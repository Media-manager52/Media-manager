package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestReconfirmTimedOutPlexOnlyRequeuesTimedOutWorkflows proves the owner's
// bulk re-check queues exact-path confirmation for timed-out publications,
// leaves every other workflow untouched, and is idempotent.
func TestReconfirmTimedOutPlexOnlyRequeuesTimedOutWorkflows(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	timedOut, _, _, _ := publishTimedOutMovie(t, fixture, "Bulk Reconfirm Movie", "770001", "bulk-reconfirm")
	_, untouched := fixture.movieWorkflow(t, "Bulk Untouched Movie", "770002")

	result, err := fixture.store.ReconfirmTimedOutPlex(fixture.ctx, "owner", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Queued) != 1 || result.Queued[0] != timedOut || len(result.Skipped) != 0 {
		t.Fatalf("result=%+v, want only %s queued", result, timedOut)
	}
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, timedOut.String()).Scan(&state); err != nil || state != "plex_scanning" {
		t.Fatalf("timed-out workflow state=%q err=%v, want plex_scanning", state, err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, untouched.String()).Scan(&state); err != nil || state == "plex_scanning" {
		t.Fatalf("unrelated workflow state=%q err=%v, want unchanged", state, err)
	}

	again, err := fixture.store.ReconfirmTimedOutPlex(fixture.ctx, "owner", time.Now().UTC())
	if err != nil || len(again.Queued) != 0 || len(again.Skipped) != 0 {
		t.Fatalf("second pass=%+v err=%v, want nothing to do", again, err)
	}
}

// TestReconfirmTimedOutPlexReachesRetryableWorkflowsPastExhaustedOnes proves
// that workflows which can no longer be retried (here: at the retry limit)
// do not use up a pass, so a newer retryable workflow is still re-queued.
func TestReconfirmTimedOutPlexReachesRetryableWorkflowsPastExhaustedOnes(t *testing.T) {
	previous := maxBulkPlexReconfirmations
	maxBulkPlexReconfirmations = 1
	t.Cleanup(func() { maxBulkPlexReconfirmations = previous })

	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	exhausted, _, _, _ := publishTimedOutMovie(t, fixture, "Exhausted Reconfirm Movie", "770011", "bulk-exhausted")
	retryable, _, _, _ := publishTimedOutMovie(t, fixture, "Retryable Reconfirm Movie", "770012", "bulk-retryable")
	// The exhausted workflow is the oldest and has already used every retry.
	for retry := 1; retry <= maxPlexReconfirmations; retry++ {
		scan, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, exhausted, "owner", time.Now().UTC())
		if err != nil {
			t.Fatalf("exhaust retry %d: %v", retry, err)
		}
		timeOutScan(t, fixture, scan)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE workflows SET updated_at=now()-interval '1 day' WHERE id=$1`, exhausted.String()); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.store.ReconfirmTimedOutPlex(fixture.ctx, "owner", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Queued) != 1 || result.Queued[0] != retryable {
		t.Fatalf("result=%+v, want the retryable workflow %s queued", result, retryable)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].WorkflowID != exhausted || result.Skipped[0].Reason != ErrPlexReconfirmationLimit.Error() {
		t.Fatalf("skipped=%+v, want the exhausted workflow with the retry-limit reason", result.Skipped)
	}
}

// TestPlexReconfirmSkippableOnlyCoversOwnerExplainableReasons proves real
// failures are never reported as a quietly skipped workflow.
func TestPlexReconfirmSkippableOnlyCoversOwnerExplainableReasons(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrPlexReconfirmationNotTimedOut, ErrPlexReconfirmationUnverified, ErrPlexReconfirmationLimit, ErrPlexReconfirmationNoLibrary, ErrConcurrentModification} {
		if !plexReconfirmSkippable(err) {
			t.Fatalf("%v should be a skip reason", err)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("connection reset")} {
		if plexReconfirmSkippable(err) {
			t.Fatalf("%v must fail the pass, not be skipped", err)
		}
	}
}
