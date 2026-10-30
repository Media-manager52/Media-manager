package postgres

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

type partialRollbackBaseline struct {
	revision     int64
	searchRuns   int
	observations int
	candidates   int
	decisions    int
	states       int
	outbox       int
	attempts     int
	altmountJobs int
}

func partialRollbackCounts(t *testing.T, store *Store, workflowID string) partialRollbackBaseline {
	t.Helper()
	var result partialRollbackBaseline
	if err := store.Pool.QueryRow(context.Background(), `SELECT revision FROM workflows WHERE id=$1`, workflowID).Scan(&result.revision); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM search_runs WHERE workflow_id=$1),
		(SELECT count(*) FROM release_observations o JOIN release_candidates c ON c.observation_id=o.id WHERE c.workflow_id=$1),
		(SELECT count(*) FROM release_candidates WHERE workflow_id=$1),
		(SELECT count(*) FROM candidate_decisions WHERE workflow_id=$1),
		(SELECT count(*) FROM search_partial_retries WHERE workflow_id=$1),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1),
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1)`, workflowID).Scan(&result.searchRuns, &result.observations, &result.candidates, &result.decisions, &result.states, &result.outbox, &result.attempts, &result.altmountJobs); err != nil {
		t.Fatal(err)
	}
	return result
}

func createPartialRollbackFixture(t *testing.T) (*failureRecoveryFixture, workflows.Workflow, SearchContext, SearchPartialRetry, releases.Observation, []EvaluatedObservation, []SearchIndexerOutcome) {
	t.Helper()
	fixture := newFailureRecoveryFixture(t)
	now := time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC)
	workflow := fixture.newMovieWorkflow(t, "Partial Rollback", "901401")
	var err error
	workflow, err = fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("partial-rollback-intent"))
	retry := partialRetryStateForTest(search, fingerprint[:], "targeted")
	observation := partialRetryTestObservation(fixture.indexer.ID, now)
	evaluated := []EvaluatedObservation{{Observation: observation, Evaluation: releases.Evaluation{ObservationID: observation.ID, Verdict: releases.VerdictHeld, Explanations: []releases.Explanation{}}, ProviderResult: true}}
	outcomes := []SearchIndexerOutcome{{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"}, {IndexerInstanceID: fixture.indexerTwo.ID, State: "deferred", Detail: "indexer_pacing_deferred"}}
	return fixture, workflow, search, retry, observation, evaluated, outcomes
}

func TestPersistSearchRollbackWhenNextContinuationEventFails(t *testing.T) {
	fixture, workflow, search, retry, _, evaluated, outcomes := createPartialRollbackFixture(t)
	fingerprint := retry.IntentFingerprint
	baseline := partialRollbackCounts(t, fixture.store, workflow.ID.String())
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `CREATE OR REPLACE FUNCTION mm_review_fail_next_search_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.event_type = 'workflow.search_requested' THEN
    RAISE EXCEPTION 'injected next continuation event failure';
  END IF;
  RETURN NEW;
END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `CREATE TRIGGER mm_review_fail_next_search_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION mm_review_fail_next_search_event()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = fixture.store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS mm_review_fail_next_search_event ON outbox_events`)
		_, _ = fixture.store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS mm_review_fail_next_search_event()`)
	})
	if _, err := fixture.store.PersistSearch(fixture.ctx, search, fingerprint, evaluated, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &retry}, time.Date(2026, 10, 29, 16, 1, 0, 0, time.UTC)); err == nil {
		t.Fatal("injected next-event failure unexpectedly committed")
	}
	after := partialRollbackCounts(t, fixture.store, workflow.ID.String())
	if after != baseline {
		t.Fatalf("rollback mismatch before=%+v after=%+v", baseline, after)
	}
}

func TestPersistSearchRollbackWhenPartialStateWriteFails(t *testing.T) {
	fixture, workflow, search, retry, _, evaluated, outcomes := createPartialRollbackFixture(t)
	fingerprint := retry.IntentFingerprint
	baseline := partialRollbackCounts(t, fixture.store, workflow.ID.String())
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `CREATE OR REPLACE FUNCTION mm_review_fail_partial_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'injected partial state failure';
END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `CREATE TRIGGER mm_review_fail_partial_state BEFORE INSERT OR UPDATE ON search_partial_retries FOR EACH ROW EXECUTE FUNCTION mm_review_fail_partial_state()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = fixture.store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS mm_review_fail_partial_state ON search_partial_retries`)
		_, _ = fixture.store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS mm_review_fail_partial_state()`)
	})
	if _, err := fixture.store.PersistSearch(fixture.ctx, search, fingerprint, evaluated, SearchPersistenceOptions{IndexerOutcomes: outcomes, PartialRetry: &retry}, time.Date(2026, 10, 29, 16, 1, 0, 0, time.UTC)); err == nil {
		t.Fatal("injected state failure unexpectedly committed")
	}
	after := partialRollbackCounts(t, fixture.store, workflow.ID.String())
	if after != baseline {
		t.Fatalf("rollback mismatch before=%+v after=%+v", baseline, after)
	}
}
