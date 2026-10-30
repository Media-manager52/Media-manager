package postgres

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

func partialRetryTestObservation(indexerID uuid.UUID, now time.Time) releases.Observation {
	return releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    indexerID,
		Operation:    "test",
		NativeGUID:   "partial-retry-native",
		Title:        "Partial.Retry.2026.720p.WEB-DL",
		Facts:        []releases.Fact{{Name: "resolution", Values: []string{"720p"}, State: releases.FactObserved}},
		BodyHash:     "partial-retry-body",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
}

func partialRetryStateForTest(search SearchContext, fingerprint []byte, mode string) SearchPartialRetry {
	planned := make([]uuid.UUID, 0, len(search.Indexers))
	for _, indexer := range search.Indexers {
		planned = append(planned, indexer.ID)
	}
	return SearchPartialRetry{WorkflowID: search.Workflow.ID, RoundID: search.SearchRoundID, Mode: mode, IntentFingerprint: append([]byte(nil), fingerprint...), PlannedIndexerIDs: planned}
}

func TestSearchPartialRetryPersistsScopesAndCloses(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	workflow := fixture.newMovieWorkflow(t, "Partial State", "901301")
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if search.SearchRoundID == (uuid.UUID{}) {
		t.Fatal("open search round was not loaded")
	}
	fingerprint := sha256.Sum256([]byte("partial-state-intent"))
	retry := partialRetryStateForTest(search, fingerprint[:], "targeted")
	observation := partialRetryTestObservation(fixture.indexer.ID, now)
	evaluated := []EvaluatedObservation{{Observation: observation, Evaluation: releases.Evaluation{ObservationID: observation.ID, Verdict: releases.VerdictHeld, Explanations: []releases.Explanation{}}, ProviderResult: true}}
	outcomes := []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "deferred", Detail: "indexer_pacing_deferred"}}
	persisted, err := fixture.store.PersistSearch(fixture.ctx, search, fingerprint[:], evaluated, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &retry}, now)
	if err != nil {
		t.Fatalf("persist partial state: %v", err)
	}
	if persisted.State != workflows.StateHeld && persisted.State != workflows.StateWaiting {
		t.Fatalf("partial state workflow=%q", persisted.State)
	}
	state, err := fixture.store.LoadSearchPartialRetry(fixture.ctx, workflow.ID, search.SearchRoundID, "targeted", fingerprint[:], now)
	if err != nil {
		t.Fatalf("load partial state: %v", err)
	}
	if len(state.CompletedIndexers[fixture.indexer.ID]) != 1 {
		t.Fatalf("successful provider evidence=%+v", state.CompletedIndexers)
	}
	if _, exists := state.CompletedIndexers[fixture.indexerTwo.ID]; exists {
		t.Fatal("deferred provider was recorded as complete")
	}
	var runState string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM search_runs WHERE workflow_id=$1 ORDER BY started_at DESC LIMIT 1`, workflow.ID.String()).Scan(&runState); err != nil || runState != "partial" {
		t.Fatalf("search run state=%q err=%v, want partial", runState, err)
	}
	var candidateID, storedObservationID string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT id::text,observation_id::text FROM release_candidates WHERE workflow_id=$1 AND native_key=$2`, workflow.ID.String(), observation.NativeGUID).Scan(&candidateID, &storedObservationID); err != nil {
		t.Fatal(err)
	}
	if storedObservationID != observation.ID.String() {
		t.Fatalf("candidate observation=%s, want %s", storedObservationID, observation.ID)
	}
	loaded, err := fixture.store.LoadSearchPartialObservations(fixture.ctx, state)
	if err != nil || len(loaded) != 1 || loaded[0].ID != observation.ID {
		t.Fatalf("loaded evidence=%+v err=%v", loaded, err)
	}

	otherFingerprint := sha256.Sum256([]byte("different-intent"))
	if _, err := fixture.store.LoadSearchPartialRetry(fixture.ctx, workflow.ID, search.SearchRoundID, "targeted", otherFingerprint[:], now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("different intent load err=%v, want not found", err)
	}
	if _, err := fixture.store.LoadSearchPartialRetry(fixture.ctx, workflow.ID, search.SearchRoundID, "recovery", fingerprint[:], now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("different mode load err=%v, want not found", err)
	}
	if _, err := fixture.store.LoadSearchPartialRetry(fixture.ctx, workflow.ID, uuid.NewV7(), "targeted", fingerprint[:], now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("different round load err=%v, want not found", err)
	}

	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE search_partial_retries SET created_at=$2,expires_at=$3 WHERE workflow_id=$1`, workflow.ID.String(), now.Add(-2*time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.LoadSearchPartialRetry(fixture.ctx, workflow.ID, search.SearchRoundID, "targeted", fingerprint[:], now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired state load err=%v, want not found", err)
	}

	// A later search after the state expired must query all providers and close
	// the old state rather than inheriting it.
	workflow, err = fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, persisted.Revision, workflows.StateSearching, "test", "test", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	search, err = fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The state expired, so this direct persistence call models a fresh chain.
	retry = partialRetryStateForTest(search, fingerprint[:], "targeted")
	outcomes = []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "succeeded"}}
	if _, err := fixture.store.PersistSearch(fixture.ctx, search, fingerprint[:], nil, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &retry}, now.Add(time.Minute)); err != nil {
		t.Fatalf("complete fresh chain: %v", err)
	}
	var count int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM search_partial_retries WHERE workflow_id=$1`, workflow.ID.String()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("completed chain states=%d err=%v", count, err)
	}
}

func TestSearchPartialRetryConcurrentPersistenceIsAtomic(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	now := time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)
	workflow := fixture.newMovieWorkflow(t, "Partial Concurrent", "901302")
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("partial-concurrent-intent"))
	firstRetry := partialRetryStateForTest(first, fingerprint[:], "targeted")
	secondRetry := partialRetryStateForTest(second, fingerprint[:], "targeted")
	outcomes := []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "deferred", Detail: "indexer_pacing_deferred"}}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := fixture.store.PersistSearch(fixture.ctx, first, fingerprint[:], nil, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &firstRetry}, now)
		errs <- err
	}()
	go func() {
		<-start
		_, err := fixture.store.PersistSearch(fixture.ctx, second, fingerprint[:], nil, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &secondRetry}, now)
		errs <- err
	}()
	close(start)
	successes := 0
	for range 2 {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.Is(err, ErrConcurrentModification) && !(errors.As(err, &pgErr) && pgErr.Code == "40001") {
			t.Fatalf("concurrent persistence error=%v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successes=%d, want exactly 1", successes)
	}
	var states, runs, jobs int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT
		(SELECT count(*) FROM search_partial_retries WHERE workflow_id=$1),
		(SELECT count(*) FROM search_runs WHERE workflow_id=$1),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&states, &runs, &jobs); err != nil {
		t.Fatal(err)
	}
	if states != 1 || runs != 1 || jobs != 0 {
		t.Fatalf("concurrent state=%d runs=%d jobs=%d", states, runs, jobs)
	}
}

func TestSearchPartialRetryRejectsMovedObservation(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	workflow := fixture.newMovieWorkflow(t, "Partial Moved", "901303")
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("partial-moved-intent"))
	retry := partialRetryStateForTest(search, fingerprint[:], "targeted")
	observation := partialRetryTestObservation(fixture.indexer.ID, now)
	evaluated := []EvaluatedObservation{{Observation: observation, Evaluation: releases.Evaluation{ObservationID: observation.ID, Verdict: releases.VerdictHeld, Explanations: []releases.Explanation{}}, ProviderResult: true}}
	outcomes := []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "deferred", Detail: "indexer_pacing_deferred"}}
	if _, err := fixture.store.PersistSearch(fixture.ctx, search, fingerprint[:], evaluated, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &retry}, now); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.store.LoadSearchPartialRetry(fixture.ctx, workflow.ID, search.SearchRoundID, "targeted", fingerprint[:], now)
	if err != nil {
		t.Fatal(err)
	}
	var candidateText, oldObservationText string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT id::text,observation_id::text FROM release_candidates WHERE workflow_id=$1 AND native_key=$2`, workflow.ID.String(), observation.NativeGUID).Scan(&candidateText, &oldObservationText); err != nil {
		t.Fatal(err)
	}
	newObservationID := uuid.NewV7()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at)
		SELECT $1,indexer_instance_id,operation_key,native_guid,title,body_sha256,'[]','[]',parse_version,'[]',observed_at FROM release_observations WHERE id=$2`, newObservationID.String(), oldObservationText); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE release_candidates SET observation_id=$2 WHERE id=$1`, candidateText, newObservationID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.LoadSearchPartialObservations(fixture.ctx, state); !errors.Is(err, ErrSearchPartialRetryInvalid) {
		t.Fatalf("moved observation load err=%v, want invalid", err)
	}
}

func TestSearchContinuationCannotPersistPastPendingOwnerSearch(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	workflow := fixture.newMovieWorkflow(t, "Continuation Owner Race", "901405")
	workflow, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("continuation-owner-race"))
	retry := partialRetryStateForTest(search, fingerprint[:], "targeted")
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested','{"workflow_id":"owner"}',now(),now())`, uuid.NewV7().String(), workflow.ID.String(), int64(workflow.Revision)); err != nil {
		t.Fatal(err)
	}
	outcomes := []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "deferred", Detail: "indexer_pacing_deferred"}}
	_, err = fixture.store.PersistSearch(fixture.ctx, search, fingerprint[:], nil, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &retry, Continuation: true}, now)
	if !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("continuation past owner search err=%v, want concurrent modification", err)
	}
	var runs, states, pending int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM search_runs WHERE workflow_id=$1),(SELECT count(*) FROM search_partial_retries WHERE workflow_id=$1),(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL)`, workflow.ID.String()).Scan(&runs, &states, &pending); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || states != 0 || pending != 1 {
		t.Fatalf("race commit changed state: runs=%d states=%d pending=%d", runs, states, pending)
	}
}
