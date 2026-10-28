package acquisition

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

// A second provider poll can carry a fresh fetch timestamp while preserving
// every semantic fact. The evaluator deliberately adds timestamped inferred
// facts, so persistence must compare the facts without those volatile times.
func TestFreshObservationTimestampOnlyReusesStoredEvidence(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	store, workflow, indexer := heldRescoreFixture(t, now)
	search := transitionHeldRescore(t, store, workflow, now)
	raw := releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    indexer.ID,
		Operation:    "test",
		NativeGUID:   "fresh-timestamp-stable",
		Title:        "Example Series S2026 WEB-DL",
		Facts:        []releases.Fact{{Name: "tvdb_id", Values: []string{"378992"}, State: releases.FactObserved}},
		BodyHash:     "fresh-timestamp-stable",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	first, firstEvaluation := evaluateHeldRescore(t, search, raw, now)
	workflow = persistHeldRescore(t, store, search, first)
	var firstObservationID string
	if err := store.Pool.QueryRow(context.Background(), `SELECT observation_id::text FROM release_candidates WHERE workflow_id=$1 AND native_key=$2`, workflow.ID.String(), raw.NativeGUID).Scan(&firstObservationID); err != nil {
		t.Fatal(err)
	}
	if firstObservationID != first.ID.String() {
		t.Fatalf("first observation=%s, want %s", firstObservationID, first.ID)
	}
	if firstEvaluation.Verdict != releases.VerdictHeld {
		t.Fatalf("first verdict=%s, want held", firstEvaluation.Verdict)
	}

	search = transitionHeldRescore(t, store, workflow, now.Add(time.Minute))
	second, _ := evaluateHeldRescore(t, search, raw, now.Add(time.Minute))
	workflow = persistHeldRescore(t, store, search, second)
	var observations, candidates int
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2),
		(SELECT count(*) FROM release_candidates WHERE workflow_id=$3 AND native_key=$2)`, indexer.ID.String(), raw.NativeGUID, workflow.ID.String()).Scan(&observations, &candidates); err != nil {
		t.Fatal(err)
	}
	if observations != 1 || candidates != 1 {
		t.Fatalf("timestamp-only poll observations=%d candidates=%d, want 1 and 1", observations, candidates)
	}
	var candidateObservationID string
	if err := store.Pool.QueryRow(context.Background(), `SELECT observation_id::text FROM release_candidates WHERE workflow_id=$1 AND native_key=$2`, workflow.ID.String(), raw.NativeGUID).Scan(&candidateObservationID); err != nil {
		t.Fatal(err)
	}
	if candidateObservationID != firstObservationID {
		t.Fatalf("timestamp-only poll changed candidate observation from %s to %s", firstObservationID, candidateObservationID)
	}
}
