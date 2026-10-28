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

func releaseFeedObservation(now time.Time, indexerID uuid.UUID, nativeGUID, title string) releases.Observation {
	observation := semanticTestObservationWithIndexer(now, indexerID)
	observation.NativeGUID = nativeGUID
	observation.Title = title
	return observation
}

func releaseFeedCandidateIDs(t *testing.T, fixture *failureRecoveryFixture, workflowID uuid.UUID, nativeGUID string) (candidateID, observationID uuid.UUID) {
	t.Helper()
	var candidateText, observationText string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT candidate.id::text,candidate.observation_id::text
		FROM release_candidates candidate
		WHERE candidate.workflow_id=$1 AND candidate.native_key=$2`, workflowID.String(), nativeGUID).Scan(&candidateText, &observationText); err != nil {
		t.Fatalf("load release-feed candidate: %v", err)
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

// Release Intelligence must use the candidate's last-seen time. A semantic
// reuse advances last_observed_at without replacing the immutable observation
// timestamp, so ordering by the observation timestamp would leave A stale
// behind B even though A was rechecked later.
func TestListReleaseCandidatesOrdersByLastObservedAfterSemanticReuse(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	firstAt := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(10 * time.Minute)
	workflowA := fixture.newMovieWorkflow(t, "Release Feed A", "901301")
	workflowB := fixture.newMovieWorkflow(t, "Release Feed B", "901302")
	observationA := releaseFeedObservation(firstAt, fixture.indexer.ID, "release-feed-a", "Release.Feed.A.2026.1080p.WEB-DL")
	observationB := releaseFeedObservation(firstAt.Add(5*time.Minute), fixture.indexer.ID, "release-feed-b", "Release.Feed.B.2026.1080p.WEB-DL")
	workflowA = persistSemanticTestObservation(t, fixture, workflowA, observationA, firstAt)
	workflowB = persistSemanticTestObservation(t, fixture, workflowB, observationB, firstAt.Add(5*time.Minute))
	candidateA, firstObservationA := releaseFeedCandidateIDs(t, fixture, workflowA.ID, observationA.NativeGUID)
	candidateB, _ := releaseFeedCandidateIDs(t, fixture, workflowB.ID, observationB.NativeGUID)

	reusedA := observationA
	reusedA.ID = uuid.NewV7()
	reusedA.ObservedAt = secondAt
	reusedA.Facts = append([]releases.Fact(nil), observationA.Facts...)
	reusedA.Facts[0].ObservedAt = secondAt
	workflowA = persistSemanticTestObservation(t, fixture, workflowA, reusedA, secondAt)

	items, err := fixture.store.ListReleaseCandidates(fixture.ctx, 10)
	if err != nil {
		t.Fatalf("list release candidates: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("release candidates=%d, want 2", len(items))
	}
	if items[0].ID != candidateA || items[1].ID != candidateB {
		t.Fatalf("release feed order=%s,%s, want A=%s before B=%s", items[0].ID, items[1].ID, candidateA, candidateB)
	}
	if !items[0].ObservedAt.Equal(firstAt) {
		t.Fatalf("A evidence observed_at=%s, want %s", items[0].ObservedAt, firstAt)
	}
	if !items[0].LastObservedAt.Equal(secondAt) {
		t.Fatalf("A last_observed_at=%s, want %s", items[0].LastObservedAt, secondAt)
	}
	if !items[1].LastObservedAt.Equal(firstAt.Add(5 * time.Minute)) {
		t.Fatalf("B last_observed_at=%s, want %s", items[1].LastObservedAt, firstAt.Add(5*time.Minute))
	}
	_, currentObservationA := releaseFeedCandidateIDs(t, fixture, workflowA.ID, observationA.NativeGUID)
	if currentObservationA != firstObservationA {
		t.Fatalf("A observation changed from %s to %s despite semantic reuse", firstObservationA, currentObservationA)
	}
	if got := fixture.count(t, `SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2`, fixture.indexer.ID.String(), observationA.NativeGUID); got != 1 {
		t.Fatalf("A observation versions=%d, want 1", got)
	}
	// The read model must expose evidence and last-seen as distinct values.
	if items[0].ObservedAt.Equal(items[0].LastObservedAt) {
		t.Fatalf("feed collapsed evidence and last-seen timestamps: %+v", items[0])
	}
}

func TestListReleaseCandidatesUsesCandidateIDAsDeterministicTieBreaker(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	at := time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)
	workflowA := fixture.newMovieWorkflow(t, "Release Tie A", "901303")
	workflowB := fixture.newMovieWorkflow(t, "Release Tie B", "901304")
	idA, _ := releaseFeedCandidateIDsForWorkflow(t, fixture, workflowA, at, "release-tie-a", "Release.Tie.A.2026.1080p.WEB-DL")
	idB, _ := releaseFeedCandidateIDsForWorkflow(t, fixture, workflowB, at, "release-tie-b", "Release.Tie.B.2026.1080p.WEB-DL")
	items, err := fixture.store.ListReleaseCandidates(fixture.ctx, 10)
	if err != nil {
		t.Fatalf("list release candidates: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("release candidates=%d, want 2", len(items))
	}
	wantFirst, wantSecond := idA, idB
	if wantSecond.String() < wantFirst.String() {
		wantFirst, wantSecond = wantSecond, wantFirst
	}
	if items[0].ID != wantFirst || items[1].ID != wantSecond {
		t.Fatalf("tie order=%s,%s, want %s,%s", items[0].ID, items[1].ID, wantFirst, wantSecond)
	}
	if !items[0].LastObservedAt.Equal(at) || !items[1].LastObservedAt.Equal(at) {
		t.Fatalf("tie last-seen timestamps=%s,%s, want %s", items[0].LastObservedAt, items[1].LastObservedAt, at)
	}
}

func releaseFeedCandidateIDsForWorkflow(t *testing.T, fixture *failureRecoveryFixture, workflow workflows.Workflow, at time.Time, nativeGUID, title string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	observation := releaseFeedObservation(at, fixture.indexer.ID, nativeGUID, title)
	workflow = persistSemanticTestObservation(t, fixture, workflow, observation, at)
	return releaseFeedCandidateIDs(t, fixture, workflow.ID, nativeGUID)
}

// The candidate upsert keeps last-seen monotonic even if a caller persists an
// older evaluation after a newer one. Observation_count still records both
// sightings, while the feed cannot move backward.
func TestPersistSearchDoesNotMoveLastObservedBackward(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	newerAt := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	olderAt := newerAt.Add(-time.Minute)
	workflow := fixture.newMovieWorkflow(t, "Monotonic Last Seen", "901305")
	observation := releaseFeedObservation(newerAt, fixture.indexer.ID, "monotonic-last-seen", "Monotonic.Last.Seen.2026.1080p.WEB-DL")
	workflow = persistSemanticTestObservation(t, fixture, workflow, observation, newerAt)
	candidateID, _ := releaseFeedCandidateIDs(t, fixture, workflow.ID, observation.NativeGUID)

	stale := observation
	stale.ID = uuid.NewV7()
	stale.ObservedAt = olderAt
	stale.Facts = append([]releases.Fact(nil), observation.Facts...)
	stale.Facts[0].ObservedAt = olderAt
	workflow = persistSemanticTestObservation(t, fixture, workflow, stale, olderAt)

	var lastObserved time.Time
	var observationCount int64
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT last_observed_at,observation_count FROM release_candidates WHERE id=$1`, candidateID.String()).Scan(&lastObserved, &observationCount); err != nil {
		t.Fatal(err)
	}
	if !lastObserved.Equal(newerAt) {
		t.Fatalf("last_observed_at moved backward to %s, want %s", lastObserved, newerAt)
	}
	if observationCount != 2 {
		t.Fatalf("observation_count=%d, want two sightings", observationCount)
	}
}
