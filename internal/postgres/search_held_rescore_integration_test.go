package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestPersistSearchRescoredHeldObservationPreservesHistoryAndIsIdempotent
// reproduces the production P0 where a held observation was re-scored with a
// newly available episode mapping and then persisted again with its existing
// observation ID. The observation is an immutable evidence snapshot: changed
// facts must become a new version, while the old row and the candidate's
// stable candidate identity remain intact.
func TestPersistSearchRescoredHeldObservationPreservesHistoryAndIsIdempotent(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Held Rescore Persistence", "901101")
	now := time.Now().UTC().Truncate(time.Microsecond)

	transitionToSearching := func(current workflows.Workflow) workflows.Workflow {
		t.Helper()
		next, err := fixture.store.TransitionWorkflow(fixture.ctx, current.ID, current.Revision, workflows.StateSearching, "test held rescore", "test", now)
		if err != nil {
			t.Fatalf("transition to searching: %v", err)
		}
		return next
	}
	persist := func(current workflows.Workflow, observation releases.Observation) workflows.Workflow {
		t.Helper()
		search, err := fixture.store.LoadSearchContext(fixture.ctx, current.ID)
		if err != nil {
			t.Fatalf("load search context: %v", err)
		}
		persisted, err := fixture.store.PersistSearch(fixture.ctx, search, []byte("held-rescore"), []EvaluatedObservation{{
			Observation: observation,
			Evaluation: releases.Evaluation{
				ObservationID: observation.ID,
				Verdict:       releases.VerdictHeld,
				Score:         0,
				Explanations:  []releases.Explanation{},
			},
		}}, SearchPersistenceOptions{IndexerOutcomes: []SearchIndexerOutcome{
			{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"},
			{IndexerInstanceID: fixture.indexerTwo.ID, State: "succeeded"},
		}}, now)
		if err != nil {
			t.Fatalf("persist held observation: %v", err)
		}
		if persisted.State != workflows.StateHeld {
			t.Fatalf("workflow state=%q, want held", persisted.State)
		}
		return persisted
	}

	workflow = transitionToSearching(workflow)
	originalID := uuid.NewV7()
	original := releases.Observation{
		ID:           originalID,
		IndexerID:    fixture.indexer.ID,
		Operation:    "test",
		NativeGUID:   "held-rescore-guid",
		Title:        "Held.Rescore.2026.1080p.WEB-DL",
		Fields:       []releases.RawField{},
		Facts:        []releases.Fact{},
		BodyHash:     "held-rescore",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	workflow = persist(workflow, original)

	held, err := fixture.store.HeldCandidateObservations(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatalf("load held observations: %v", err)
	}
	if len(held) != 1 || held[0].ID != originalID {
		t.Fatalf("held observations=%+v, want the stored observation %s", held, originalID)
	}
	var candidateIDText, originalObservationIDText string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT candidate.id::text,observation.id::text
		FROM release_candidates candidate JOIN release_observations observation ON observation.id=candidate.observation_id
		WHERE candidate.workflow_id=$1 AND candidate.indexer_instance_id=$2 AND candidate.native_key=$3`, workflow.ID.String(), fixture.indexer.ID.String(), original.NativeGUID).Scan(&candidateIDText, &originalObservationIDText); err != nil {
		t.Fatalf("load candidate identity: %v", err)
	}
	if originalObservationIDText != originalID.String() {
		t.Fatalf("candidate observation=%s, want %s", originalObservationIDText, originalID)
	}

	// This is the rescoring result: the same provider observation now has a
	// frozen episode-numbering fact that was absent when it was first stored.
	rescored := held[0]
	rescored.Facts = []releases.Fact{{
		Name:       "release_scope",
		Values:     []string{"episode"},
		State:      releases.FactInferred,
		ObservedAt: now.Add(time.Minute),
	}}
	workflow = transitionToSearching(workflow)
	workflow = persist(workflow, rescored)

	var candidateObservationIDText string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT observation_id::text FROM release_candidates WHERE workflow_id=$1 AND id=$2`, workflow.ID.String(), candidateIDText).Scan(&candidateObservationIDText); err != nil {
		t.Fatalf("load rescored candidate: %v", err)
	}
	if candidateObservationIDText == originalID.String() {
		t.Fatalf("rescored candidate kept changed observation ID %s; want a new immutable version", originalID)
	}
	if _, err := uuid.Parse(candidateObservationIDText); err != nil {
		t.Fatalf("candidate observation=%q is invalid: %v", candidateObservationIDText, err)
	}
	held, err = fixture.store.HeldCandidateObservations(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatalf("load rescored held observation: %v", err)
	}
	if len(held) != 1 || held[0].ID.String() != candidateObservationIDText {
		t.Fatalf("rescored held observations=%+v, want current observation %s", held, candidateObservationIDText)
	}

	var oldFacts, newFacts []byte
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT normalized_facts FROM release_observations WHERE id=$1`, originalID.String()).Scan(&oldFacts); err != nil {
		t.Fatalf("old observation was discarded: %v", err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT normalized_facts FROM release_observations WHERE id=$1`, candidateObservationIDText).Scan(&newFacts); err != nil {
		t.Fatalf("rescored observation missing: %v", err)
	}
	var oldFactList, newFactList []releases.Fact
	if err := json.Unmarshal(oldFacts, &oldFactList); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(newFacts, &newFactList); err != nil {
		t.Fatal(err)
	}
	if len(oldFactList) != 0 || len(newFactList) == 0 || newFactList[0].Name != "release_scope" {
		t.Fatalf("observation history was overwritten: old=%+v new=%+v", oldFactList, newFactList)
	}

	// Repeating the same rescore is a no-op for observation history. The
	// candidate's current observation is reused instead of creating another
	// version or colliding on the old primary key.
	workflow = transitionToSearching(workflow)
	workflow = persist(workflow, rescored)
	var observationCount, candidateCount int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM release_observations WHERE id IN ($1,$2)`, originalID.String(), candidateObservationIDText).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM release_candidates WHERE workflow_id=$1 AND id=$2`, workflow.ID.String(), candidateIDText).Scan(&candidateCount); err != nil {
		t.Fatal(err)
	}
	if observationCount != 2 || candidateCount != 1 {
		t.Fatalf("repeat persisted observations=%d candidates=%d, want 2 and 1", observationCount, candidateCount)
	}
}

// TestPersistSearchRollsBackAllRowsOnLateFailure proves that a persistence
// failure after observations and candidates have been written cannot leave a
// partial search, consume an acquisition attempt, or change the workflow.
func TestPersistSearchRollsBackAllRowsOnLateFailure(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Persist Search Rollback", "901102")
	now := time.Now().UTC().Truncate(time.Microsecond)
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test rollback", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Parsing the policy happens after the observation, candidate, decision
	// and search-run writes. Returning here exercises the transaction's
	// rollback path, not an early validation guard.
	search.Policy = json.RawMessage("{")
	observation := releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    fixture.indexer.ID,
		Operation:    "test",
		NativeGUID:   "rollback-guid",
		Title:        "Rollback.2026.1080p.WEB-DL",
		Fields:       []releases.RawField{},
		Facts:        []releases.Fact{},
		BodyHash:     "rollback",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	_, err = fixture.store.PersistSearch(fixture.ctx, search, []byte("rollback"), []EvaluatedObservation{{
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
	if err == nil {
		t.Fatal("invalid policy unexpectedly committed")
	}

	if got := fixture.count(t, `SELECT count(*) FROM release_observations WHERE id=$1`, observation.ID.String()); got != 0 {
		t.Fatalf("rollback left %d observation(s)", got)
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM release_candidates WHERE workflow_id=$1`:          0,
		`SELECT count(*) FROM candidate_decisions WHERE workflow_id=$1`:         0,
		`SELECT count(*) FROM search_runs WHERE workflow_id=$1`:                 0,
		`SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1`: 0,
		`SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`:               0,
	} {
		if got := fixture.count(t, query, workflow.ID.String()); got != want {
			t.Fatalf("rollback count %q=%d, want %d", query, got, want)
		}
	}

	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateSearching) {
		t.Fatalf("workflow state=%q after rollback, want searching", state)
	}
}

// TestPersistSearchConcurrentAttemptsDoNotDuplicateObservation exercises the
// worker retry boundary: two attempts may load the same workflow revision, but
// the serializable transaction and workflow lock must let at most one commit.
// The loser is a normal concurrent-modification retry, never a primary-key
// collision or a second candidate for the same release.
func TestPersistSearchConcurrentAttemptsDoNotDuplicateObservation(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Concurrent Search Persistence", "901103")
	now := time.Now().UTC().Truncate(time.Microsecond)
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test concurrent search", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	observation := releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    fixture.indexer.ID,
		Operation:    "test",
		NativeGUID:   "concurrent-guid",
		Title:        "Concurrent.2026.1080p.WEB-DL",
		Fields:       []releases.RawField{},
		Facts:        []releases.Fact{},
		BodyHash:     "concurrent",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	evaluated := []EvaluatedObservation{{
		Observation: observation,
		Evaluation: releases.Evaluation{
			ObservationID: observation.ID,
			Verdict:       releases.VerdictHeld,
			Explanations:  []releases.Explanation{},
		},
	}}
	outcomes := []SearchIndexerOutcome{
		{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"},
		{IndexerInstanceID: fixture.indexerTwo.ID, State: "succeeded"},
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, persistErr := fixture.store.PersistSearch(fixture.ctx, search, []byte("concurrent"), evaluated, SearchPersistenceOptions{IndexerOutcomes: outcomes}, now)
			results <- persistErr
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		persistErr := <-results
		if persistErr == nil {
			successes++
			continue
		}
		var pgErr *pgconn.PgError
		if errors.Is(persistErr, ErrConcurrentModification) || errors.As(persistErr, &pgErr) && pgErr.Code == "40001" {
			continue
		}
		t.Fatalf("concurrent persist returned %v, want success or a serializable/workflow conflict", persistErr)
	}
	if successes != 1 {
		t.Fatalf("concurrent persist successes=%d, want exactly one committed search", successes)
	}
	var observations, candidates int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM release_observations WHERE id=$1`, observation.ID.String()).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM release_candidates WHERE workflow_id=$1 AND native_key=$2`, workflow.ID.String(), observation.NativeGUID).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if observations != 1 || candidates != 1 {
		t.Fatalf("concurrent persist observations=%d candidates=%d, want 1 and 1", observations, candidates)
	}
}
