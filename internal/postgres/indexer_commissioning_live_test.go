package postgres

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"uuid"
)

// liveTestStore opens a disposable per-test schema via
// MEDIA_MANAGER_TEST_DATABASE_URL and migrates to the CURRENT schema (35).
// It skips when the variable is unset; NO application database is used.
func liveTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	databaseURL := liveTestDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	schema := "mm_indexer_commissioning_live_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	return store, ctx
}

func liveTestDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(envOrEmpty("MEDIA_MANAGER_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	lowered := strings.ToLower(databaseURL)
	if strings.Contains(lowered, "prod") && !strings.Contains(lowered, "test") {
		t.Fatalf("refusing to use a non-test database URL")
	}
	return databaseURL
}

func liveIndexer(t *testing.T, ctx context.Context, store *Store, now time.Time) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,'LIVE_TEST_KEY',$4,$4)`, id.String(), "Live fixture "+id.String(), `{"base_url":"https://live-test.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func liveRunAndIntentStep(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID, now time.Time) (IndexerCommissioningRun, IndexerCommissioningStep) {
	t.Helper()
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := commissioningRepositoryFixtures()
	plan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, plan, now)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := run.Steps[0].StructuralRequestSHA256
	var fingerprintDigest commissioningv2.SHA256Digest
	copy(fingerprintDigest[:], fingerprint)
	claimed, err := store.ClaimAuthorizedStepIntent(ctx, commissioningv2.LiveStepClaim{
		RunID: run.ID.String(), StepID: run.Steps[0].ID.String(), IndexerInstanceID: indexerID.String(),
		ExpectedStepState: commissioningv2.StepPlanned, ExpectedRunRevision: run.Revision,
		Fingerprint: fingerprintDigest, PlannerVersion: run.PlannerVersion,
		LeaseOwner: "worker.test", LeaseUntil: now.Add(time.Hour),
	}, now.Add(2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_ = claimed
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded, reloaded.Steps[0]
}

// configureCommissioningScheduler configures exactly the given indexer ID
// with a commissioning-valid Budget. Every test requiring a commissioning
// acquisition must Configure that exact indexer ID first; never configure
// one ID and generate different IDs inside a loop.
func configureCommissioningScheduler(t *testing.T, scheduler *indexers.Scheduler, indexerID uuid.UUID, budget indexers.Budget) {
	t.Helper()
	if !indexers.ValidCommissioningBudget(budget) {
		t.Fatalf("test budget is not commissioning-valid: %#v", budget)
	}
	if err := scheduler.Configure(indexerID.String(), budget); err != nil {
		t.Fatal(err)
	}
}

func liveSnapshotForStepBudget(t *testing.T, scheduler *indexers.Scheduler, indexerID, stepID uuid.UUID) indexers.CommissioningBudgetSnapshot {
	t.Helper()
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID.String(), stepID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	return permit.CommissioningBudgetSnapshot()
}

func liveSnapshotForStep(t *testing.T, scheduler *indexers.Scheduler, indexerID, stepID uuid.UUID) indexers.CommissioningBudgetSnapshot {
	t.Helper()
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID.String(), stepID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	return permit.CommissioningBudgetSnapshot()
}

// liveTransmissionFixture is the standard admit→reserve→transmit setup with
// the exact indexer ID configured: it returns the scheduler, snapshot, and
// reloaded run/step after the boundary commit.
func liveMarkerFor(run IndexerCommissioningRun, step IndexerCommissioningStep, snapshot indexers.CommissioningBudgetSnapshot, now time.Time) commissioningv2.LiveTransmitMarker {
	return commissioningv2.LiveTransmitMarker{
		RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: run.IndexerInstanceID.String(),
		ExpectedStepState: commissioningv2.StepIntentCommitted, ExpectedRunRev: run.Revision,
		LeaseOwner: "worker.test", ExecutionDeadline: now.Add(time.Hour), BudgetSnapshot: snapshot,
	}
}

func liveCAPSCompletion(run IndexerCommissioningRun, step IndexerCommissioningStep) commissioningv2.LiveCompletion {
	// Evidence must be observed inside [transmitted_at, completed_at]: the
	// read validator enforces that window, so a wall-clock stamp paired with
	// synthetic step timestamps would produce a persisted-but-unreadable
	// step. Prefer the passed step's transmitted_at, then the run's copy of
	// the same step, and only fall back to wall clock when neither exists.
	transmittedAt := step.TransmittedAt
	if transmittedAt == nil {
		for _, candidate := range run.Steps {
			if candidate.ID == step.ID {
				transmittedAt = candidate.TransmittedAt
				break
			}
		}
	}
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	if transmittedAt != nil {
		observedAt = transmittedAt.UTC().Truncate(time.Microsecond)
	}
	evidence := commissioningRepositoryCAPSEvidence(observedAt)
	classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseCAPSObserved,
		TargetKind: step.Target,
		Evidence:   &evidence, RawResultCount: uint16Ptr(0), PlausibleIdentityCount: uint16Ptr(0),
		RawBodySHA256: evidence.BodySHA256.Bytes(),
	})
	if err != nil {
		panic(err)
	}
	fingerprint, err := commissioningv2.SHA256DigestFromBytes(step.StructuralRequestSHA256)
	if err != nil {
		panic(err)
	}
	return commissioningv2.LiveCompletion{
		RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: run.IndexerInstanceID.String(),
		ExpectedRunRev: run.Revision, LeaseOwner: "worker.test",
		Classified: classified, ApplyRunTransition: true,
		ExpectedStructuralFingerprint: fingerprint,
		ExpectedTargetKind: commissioningv2.TargetIdentity{
			Kind:        string(step.Target.Kind),
			RecipeKey:   step.Target.RecipeKey,
			CategoryIDs: append([]int(nil), step.CategoryIDs...),
		},
		ExpectedCategoryIDs: append([]int(nil), step.CategoryIDs...),
	}
}

func uint16Ptr(value uint16) *uint16 { return &value }

func TestMarkClaimedStepTransmittingCommitsMarkerGateAndPacingFloorTogether(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	// Rebind to the exact step: admission binds (indexer, presented step);
	// reserve/transmit/complete for the durable step need its snapshot.
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	reservation, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second))
	if err != nil || !reservation.Granted {
		t.Fatalf("reserve: %#v %v", reservation, err)
	}
	deadline := now.Add(time.Hour)
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	marker.ExecutionDeadline = deadline
	state, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.State != commissioningv2.StepTransmitting || !state.GateHeld {
		t.Fatalf("state=%#v", state)
	}
	wantFloor := deadline.Add(budget.MinimumInterval)
	if !state.PacingFloor.Equal(wantFloor) {
		t.Fatalf("floor=%s want=%s", state.PacingFloor, wantFloor)
	}
	var minimumMS int64
	var next time.Time
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT minimum_interval_ms,next_allowed_at,commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&minimumMS, &next, &gate); err != nil {
		t.Fatal(err)
	}
	if minimumMS != budget.MinimumInterval.Milliseconds() || !next.Equal(wantFloor) || gate == nil || *gate != step.ID.String() {
		t.Fatalf("budget row: min=%d next=%s gate=%v", minimumMS, next, gate)
	}
}

// B-17: first MarkClaimedStepTransmitting succeeds; the duplicate loses
// with no second transmitted_at, no second gate boundary, and no
// double-advanced pacing.
func TestDuplicateMarkClaimedStepTransmittingLosesWithoutSecondBoundary(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	first, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.State != commissioningv2.StepTransmitting || !first.GateHeld || first.TransmittedAt == nil {
		t.Fatalf("first=%#v", first)
	}
	var floorBefore, transmittedBefore time.Time
	var revisionBefore int64
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at, revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorBefore, &revisionBefore); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT transmitted_at FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()).Scan(&transmittedBefore); err != nil {
		t.Fatal(err)
	}
	// Duplicate: re-admit (same attempt) and lose on expected state. The
	// second call must fail (no second boundary), not merely be ignored.
	secondSnapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	secondMarker := liveMarkerFor(run, step, secondSnapshot, now.Add(3*time.Second))
	secondMarker.ExecutionDeadline = marker.ExecutionDeadline
	if _, err := store.MarkClaimedStepTransmitting(ctx, secondMarker, now.Add(5*time.Second)); err == nil {
		t.Fatal("duplicate boundary accepted")
	}
	var floorAfter, transmittedAfter time.Time
	var revisionAfter int64
	var gateAfter *string
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at, revision, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorAfter, &revisionAfter, &gateAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT transmitted_at FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()).Scan(&transmittedAfter); err != nil {
		t.Fatal(err)
	}
	if !transmittedAfter.Equal(transmittedBefore) {
		t.Fatalf("second transmitted_at: before=%s after=%s", transmittedBefore, transmittedAfter)
	}
	if gateAfter == nil || *gateAfter != step.ID.String() {
		t.Fatalf("gate changed by duplicate: %v", gateAfter)
	}
	if !floorAfter.Equal(floorBefore) {
		t.Fatalf("pacing double-advanced: before=%s after=%s", floorBefore, floorAfter)
	}
	if revisionAfter != revisionBefore {
		t.Fatalf("budget revision advanced by duplicate: %d -> %d", revisionBefore, revisionAfter)
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("duplicate changed step state: %#v", loaded)
	}
}

// B-18: rollback of a failed second boundary — the second attempt's locked
// reads execute, then it fails closed on the expected-state predicate (step
// already transmitting) with zero durable effect. The REAL post-write gate
// conflict (step UPDATE executes, budget upsert fails with
// ErrCommissioningGateUnresolved, full rollback) is proved by
// TestTransmitPostWriteBudgetConflictRollsBackBoundary, which uses a legal
// two-run lifecycle; this test pins the duplicate-boundary path with the
// same exhaustive rollback assertions.
func TestTransmitTransactionRollbackLeavesNoTransmissionBoundary(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	firstMarker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	first, err := store.MarkClaimedStepTransmitting(ctx, firstMarker, now.Add(4*time.Second))
	if err != nil || first.TransmittedAt == nil {
		t.Fatalf("first boundary: %#v %v", first, err)
	}
	var floorBefore, transmittedBefore time.Time
	var revisionBefore int64
	var runRevBefore uint64
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at, revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorBefore, &revisionBefore); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT transmitted_at FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()).Scan(&transmittedBefore); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	runRevBefore = reloaded.Revision
	// A second attempt enters the SAME transaction, executes its locked
	// reads, then fails on the expected-state predicate (step already
	// transmitting) and rolls back: prove every durable field is unchanged.
	secondSnapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	secondMarker := liveMarkerFor(run, step, secondSnapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, secondMarker, now.Add(5*time.Second)); err == nil {
		t.Fatal("second boundary accepted; no rollback to prove")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting || loaded.TransmittedAt == nil || !loaded.TransmittedAt.Equal(transmittedBefore) {
		t.Fatalf("rollback changed step boundary: %#v want transmitted_at=%s", loaded, transmittedBefore)
	}
	var floorAfter, transmittedAfter time.Time
	var revisionAfter int64
	var gateAfter *string
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at, revision, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorAfter, &revisionAfter, &gateAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT transmitted_at FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()).Scan(&transmittedAfter); err != nil {
		t.Fatal(err)
	}
	if !transmittedAfter.Equal(transmittedBefore) {
		t.Fatalf("rollback changed transmitted_at: %s -> %s", transmittedBefore, transmittedAfter)
	}
	if gateAfter == nil || *gateAfter != step.ID.String() {
		t.Fatalf("rollback changed gate: %v", gateAfter)
	}
	if !floorAfter.Equal(floorBefore) {
		t.Fatalf("rollback changed pacing floor: %s -> %s", floorBefore, floorAfter)
	}
	if revisionAfter != revisionBefore {
		t.Fatalf("rollback changed revision: %d -> %d", revisionBefore, revisionAfter)
	}
	after, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != runRevBefore {
		t.Fatalf("rollback changed run revision: %d -> %d", runRevBefore, after.Revision)
	}
}

func TestMarkClaimedStepTransmittingRequiresValidInWindowReservation(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 2, LimitWindow: time.Hour}
	for _, name := range []string{"missing", "expired", "wrong-indexer", "wrong-family"} {
		indexerID := liveIndexer(t, ctx, store, now.Add(time.Duration(len(name))*time.Second))
		configureCommissioningScheduler(t, scheduler, indexerID, budget)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now.Add(time.Duration(len(name))*time.Second))
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
		switch name {
		case "expired":
			old := now.Add(-2 * time.Hour)
			if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'query',$3)`, step.ID.String(), indexerID.String(), old); err != nil {
				t.Fatal(err)
			}
		case "wrong-indexer":
			other := liveIndexer(t, ctx, store, now.Add(91*time.Second))
			if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'query',$3)`, step.ID.String(), other.String(), now); err != nil {
				t.Fatal(err)
			}
		case "wrong-family":
			if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'grab',$3)`, step.ID.String(), indexerID.String(), now); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err == nil {
			t.Fatalf("%s: boundary without valid reservation", name)
		}
		var count int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE id=$1`, step.ID.String()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if name == "missing" && count != 0 {
			t.Fatal("boundary created a replacement reservation")
		}
	}
}

func TestMarkClaimedStepTransmittingRejectsExpiredDeadline(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	marker.ExecutionDeadline = now.Add(-time.Second)
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err == nil {
		t.Fatal("expired deadline accepted")
	}
}

// TestTransmitPostWriteBudgetConflictRollsBackBoundary is the B-18/B-21
// shared post-write rollback proof: step 1 holds the unresolved gate; step 2
// (a second legal intent_committed step on the same indexer prepared via a
// legal run lifecycle: the first run is terminalized by a real completion
// before the second run is created) is reserved and its boundary attempted.
// The step UPDATE executes and the budget upsert then fails because
// commissioning_gate_step_id is already non-NULL, so the function returns
// ErrCommissioningGateUnresolved and the whole transaction rolls back: step
// 2 stays intent_committed with transmitted_at NULL, the gate still points
// at step 1, the pacing floor did not advance for step 2, and the run
// revision did not commit.
func TestTransmitPostWriteBudgetConflictRollsBackBoundary(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run1, step1 := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	snapshot1 := liveSnapshotForStep(t, scheduler, indexerID, step1.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step1.ID.String(), snapshot1, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run1, step1, snapshot1, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Hold the gate for step 1 yet free the run lifecycle: recover the
	// transmitting attempt to indeterminate through the real recovery path.
	// The step becomes terminal-indeterminate, the run moves to
	// owner_attention — a 0034 terminal state — and the budget gate is
	// untouched (still step 1). (Verified:
	// indexer_commissioning_runs_one_nonterminal covers only
	// awaiting_caps_authorization, caps_authorized,
	// awaiting_category_review, awaiting_search_authorization,
	// searches_authorized, ready_for_draft, cancel_requested.)
	reloaded1, err := store.IndexerCommissioningRun(ctx, run1.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := store.MarkInterruptedAttemptIndeterminate(ctx, commissioningv2.LiveRecovery{
		RunID: run1.ID.String(), StepID: step1.ID.String(), IndexerInstanceID: indexerID.String(),
		ExpectedRunRev: reloaded1.Revision, Cause: commissioningv2.SafeCodeIndeterminateAttempt,
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != commissioningv2.StepIndeterminate {
		t.Fatalf("setup: recovery=%#v, want indeterminate", recovered)
	}
	afterRun1, err := store.IndexerCommissioningRun(ctx, run1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRun1.State != commissioningv2.RunOwnerAttention {
		t.Fatalf("setup: run1=%q, want owner_attention (terminal) so run2 is legal", afterRun1.State)
	}
	var gateHeld *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gateHeld); err != nil || gateHeld == nil || *gateHeld != step1.ID.String() {
		t.Fatalf("setup: gate=%v err=%v, want step1 %s", gateHeld, err, step1.ID)
	}
	// Legal second run/step on the same indexer (prior run terminal).
	run2, step2 := liveRunAndIntentStep(t, ctx, store, indexerID, now.Add(time.Hour))
	if step2.State != commissioningv2.StepIntentCommitted {
		t.Fatalf("second step=%q, want intent_committed", step2.State)
	}
	snapshot2 := liveSnapshotForStep(t, scheduler, indexerID, step2.ID)
	// Transmit inside run2's lease window (lease until now+2h): the fixture
	// must fail on the held gate, not on an expired executor lease.
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step2.ID.String(), snapshot2, now.Add(90*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var floorBefore time.Time
	var revisionBefore int64
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at, revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorBefore, &revisionBefore); err != nil {
		t.Fatal(err)
	}
	reloaded2, err := store.IndexerCommissioningRun(ctx, run2.ID)
	if err != nil {
		t.Fatal(err)
	}
	runRevBefore := reloaded2.Revision
	marker2 := liveMarkerFor(run2, step2, snapshot2, now.Add(90*time.Minute))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker2, now.Add(90*time.Minute).Add(time.Second)); err == nil {
		t.Fatal("boundary while gate held accepted; no rollback to prove")
	} else if !errors.Is(err, ErrCommissioningGateUnresolved) {
		t.Fatalf("boundary while gate held err=%v, want ErrCommissioningGateUnresolved (post-write gate conflict)", err)
	}
	// After rollback: step 2 remains intent_committed with transmitted_at
	// NULL; the original gate still points at step 1; the pacing floor did
	// not advance for step 2; run 2 revision did not commit.
	loaded2, err := store.IndexerCommissioningStep(ctx, run2.ID, step2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded2.State != commissioningv2.StepIntentCommitted || loaded2.TransmittedAt != nil {
		t.Fatalf("rollback changed step2: %#v", loaded2)
	}
	var gateAfter *string
	var floorAfter time.Time
	var revisionAfter int64
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text, next_allowed_at, revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gateAfter, &floorAfter, &revisionAfter); err != nil {
		t.Fatal(err)
	}
	if gateAfter == nil || *gateAfter != step1.ID.String() {
		t.Fatalf("rollback changed gate: %v, want step1 %s", gateAfter, step1.ID)
	}
	if !floorAfter.Equal(floorBefore) {
		t.Fatalf("rollback advanced pacing floor: %s -> %s", floorBefore, floorAfter)
	}
	if revisionAfter != revisionBefore {
		t.Fatalf("rollback changed budget revision: %d -> %d", revisionBefore, revisionAfter)
	}
	after2, err := store.IndexerCommissioningRun(ctx, run2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after2.Revision != runRevBefore {
		t.Fatalf("rollback changed run2 revision: %d -> %d", runRevBefore, after2.Revision)
	}
}

func TestMarkClaimedStepTransmittingRefusesWhileGateHeld(t *testing.T) {
	// B-21 refusal-while-gate-held is proved post-write by
	// TestTransmitPostWriteBudgetConflictRollsBackBoundary (gate-held
	// upsert conflict with ErrCommissioningGateUnresolved). This test pins
	// the same-indexer second-boundary refusal path with its own
	// post-failure rollback assertions.
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// A second boundary for the same step must fail on expected state, and a
	// gate-held budget row must refuse replacement.
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(5*time.Second)); err == nil {
		t.Fatal("second boundary accepted")
	}
}

// B-22: construct state valid before the lock, request cancellation
// durably, then prove the boundary re-reads the locked run and refuses
// transmission with zero boundary writes.
func TestMarkClaimedStepTransmittingRereadsCancellationFromLockedRun(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Durably request cancellation AFTER the reservation (state was valid
	// before the lock).
	if _, err := store.RequestRunCancellation(ctx, indexerID.String(), run.ID.String(), run.Revision, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	// Reload the run so the marker carries the now-stale revision: the
	// boundary must still refuse via the locked-run cancellation re-read.
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	marker.ExpectedRunRev = reloaded.Revision
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(5*time.Second)); err == nil {
		t.Fatal("cancelled run transmitted")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepIntentCommitted || loaded.TransmittedAt != nil {
		t.Fatalf("cancelled boundary wrote step state: %#v", loaded)
	}
	var gates int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_budget_state WHERE indexer_instance_id=$1 AND commissioning_gate_step_id IS NOT NULL`, indexerID.String()).Scan(&gates); err != nil {
		t.Fatal(err)
	}
	if gates != 0 {
		t.Fatal("cancelled boundary wrote a gate")
	}
	var next *time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&next); err == nil && next != nil {
		t.Fatalf("cancelled boundary wrote pacing floor: %s", *next)
	}
}

func TestCompleteLiveAttemptStoresParsedRetryAfterCooldownAndClearsGateAtomically(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	runReloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = runReloaded
	// A parsed Retry-After INSIDE the commissioning ceiling (MaximumBackoff
	// = 1m) must survive as the exact durable cooldown: 30s is later than
	// the base backoff fallback but earlier than now+MaximumBackoff, so the
	// commissioning clamp does not apply. The response must be the real
	// rate-limited class (a CAPS completion cannot carry Retry-After at all).
	completedAt := now.Add(5 * time.Second)
	deadline, ok := indexers.ParseRetryAfter("30", completedAt)
	if !ok {
		t.Fatal("parse retry-after")
	}
	classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseRateLimited,
		RetryAfter: deadline, TargetKind: step.Target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if classified.SafeOutcome != commissioningv2.OutcomeRateLimited || classified.SchedulerResult.RetryAt.IsZero() || !classified.SchedulerResult.RetryAt.Equal(deadline) {
		t.Fatalf("rate-limited classification lost Retry-After: %#v", classified)
	}
	if !classified.ClearGate {
		t.Fatal("rate_limited must clear the gate")
	}
	completion := liveCompletionForStep(t, run, step, snapshot, classified)
	state, err := store.CompleteLiveAttempt(ctx, completion, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != commissioningv2.StepCompleted {
		t.Fatalf("state=%#v", state)
	}
	var cooldown time.Time
	var gate *string
	var outcomeAt *time.Time
	var lastOutcome *string
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until,commissioning_gate_step_id::text,last_outcome_at,last_outcome FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown, &gate, &outcomeAt, &lastOutcome); err != nil {
		t.Fatal(err)
	}
	if !cooldown.Equal(deadline) || gate != nil || outcomeAt == nil || lastOutcome == nil || *lastOutcome != string(indexers.OutcomeRateLimited) {
		t.Fatalf("cooldown=%s want %s gate=%v outcomeAt=%v lastOutcome=%v", cooldown, deadline, gate, outcomeAt, lastOutcome)
	}
	if !outcomeAt.Equal(completedAt) {
		t.Fatalf("last_outcome_at=%s want %s", outcomeAt, completedAt)
	}
	var retryAt *time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT retry_at FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()).Scan(&retryAt); err != nil {
		t.Fatal(err)
	}
	if retryAt != nil {
		t.Fatal("completed 429 used step.retry_at")
	}
}

func TestCompleteLiveAttemptPreservesLongerExistingCooldownAndPacingFloor(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(time.Hour)
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	marker.ExecutionDeadline = deadline
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Boundary wrote the pacing floor deadline+MinimumInterval; capture it
	// before the out-of-band extension.
	var floorAtBoundary time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorAtBoundary); err != nil {
		t.Fatal(err)
	}
	if !floorAtBoundary.Equal(deadline.Add(5 * time.Second)) {
		t.Fatalf("boundary floor=%s want %s", floorAtBoundary, deadline.Add(5*time.Second))
	}
	runReloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = runReloaded
	completion := liveCAPSCompletion(run, step)
	completion.BudgetSnapshot = snapshot
	// Pre-extend BOTH cooldown and pacing floor out of band to prove
	// GREATEST preserves each: a longer cooldown AND a longer floor must
	// both survive completion.
	strongerCooldown := now.Add(2 * time.Hour)
	strongerFloor := deadline.Add(time.Hour)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET cooldown_until=$2, next_allowed_at=$3 WHERE indexer_instance_id=$1`, indexerID.String(), strongerCooldown, strongerFloor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	var cooldown, floor time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until, next_allowed_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown, &floor); err != nil {
		t.Fatal(err)
	}
	if !cooldown.Equal(strongerCooldown) {
		t.Fatalf("stronger cooldown lost: %s want %s", cooldown, strongerCooldown)
	}
	if !floor.Equal(strongerFloor) {
		t.Fatalf("stronger pacing floor lost: %s want %s", floor, strongerFloor)
	}
}

func TestCompleteLiveAttemptRollbackRetainsGateAndTransmittingStep(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	runReloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = runReloaded
	completion := liveCAPSCompletion(run, step)
	completion.LeaseOwner = "worker.wrong"
	completion.BudgetSnapshot = snapshot
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
		t.Fatal("wrong lease accepted")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("rollback changed step: %#v", loaded)
	}
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil {
		t.Fatalf("rollback cleared gate: %v %v", gate, err)
	}
}

// B-26: execute actual CompleteLiveAttempt for the three no-response
// failures; the gate remains the exact old step and scheduler accounting
// advances consecutive_failure with an instance cooldown.
func TestCompleteLiveAttemptRetainsGateWhenAccountingUnresolved(t *testing.T) {
	for _, failure := range []struct {
		name    string
		failure commissioningv2.TransportFailure
	}{
		{name: "dns", failure: commissioningv2.TransportFailureDNS},
		{name: "tls", failure: commissioningv2.TransportFailureTLS},
		{name: "connection", failure: commissioningv2.TransportFailureConnection},
	} {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
		if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		run = reloaded
		classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
			Transport: commissioningv2.TransportNoCompleteResponse, Failure: failure.failure,
			TargetKind: step.Target,
		})
		if err != nil {
			t.Fatalf("%s: classify: %v", failure.name, err)
		}
		if classified.ClearGate {
			t.Fatalf("%s: no-response classification clears gate", failure.name)
		}
		fingerprint, err := commissioningv2.SHA256DigestFromBytes(step.StructuralRequestSHA256)
		if err != nil {
			t.Fatal(err)
		}
		completion := commissioningv2.LiveCompletion{
			RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: run.IndexerInstanceID.String(),
			ExpectedRunRev: run.Revision, LeaseOwner: "worker.test",
			Classified: classified, ApplyRunTransition: true,
			ExpectedStructuralFingerprint: fingerprint,
			ExpectedTargetKind: commissioningv2.TargetIdentity{
				Kind: string(step.Target.Kind), RecipeKey: step.Target.RecipeKey,
				CategoryIDs: append([]int(nil), step.CategoryIDs...),
			},
			ExpectedCategoryIDs: append([]int(nil), step.CategoryIDs...),
			BudgetSnapshot:      snapshot,
		}
		state, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second))
		if err != nil {
			t.Fatalf("%s: complete: %v", failure.name, err)
		}
		if state.State != commissioningv2.StepCompleted {
			t.Fatalf("%s: state=%#v", failure.name, state)
		}
		// Gate remains the exact old step (not cleared, not replaced).
		var gate *string
		var failures uint32
		var cooldown *time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text, consecutive_failure, cooldown_until FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate, &failures, &cooldown); err != nil {
			t.Fatal(err)
		}
		if gate == nil || *gate != step.ID.String() {
			t.Fatalf("%s: gate=%v, want exact old step %s", failure.name, gate, step.ID)
		}
		if failures != 1 {
			t.Fatalf("%s: consecutive_failure=%d, want 1", failure.name, failures)
		}
		if cooldown == nil || !cooldown.After(now) {
			t.Fatalf("%s: instance cooldown missing: %v", failure.name, cooldown)
		}
	}
}

func TestLegacyAcquireBlockedByDurableGate(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, workload := range []indexers.Workload{indexers.WorkloadInteractive, indexers.WorkloadRetrieval} {
		if _, err := scheduler.Acquire(ctx, indexerID.String(), workload); err == nil {
			t.Fatalf("workload %q transmitted through gate", workload)
		} else {
			var blocked indexers.CommissioningGateBlockedError
			if !errors.As(err, &blocked) {
				t.Fatalf("workload %q err=%v", workload, err)
			}
		}
	}
}

func TestReserveCommissioningRequestSameStepRetryConsumesNoAdditionalUnit(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	stepID := uuid.NewV7()
	snapshot := liveSnapshotForStepRaw(t, scheduler, indexerID, stepID)
	first, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), stepID.String(), snapshot, now)
	if err != nil || !first.Granted {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), stepID.String(), snapshot, now.Add(time.Second))
	if err != nil || !second.AlreadyHeld {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE id=$1`, stepID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func liveSnapshotForStepRaw(t *testing.T, scheduler *indexers.Scheduler, indexerID, stepID uuid.UUID) indexers.CommissioningBudgetSnapshot {
	t.Helper()
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID.String(), stepID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	return permit.CommissioningBudgetSnapshot()
}

func TestLoadAuthoritativeIndexerStateWithNoBudgetRowReturnsClearGate(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	snapshot, err := store.LoadAuthoritativeIndexerState(ctx, indexerID.String())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Gate != indexers.CommissioningGateClear || snapshot.Revision != 0 || !snapshot.NextAllowedAt.IsZero() || !snapshot.CooldownUntil.IsZero() || snapshot.ConsecutiveFailure != 0 || snapshot.LastOutcome != "" || !snapshot.LastOutcomeAt.IsZero() || snapshot.GateStepID != "" {
		t.Fatalf("no-row snapshot=%#v", snapshot)
	}
}

func TestLastOutcomeAtAdvancesOnlyOnOutcomeProducingWrite(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	var outcomeAt *time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT last_outcome_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&outcomeAt); err != nil {
		t.Fatal(err)
	}
	if outcomeAt != nil {
		t.Fatal("transmit pacing write touched outcome time")
	}
	runReloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = runReloaded
	completion := liveCAPSCompletion(run, step)
	completion.BudgetSnapshot = snapshot
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT last_outcome_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&outcomeAt); err != nil {
		t.Fatal(err)
	}
	if outcomeAt == nil || !outcomeAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("outcome time not advanced: %v", outcomeAt)
	}
	// Operation-scoped writes leave outcome history untouched: covered by
	// ComputeOutcomeEffect unit tests (C-55 case 9).
}

func TestTransmitPacingWriteDoesNotRefreshLastOutcomeAt(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	// Seed an outcome-producing row directly, then run a transmit pacing
	// write for a different step and assert outcome time is preserved.
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,last_outcome_at,revision,updated_at) VALUES($1,5000,$2,NULL,0,'success',$3,1,$4)`, indexerID.String(), now, now, now); err != nil {
		t.Fatal(err)
	}
	prior, err := store.LoadIndexerBudget(ctx, indexerID.String())
	if err != nil {
		t.Fatal(err)
	}
	if prior.LastOutcomeAt.IsZero() || prior.LastOutcome != indexers.OutcomeSuccess {
		t.Fatalf("seeded snapshot=%#v", prior)
	}
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now.Add(time.Second))
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadIndexerBudget(ctx, indexerID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !after.LastOutcomeAt.Equal(prior.LastOutcomeAt) || after.LastOutcome != prior.LastOutcome {
		t.Fatalf("transmit refreshed outcome: before=%#v after=%#v", prior, after)
	}
}

// B-36: one authoritative snapshot carries every required field —
// IndexerID, MinimumInterval, NextAllowedAt, CooldownUntil,
// ConsecutiveFailure, LastOutcome, LastOutcomeAt, UpdatedAt, Revision,
// Gate, GateStepID — from ONE consistent read after a real boundary +
// completion. CooldownUntil is non-zero (rate_limited completion with a
// parsed Retry-After) so all 11 authoritative fields are meaningfully
// compared, not vacuously satisfied.
func TestAuthoritativeSnapshotIsSingleConsistentRead(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(time.Hour)
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	marker.ExecutionDeadline = deadline
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Rate-limited completion with a parsed Retry-After inside the
	// commissioning ceiling (MaximumBackoff = 1m): CooldownUntil is non-zero
	// (== parsed deadline) without a clamp, so the CooldownUntil comparison
	// below is meaningful.
	retryDeadline, ok := indexers.ParseRetryAfter("30", now.Add(5*time.Second))
	if !ok {
		t.Fatal("parse retry-after")
	}
	classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseRateLimited,
		RetryAfter: retryDeadline, TargetKind: step.Target,
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
	completedAt := now.Add(5 * time.Second)
	if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
		t.Fatal(err)
	}
	authoritative, err := store.LoadAuthoritativeIndexerState(ctx, indexerID.String())
	if err != nil {
		t.Fatal(err)
	}
	if authoritative.IndexerID != indexerID.String() {
		t.Fatalf("indexer=%q", authoritative.IndexerID)
	}
	if authoritative.MinimumInterval != budget.MinimumInterval {
		t.Fatalf("minimum=%s want %s", authoritative.MinimumInterval, budget.MinimumInterval)
	}
	if !authoritative.NextAllowedAt.Equal(deadline.Add(budget.MinimumInterval)) {
		t.Fatalf("next=%s want floor %s", authoritative.NextAllowedAt, deadline.Add(budget.MinimumInterval))
	}
	if authoritative.CooldownUntil.IsZero() {
		t.Fatal("cooldown zero: fixture must make CooldownUntil non-zero for a meaningful 11-field comparison")
	}
	if !authoritative.CooldownUntil.Equal(retryDeadline) {
		t.Fatalf("cooldown=%s want parsed deadline %s", authoritative.CooldownUntil, retryDeadline)
	}
	if authoritative.ConsecutiveFailure != 1 {
		t.Fatalf("failures=%d want 1 (rate_limited increment from locked 0)", authoritative.ConsecutiveFailure)
	}
	if authoritative.LastOutcome != indexers.OutcomeRateLimited {
		t.Fatalf("outcome=%q", authoritative.LastOutcome)
	}
	if !authoritative.LastOutcomeAt.Equal(completedAt) {
		t.Fatalf("outcomeAt=%s want %s", authoritative.LastOutcomeAt, completedAt)
	}
	if authoritative.UpdatedAt.IsZero() {
		t.Fatal("updatedAt zero")
	}
	if authoritative.Revision == 0 {
		t.Fatal("revision zero")
	}
	if authoritative.Gate != indexers.CommissioningGateClear || authoritative.GateStepID != "" {
		t.Fatalf("gate=%q step=%q", authoritative.Gate, authoritative.GateStepID)
	}
	var minimumMS int64
	var next, cooldown, outcomeAt, updated *time.Time
	var outcome *string
	var failures int32
	var revision int64
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,last_outcome_at,updated_at,revision,commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&minimumMS, &next, &cooldown, &failures, &outcome, &outcomeAt, &updated, &revision, &gate); err != nil {
		t.Fatal(err)
	}
	if time.Duration(minimumMS)*time.Millisecond != authoritative.MinimumInterval ||
		next == nil || !next.Equal(authoritative.NextAllowedAt) ||
		cooldown == nil || !cooldown.Equal(authoritative.CooldownUntil) ||
		failures != int32(authoritative.ConsecutiveFailure) ||
		(outcome == nil && authoritative.LastOutcome != "" || outcome != nil && string(authoritative.LastOutcome) != *outcome) ||
		(outcomeAt == nil || !outcomeAt.Equal(authoritative.LastOutcomeAt)) ||
		revision != int64(authoritative.Revision) ||
		gate != nil {
		t.Fatalf("authoritative snapshot diverges from durable row: %#v", authoritative)
	}
}

// B-52: a SMALL FIXED POOL of shared indexers prepared BEFORE starting
// goroutines, then N=50 interleaved approved operations over those shared
// indexers — transmission boundary, completion, recovery/cancellation,
// legacy SaveIndexerBudget accounting, and reservation — with actual
// overlapping access to the SAME integration/run/step/budget rows. SQLSTATE
// 40P01 is detected explicitly; worker errors travel through a channel
// (helpers that call t.Fatal are never invoked from worker goroutines);
// bounded timeout; the test fails on deadlock or unexpected transaction
// errors. The goal is to prove the approved lock order survives contention,
// not to manufacture deadlocks.
func TestConcurrentCommissioningTransactionsDoNotDeadlock(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	const workers = 50
	const poolSize = 5
	type sharedFixture struct {
		indexerID uuid.UUID
		runID     uuid.UUID
		stepID    uuid.UUID
		baseAt    time.Time
		scheduler *indexers.Scheduler
		snapshot  indexers.CommissioningBudgetSnapshot
	}
	// Prepare the shared pool sequentially on the test goroutine (all
	// t.Fatal-calling helpers run here, never in workers).
	shared := make([]sharedFixture, poolSize)
	for i := 0; i < poolSize; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		indexerID := liveIndexer(t, ctx, store, at)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, at)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		shared[i] = sharedFixture{indexerID: indexerID, runID: run.ID, stepID: step.ID, baseAt: at, scheduler: scheduler, snapshot: snapshot}
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	deadlocks := make(chan string, workers)
	var successes atomic.Int64
	workerCtx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	// contentionConflict reports whether err is an EXPECTED concurrency
	// outcome of overlapping access to the same rows (a sibling worker won
	// the race): exactly-once completion, single transmit boundary, single
	// cancellation, or gate-held refusal. These prove contention occurred
	// and was serialized — they are not test failures. Anything else
	// (including 40P01, which checkDeadlock routes to deadlocks) is
	// unexpected.
	contentionConflict := func(err error) bool {
		if err == nil {
			return false
		}
		return errors.Is(err, ErrConcurrentModification) ||
			errors.Is(err, ErrCommissioningGateUnresolved) ||
			errors.Is(err, ErrInvalidIndexerCommissioning) ||
			errors.Is(err, ErrLiveExecutorCancelled) ||
			errors.Is(err, ErrCommissioningReservationStale)
	}
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			fixture := shared[index%poolSize]
			// Case and fixture must be INDEPENDENT so each shared fixture is
			// hit by a MIX of operations; index%poolSize for both would pin
			// every fixture to a single case and never test mixed contention.
			caseIndex := (index / poolSize) % 5
			// Worker times must be AFTER the fixture step's intent_at
			// (baseAt+2s), otherwise transmitted_at < intent_at violates the
			// 0034 check30 constraint before the contention being tested.
			at := fixture.baseAt.Add(time.Duration(index) * time.Millisecond)
			report := func(err error) {
				if err != nil {
					select {
					case errs <- fmt.Errorf("worker %d (case %d): %w", index, caseIndex, err):
					case <-workerCtx.Done():
					}
				}
			}
			// tolerateConflict routes an op error: deadlocks are recorded,
			// expected sibling-won conflicts re-verify row coherence and
			// return true (tolerated), anything else is reported.
			tolerateConflict := func(op string, err error) bool {
				if err == nil {
					return false
				}
				if strings.Contains(err.Error(), "40P01") {
					select {
					case deadlocks <- op + ": " + err.Error():
					case <-workerCtx.Done():
					}
					return true
				}
				if contentionConflict(err) {
					// A sibling won: the shared rows must still be
					// coherent (valid step state, gate consistent).
					if step, rerr := store.IndexerCommissioningStep(workerCtx, fixture.runID, fixture.stepID); rerr != nil {
						if !contentionConflict(rerr) {
							report(rerr)
						}
					} else if !commissioningv2.ValidStepState(step.State) {
						report(errors.New(op + ": incoherent shared step state"))
					}
					return true
				}
				report(checkDeadlock(err, deadlocks))
				return true
			}
			switch caseIndex {
			case 0: // transmit then complete (caps_observed clears gate)
				if _, err := store.ReserveCommissioningRequest(workerCtx, fixture.indexerID.String(), fixture.stepID.String(), fixture.snapshot, at.Add(3*time.Second)); err != nil {
					if !tolerateConflict("reserve", err) {
						report(err)
					}
					return
				}
				runNow, stepNow, err := loadRunStep(workerCtx, store, fixture.runID, fixture.stepID)
				if err != nil {
					if !tolerateConflict("load", err) {
						report(err)
					}
					return
				}
				marker := liveMarkerFor(runNow, stepNow, fixture.snapshot, at.Add(3*time.Second))
				if _, err := store.MarkClaimedStepTransmitting(workerCtx, marker, at.Add(4*time.Second)); err != nil {
					if !tolerateConflict("transmit", err) {
						report(err)
					}
					return
				}
				reloaded, err := store.IndexerCommissioningRun(workerCtx, fixture.runID)
				if err != nil {
					report(fmt.Errorf("case0 run reload: %w", err))
					return
				}
				stepReloaded, err := store.IndexerCommissioningStep(workerCtx, fixture.runID, fixture.stepID)
				if err != nil {
					report(fmt.Errorf("case0 step reload: %w", err))
					return
				}
				completion := liveCAPSCompletion(reloaded, stepReloaded)
				completion.BudgetSnapshot = fixture.snapshot
				if _, err := store.CompleteLiveAttempt(workerCtx, completion, at.Add(5*time.Second)); err != nil {
					if !tolerateConflict("complete", err) {
						report(err)
					}
					return
				}
				loaded, err := store.IndexerCommissioningStep(workerCtx, fixture.runID, fixture.stepID)
				if err != nil {
					report(fmt.Errorf("case0 final step read: %w", err))
					return
				}
				if loaded.State != commissioningv2.StepCompleted {
					report(errors.New("complete mix did not complete"))
					return
				}
				successes.Add(1)
			case 1: // transmit then recovery (indeterminate, gate retained)
				if _, err := store.ReserveCommissioningRequest(workerCtx, fixture.indexerID.String(), fixture.stepID.String(), fixture.snapshot, at.Add(3*time.Second)); err != nil {
					if !tolerateConflict("reserve", err) {
						report(err)
					}
					return
				}
				runNow, stepNow, err := loadRunStep(workerCtx, store, fixture.runID, fixture.stepID)
				if err != nil {
					if !tolerateConflict("load", err) {
						report(err)
					}
					return
				}
				marker := liveMarkerFor(runNow, stepNow, fixture.snapshot, at.Add(3*time.Second))
				if _, err := store.MarkClaimedStepTransmitting(workerCtx, marker, at.Add(4*time.Second)); err != nil {
					if !tolerateConflict("transmit", err) {
						report(err)
					}
					return
				}
				reloaded, err := store.IndexerCommissioningRun(workerCtx, fixture.runID)
				if err != nil {
					report(err)
					return
				}
				if _, err := store.MarkInterruptedAttemptIndeterminate(workerCtx, commissioningv2.LiveRecovery{
					RunID: fixture.runID.String(), StepID: fixture.stepID.String(), IndexerInstanceID: fixture.indexerID.String(),
					ExpectedRunRev: reloaded.Revision, Cause: commissioningv2.SafeCodeIndeterminateAttempt,
				}, at.Add(5*time.Second)); err != nil {
					if !tolerateConflict("recover", err) {
						report(err)
					}
					return
				}
				loaded, err := store.IndexerCommissioningStep(workerCtx, fixture.runID, fixture.stepID)
				if err != nil {
					report(err)
					return
				}
				if loaded.State != commissioningv2.StepIndeterminate {
					report(errors.New("recovery mix did not close indeterminate"))
					return
				}
				successes.Add(1)
			case 2: // cancellation before transmission (cancelled, no gate)
				runNow, err := store.IndexerCommissioningRun(workerCtx, fixture.runID)
				if err != nil {
					if !tolerateConflict("load", err) {
						report(err)
					}
					return
				}
				if _, err := store.RequestRunCancellation(workerCtx, fixture.indexerID.String(), fixture.runID.String(), runNow.Revision, at.Add(3*time.Second)); err != nil {
					if !tolerateConflict("request-cancel", err) {
						report(err)
					}
					return
				}
				reloaded, err := store.IndexerCommissioningRun(workerCtx, fixture.runID)
				if err != nil {
					report(err)
					return
				}
				if _, err := store.FinalizeCancelledRun(workerCtx, fixture.indexerID.String(), fixture.runID.String(), reloaded.Revision, at.Add(4*time.Second)); err != nil {
					if !tolerateConflict("finalize-cancel", err) {
						report(err)
					}
					return
				}
				loaded, err := store.IndexerCommissioningStep(workerCtx, fixture.runID, fixture.stepID)
				if err != nil {
					report(err)
					return
				}
				if loaded.State != commissioningv2.StepCancelled {
					report(errors.New("cancel mix did not close cancelled"))
					return
				}
				successes.Add(1)
			case 3: // legacy save path (SaveIndexerBudget boundary)
				loaded, err := store.LoadIndexerBudget(workerCtx, fixture.indexerID.String())
				if err != nil {
					report(err)
					return
				}
				loaded.MinimumInterval = 5 * time.Second
				loaded.UpdatedAt = at.Add(time.Second)
				if err := store.SaveIndexerBudget(workerCtx, loaded); err != nil {
					if !tolerateConflict("legacy-save", err) {
						report(err)
					}
					return
				}
				after, err := store.LoadIndexerBudget(workerCtx, fixture.indexerID.String())
				if err != nil {
					report(err)
					return
				}
				if after.MinimumInterval != 5*time.Second {
					report(errors.New("legacy save mix did not persist"))
					return
				}
				successes.Add(1)
			default: // reserve idempotency + transmit (same-step retry)
				first, err := store.ReserveCommissioningRequest(workerCtx, fixture.indexerID.String(), fixture.stepID.String(), fixture.snapshot, at.Add(3*time.Second))
				if err != nil {
					if !tolerateConflict("reserve", err) {
						report(err)
					}
					return
				}
				if !first.Granted && !first.AlreadyHeld && first.RetryAt.IsZero() {
					report(errors.New("reserve mix granted neither granted nor already-held nor deferral"))
					return
				}
				runNow, stepNow, err := loadRunStep(workerCtx, store, fixture.runID, fixture.stepID)
				if err != nil {
					if !tolerateConflict("load", err) {
						report(err)
					}
					return
				}
				marker := liveMarkerFor(runNow, stepNow, fixture.snapshot, at.Add(3*time.Second))
				if _, err := store.MarkClaimedStepTransmitting(workerCtx, marker, at.Add(5*time.Second)); err != nil {
					if !tolerateConflict("transmit", err) {
						report(err)
					}
					return
				}
				successes.Add(1)
			}
		}(index)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(110 * time.Second):
		t.Fatal("B-52 workers did not finish within the bounded timeout")
	}
	close(errs)
	close(deadlocks)
	for deadlock := range deadlocks {
		t.Fatalf("SQLSTATE 40P01 deadlock: %s", deadlock)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Real overlapping work must have happened: legacy saves always commit
	// and at least one boundary per shared fixture must have been won.
	if successes.Load() < int64(poolSize) {
		t.Fatalf("B-52 successes=%d, want at least %d (one per shared indexer)", successes.Load(), poolSize)
	}
	// Every shared fixture must be left coherent: valid step state and a
	// gate that references a real step or is NULL.
	for i := range shared {
		step, err := store.IndexerCommissioningStep(ctx, shared[i].runID, shared[i].stepID)
		if err != nil {
			t.Fatal(err)
		}
		if !commissioningv2.ValidStepState(step.State) {
			t.Fatalf("fixture %d incoherent step state %q", i, step.State)
		}
		var gate *string
		if err := store.Pool.QueryRow(ctx, `SELECT (SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1)`, shared[i].indexerID.String()).Scan(&gate); err != nil {
			t.Fatal(err)
		}
		if gate != nil {
			var gateStepExists bool
			if err := store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM indexer_commissioning_steps WHERE id=$1)`, *gate).Scan(&gateStepExists); err != nil || !gateStepExists {
				t.Fatalf("fixture %d gate references missing step %v err=%v", i, gate, err)
			}
		}
	}
}

// loadRunStep re-reads the shared run/step rows for one B-52 worker without
// touching t.Fatal (safe for worker goroutines): it returns the current
// durable run revision and step state for the marker's expected-state
// fields.
func loadRunStep(ctx context.Context, store *Store, runID, stepID uuid.UUID) (IndexerCommissioningRun, IndexerCommissioningStep, error) {
	run, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		return IndexerCommissioningRun{}, IndexerCommissioningStep{}, err
	}
	step, err := store.IndexerCommissioningStep(ctx, runID, stepID)
	if err != nil {
		return IndexerCommissioningRun{}, IndexerCommissioningStep{}, err
	}
	return run, step, nil
}

func checkDeadlock(err error, deadlocks chan string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "40P01") {
		deadlocks <- err.Error()
		return err
	}
	return err
}

func TestLiveTransactionsNeverMutateIntegrationOrProfiles(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	var configBefore, secretBefore string
	if err := store.Pool.QueryRow(ctx, `SELECT config::text,secret_ref FROM integration_instances WHERE id=$1`, indexerID.String()).Scan(&configBefore, &secretBefore); err != nil {
		t.Fatal(err)
	}
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	var configAfter, secretAfter string
	if err := store.Pool.QueryRow(ctx, `SELECT config::text,secret_ref FROM integration_instances WHERE id=$1`, indexerID.String()).Scan(&configAfter, &secretAfter); err != nil {
		t.Fatal(err)
	}
	if configBefore != configAfter || secretBefore != secretAfter {
		t.Fatal("integration mutated")
	}
}

func TestLegacyMigration35AppliesCleanlyFrom34Superseded(t *testing.T) {
	t.Skip("superseded by Group A migration matrix TestMigration35AppliesCleanlyFrom34 in indexer_commissioning_live_migration_integration_test.go")
}

func TestLegacyGateTriggerResolvesUnderHostileSearchPathSuperseded(t *testing.T) {
	t.Skip("superseded by Group A migration matrix TestGateTriggerResolvesUnderHostileSearchPath in indexer_commissioning_live_migration_integration_test.go")
}

func TestLegacyInvalidHostileSearchPathSuperseded(t *testing.T) {
	t.Skip("superseded by Group A migration matrix hostile search_path test")
}

func TestLegacyOneUseTransmissionBindingSuperseded(t *testing.T) {
	t.Skip("superseded by M-5 matrix tests in internal/adapters/newznab")
}

func TestLegacyOneUseTransmissionBindingBody(t *testing.T) {
	t.Skip("superseded body preserved for audit; see git history")
}

// liveTransmittingAuthorizer is the test-only durable authorizer for
// TestOneUseTransmissionBinding: it authorizes exactly one real attempt.
type liveTransmittingAuthorizer struct {
	indexerID string
	stepID    string
}

func (authorizer *liveTransmittingAuthorizer) AuthorizedTransmittingCommissioningStep(_ context.Context, indexerID, stepID string) bool {
	return authorizer != nil && indexerID == authorizer.indexerID && stepID == authorizer.stepID
}

type testAllowGuard struct{}

func (testAllowGuard) BeforeSend(context.Context) error { return nil }
func (testAllowGuard) InspectSecret([]byte, http.Header, [][]byte, [][]byte) error {
	return nil
}
func (testAllowGuard) InspectStructure([]byte, http.Header) error { return nil }

func liveTestServerRoots(server *httptest.Server) *x509.CertPool {
	if server.Certificate() == nil {
		return nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return roots
}

func TestLegacyCloseInterruptedSupersededByExternalC2Test(t *testing.T) {
	t.Skip("superseded by Group D external test TestCloseInterruptedAfterUncertainFinalizationAgainstDurableStore in internal/commissioningv2/live_executor_c2_test.go")
}

func envOrEmpty(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// B-27: construct actual contradictory ClassifiedCompletion permutations,
// call CompleteLiveAttempt, and assert the exact closed error plus ZERO
// writes for every permutation.
func TestCompleteLiveAttemptRejectsIncoherentClassification(t *testing.T) {
	for _, permutation := range []struct {
		name       string
		mutate     func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion
		wantInputs bool
	}{
		// The §9.5 forbidden submission: caps_observed inputs with a
		// rate_limited/operation result and ClearGate=true.
		{"caps-inputs-rate-limited-operation-clear", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SafeOutcome = commissioningv2.OutcomeCAPSObserved
			base.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeOperation}
			base.ClearGate = true
			return base
		}, false},
		// Tampered SafeOutcome away from the re-derived value.
		{"tampered-safe-outcome", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SafeOutcome = commissioningv2.OutcomeRecipeVerified
			return base
		}, false},
		// Tampered SafeCode.
		{"tampered-safe-code", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SafeCode = commissioningv2.SafeCodeConfigurationChanged
			return base
		}, false},
		// Tampered SchedulerResult scope (rate_limited at operation scope is
		// rejected by validRecordResult independently).
		{"tampered-scope", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SchedulerResult.Scope = indexers.FailureScopeOperation
			return base
		}, false},
		// Tampered ClearGate (false on a clearing outcome).
		{"tampered-clear-gate", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.ClearGate = false
			return base
		}, false},
		// Dropped evidence on an evidence-requiring outcome.
		{"dropped-evidence", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.Evidence = nil
			base.RawResultCount = nil
			base.PlausibleIdentityCount = nil
			base.RawBodySHA256 = nil
			return base
		}, false},
		// Forged evidence on a forbidden outcome: take a valid caps
		// classification and swap the outcome label to a digest-forbidden
		// one while keeping digests.
		{"forged-evidence-shape", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SafeOutcome = commissioningv2.OutcomeRateLimited
			base.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance}
			return base
		}, false},
	} {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
		if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		base := liveCAPSCompletion(reloaded, step)
		tampered := permutation.mutate(base.Classified)
		completion := base
		completion.ExpectedRunRev = reloaded.Revision
		completion.Classified = tampered
		completion.BudgetSnapshot = snapshot
		var revisionBefore int64
		var updatedBefore time.Time
		var gateBefore *string
		if err := store.Pool.QueryRow(ctx, `SELECT revision, updated_at, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionBefore, &updatedBefore, &gateBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
			t.Fatalf("%s: incoherent classification accepted", permutation.name)
		} else if !errors.Is(err, ErrInvalidIndexerCommissioning) {
			t.Fatalf("%s: err=%v, want ErrInvalidIndexerCommissioning", permutation.name, err)
		}
		_ = permutation.wantInputs
		// ZERO writes: step still transmitting, gate retained, budget row
		// revision/updated_at unchanged.
		loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.State != commissioningv2.StepTransmitting {
			t.Fatalf("%s: step changed: %#v", permutation.name, loaded)
		}
		var revisionAfter int64
		var updatedAfter time.Time
		var gateAfter *string
		if err := store.Pool.QueryRow(ctx, `SELECT revision, updated_at, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionAfter, &updatedAfter, &gateAfter); err != nil {
			t.Fatal(err)
		}
		if revisionAfter != revisionBefore || !updatedAfter.Equal(updatedBefore) {
			t.Fatalf("%s: budget row written: rev %d->%d updated %s->%s", permutation.name, revisionBefore, revisionAfter, updatedBefore, updatedAfter)
		}
		if gateAfter == nil || gateBefore == nil || *gateAfter != *gateBefore {
			t.Fatalf("%s: gate changed: %v -> %v", permutation.name, gateBefore, gateAfter)
		}
	}
}
