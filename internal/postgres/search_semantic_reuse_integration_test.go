package postgres

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

func persistSemanticTestObservation(t *testing.T, fixture *failureRecoveryFixture, current workflows.Workflow, observation releases.Observation, now time.Time) workflows.Workflow {
	t.Helper()
	if current.State != workflows.StateSearching {
		next, err := fixture.store.TransitionWorkflow(fixture.ctx, current.ID, current.Revision, workflows.StateSearching, "test semantic reuse", "test", now)
		if err != nil {
			t.Fatalf("transition to searching: %v", err)
		}
		current = next
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, current.ID)
	if err != nil {
		t.Fatalf("load search context: %v", err)
	}
	persisted, err := fixture.store.PersistSearch(fixture.ctx, search, []byte("semantic-reuse"), []EvaluatedObservation{{
		Observation: observation,
		Evaluation: releases.Evaluation{
			ObservationID: observation.ID,
			Verdict:       releases.VerdictHeld,
			Explanations:  []releases.Explanation{},
		},
	}}, SearchPersistenceOptions{IndexerOutcomes: []SearchIndexerOutcome{
		{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"},
		{IndexerInstanceID: fixture.indexerTwo.ID, State: "succeeded"},
	}}, now)
	if err != nil {
		t.Fatalf("persist semantic observation: %v", err)
	}
	if persisted.State != workflows.StateHeld {
		t.Fatalf("workflow state=%q, want held", persisted.State)
	}
	return persisted
}

func semanticTestObservation(now time.Time) releases.Observation {
	return releases.Observation{
		ID:         uuid.NewV7(),
		IndexerID:  uuid.NewV7(), // replaced by the fixture's indexer
		Operation:  "test",
		NativeGUID: "semantic-reuse-guid",
		Title:      "Semantic.Reuse.2026.1080p.WEB-DL",
		Fields: []releases.RawField{
			{Name: "size", Value: "1000", Ordinal: 0},
			{Name: "pubdate", Value: "2026-01-01T00:00:00Z", Ordinal: 1},
		},
		Facts: []releases.Fact{{
			Name:       "resolution",
			Values:     []string{"1080p"},
			State:      releases.FactObserved,
			EvidenceAt: []int{0},
			ObservedAt: now,
		}},
		BodyHash:     "semantic-reuse-body",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
}

func semanticTestObservationWithIndexer(now time.Time, indexerID uuid.UUID) releases.Observation {
	observation := semanticTestObservation(now)
	observation.IndexerID = indexerID
	return observation
}

func semanticTestQueryCandidate(t *testing.T, fixture *failureRecoveryFixture, workflowID uuid.UUID) (candidateID uuid.UUID, observationID uuid.UUID) {
	t.Helper()
	var candidateText, observationText string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT candidate.id::text,candidate.observation_id::text
		FROM release_candidates candidate
		WHERE candidate.workflow_id=$1 AND candidate.native_key=$2`, workflowID.String(), "semantic-reuse-guid").Scan(&candidateText, &observationText); err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	candidateID, err := uuid.Parse(candidateText)
	if err != nil {
		t.Fatal(err)
	}
	observationID, err = uuid.Parse(observationText)
	if err != nil {
		t.Fatal(err)
	}
	return candidateID, observationID
}

// A repeated provider poll with the same semantic evidence must reuse the
// immutable observation snapshot. Only the volatile observation/fact
// timestamps and the transient provider response body hash differ.
func TestPersistSearchReusesObservationWhenOnlyTimestampsChange(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Semantic Reuse Timestamp", "901201")
	firstAt := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(5 * time.Minute)

	first := semanticTestObservationWithIndexer(firstAt, fixture.indexer.ID)
	workflow = persistSemanticTestObservation(t, fixture, workflow, first, firstAt)
	candidateID, candidateObservationID := semanticTestQueryCandidate(t, fixture, workflow.ID)
	if candidateObservationID != first.ID {
		t.Fatalf("initial candidate observation=%s, want %s", candidateObservationID, first.ID)
	}

	second := first
	second.ID = uuid.NewV7()
	second.ObservedAt = secondAt
	second.BodyHash = "different-provider-body-hash"
	second.Facts = append([]releases.Fact(nil), first.Facts...)
	second.Facts[0].ObservedAt = secondAt
	workflow = persistSemanticTestObservation(t, fixture, workflow, second, secondAt)

	if got := fixture.count(t, `SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2`, fixture.indexer.ID.String(), first.NativeGUID); got != 1 {
		t.Fatalf("unchanged semantic observation versions=%d, want 1", got)
	}
	_, candidateObservationID = semanticTestQueryCandidate(t, fixture, workflow.ID)
	if candidateObservationID != first.ID {
		t.Fatalf("reused candidate observation=%s, want %s", candidateObservationID, first.ID)
	}

	var lastObserved time.Time
	var observationCount int64
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT last_observed_at,observation_count FROM release_candidates WHERE id=$1`, candidateID.String()).Scan(&lastObserved, &observationCount); err != nil {
		t.Fatalf("load freshness: %v", err)
	}
	if !lastObserved.Equal(secondAt) {
		t.Fatalf("candidate last_observed_at=%s, want %s", lastObserved, secondAt)
	}
	if observationCount != 2 {
		t.Fatalf("candidate observation_count=%d, want 2 sightings", observationCount)
	}
	var storedObservedAt time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT observed_at FROM release_observations WHERE id=$1`, first.ID.String()).Scan(&storedObservedAt); err != nil {
		t.Fatal(err)
	}
	if !storedObservedAt.Equal(firstAt) {
		t.Fatalf("reused observation observed_at=%s, want original evidence time %s", storedObservedAt, firstAt)
	}
	var attempts, jobs int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&attempts, &jobs); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || jobs != 0 {
		t.Fatalf("semantic reuse consumed acquisition work: attempts=%d jobs=%d", attempts, jobs)
	}
}

// Any semantic evidence change must still create exactly one immutable
// version, preserve the prior row, and update the candidate to the new row.
func TestPersistSearchCreatesVersionForMeaningfulEvidenceChange(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*releases.Observation, time.Time)
	}{
		{
			name: "fact value",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Facts[0].Values = []string{"720p"}
			},
		},
		{
			name: "fact state",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Facts[0].State = releases.FactInferred
			},
		},
		{
			name: "evidence index",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Facts[0].EvidenceAt = []int{1}
			},
		},
		{
			name: "raw provider field",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Fields[0].Value = "2000"
			},
		},
		{
			name: "provider publication timestamp",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Fields[1].Value = "2026-01-02T00:00:00Z"
			},
		},
		{
			name: "parsing version",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.ParseVersion = "test-v2"
			},
		},
		{
			name: "problems and classification",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Problems = []string{"provider classification changed"}
			},
		},
		{
			name: "operation",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Operation = "details"
			},
		},
		{
			name: "title",
			mutate: func(observation *releases.Observation, _ time.Time) {
				observation.Title = "Semantic.Reuse.2026.1080p.REPACK.WEB-DL"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFailureRecoveryFixture(t)
			workflow := fixture.newMovieWorkflow(t, "Semantic Reuse Change", "901202")
			firstAt := time.Date(2026, 10, 28, 13, 0, 0, 0, time.UTC)
			secondAt := firstAt.Add(5 * time.Minute)

			first := semanticTestObservationWithIndexer(firstAt, fixture.indexer.ID)
			workflow = persistSemanticTestObservation(t, fixture, workflow, first, firstAt)
			candidateID, firstObservationID := semanticTestQueryCandidate(t, fixture, workflow.ID)

			second := first
			second.ID = uuid.NewV7()
			second.ObservedAt = secondAt
			second.BodyHash = "different-provider-body-hash"
			second.Fields = append([]releases.RawField(nil), first.Fields...)
			second.Facts = append([]releases.Fact(nil), first.Facts...)
			second.Facts[0].ObservedAt = secondAt
			test.mutate(&second, secondAt)
			workflow = persistSemanticTestObservation(t, fixture, workflow, second, secondAt)

			if got := fixture.count(t, `SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2`, fixture.indexer.ID.String(), first.NativeGUID); got != 2 {
				t.Fatalf("changed semantic evidence versions=%d, want 2", got)
			}
			candidateIDAfter, secondObservationID := semanticTestQueryCandidate(t, fixture, workflow.ID)
			if candidateIDAfter != candidateID {
				t.Fatalf("candidate identity changed from %s to %s", candidateID, candidateIDAfter)
			}
			if secondObservationID == firstObservationID {
				t.Fatalf("changed evidence reused old observation %s", firstObservationID)
			}
			var oldFacts []byte
			if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT normalized_facts FROM release_observations WHERE id=$1`, firstObservationID.String()).Scan(&oldFacts); err != nil {
				t.Fatalf("old observation disappeared: %v", err)
			}
			var oldFactList []releases.Fact
			if err := json.Unmarshal(oldFacts, &oldFactList); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldFactList, first.Facts) {
				t.Fatalf("old observation was mutated: before=%+v after=%+v", first.Facts, oldFactList)
			}
			var attempts, jobs int
			if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT
				(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1),
				(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&attempts, &jobs); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 || jobs != 0 {
				t.Fatalf("changed evidence consumed acquisition work: attempts=%d jobs=%d", attempts, jobs)
			}
		})
	}
}
