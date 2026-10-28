package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

func heldRescoreFixture(t *testing.T, now time.Time) (*postgres.Store, workflows.Workflow, postgres.Integration) {
	t.Helper()
	store := openMovieSearchTestStore(t)
	indexer := configureSeasonSearchIndexer(t, store, "https://indexer.invalid/api", false)
	workflow := createSeasonSearchWorkflow(t, store, true)
	ctx := context.Background()
	if _, err := store.Pool.Exec(ctx, `UPDATE pack_expected_episodes e SET air_date=$2 FROM pack_expectation_sets s WHERE e.expectation_id=s.id AND s.workflow_id=$1 AND e.season=1 AND e.episode=1`, workflow.ID.String(), "2026-04-15"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE pack_expected_episodes e SET air_date=$2 FROM pack_expectation_sets s WHERE e.expectation_id=s.id AND s.workflow_id=$1 AND e.season=1 AND e.episode=2`, workflow.ID.String(), "2026-04-22"); err != nil {
		t.Fatal(err)
	}
	count := uint16(1)
	if _, err := store.RefreshMediaMetadata(ctx, workflow.MediaID, &catalog.MediaMetadata{SeasonCount: &count}, nil, "test", now); err != nil {
		t.Fatal(err)
	}
	return store, workflow, indexer
}

func persistHeldRescore(t *testing.T, store *postgres.Store, search postgres.SearchContext, observation releases.Observation) workflows.Workflow {
	t.Helper()
	if _, err := store.PersistSearch(context.Background(), search, []byte("held-rescore"), []postgres.EvaluatedObservation{{
		Observation: observation,
		Evaluation:  releases.Evaluation{ObservationID: observation.ID, Verdict: releases.VerdictHeld, Explanations: []releases.Explanation{}},
	}}, postgres.SearchPersistenceOptions{IndexerOutcomes: []postgres.SearchIndexerOutcome{{IndexerInstanceID: search.Indexers[0].ID, State: "succeeded"}}}, search.Workflow.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	workflow, err := store.GetWorkflow(context.Background(), search.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	return workflow
}

func transitionHeldRescore(t *testing.T, store *postgres.Store, workflow workflows.Workflow, now time.Time) postgres.SearchContext {
	t.Helper()
	workflow, err := store.TransitionWorkflow(context.Background(), workflow.ID, workflow.Revision, workflows.StateSearching, "test held rescore", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := store.LoadSearchContext(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	return search
}

func evaluateHeldRescore(t *testing.T, search postgres.SearchContext, observation releases.Observation, now time.Time) (releases.Observation, releases.Evaluation) {
	t.Helper()
	rules, err := parseRules(search.Policy)
	if err != nil {
		t.Fatal(err)
	}
	scopeRules, err := workflowScopeRules(search.Variant)
	if err != nil {
		t.Fatal(err)
	}
	observation, evaluation, err := evaluateCandidateObservation(observation, search, rules, scopeRules, rules, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	return observation, evaluation
}

func factCount(facts []releases.Fact, name string) int {
	count := 0
	for _, fact := range facts {
		if strings.EqualFold(fact.Name, name) {
			count++
		}
	}
	return count
}

func assertDerivedFactsSingle(t *testing.T, facts []releases.Fact) {
	t.Helper()
	for _, name := range []string{"season_number", "episode_number", "release_scope", "episode_numbering", "unmapped_season_number", "unmapped_episode_number", "unmapped_release_scope"} {
		if count := factCount(facts, name); count > 1 {
			t.Fatalf("duplicate derived fact %s=%d: %+v", name, count, facts)
		}
	}
}

func TestHeldRescoreAtAcquisitionBoundary(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	store, workflow, indexer := heldRescoreFixture(t, now)
	search := transitionHeldRescore(t, store, workflow, now)
	observation := releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    indexer.ID,
		Operation:    "test",
		NativeGUID:   "held-rescore-stable",
		Title:        "Example Series S2026 WEB-DL",
		Facts:        []releases.Fact{{Name: "tvdb_id", Values: []string{"378992"}, State: releases.FactObserved}},
		BodyHash:     "held-rescore-stable",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	workflow = persistHeldRescore(t, store, search, observation)
	search = transitionHeldRescore(t, store, workflow, now.Add(time.Minute))
	firstObservation, firstEvaluation := evaluateHeldRescore(t, search, observation, now.Add(time.Minute))
	workflow = persistHeldRescore(t, store, search, firstObservation)
	held, err := store.HeldCandidateObservations(context.Background(), workflow.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("held after first evaluation=%+v err=%v", held, err)
	}
	firstFacts := held[0].Facts
	assertDerivedFactsSingle(t, firstFacts)
	if factCount(firstFacts, "tvdb_id") != 1 {
		t.Fatalf("provider fact was not preserved: %+v", firstFacts)
	}
	var candidateID, firstVersionID string
	if err := store.Pool.QueryRow(context.Background(), `SELECT c.id::text,c.observation_id::text FROM release_candidates c WHERE c.workflow_id=$1 AND c.native_key=$2`, workflow.ID.String(), observation.NativeGUID).Scan(&candidateID, &firstVersionID); err != nil {
		t.Fatal(err)
	}
	if firstEvaluation.Verdict != releases.VerdictHeld {
		t.Fatalf("first evaluation verdict=%s, want held", firstEvaluation.Verdict)
	}

	search = transitionHeldRescore(t, store, workflow, now.Add(3*time.Minute))
	secondObservation, _ := evaluateHeldRescore(t, search, held[0], now.Add(3*time.Minute))
	workflow = persistHeldRescore(t, store, search, secondObservation)
	held, err = store.HeldCandidateObservations(context.Background(), workflow.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("held after repeat=%+v err=%v", held, err)
	}
	if !reflect.DeepEqual(firstFacts, held[0].Facts) {
		t.Fatalf("unchanged rescore changed facts\nfirst=%+v\nsecond=%+v", firstFacts, held[0].Facts)
	}
	var observationCount int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2`, indexer.ID.String(), observation.NativeGUID).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if observationCount != 2 {
		t.Fatalf("unchanged rescore observation versions=%d, want 2 (raw plus one evaluated version)", observationCount)
	}
	var candidateObservationID string
	if err := store.Pool.QueryRow(context.Background(), `SELECT observation_id::text FROM release_candidates WHERE workflow_id=$1 AND id=$2`, workflow.ID.String(), candidateID).Scan(&candidateObservationID); err != nil {
		t.Fatal(err)
	}
	if candidateObservationID != firstVersionID || candidateObservationID != held[0].ID.String() {
		t.Fatalf("candidate observation=%s first=%s held=%s", candidateObservationID, firstVersionID, held[0].ID)
	}
	var candidateRowID, candidateFKObservationID string
	if err := store.Pool.QueryRow(context.Background(), `SELECT c.id::text,o.id::text FROM release_candidates c JOIN release_observations o ON o.id=c.observation_id WHERE c.id=$1`, candidateID).Scan(&candidateRowID, &candidateFKObservationID); err != nil {
		t.Fatalf("candidate observation FK: %v", err)
	}
	if candidateRowID != candidateID || candidateFKObservationID != candidateObservationID {
		t.Fatalf("candidate identity changed: row=%s fk=%s", candidateRowID, candidateFKObservationID)
	}

	// A genuinely changed frozen mapping must produce exactly one new version.
	if _, err := store.Pool.Exec(context.Background(), `UPDATE pack_expected_episodes e SET air_date='2025-04-15' FROM pack_expectation_sets s WHERE e.expectation_id=s.id AND s.workflow_id=$1 AND e.season=1 AND e.episode=1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(context.Background(), `UPDATE pack_expected_episodes e SET air_date='2025-04-22' FROM pack_expectation_sets s WHERE e.expectation_id=s.id AND s.workflow_id=$1 AND e.season=1 AND e.episode=2`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	search = transitionHeldRescore(t, store, workflow, now.Add(4*time.Minute))
	thirdObservation, _ := evaluateHeldRescore(t, search, held[0], now.Add(4*time.Minute))
	workflow = persistHeldRescore(t, store, search, thirdObservation)
	held, err = store.HeldCandidateObservations(context.Background(), workflow.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("held after mapping change=%+v err=%v", held, err)
	}
	if reflect.DeepEqual(firstFacts, held[0].Facts) {
		t.Fatalf("changed mapping did not change facts: %+v", held[0].Facts)
	}
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2`, indexer.ID.String(), observation.NativeGUID).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if observationCount != 3 {
		t.Fatalf("changed mapping observation versions=%d, want 3", observationCount)
	}
	var changedCandidateObservationID string
	if err := store.Pool.QueryRow(context.Background(), `SELECT c.observation_id::text FROM release_candidates c JOIN release_observations o ON o.id=c.observation_id WHERE c.workflow_id=$1 AND c.id=$2`, workflow.ID.String(), candidateID).Scan(&changedCandidateObservationID); err != nil {
		t.Fatalf("changed mapping candidate FK: %v", err)
	}
	if changedCandidateObservationID != held[0].ID.String() {
		t.Fatalf("changed mapping candidate observation=%s held=%s", changedCandidateObservationID, held[0].ID)
	}
	var oldFacts string
	if err := store.Pool.QueryRow(context.Background(), `SELECT normalized_facts::text FROM release_observations WHERE id=$1`, firstVersionID).Scan(&oldFacts); err != nil {
		t.Fatalf("old version missing: %v", err)
	}
	var oldFactList []releases.Fact
	if err := json.Unmarshal([]byte(oldFacts), &oldFactList); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstFacts, oldFactList) {
		t.Fatalf("old version was overwritten: before=%+v after=%+v", firstFacts, oldFactList)
	}
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM release_candidates WHERE workflow_id=$1`, workflow.ID.String()).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if observationCount != 1 {
		t.Fatalf("candidate count=%d, want 1", observationCount)
	}
	var attempts, jobs int
	if err := store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1),(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&attempts, &jobs); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || jobs != 0 {
		t.Fatalf("held rescore consumed acquisition work: attempts=%d jobs=%d", attempts, jobs)
	}
	// Repeating the changed mapping must reuse the second version.
	search = transitionHeldRescore(t, store, workflow, now.Add(5*time.Minute))
	fourthObservation, _ := evaluateHeldRescore(t, search, held[0], now.Add(5*time.Minute))
	workflow = persistHeldRescore(t, store, search, fourthObservation)
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2`, indexer.ID.String(), observation.NativeGUID).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if observationCount != 3 {
		t.Fatalf("repeat changed mapping observation versions=%d, want 3", observationCount)
	}
}

func TestHeldRescoreConcurrentPersistenceDoesNotDuplicateVersion(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	store, workflow, indexer := heldRescoreFixture(t, now)
	search := transitionHeldRescore(t, store, workflow, now)
	raw := releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    indexer.ID,
		Operation:    "test",
		NativeGUID:   "held-rescore-concurrent",
		Title:        "Example Series S2026 WEB-DL",
		Facts:        []releases.Fact{{Name: "tvdb_id", Values: []string{"378992"}, State: releases.FactObserved}},
		BodyHash:     "held-rescore-concurrent",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	workflow = persistHeldRescore(t, store, search, raw)
	search = transitionHeldRescore(t, store, workflow, now.Add(time.Minute))
	evaluated, evaluation := evaluateHeldRescore(t, search, raw, now.Add(time.Minute))
	entry := []postgres.EvaluatedObservation{{Observation: evaluated, Evaluation: evaluation}}
	outcomes := []postgres.SearchIndexerOutcome{{IndexerInstanceID: indexer.ID, State: "succeeded"}}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := store.PersistSearch(context.Background(), search, []byte("held-rescore-concurrent"), entry, postgres.SearchPersistenceOptions{IndexerOutcomes: outcomes}, now.Add(time.Minute))
			results <- err
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		var pgErr *pgconn.PgError
		if errors.Is(err, postgres.ErrConcurrentModification) || errors.As(err, &pgErr) && pgErr.Code == "40001" {
			continue
		}
		t.Fatalf("concurrent held rescore returned %v", err)
	}
	if successes != 1 {
		t.Fatalf("concurrent held rescore successes=%d, want exactly one", successes)
	}
	var observations, candidates, attempts, jobs int
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2),
		(SELECT count(*) FROM release_candidates WHERE workflow_id=$3 AND native_key=$2),
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$3),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$3)`, indexer.ID.String(), raw.NativeGUID, workflow.ID.String()).Scan(&observations, &candidates, &attempts, &jobs); err != nil {
		t.Fatal(err)
	}
	if observations != 2 || candidates != 1 || attempts != 0 || jobs != 0 {
		t.Fatalf("concurrent held rescore observations=%d candidates=%d attempts=%d jobs=%d, want 2/1/0/0", observations, candidates, attempts, jobs)
	}
}

func TestHeldRescoreLateFailureRollsBackVersion(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	store, workflow, indexer := heldRescoreFixture(t, now)
	search := transitionHeldRescore(t, store, workflow, now)
	raw := releases.Observation{
		ID:           uuid.NewV7(),
		IndexerID:    indexer.ID,
		Operation:    "test",
		NativeGUID:   "held-rescore-rollback",
		Title:        "Example Series S2026 WEB-DL",
		Facts:        []releases.Fact{{Name: "tvdb_id", Values: []string{"378992"}, State: releases.FactObserved}},
		BodyHash:     "held-rescore-rollback",
		ObservedAt:   now,
		ParseVersion: "test",
		Problems:     []string{},
	}
	workflow = persistHeldRescore(t, store, search, raw)
	search = transitionHeldRescore(t, store, workflow, now.Add(time.Minute))
	evaluated, evaluation := evaluateHeldRescore(t, search, raw, now.Add(time.Minute))
	search.Policy = []byte("{")
	if _, err := store.PersistSearch(context.Background(), search, []byte("held-rescore-rollback"), []postgres.EvaluatedObservation{{Observation: evaluated, Evaluation: evaluation}}, postgres.SearchPersistenceOptions{IndexerOutcomes: []postgres.SearchIndexerOutcome{{IndexerInstanceID: indexer.ID, State: "succeeded"}}}, now.Add(time.Minute)); err == nil {
		t.Fatal("invalid policy unexpectedly committed")
	}
	var observations, candidates, attempts, jobs int
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM release_observations WHERE indexer_instance_id=$1 AND native_guid=$2),
		(SELECT count(*) FROM release_candidates WHERE workflow_id=$3 AND native_key=$2),
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$3),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$3)`, indexer.ID.String(), raw.NativeGUID, workflow.ID.String()).Scan(&observations, &candidates, &attempts, &jobs); err != nil {
		t.Fatal(err)
	}
	if observations != 1 || candidates != 1 || attempts != 0 || jobs != 0 {
		t.Fatalf("late failure left observations=%d candidates=%d attempts=%d jobs=%d, want 1/1/0/0", observations, candidates, attempts, jobs)
	}
	state, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || state.State != workflows.StateSearching {
		t.Fatalf("late failure workflow=%+v err=%v, want searching", state, err)
	}
}
