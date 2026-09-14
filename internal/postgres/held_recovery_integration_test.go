package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestHeldSearchSchedulesPacedRetry guards the owner-gated acquisition recovery
// regression: a search whose best candidates are held (no eligible release, but
// observed releases that fail required evidence) must not park the workflow in
// a dead-end. It records a truthful reason and schedules the same paced retry
// the waiting state uses, so the search worker can resume from held.
func TestHeldSearchSchedulesPacedRetry(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Held Recovery", "900777")
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test held recovery", "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	observationID := uuid.NewV7()
	held := EvaluatedObservation{
		Observation: releases.Observation{ID: observationID, IndexerID: fixture.indexer.ID, Operation: "test", NativeGUID: "held-recovery-guid", Title: "Held.Recovery.2026.1080p.WEB-DL", Fields: []releases.RawField{}, Facts: []releases.Fact{}, BodyHash: "held-recovery", ObservedAt: time.Now().UTC(), ParseVersion: "test", Problems: []string{}},
		Evaluation:  releases.Evaluation{ObservationID: observationID, Verdict: releases.VerdictHeld, Score: 0, Explanations: []releases.Explanation{}},
	}
	if _, err := fixture.store.PersistSearch(fixture.ctx, search, []byte("held-recovery"), []EvaluatedObservation{held}, SearchPersistenceOptions{IndexerOutcomes: []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "succeeded"}}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var state, attentionDetail string
	var revision int64
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,COALESCE(attention_detail,''),revision FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state, &attentionDetail, &revision); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateHeld) {
		t.Fatalf("workflow state=%q, want held", state)
	}
	if attentionDetail == "" {
		t.Fatalf("held workflow has no owner-visible reason")
	}

	var eventType string
	var aggregateRevision int64
	var availableAt time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT event_type,aggregate_revision,available_at FROM outbox_events
		WHERE aggregate_id=$1 AND aggregate_type='workflow' AND event_type='workflow.search_requested' AND completed_at IS NULL
		ORDER BY occurred_at DESC LIMIT 1`, workflow.ID.String()).Scan(&eventType, &aggregateRevision, &availableAt); err != nil {
		t.Fatalf("held workflow scheduled no retry: %v", err)
	}
	if aggregateRevision != revision {
		t.Fatalf("retry aggregate revision=%d, want %d", aggregateRevision, revision)
	}
	if !availableAt.After(time.Now().UTC()) {
		t.Fatalf("retry available_at=%s is not in the future", availableAt)
	}
}
