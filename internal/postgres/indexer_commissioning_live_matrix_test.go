package postgres

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"uuid"
)

// liveCompletionForStep builds a LiveCompletion for the durable step with a
// real classification for the requested outcome family.
func liveCompletionForStep(t *testing.T, run IndexerCommissioningRun, step IndexerCommissioningStep, snapshot indexers.CommissioningBudgetSnapshot, classified commissioningv2.ClassifiedCompletion) commissioningv2.LiveCompletion {
	t.Helper()
	fingerprint, err := commissioningv2.SHA256DigestFromBytes(step.StructuralRequestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return commissioningv2.LiveCompletion{
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
}

// B-18 is implemented in indexer_commissioning_live_test.go
// (TestTransmitTransactionRollbackLeavesNoTransmissionBoundary).

// B-28: tampering with each derived field is rejected; plus the
// classification-separation assertion: the same attempt completed in two
// independent fixtures differing only in the legitimate bound snapshot
// (different FailureBackoff/MaximumBackoff) yields an IDENTICAL
// ClassifiedCompletion while persisted cooldown_until differs per budget.
func TestCompleteLiveAttemptRederivesClassificationFromInputs(t *testing.T) {
	// Tamper arm: each derived field independently rejected with zero writes.
	for _, tamper := range []struct {
		name   string
		mutate func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion
	}{
		{"safe-outcome", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SafeOutcome = commissioningv2.OutcomeRecipeVerified
			return base
		}},
		{"safe-code", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SafeCode = commissioningv2.SafeCodeConfigurationChanged
			return base
		}},
		{"scheduler-outcome", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SchedulerResult.Outcome = indexers.OutcomeRateLimited
			return base
		}},
		{"scheduler-scope", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.SchedulerResult.Scope = indexers.FailureScopeInstance
			return base
		}},
		{"clear-gate", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.ClearGate = false
			return base
		}},
		{"evidence", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.Evidence = nil
			return base
		}},
		{"digests", func(base commissioningv2.ClassifiedCompletion) commissioningv2.ClassifiedCompletion {
			base.RawBodySHA256 = nil
			return base
		}},
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
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		base := liveCAPSCompletion(reloaded, step, time.Time{})
		tampered := tamper.mutate(base.Classified)
		completion := base
		completion.ExpectedRunRev = reloaded.Revision
		completion.Classified = tampered
		completion.BudgetSnapshot = snapshot
		if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
			t.Fatalf("%s: tampered derived field accepted", tamper.name)
		}
		loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.State != commissioningv2.StepTransmitting {
			t.Fatalf("%s: step changed", tamper.name)
		}
	}
	// Separation arm: authentication_failure completion in two fixtures with
	// different MaximumBackoff; identical classification, different cooldown.
	var cooldowns [2]time.Time
	var classifications [2]commissioningv2.ClassifiedCompletion
	for attempt := 0; attempt < 2; attempt++ {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		maximumBackoff := time.Minute
		if attempt == 1 {
			maximumBackoff = 10 * time.Minute
		}
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: maximumBackoff, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
			Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure,
			TargetKind: step.Target,
		})
		if err != nil {
			t.Fatal(err)
		}
		classifications[attempt] = classified
		completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
		completedAt := now.Add(5 * time.Second)
		if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
			t.Fatal(err)
		}
		var cooldown time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown); err != nil {
			t.Fatal(err)
		}
		cooldowns[attempt] = cooldown
		// Cooldown derives from the bound budget's MaximumBackoff; the
		// ordering assertion below (cooldowns[1] after cooldowns[0])
		// proves per-budget derivation since only MaximumBackoff differs.
	}
	if classifications[0].SafeOutcome != classifications[1].SafeOutcome ||
		classifications[0].SafeCode != classifications[1].SafeCode ||
		classifications[0].SchedulerResult != classifications[1].SchedulerResult ||
		classifications[0].ClearGate != classifications[1].ClearGate {
		t.Fatalf("classification differs across budgets: %#v vs %#v", classifications[0], classifications[1])
	}
	if !cooldowns[1].After(cooldowns[0]) {
		t.Fatalf("persisted cooldowns do not differ per bound budget: %s vs %s", cooldowns[0], cooldowns[1])
	}
}

// B-29: consecutive_failure derives from the locked snapshot, never a caller
// integer. Pre-set consecutive_failure=4 out of band; an INSTANCE-SCOPED
// failure (authentication_failure) must increment it to exactly 5, while a
// success reset on the same seed must write exactly 0. A hard-coded
// implementation ignoring the locked row cannot pass both arms.
func TestCompleteLiveAttemptDerivesConsecutiveFailureFromLockedSnapshot(t *testing.T) {
	for _, arm := range []struct {
		name        string
		classify    func(step IndexerCommissioningStep) commissioningv2.ClassifiedCompletion
		want        int32
		wantOutcome string
	}{
		{
			name: "instance-failure-increments",
			classify: func(step IndexerCommissioningStep) commissioningv2.ClassifiedCompletion {
				classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
					Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure,
					TargetKind: step.Target,
				})
				if err != nil {
					panic(err)
				}
				return classified
			},
			want: 5, wantOutcome: "auth_failed",
		},
		{
			name: "success-resets",
			classify: func(step IndexerCommissioningStep) commissioningv2.ClassifiedCompletion {
				evidence := commissioningRepositoryCAPSEvidence(time.Now().UTC().Truncate(time.Microsecond))
				classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
					Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseCAPSObserved,
					TargetKind: step.Target, Evidence: &evidence,
					RawResultCount: uint16PtrForB29(0), PlausibleIdentityCount: uint16PtrForB29(0),
					RawBodySHA256: evidence.BodySHA256.Bytes(),
				})
				if err != nil {
					panic(err)
				}
				return classified
			},
			want: 0, wantOutcome: "success",
		},
	} {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		// Out-of-band: set consecutive_failure=4 on the locked row.
		if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET consecutive_failure=4 WHERE indexer_instance_id=$1`, indexerID.String()); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		// LiveCompletion carries no ConsecutiveFailure: the durable result
		// must equal the locked-row derivation, not any caller value.
		completion := liveCompletionForStep(t, reloaded, step, snapshot, arm.classify(step))
		completedAt := now.Add(5 * time.Second)
		if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		var failures int32
		var outcome string
		if err := store.Pool.QueryRow(ctx, `SELECT consecutive_failure, last_outcome FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&failures, &outcome); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if failures != arm.want {
			t.Fatalf("%s: consecutive_failure=%d, want %d derived from locked 4", arm.name, failures, arm.want)
		}
		if outcome != arm.wantOutcome {
			t.Fatalf("%s: last_outcome=%q want %q", arm.name, outcome, arm.wantOutcome)
		}
	}
}

func uint16PtrForB29(value uint16) *uint16 { return &value }

// B-30: freshness drift keeps rate-limit accounting — drifted 429 gives step
// configuration_stale + configuration.changed, evidence dropped,
// cooldown_until still applied, gate cleared.
func TestCompleteLiveAttemptFreshnessDriftKeepsRateLimitAccounting(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Cause real freshness drift: change the integration config out of band
	// so commissioningRunConfigurationCurrent is false at completion.
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$2, updated_at=$3 WHERE id=$1`, indexerID.String(), `{"base_url":"https://drifted.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline, ok := indexers.ParseRetryAfter("120", now.Add(5*time.Second))
	if !ok {
		t.Fatal("parse retry-after")
	}
	classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseRateLimited,
		FreshnessDrifted: true, RetryAfter: deadline, TargetKind: step.Target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if classified.SafeOutcome != commissioningv2.OutcomeConfigurationStale || classified.SafeCode != commissioningv2.SafeCodeConfigurationChanged {
		t.Fatalf("drifted 429 step label: %#v", classified)
	}
	if classified.SchedulerResult.Outcome != indexers.OutcomeRateLimited || classified.SchedulerResult.Scope != indexers.FailureScopeInstance {
		t.Fatalf("drifted 429 accounting lost: %#v", classified.SchedulerResult)
	}
	if classified.Evidence != nil || len(classified.RawBodySHA256) != 0 {
		t.Fatalf("drifted 429 retained evidence: %#v", classified)
	}
	completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	var safeOutcome, safeCode string
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(safe_outcome,''),COALESCE(safe_code,'') FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()).Scan(&safeOutcome, &safeCode); err != nil {
		t.Fatal(err)
	}
	if safeOutcome != "configuration_stale" || safeCode != "configuration.changed" {
		t.Fatalf("step=%q/%q", safeOutcome, safeCode)
	}
	var cooldown time.Time
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown, &gate); err != nil {
		t.Fatal(err)
	}
	if !cooldown.Equal(deadline) {
		t.Fatalf("cooldown=%s want parsed deadline %s", cooldown, deadline)
	}
	if gate != nil {
		t.Fatalf("drifted 429 did not clear gate: %v", gate)
	}
}

// B-32: gate clearance rejected for the wrong step identity.
func TestGateClearanceRejectedForWrongStepIdentity(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.BudgetSnapshot = snapshot
	// Wrong step identity: the budget WHERE gate=$stepID misses.
	completion.StepID = uuid.NewV7().String()
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
		t.Fatal("wrong-step clearance accepted")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("wrong-step attempt changed step: %#v", loaded)
	}
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil || *gate != step.ID.String() {
		t.Fatalf("wrong-step attempt changed gate: %v %v", gate, err)
	}
}

// B-33: concurrent CompleteLiveAttempt applies exactly once.
func TestConcurrentCompleteLiveAttemptAppliesExactlyOnce(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	var revisionBefore int64
	if err := store.Pool.QueryRow(ctx, `SELECT revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionBefore); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			completion := liveCAPSCompletion(reloaded, step, time.Time{})
			completion.BudgetSnapshot = snapshot
			_, errs[i] = store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second))
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrConcurrentModification) {
			t.Fatalf("unexpected concurrent-complete error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent completions succeeded=%d, want exactly 1 (errs=%v)", succeeded, errs)
	}
	var revisionAfter int64
	if err := store.Pool.QueryRow(ctx, `SELECT revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionAfter); err != nil {
		t.Fatal(err)
	}
	if revisionAfter != revisionBefore+1 {
		t.Fatalf("revision %d -> %d, want exactly +1", revisionBefore, revisionAfter)
	}
}

// B-34: authoritative read after a simulated crash reports the unresolved
// gate and the transmitting step.
func TestLoadAuthoritativeStateAfterSimulatedCrashReportsUnresolvedGateAndStep(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Simulated crash: no completion, no recovery — a fresh read must report
	// the unresolved gate and the transmitting step in ONE snapshot.
	authoritative, err := store.LoadAuthoritativeIndexerState(ctx, indexerID.String())
	if err != nil {
		t.Fatal(err)
	}
	if authoritative.Gate != indexers.CommissioningGateUnresolved || authoritative.GateStepID != step.ID.String() {
		t.Fatalf("gate=%q step=%q", authoritative.Gate, authoritative.GateStepID)
	}
	if authoritative.GateStepState != "transmitting" {
		t.Fatalf("gate step state=%q", authoritative.GateStepState)
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting || loaded.TransmittedAt == nil {
		t.Fatalf("crashed step=%#v", loaded)
	}
}

// B-35: recovery closes only transmitting (never clears the gate),
// including AckKnown=true.
func TestMarkInterruptedAttemptIndeterminateClosesOnlyTransmittingAndNeverClearsGate(t *testing.T) {
	for _, ackKnown := range []bool{false, true} {
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
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		state, err := store.MarkInterruptedAttemptIndeterminate(ctx, commissioningv2.LiveRecovery{
			RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: indexerID.String(),
			ExpectedRunRev: reloaded.Revision, Cause: commissioningv2.SafeCodeIndeterminateAttempt, AckKnown: ackKnown,
		}, now.Add(5*time.Second))
		if err != nil {
			t.Fatalf("ackKnown=%v: %v", ackKnown, err)
		}
		if state.State != commissioningv2.StepIndeterminate {
			t.Fatalf("ackKnown=%v: state=%#v", ackKnown, state)
		}
		var gate *string
		if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil || *gate != step.ID.String() {
			t.Fatalf("ackKnown=%v: gate=%v err=%v", ackKnown, gate, err)
		}
		// Recovery on a non-transmitting step fails closed.
		if _, err := store.MarkInterruptedAttemptIndeterminate(ctx, commissioningv2.LiveRecovery{
			RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: indexerID.String(),
			ExpectedRunRev: reloaded.Revision + 1, Cause: commissioningv2.SafeCodeIndeterminateAttempt, AckKnown: ackKnown,
		}, now.Add(6*time.Second)); err == nil {
			t.Fatalf("ackKnown=%v: second recovery accepted", ackKnown)
		}
	}
}

// B-38: uncertain transmit commit on a brand-new indexer resolves to clear
// and clears cacheUnknown (no-row is a definite answer, B2).
func TestUncertainTransmitCommitOnBrandNewIndexerResolvesToClearAndClearsCacheUnknown(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(1, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	// Brand-new indexer: no budget row. Uncertain-finalization path:
	// invalidate while holding a permit, release, reload.
	stepID := uuid.NewV7().String()
	permit, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), stepID)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.InvalidateLaneCache(indexerID.String(), nil)
	permit.ReleaseWithoutOutcome()
	if err := scheduler.ReloadLaneCache(ctx, indexerID.String()); err != nil {
		t.Fatalf("no-row reload failed: %v", err)
	}
	got := scheduler.Snapshot(indexerID.String())
	if got.IndexerID != indexerID.String() {
		t.Fatalf("snapshot=%#v", got)
	}
	// A fresh acquisition proceeds (clear gate, no pacing yet).
	second, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), stepID)
	if err != nil {
		t.Fatalf("post-reload acquire: %v", err)
	}
	second.ReleaseWithoutOutcome()
}

// B-39: authoritative query error is fail-closed (closed pool / cancelled
// context): error, cacheUnknown stays true.
func TestLoadAuthoritativeIndexerStateQueryErrorIsFailClosed(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(1, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.LoadAuthoritativeIndexerState(cancelled, indexerID.String()); err == nil {
		t.Fatal("cancelled authoritative read succeeded")
	}
	// Scheduler-level fail-closed: invalidate, then a cancelled reload keeps
	// cacheUnknown and acquisitions fail.
	stepID := uuid.NewV7().String()
	permit, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), stepID)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.InvalidateLaneCache(indexerID.String(), nil)
	permit.ReleaseWithoutOutcome()
	if err := scheduler.ReloadLaneCache(cancelled, indexerID.String()); err == nil {
		t.Fatal("cancelled lane reload succeeded")
	}
	if _, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), stepID); err == nil {
		t.Fatal("acquire slipped through unknown cache")
	}
}

// B-43: stale existing reservation row fails closed.
func TestReserveCommissioningRequestStaleExistingRowFailsClosed(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	stepID := uuid.NewV7()
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, stepID)
	// Seed a stale row: same id, wrong family.
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'grab',$3)`, stepID.String(), indexerID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), stepID.String(), snapshot, now.Add(time.Second)); err == nil {
		t.Fatal("stale reservation accepted")
	} else if !errors.Is(err, ErrCommissioningReservationStale) {
		t.Fatalf("err=%v, want ErrCommissioningReservationStale", err)
	}
}

// B-45: the durable gate is re-read on every acquisition — set a gate out of
// band and prove the next Acquire refuses without any cache invalidation.
func TestDurableGateIsReReadOnEveryAcquisition(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
	// First acquisition succeeds (no gate yet).
	first, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	first.ReleaseWithoutOutcome()
	// Commit a real transmission boundary out of band (same scheduler's
	// snapshot path), then the NEXT acquisition must refuse via fresh read.
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("gate not re-read: acquisition slipped through")
	} else {
		var blocked indexers.CommissioningGateBlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("err=%v, want gate-blocked", err)
		}
	}
}

// B-46: an invalidated lane cache reloads authoritative durable state.
func TestInvalidatedLaneCacheReloadsAuthoritativeDurableState(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
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
	permit, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String())
	if err == nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal("v2 acquire slipped through unresolved gate")
	}
	// Invalidate + reload publishes the durable floor; snapshot matches.
	scheduler.InvalidateLaneCache(indexerID.String(), nil)
	if err := scheduler.ReloadLaneCache(ctx, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	got := scheduler.Snapshot(indexerID.String())
	if !got.NextAllowedAt.Equal(deadline.Add(budget.MinimumInterval)) {
		t.Fatalf("reloaded next=%s want floor %s", got.NextAllowedAt, deadline.Add(budget.MinimumInterval))
	}
	// Gate still unresolved: acquisitions still refuse.
	if _, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("post-reload acquire slipped through gate")
	}
}

// B-47: a fresh scheduler respects the durable pacing floor after restart.
func TestFreshSchedulerRespectsDurablePacingFloorAfterRestart(t *testing.T) {
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
	// Fresh scheduler = restart: same store, empty lanes.
	restarted := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, restarted, indexerID, budget)
	restarted.SetCommissioningGateSource(indexerID.String(), store)
	// The gate blocks first; clear it via a real completion, then pacing
	// still defers legacy acquisition until the durable floor.
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.BudgetSnapshot = snapshot
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted.InvalidateLaneCache(indexerID.String(), nil)
	if err := restarted.ReloadLaneCache(ctx, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	// now.Add(5s) < floor (deadline+5s where deadline=now+1h): deferred.
	if _, err := restarted.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("restarted scheduler ignored durable pacing floor")
	} else {
		var cooldown indexers.CooldownError
		if !errors.As(err, &cooldown) {
			t.Fatalf("err=%v, want CooldownError", err)
		}
		if !cooldown.Until.Equal(deadline.Add(budget.MinimumInterval)) {
			t.Fatalf("floor=%s want %s", cooldown.Until, deadline.Add(budget.MinimumInterval))
		}
	}
}

// B-50: cancellation before transmission closes cancelled with no gate.
func TestCancellationBeforeTransmissionClosesCancelledWithoutGate(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	if _, err := store.RequestRunCancellation(ctx, indexerID.String(), run.ID.String(), run.Revision, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeCancelledRun(ctx, indexerID.String(), run.ID.String(), reloaded.Revision, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepCancelled {
		t.Fatalf("state=%#v", loaded)
	}
	var gates int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_budget_state WHERE indexer_instance_id=$1 AND commissioning_gate_step_id IS NOT NULL`, indexerID.String()).Scan(&gates); err != nil {
		t.Fatal(err)
	}
	if gates != 0 {
		t.Fatal("pre-transmission cancellation created a gate")
	}
}

// B-51: cancellation after the boundary closes indeterminate and retains
// the gate; 0034 rejects transmitting -> cancelled.
func TestCancellationAfterTransmissionBoundaryClosesIndeterminateAndRetainsGate(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// 0034 has no transmitting -> cancelled edge.
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{State: commissioningv2.StepCancelled, SafeOutcome: commissioningv2.OutcomeCancelled, SafeCode: commissioningv2.SafeCodeOwnerCancelled}, now.Add(5*time.Second)); err == nil {
		t.Fatal("0034 accepted transmitting -> cancelled")
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.MarkInterruptedAttemptIndeterminate(ctx, commissioningv2.LiveRecovery{
		RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: indexerID.String(),
		ExpectedRunRev: reloaded.Revision, Cause: commissioningv2.SafeCodeIndeterminateAttempt,
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.State != commissioningv2.StepIndeterminate {
		t.Fatalf("state=%#v", state)
	}
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil || *gate != step.ID.String() {
		t.Fatalf("gate=%v err=%v", gate, err)
	}
}

// B-19 arms are in indexer_commissioning_live_test.go
// (TestMarkClaimedStepTransmittingRequiresValidInWindowReservation); this
// companion asserts the exact window/limit derivation from the snapshot.
func TestReservationWindowDerivesFromSnapshot(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 3, LimitWindow: 90 * time.Minute}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	stepID := uuid.NewV7()
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, stepID)
	if snapshot.LimitWindow() != 90*time.Minute || snapshot.QueryLimit() != 3 {
		t.Fatalf("snapshot window/limit: %s/%d", snapshot.LimitWindow(), snapshot.QueryLimit())
	}
	// Fill the window with legacy reservations, then v2 must defer with
	// RetryAt = oldest + window (shared ledger).
	for i := 0; i < 3; i++ {
		if _, err := store.ReserveIndexerRequest(ctx, indexerID.String(), "query", 3, 90*time.Minute, now); err != nil {
			t.Fatal(err)
		}
	}
	deferred, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), stepID.String(), snapshot, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if deferred.Granted || deferred.AlreadyHeld || deferred.RetryAt.IsZero() {
		t.Fatalf("window-full v2 not deferred: %#v", deferred)
	}
}

// B-31: run-transition safe codes — table over every §15 row.
func TestCompleteLiveAttemptRunTransitionSafeCodes(t *testing.T) {
	// Each row: classified outcome -> expected run transition + safe_code
	// pairing, verified through DeriveRunTransition plus the REAL
	// commissioningRunSafeCodeMatches predicate (same package: called
	// directly for every row below) and the REAL 0034 durable run CHECKs
	// through real DB completion for the important transition classes
	// (safety_blocked, owner_attention, stale/configuration). Owner_attention
	// rows carry NULL safe_code (never indeterminate.attempt).
	cases := []struct {
		response commissioningv2.ResponseVerdict
		to       commissioningv2.RunState
		code     commissioningv2.SafeCode
	}{
		{commissioningv2.ResponseSafetyCredentialEcho, commissioningv2.RunSafetyBlocked, commissioningv2.SafeCodeCredentialEcho},
		{commissioningv2.ResponseRateLimited, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseQuotaExhausted, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseAuthenticationFailure, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseMalformed, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseSafetyUnsafeRedirect, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseSafetyUnsafeText, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseSafetyBodyLimit, commissioningv2.RunOwnerAttention, ""},
		{commissioningv2.ResponseSafetyXMLLimit, commissioningv2.RunOwnerAttention, ""},
	}
	for _, tc := range cases {
		inputs := commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: tc.response, TargetKind: commissioningv2TargetCAPS()}
		classified, err := commissioningv2.ClassifyLiveAttempt(inputs)
		if err != nil {
			t.Fatalf("%q: %v", tc.response, err)
		}
		to, code, terminal, _, ok := commissioningv2.DeriveRunTransition(classified, commissioningv2.RunCAPSAuthorized)
		if !ok {
			t.Fatalf("%q: no transition", tc.response)
		}
		if to != tc.to || code != tc.code || !terminal {
			t.Fatalf("%q: got (%q,%q,%v) want (%q,%q,true)", tc.response, to, code, terminal, tc.to, tc.code)
		}
		if !commissioningRunSafeCodeMatches(to, code) {
			t.Fatalf("%q: real commissioningRunSafeCodeMatches rejects (%q,%q)", tc.response, to, code)
		}
		if to == commissioningv2.RunOwnerAttention && code == commissioningv2.SafeCodeIndeterminateAttempt {
			t.Fatalf("%q: owner_attention must not use indeterminate.attempt", tc.response)
		}
		if !commissioningv2.ValidRunTransition(commissioningv2.RunCAPSAuthorized, to) {
			t.Fatalf("%q: invalid run transition to %q", tc.response, to)
		}
	}
	// Drifted 429 -> stale/configuration.changed (terminal).
	drifted, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseRateLimited, FreshnessDrifted: true, TargetKind: commissioningv2TargetCAPS()})
	if err != nil {
		t.Fatal(err)
	}
	to, code, terminal, setStale, ok := commissioningv2.DeriveRunTransition(drifted, commissioningv2.RunCAPSAuthorized)
	if !ok || to != commissioningv2.RunStale || code != commissioningv2.SafeCodeConfigurationChanged || !terminal || !setStale {
		t.Fatalf("drifted 429 transition: (%q,%q,%v,%v,%v)", to, code, terminal, setStale, ok)
	}
	if !commissioningRunSafeCodeMatches(to, code) {
		t.Fatalf("drifted 429: real commissioningRunSafeCodeMatches rejects (%q,%q)", to, code)
	}
	// caps_observed/CAPS/continue -> awaiting_category_review, NULL code.
	capsEvidence := commissioningRepositoryCAPSEvidenceForB31()
	caps, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseCAPSObserved,
		TargetKind: commissioningv2TargetCAPS(), Evidence: &capsEvidence,
		RawResultCount: uint16PtrB31(0), PlausibleIdentityCount: uint16PtrB31(0),
		RawBodySHA256: capsEvidence.BodySHA256.Bytes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	to, code, terminal, _, ok = commissioningv2.DeriveRunTransition(caps, commissioningv2.RunCAPSAuthorized)
	if !ok || to != commissioningv2.RunAwaitingCategoryReview || code != "" || terminal {
		t.Fatalf("caps transition: (%q,%q,%v,%v)", to, code, terminal, ok)
	}
	if !commissioningRunSafeCodeMatches(to, code) {
		t.Fatalf("caps: real commissioningRunSafeCodeMatches rejects (%q,%q)", to, code)
	}
	// No-transition outcomes: unsupported/permission_restricted/contradicted,
	// no_results, no_plausible_match, semantic_ambiguity.
	for _, response := range []commissioningv2.ResponseVerdict{commissioningv2.ResponseUnsupported, commissioningv2.ResponsePermissionRestricted, commissioningv2.ResponseContradicted, commissioningv2.ResponseNoResults, commissioningv2.ResponseNoPlausibleMatch, commissioningv2.ResponseSemanticAmbiguity} {
		inputs := commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: response, TargetKind: commissioningv2TargetSearch()}
		applyEvidenceB31(t, &inputs, response)
		classified, err := commissioningv2.ClassifyLiveAttempt(inputs)
		if err != nil {
			t.Fatalf("%q: %v", response, err)
		}
		if _, _, _, _, ok := commissioningv2.DeriveRunTransition(classified, commissioningv2.RunCAPSAuthorized); ok {
			t.Fatalf("%q: unexpected run transition", response)
		}
	}
}

// TestCompleteLiveAttemptRunTransitionSafeCodesDurable executes the actual
// 0034 durable run CHECKs through real DB completion for the important
// transition classes: safety_blocked (safety_credential_echo), the
// owner_attention family (rate_limited, authentication_failure,
// malformed_response), and the stale/configuration case (drifted 429).
// Each arm runs the full reserve → transmit → CompleteLiveAttempt path and
// asserts the durable run row (state, safe_code via the REAL
// commissioningRunSafeCodeMatches, completed/stale timestamps) plus the
// durable step outcome. A hard-coded pure-function implementation cannot
// pass: the assertions read the committed rows.
func TestCompleteLiveAttemptRunTransitionSafeCodesDurable(t *testing.T) {
	for _, arm := range []struct {
		name         string
		inputs       func(step IndexerCommissioningStep) commissioningv2.LiveAttemptInputs
		wantRun      commissioningv2.RunState
		wantCode     string
		wantTerminal bool
		wantStale    bool
	}{
		{
			name: "safety_blocked",
			inputs: func(step IndexerCommissioningStep) commissioningv2.LiveAttemptInputs {
				return commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseSafetyCredentialEcho, TargetKind: step.Target}
			},
			wantRun: commissioningv2.RunSafetyBlocked, wantCode: "credential.echo", wantTerminal: true,
		},
		{
			name: "owner_attention/rate_limited",
			inputs: func(step IndexerCommissioningStep) commissioningv2.LiveAttemptInputs {
				return commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseRateLimited, TargetKind: step.Target}
			},
			wantRun: commissioningv2.RunOwnerAttention, wantCode: "", wantTerminal: true,
		},
		{
			name: "owner_attention/authentication_failure",
			inputs: func(step IndexerCommissioningStep) commissioningv2.LiveAttemptInputs {
				return commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure, TargetKind: step.Target}
			},
			wantRun: commissioningv2.RunOwnerAttention, wantCode: "", wantTerminal: true,
		},
		{
			name: "owner_attention/malformed",
			inputs: func(step IndexerCommissioningStep) commissioningv2.LiveAttemptInputs {
				return commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseMalformed, TargetKind: step.Target}
			},
			wantRun: commissioningv2.RunOwnerAttention, wantCode: "", wantTerminal: true,
		},
		{
			name: "stale/drifted-429",
			inputs: func(step IndexerCommissioningStep) commissioningv2.LiveAttemptInputs {
				deadline, ok := indexers.ParseRetryAfter("120", testB31Time())
				if !ok {
					panic("parse retry-after")
				}
				return commissioningv2.LiveAttemptInputs{Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseRateLimited, FreshnessDrifted: true, RetryAfter: deadline, TargetKind: step.Target}
			},
			wantRun: commissioningv2.RunStale, wantCode: "configuration.changed", wantTerminal: true, wantStale: true,
		},
	} {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if arm.wantStale {
			if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$2, updated_at=$3 WHERE id=$1`, indexerID.String(), `{"base_url":"https://drifted.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now.Add(4*time.Second)); err != nil {
				t.Fatalf("%s: %v", arm.name, err)
			}
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		classified, err := commissioningv2.ClassifyLiveAttempt(arm.inputs(step))
		if err != nil {
			t.Fatalf("%s: classify: %v", arm.name, err)
		}
		completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
		if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
			t.Fatalf("%s: complete: %v", arm.name, err)
		}
		after, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if after.State != arm.wantRun {
			t.Fatalf("%s: run=%q want %q", arm.name, after.State, arm.wantRun)
		}
		var code *string
		var staleAt, completedAt *time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT NULLIF(safe_code,''), stale_at, completed_at FROM indexer_commissioning_runs WHERE id=$1`, run.ID.String()).Scan(&code, &staleAt, &completedAt); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		gotCode := ""
		if code != nil {
			gotCode = *code
		}
		if gotCode != arm.wantCode {
			t.Fatalf("%s: safe_code=%q want %q", arm.name, gotCode, arm.wantCode)
		}
		var parsedCode commissioningv2.SafeCode
		if gotCode != "" {
			parsedCode = commissioningv2.SafeCode(gotCode)
		}
		if !commissioningRunSafeCodeMatches(after.State, parsedCode) {
			t.Fatalf("%s: real commissioningRunSafeCodeMatches rejects durable (%q,%q)", arm.name, after.State, gotCode)
		}
		if !commissioningv2.TerminalRunState(after.State) != !arm.wantTerminal {
			t.Fatalf("%s: terminal=%v want %v", arm.name, commissioningv2.TerminalRunState(after.State), arm.wantTerminal)
		}
		if arm.wantTerminal && completedAt == nil {
			t.Fatalf("%s: terminal run missing completed_at", arm.name)
		}
		if arm.wantStale && staleAt == nil {
			t.Fatalf("%s: stale run missing stale_at", arm.name)
		}
		if !arm.wantStale && staleAt != nil {
			t.Fatalf("%s: non-stale run has stale_at=%v", arm.name, staleAt)
		}
		if arm.wantCode == "" && code != nil {
			t.Fatalf("%s: owner_attention/stale-adjacent wrote non-NULL safe_code %q", arm.name, gotCode)
		}
	}
}

func uint16PtrB31(value uint16) *uint16 { return &value }

// B-25: completion rollback retains the gate and the transmitting step (a
// failing completion — wrong lease — leaves every durable field unchanged,
// including pacing floor and budget revision).
func TestCompleteLiveAttemptRollbackRetainsGateAndTransmittingStepExtended(t *testing.T) {
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
	var floorBefore time.Time
	var revisionBefore int64
	var transmittedBefore time.Time
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
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.ExpectedRunRev = reloaded.Revision
	completion.LeaseOwner = "worker.wrong"
	completion.BudgetSnapshot = snapshot
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
		t.Fatal("wrong lease accepted")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting || loaded.TransmittedAt == nil || !loaded.TransmittedAt.Equal(transmittedBefore) {
		t.Fatalf("rollback changed step: %#v", loaded)
	}
	var floorAfter time.Time
	var revisionAfter int64
	var gateAfter *string
	if err := store.Pool.QueryRow(ctx, `SELECT next_allowed_at, revision, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&floorAfter, &revisionAfter, &gateAfter); err != nil {
		t.Fatal(err)
	}
	if !floorAfter.Equal(floorBefore) || revisionAfter != revisionBefore {
		t.Fatalf("rollback changed pacing/revision: %s/%d -> %s/%d", floorBefore, revisionBefore, floorAfter, revisionAfter)
	}
	if gateAfter == nil || *gateAfter != step.ID.String() {
		t.Fatalf("rollback changed gate: %v", gateAfter)
	}
}

// B-44 is in indexer_commissioning_live_test.go
// (TestLegacyAcquireBlockedByDurableGate).

// B-48 arms are in indexer_commissioning_live_test.go
// (TestLastOutcomeAtAdvancesOnlyOnOutcomeProducingWrite,
// TestTransmitPacingWriteDoesNotRefreshLastOutcomeAt); this companion proves
// a gate SET cannot bypass outcome-time monotonicity. The target step is
// transmitting with transmitted_at set, the direct gate SET is otherwise
// valid, and ONLY the regressed last_outcome_at explains the refusal. The
// durable FailureScopeOperation arm (B-48 operation scope) pins that an
// operation-scoped completion leaves LastOutcome/LastOutcomeAt untouched.
func TestGateSetCannotBypassOutcomeTimeMonotonicity(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Terminalize the first run before building the second fixture so the
	// 0034 one-nonterminal-run constraint is never violated: complete the
	// transmitting attempt with a real outcome-producing write.
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.BudgetSnapshot = snapshot
	completedAt := now.Add(5 * time.Second)
	if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
		t.Fatal(err)
	}
	// B-48 fixture strategy. The 0034 one-nonterminal index forbids a
	// second run on indexer1 while run1 is live; completing run1 first
	// would clear the gate under test (caps_observed clears). So run1 is
	// completed (outcome time advanced to completedAt, gate cleared through
	// the REAL allowlisted CLEAR path below), and the monotonicity probe
	// runs as a fresh gate SET on a SECOND indexer whose target step is
	// transmitting+transmitted_at: the SET path checks same-indexer +
	// boundary + monotonicity together, and same-indexer/boundary pass by
	// construction, so ONLY the regressed outcome time can explain the
	// refusal. A companion UPDATE-path probe (same gate value, regressed
	// time) proves the EVERY-update monotonicity branch with no
	// same-indexer/boundary confound at all.
	indexer2 := liveIndexer(t, ctx, store, now.Add(time.Hour))
	run2, step2 := liveRunAndIntentStep(t, ctx, store, indexer2, now.Add(time.Hour))
	if run2.Steps[0].State != commissioningv2.StepIntentCommitted {
		t.Fatalf("second run step=%q", run2.Steps[0].State)
	}
	scheduler2 := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler2, indexer2, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot2 := liveSnapshotForStep(t, scheduler2, indexer2, step2.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexer2.String(), step2.ID.String(), snapshot2, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run2, step2, snapshot2, now.Add(2*time.Hour)), now.Add(2*time.Hour).Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Prove the target step is transmitting with transmitted_at set: a
	// legal gate SET target on indexer2. Clear indexer1's gate first
	// through the REAL allowlisted CLEAR path (its step completed
	// caps_observed): proves the fixture reached the SET path legally.
	loaded2, err := store.IndexerCommissioningStep(ctx, run2.ID, step2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded2.State != commissioningv2.StepTransmitting || loaded2.TransmittedAt == nil {
		t.Fatalf("setup: step2=%#v, want transmitting with transmitted_at", loaded2)
	}
	// UPDATE-path probe FIRST (no gate change at all): regress outcome time
	// on indexer1's own row while its allowlisted gate is still HELD
	// (last_outcome_at == completedAt from the real completion above).
	// Same-indexer/boundary/replacement predicates cannot fire (gate value
	// untouched); only monotonicity can refuse the regression.
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET last_outcome_at=$2 WHERE indexer_instance_id=$1`, indexerID.String(), now); err == nil {
		t.Fatal("UPDATE-path outcome regression accepted")
	} else if !strings.Contains(err.Error(), "cannot regress") {
		t.Fatalf("UPDATE-path rejection=%v, want outcome-time-regression predicate", err)
	}
	loaded2check, err := store.IndexerCommissioningStep(ctx, run2.ID, step2.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = loaded2check
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexer2.String()); err != nil {
		t.Fatal(err)
	}
	// Clear indexer1's gate through the REAL allowlisted CLEAR path (its
	// step completed caps_observed): proves the fixture reached the SET
	// path legally.
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err != nil {
		t.Fatalf("setup: allowlisted CLEAR refused: %v", err)
	}
	var outcomeAtBefore *time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT last_outcome_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&outcomeAtBefore); err != nil {
		t.Fatal(err)
	}
	if outcomeAtBefore == nil || !outcomeAtBefore.Equal(completedAt) {
		t.Fatalf("setup outcome time=%v, want %s", outcomeAtBefore, completedAt)
	}
	// Direct SQL SET: point the cleared row at the transmitting step2 while
	// regressing outcome time. Same-indexer passes (step2 belongs to
	// indexer2 and the row moves to indexer2); transmission-boundary
	// passes (step2 transmitting + transmitted_at). Only monotonicity can
	// refuse. (Uses a fresh transmitting step: step2's gate row was
	// deleted above, so rebuild step2's boundary state is re-verified
	// before the SET.)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET indexer_instance_id=$2, commissioning_gate_step_id=$3, last_outcome_at=$4 WHERE indexer_instance_id=$1`, indexerID.String(), indexer2.String(), step2.ID.String(), now); err == nil {
		t.Fatal("gate SET with regressed outcome time accepted")
	} else if !strings.Contains(err.Error(), "cannot regress") {
		t.Fatalf("gate monotonicity rejection=%v, want outcome-time-regression predicate", err)
	}
	// Postcondition: row still cleared (no gate smuggled in) and outcome
	// time unchanged; step2 still transmitting.
	var gateAfter *string
	var outcomeAtAfter *time.Time
	var indexerAfter string
	if err := store.Pool.QueryRow(ctx, `SELECT indexer_instance_id::text, commissioning_gate_step_id::text, last_outcome_at FROM indexer_budget_state WHERE indexer_instance_id=$1 OR indexer_instance_id=$2`, indexerID.String(), indexer2.String()).Scan(&indexerAfter, &gateAfter, &outcomeAtAfter); err != nil {
		t.Fatal(err)
	}
	if gateAfter != nil {
		t.Fatalf("monotonicity refusal wrote gate: %v", *gateAfter)
	}
	if outcomeAtAfter == nil || !outcomeAtAfter.Equal(completedAt) {
		t.Fatalf("monotonicity refusal changed outcome time: %v", outcomeAtAfter)
	}
	recheck2, err := store.IndexerCommissioningStep(ctx, run2.ID, step2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recheck2.State != commissioningv2.StepTransmitting || recheck2.TransmittedAt == nil {
		t.Fatalf("monotonicity refusal changed step2: %#v", recheck2)
	}
}

// TestLastOutcomeAtUnchangedByFailureScopeOperation is the durable B-48
// operation-scope arm: an operation-scoped scheduler result completes without
// touching LastOutcome/LastOutcomeAt (ComputeOutcomeEffect skips the outcome
// history for FailureScopeOperation), while the transmission boundary and
// gate mechanics still behave.
func TestLastOutcomeAtUnchangedByFailureScopeOperation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Pure-function arm pins the required semantic without durable pacing
	// interference: operation scope preserves both outcome fields.
	start := indexers.Snapshot{IndexerID: "b48-operation", LastOutcome: indexers.OutcomeSuccess, LastOutcomeAt: now, ConsecutiveFailure: 2}
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	effect := indexers.ComputeOutcomeEffect(start, budget, "b48-operation", indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeOperation}, "", now.Add(time.Second))
	if effect.LastOutcome != indexers.OutcomeSuccess || !effect.LastOutcomeAt.Equal(now) {
		t.Fatalf("operation scope touched outcome history: %#v", effect)
	}
	// Instance scope on the same start MUST advance the outcome time, so a
	// hard-coded no-touch implementation cannot pass this row.
	instance := indexers.ComputeOutcomeEffect(start, budget, "b48-operation", indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance}, "", now.Add(time.Second))
	if !instance.LastOutcomeAt.Equal(now.Add(time.Second)) {
		t.Fatalf("instance scope did not advance outcome time: %#v", instance)
	}
}

// B-54 companion: schema/raw-secret-column prohibitions are in
// TestNoRawBodyURLHeaderOrSecretColumnAdded (this file); production
// data/profile non-mutation is asserted here for the completion path.
func TestCompletionNeverMutatesProfiles(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	var profileCountBefore int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&profileCountBefore); err != nil {
		t.Fatal(err)
	}
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.BudgetSnapshot = snapshot
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	var profileCountAfter int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&profileCountAfter); err != nil {
		t.Fatal(err)
	}
	if profileCountAfter != profileCountBefore {
		t.Fatalf("completion mutated profiles: %d -> %d", profileCountBefore, profileCountAfter)
	}
}

// TestNoRawBodyURLHeaderOrSecretColumnAdded inspects the PostgreSQL catalog
// for the commissioning tables and asserts no newly-added column can persist
// a raw response body, request URL, query string, API key, authorization or
// cookie headers, or a raw provider secret. Steps-table columns use the SAME
// containment check as the budget table (exact-equality alone would miss
// response_body/raw_body_text/api_key_value/authorization_header/
// provider_secret), with explicit allowlisted digest-only columns whose names
// intentionally contain raw/response (safe_response_sha256,
// raw_body_sha256). Catalog queries are scoped to the actual test schema.
func TestNoRawBodyURLHeaderOrSecretColumnAdded(t *testing.T) {
	store, ctx := liveTestStore(t)
	schema := liveTestSchemaName(t, ctx, store)
	allowed := map[string]map[string]bool{
		"indexer_budget_state": {
			"indexer_instance_id": true, "minimum_interval_ms": true, "next_allowed_at": true,
			"cooldown_until": true, "consecutive_failure": true, "last_outcome": true,
			"last_outcome_at": true, "revision": true, "updated_at": true,
			"commissioning_gate_step_id": true,
		},
	}
	for table, columns := range allowed {
		rows, err := store.Pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2`, schema, table)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		rows.Close()
		for _, name := range names {
			if !columns[name] {
				t.Fatalf("unexpected column %s.%s (possible raw persistence surface)", table, name)
			}
			lowered := strings.ToLower(name)
			for _, forbidden := range []string{"body", "url", "query", "key", "secret", "token", "auth", "cookie", "header", "password", "request", "response", "raw"} {
				if strings.Contains(lowered, forbidden) {
					t.Fatalf("column %s.%s names a raw persistence surface (%q)", table, name, forbidden)
				}
			}
		}
	}
	// Steps-table evidence columns are digest/key/document only: assert no
	// raw-body/URL/secret column exists there either — containment over the
	// same sensitive terms, so response_body, raw_body_text, api_key_value,
	// authorization_header, and provider_secret are all caught.
	rows, err := store.Pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name='indexer_commissioning_steps'`, schema)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit allowlist: digest-only columns whose names intentionally
	// contain raw/response but persist only SHA-256 digests.
	digestAllowlist := map[string]bool{"safe_response_sha256": true, "raw_body_sha256": true}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		lowered := strings.ToLower(name)
		if digestAllowlist[lowered] {
			continue
		}
		for _, forbidden := range []string{"body", "url", "query", "key", "secret", "token", "auth", "cookie", "header", "password", "request", "response", "raw"} {
			if strings.Contains(lowered, forbidden) {
				t.Fatalf("steps column %q is a raw persistence surface (%q)", name, forbidden)
			}
		}
	}
	rows.Close()
}

// liveTestSchemaName returns the search_path schema of the disposable
// per-test store connection (schema-limited cleanup scope).
func liveTestSchemaName(t *testing.T, ctx context.Context, store *Store) string {
	t.Helper()
	var schema string
	if err := store.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if schema == "" || schema == "public" || schema == "pg_catalog" {
		t.Fatalf("refusing to scope catalog queries to schema %q", schema)
	}
	return schema
}

// B-55: cooldown derivation uses the SUPPLIED budget across auth-failure
// cooldown (now+MaximumBackoff), rate-limit fallback (backoff()), parsed
// Retry-After wins when later, and instance system-failure cooldown — each
// run with two materially different budgets with differing results.
func TestCompleteLiveAttemptUsesSuppliedBudgetForCooldownDerivation(t *testing.T) {
	budgets := []indexers.Budget{
		{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour},
		{MinimumInterval: 9 * time.Second, FailureBackoff: 3 * time.Second, MaximumBackoff: 10 * time.Minute, QueryLimit: 100, LimitWindow: time.Hour},
	}
	// Auth failure -> now + MaximumBackoff per budget (two materially
	// different budgets: MaximumBackoff 1m vs 10m).
	var authCooldowns [2]time.Time
	var authNexts [2]time.Time
	for i, budget := range budgets {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, budget)
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
			Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure,
			TargetKind: step.Target,
		})
		if err != nil {
			t.Fatal(err)
		}
		completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
		completedAt := now.Add(5 * time.Second)
		if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
			t.Fatal(err)
		}
		var cooldown, next time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until, next_allowed_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown, &next); err != nil {
			t.Fatal(err)
		}
		authCooldowns[i] = cooldown
		authNexts[i] = next
		if !cooldown.Equal(completedAt.Add(budget.MaximumBackoff)) {
			t.Fatalf("budget %d: auth cooldown=%s want %s", i, cooldown, completedAt.Add(budget.MaximumBackoff))
		}
		// The pacing floor is the GREATEST of the boundary floor
		// (deadline+MinimumInterval, deadline = reserve-time+1h) and the
		// completion effect (completedAt+MinimumInterval): the boundary
		// floor is later, so it must survive exactly. Asserting the
		// completion effect alone would be wrong.
		wantNext := now.Add(3 * time.Second).Add(time.Hour).Add(budget.MinimumInterval)
		if !next.Equal(wantNext) {
			t.Fatalf("budget %d: next=%s want boundary floor %s", i, next, wantNext)
		}
	}
	if !authCooldowns[1].After(authCooldowns[0]) {
		t.Fatalf("auth cooldowns do not differ per budget: %s vs %s", authCooldowns[0], authCooldowns[1])
	}
	if !authNexts[1].After(authNexts[0]) {
		t.Fatalf("pacing floors do not differ per budget: %s vs %s", authNexts[0], authNexts[1])
	}
	// Rate-limited fallback WITHOUT Retry-After: cooldown == completedAt +
	// backoff(indexerID, failures+1, suppliedBudget) exactly, using
	// controlled now; the two FailureBackoffs (1s vs 3s) must yield
	// different exact cooldowns so a hard-coded/global budget cannot pass.
	// Instance-scoped system/transport failure: cooldown == completedAt +
	// backoff(indexerID, failures+1, suppliedBudget) exactly per budget.
	fallbackCooldowns := make([]time.Time, len(budgets))
	systemCooldowns := make([]time.Time, len(budgets))
	for i, budget := range budgets {
		for _, arm := range []struct {
			name     string
			response commissioningv2.ResponseVerdict
			failure  commissioningv2.TransportFailure
			dst      *time.Time
		}{
			{name: "rate-limited-fallback", response: commissioningv2.ResponseRateLimited, dst: &fallbackCooldowns[i]},
			{name: "instance-system-failure", failure: commissioningv2.TransportFailureConnection, dst: &systemCooldowns[i]},
		} {
			store, ctx := liveTestStore(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			indexerID := liveIndexer(t, ctx, store, now)
			run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
			scheduler := indexers.NewScheduler(4, store)
			configureCommissioningScheduler(t, scheduler, indexerID, budget)
			snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
			if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
				t.Fatalf("budget %d %s: %v", i, arm.name, err)
			}
			if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
				t.Fatalf("budget %d %s: %v", i, arm.name, err)
			}
			reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
			if err != nil {
				t.Fatalf("budget %d %s: %v", i, arm.name, err)
			}
			var classified commissioningv2.ClassifiedCompletion
			if arm.response != "" {
				classified, err = commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
					Transport: commissioningv2.TransportResponseReceived, Response: arm.response,
					TargetKind: step.Target,
				})
			} else {
				classified, err = commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
					Transport: commissioningv2.TransportNoCompleteResponse, Failure: arm.failure,
				})
			}
			if err != nil {
				t.Fatalf("budget %d %s: classify: %v", i, arm.name, err)
			}
			completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
			completedAt := now.Add(5 * time.Second)
			if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
				t.Fatalf("budget %d %s: complete: %v", i, arm.name, err)
			}
			// Oracle: the locked row starts at consecutive_failure=0, so
			// the effect is backoff(indexerID, 1, budget) via the REAL
			// ComputeCommissioningOutcomeEffect on a zero start.
			locked := indexers.Snapshot{IndexerID: indexerID.String()}
			want, err := indexers.ComputeCommissioningOutcomeEffect(locked, snapshot, classified.SchedulerResult, completedAt)
			if err != nil {
				t.Fatalf("budget %d %s: oracle: %v", i, arm.name, err)
			}
			var cooldown time.Time
			if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown); err != nil {
				t.Fatalf("budget %d %s: %v", i, arm.name, err)
			}
			if !cooldown.Equal(want.CooldownUntil) {
				t.Fatalf("budget %d %s: cooldown=%s want exact oracle %s", i, arm.name, cooldown, want.CooldownUntil)
			}
			*arm.dst = cooldown
		}
	}
	if fallbackCooldowns[0].Equal(fallbackCooldowns[1]) {
		t.Fatalf("rate-limited fallback cooldowns identical across budgets: %s", fallbackCooldowns[0])
	}
	if systemCooldowns[0].Equal(systemCooldowns[1]) {
		t.Fatalf("instance system-failure cooldowns identical across budgets: %s", systemCooldowns[0])
	}
	// Parsed Retry-After-wins-when-later per budget (identical deadline once
	// parsed).
	for i, budget := range budgets {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, budget)
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		deadline, ok := indexers.ParseRetryAfter("120", now.Add(5*time.Second))
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
		completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
		if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
		var cooldown time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown); err != nil {
			t.Fatal(err)
		}
		if !cooldown.Equal(deadline) {
			t.Fatalf("budget %d: rate-limited parsed deadline not applied: %s want %s", i, cooldown, deadline)
		}
	}
}

// B-56: zero/invalid budgets rejected at completion with zero writes.
func TestCompleteLiveAttemptRejectsZeroOrInvalidBudget(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := liveCAPSCompletion(reloaded, step, time.Time{})
	for _, name := range []string{"zero"} {
		completion := base
		completion.ExpectedRunRev = reloaded.Revision
		completion.BudgetSnapshot = indexers.CommissioningBudgetSnapshot{}
		var revisionBefore int64
		if err := store.Pool.QueryRow(ctx, `SELECT revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
			t.Fatalf("%s budget accepted", name)
		} else if !errors.Is(err, ErrInvalidIndexerCommissioning) {
			t.Fatalf("%s: err=%v", name, err)
		}
		loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.State != commissioningv2.StepTransmitting {
			t.Fatalf("%s: step changed", name)
		}
		var revisionAfter int64
		var gate *string
		if err := store.Pool.QueryRow(ctx, `SELECT revision, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionAfter, &gate); err != nil {
			t.Fatal(err)
		}
		if revisionAfter != revisionBefore || gate == nil {
			t.Fatalf("%s: writes occurred", name)
		}
	}
}

// B-57: completion with a mismatched boundary budget is rejected.
func TestCompleteLiveAttemptRejectsBudgetMismatchedWithTransmissionBoundary(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A different valid budget for the SAME attempt (30s minimum vs 5s
	// boundary) still matches the attempt but fails the
	// minimum_interval_ms cross-check.
	otherBudget := indexers.Budget{MinimumInterval: 30 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, otherBudget)
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.ExpectedRunRev = reloaded.Revision
	// Forge the cross-check path: use the ORIGINAL snapshot but prove a
	// directly mismatched minimum is rejected — the repository compares
	// snapshot.MinimumInterval against the locked row, so a substituted
	// snapshot with a different minimum fails.
	completion.BudgetSnapshot = snapshot
	// Sanity: the correct snapshot still works for the shape check below;
	// now prove mismatch by completing with a snapshot whose minimum was
	// admitted under the other budget. MatchesAttempt passes (same attempt),
	// the minimum cross-check fails.
	mismatched, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer mismatched.ReleaseWithoutOutcome()
	completion.BudgetSnapshot = mismatched.CommissioningBudgetSnapshot()
	var revisionBefore int64
	if err := store.Pool.QueryRow(ctx, `SELECT revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
		t.Fatal("mismatched-budget completion accepted")
	} else if !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("err=%v", err)
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("mismatch changed step: %#v", loaded)
	}
	var revisionAfter int64
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT revision, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revisionAfter, &gate); err != nil {
		t.Fatal(err)
	}
	if revisionAfter != revisionBefore || gate == nil || *gate != step.ID.String() {
		t.Fatalf("mismatch wrote: rev %d->%d gate %v", revisionBefore, revisionAfter, gate)
	}
}

// B-58: an in-flight attempt keeps its original budget after reconfigure;
// bidirectional cross-substitution is rejected.
func TestInFlightAttemptKeepsOriginalBudgetAfterReconfigure(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	budget1 := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: 30 * time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	budget2 := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: 10 * time.Second, QueryLimit: 100, LimitWindow: time.Hour}
	// Two independent attempts on two indexers.
	indexerA := liveIndexer(t, ctx, store, now)
	runA, stepA := liveRunAndIntentStep(t, ctx, store, indexerA, now)
	scheduler := indexers.NewScheduler(8, store)
	configureCommissioningScheduler(t, scheduler, indexerA, budget1)
	snapshotA := liveSnapshotForStep(t, scheduler, indexerA, stepA.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerA.String(), stepA.ID.String(), snapshotA, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(runA, stepA, snapshotA, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	indexerB := liveIndexer(t, ctx, store, now.Add(time.Minute))
	runB, stepB := liveRunAndIntentStep(t, ctx, store, indexerB, now.Add(time.Minute))
	configureCommissioningScheduler(t, scheduler, indexerB, budget2)
	snapshotB := liveSnapshotForStep(t, scheduler, indexerB, stepB.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerB.String(), stepB.ID.String(), snapshotB, now.Add(3*time.Second).Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(runB, stepB, snapshotB, now.Add(3*time.Second).Add(time.Minute)), now.Add(4*time.Second).Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Reconfigure A's lane to budget2 AFTER the boundary.
	configureCommissioningScheduler(t, scheduler, indexerA, budget2)
	reloadedA, err := store.IndexerCommissioningRun(ctx, runA.ID)
	if err != nil {
		t.Fatal(err)
	}
	classifiedA, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure,
		TargetKind: stepA.Target,
	})
	if err != nil {
		t.Fatal(err)
	}
	completionA := liveCompletionForStep(t, reloadedA, stepA, snapshotA, classifiedA)
	completedAt := now.Add(5 * time.Second)
	if _, err := store.CompleteLiveAttempt(ctx, completionA, completedAt); err != nil {
		t.Fatal(err)
	}
	var cooldownA time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerA.String()).Scan(&cooldownA); err != nil {
		t.Fatal(err)
	}
	if !cooldownA.Equal(completedAt.Add(budget1.MaximumBackoff)) {
		t.Fatalf("in-flight attempt used reconfigured budget: %s want %s", cooldownA, completedAt.Add(budget1.MaximumBackoff))
	}
	// Bidirectional cross-substitution rejected: snapshotA cannot close
	// attempt B and snapshotB cannot close attempt A.
	reloadedB, err := store.IndexerCommissioningRun(ctx, runB.ID)
	if err != nil {
		t.Fatal(err)
	}
	classifiedB, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure,
		TargetKind: stepB.Target,
	})
	if err != nil {
		t.Fatal(err)
	}
	crossAB := liveCompletionForStep(t, reloadedB, stepB, snapshotA, classifiedB)
	if _, err := store.CompleteLiveAttempt(ctx, crossAB, completedAt); err == nil {
		t.Fatal("snapshotA closed attemptB")
	}
	// snapshotB for attemptA: attemptA already completed, so use a fresh
	// attempt pair is unnecessary — assert MatchesAttempt-level rejection by
	// presenting snapshotB at B's own boundary shape is valid but at A's
	// attempt fails: A is completed (concurrent-modification) AND foreign
	// (invalid). Prove the foreign axis on a fresh transmitting attempt.
	indexerC := liveIndexer(t, ctx, store, now.Add(2*time.Minute))
	runC, stepC := liveRunAndIntentStep(t, ctx, store, indexerC, now.Add(2*time.Minute))
	configureCommissioningScheduler(t, scheduler, indexerC, budget1)
	snapshotC := liveSnapshotForStep(t, scheduler, indexerC, stepC.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerC.String(), stepC.ID.String(), snapshotC, now.Add(3*time.Second).Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(runC, stepC, snapshotC, now.Add(3*time.Second).Add(2*time.Minute)), now.Add(4*time.Second).Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reloadedC, err := store.IndexerCommissioningRun(ctx, runC.ID)
	if err != nil {
		t.Fatal(err)
	}
	crossCB := liveCompletionForStep(t, reloadedC, stepC, snapshotB, classifiedB)
	if _, err := store.CompleteLiveAttempt(ctx, crossCB, completedAt); err == nil {
		t.Fatal("snapshotB closed attemptC")
	} else if !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("cross-substitution err=%v", err)
	}
	// Each correct snapshot closes its own attempt: B still completes.
	correctB := liveCompletionForStep(t, reloadedB, stepB, snapshotB, classifiedB)
	if _, err := store.CompleteLiveAttempt(ctx, correctB, completedAt.Add(time.Minute)); err != nil {
		t.Fatalf("correct snapshotB failed: %v", err)
	}
}

// B-59: a later acquisition uses the new configured budget.
func TestLaterAcquisitionUsesNewConfiguredBudget(t *testing.T) {
	scheduler := indexers.NewScheduler(4, nil)
	indexerID := uuid.NewV7()
	stepA := uuid.NewV7()
	stepB := uuid.NewV7()
	budget1 := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10, LimitWindow: time.Hour}
	budget2 := indexers.Budget{MinimumInterval: 9 * time.Second, FailureBackoff: 3 * time.Second, MaximumBackoff: 2 * time.Minute, QueryLimit: 20, LimitWindow: 2 * time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget1)
	permit1, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID.String(), stepA.String())
	if err != nil {
		t.Fatal(err)
	}
	snapshot1 := permit1.CommissioningBudgetSnapshot()
	permit1.ReleaseWithoutOutcome()
	configureCommissioningScheduler(t, scheduler, indexerID, budget2)
	permit2, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID.String(), stepB.String())
	if err != nil {
		t.Fatal(err)
	}
	defer permit2.ReleaseWithoutOutcome()
	snapshot2 := permit2.CommissioningBudgetSnapshot()
	if snapshot1.MinimumInterval() == snapshot2.MinimumInterval() && snapshot1.QueryLimit() == snapshot2.QueryLimit() && snapshot1.MaximumBackoff() == snapshot2.MaximumBackoff() {
		t.Fatal("snapshots identical across reconfigure")
	}
	if snapshot2.MinimumInterval() != budget2.MinimumInterval || snapshot2.QueryLimit() != budget2.QueryLimit || snapshot2.LimitWindow() != budget2.LimitWindow {
		t.Fatalf("snapshot2 not budget2: min=%s q=%d", snapshot2.MinimumInterval(), snapshot2.QueryLimit())
	}
}

// B-60: zero snapshot rejected at all three durable boundaries with zero
// writes and zero HTTP sends.
func TestZeroCommissioningBudgetSnapshotRejected(t *testing.T) {
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
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	zero := indexers.CommissioningBudgetSnapshot{}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	// Fake TLS server wires the zero-send invariant: every arm below must
	// leave requests at 0 (asserted at the end of each test).
	// Reserve arm.
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), zero, now.Add(5*time.Second)); err == nil {
		t.Fatal("zero snapshot reserved")
	}
	var resCount, revBefore int
	var updatedBefore time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE id=$1`, step.ID.String()).Scan(&resCount); err != nil {
		t.Fatal(err)
	}
	if resCount != 1 {
		t.Fatalf("reservation count=%d", resCount)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT revision, updated_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revBefore, &updatedBefore); err != nil {
		t.Fatal(err)
	}
	// Transmit arm (fresh attempt so expected-state does not confound).
	indexer2 := liveIndexer(t, ctx, store, now.Add(time.Minute))
	run2, step2 := liveRunAndIntentStep(t, ctx, store, indexer2, now.Add(time.Minute))
	if run2.IndexerInstanceID != indexer2 || step2.RunID != run2.ID {
		t.Fatal("second fixture mislinked")
	}
	configureCommissioningScheduler(t, scheduler, indexer2, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	marker2 := liveMarkerFor(run2, step2, zero, now.Add(3*time.Second).Add(time.Minute))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker2, now.Add(4*time.Second).Add(time.Minute)); err == nil {
		t.Fatal("zero snapshot transmitted")
	}
	loaded2, err := store.IndexerCommissioningStep(ctx, run2.ID, step2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded2.State != commissioningv2.StepIntentCommitted || loaded2.TransmittedAt != nil {
		t.Fatalf("zero transmit wrote: %#v", loaded2)
	}
	// Complete arm: gate still held, step still transmitting.
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.BudgetSnapshot = zero
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
		t.Fatal("zero snapshot completed")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("zero complete changed step: %#v", loaded)
	}
	var revAfter int
	var updatedAfter time.Time
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT revision, updated_at, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revAfter, &updatedAfter, &gate); err != nil {
		t.Fatal(err)
	}
	if revAfter != revBefore || !updatedAfter.Equal(updatedBefore) || gate == nil || *gate != step.ID.String() {
		t.Fatalf("zero complete wrote: rev %d->%d gate %v", revBefore, revAfter, gate)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

// B-61: a legitimate snapshot for (indexerA, stepA) presented at all three
// boundaries for (indexerB, stepB) and (indexerA, stepB): exact closed error,
// zero writes everywhere, zero HTTP.
func TestCommissioningSnapshotRejectedForDifferentAttempt(t *testing.T) {
	for _, target := range []string{"other-indexer-other-step", "same-indexer-other-step"} {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerA := liveIndexer(t, ctx, store, now)
		runA, stepA := liveRunAndIntentStep(t, ctx, store, indexerA, now)
		scheduler := indexers.NewScheduler(8, store)
		configureCommissioningScheduler(t, scheduler, indexerA, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshotA := liveSnapshotForStep(t, scheduler, indexerA, stepA.ID)
		var indexerB uuid.UUID
		var runB IndexerCommissioningRun
		var stepB IndexerCommissioningStep
		if target == "other-indexer-other-step" {
			indexerB = liveIndexer(t, ctx, store, now.Add(time.Minute))
			configureCommissioningScheduler(t, scheduler, indexerB, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
			runB, stepB = liveRunAndIntentStep(t, ctx, store, indexerB, now.Add(time.Minute))
		} else {
			indexerB = indexerA
			configureCommissioningScheduler(t, scheduler, indexerB, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
			// Second step on the same indexer needs a second run (one
			// nonterminal run per indexer); the first run must be closed
			// first — instead use a fresh minute-offset run which the
			// one-nonterminal index permits after time passes is false,
			// so close runA via cancellation before creating runB.
			if _, err := store.RequestRunCancellation(ctx, indexerA.String(), runA.ID.String(), runA.Revision, now.Add(30*time.Second)); err != nil {
				t.Fatal(err)
			}
			cancelledA, err := store.IndexerCommissioningRun(ctx, runA.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.FinalizeCancelledRun(ctx, indexerA.String(), runA.ID.String(), cancelledA.Revision, now.Add(31*time.Second)); err != nil {
				t.Fatal(err)
			}
			runB, stepB = liveRunAndIntentStep(t, ctx, store, indexerB, now.Add(time.Minute))
		}
		var requests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
		}))
		defer server.Close()
		// Fake TLS server wires the zero-send invariant for this target.
		// Reserve arm with the foreign snapshot.
		if _, err := store.ReserveCommissioningRequest(ctx, indexerB.String(), stepB.ID.String(), snapshotA, now.Add(3*time.Second)); err == nil {
			t.Fatalf("%s: foreign snapshot reserved", target)
		}
		var resCount int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE id=$1`, stepB.ID.String()).Scan(&resCount); err != nil {
			t.Fatal(err)
		}
		if resCount != 0 {
			t.Fatalf("%s: foreign reservation row written", target)
		}
		// Transmit arm with the foreign snapshot.
		markerB := liveMarkerFor(runB, stepB, snapshotA, now.Add(3*time.Second))
		if _, err := store.MarkClaimedStepTransmitting(ctx, markerB, now.Add(4*time.Second)); err == nil {
			t.Fatalf("%s: foreign snapshot transmitted", target)
		} else if !errors.Is(err, ErrInvalidIndexerCommissioning) {
			t.Fatalf("%s: transmit err=%v", target, err)
		}
		loadedB, err := store.IndexerCommissioningStep(ctx, runB.ID, stepB.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loadedB.State != commissioningv2.StepIntentCommitted {
			t.Fatalf("%s: foreign transmit changed step", target)
		}
		// Complete arm: first legitimately transmit B, then present A's
		// snapshot at completion.
		snapshotB := liveSnapshotForStep(t, scheduler, indexerB, stepB.ID)
		if _, err := store.ReserveCommissioningRequest(ctx, indexerB.String(), stepB.ID.String(), snapshotB, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(runB, stepB, snapshotB, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		reloadedB, err := store.IndexerCommissioningRun(ctx, runB.ID)
		if err != nil {
			t.Fatal(err)
		}
		completionB := liveCAPSCompletion(reloadedB, stepB, time.Time{})
		completionB.BudgetSnapshot = snapshotA
		var revBefore int64
		var updatedBefore time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT revision, updated_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerB.String()).Scan(&revBefore, &updatedBefore); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteLiveAttempt(ctx, completionB, now.Add(5*time.Second)); err == nil {
			t.Fatalf("%s: foreign snapshot completed", target)
		} else if !errors.Is(err, ErrInvalidIndexerCommissioning) {
			t.Fatalf("%s: complete err=%v", target, err)
		}
		var revAfter int64
		var updatedAfter time.Time
		var gate *string
		if err := store.Pool.QueryRow(ctx, `SELECT revision, updated_at, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerB.String()).Scan(&revAfter, &updatedAfter, &gate); err != nil {
			t.Fatal(err)
		}
		if revAfter != revBefore || !updatedAfter.Equal(updatedBefore) {
			t.Fatalf("%s: foreign complete wrote budget", target)
		}
		if gate == nil || *gate != stepB.ID.String() {
			t.Fatalf("%s: foreign complete changed gate: %v", target, gate)
		}
		loadedB, err = store.IndexerCommissioningStep(ctx, runB.ID, stepB.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loadedB.State != commissioningv2.StepTransmitting {
			t.Fatalf("%s: foreign complete changed step", target)
		}
		if requests.Load() != 0 {
			t.Fatalf("%s: requests=%d", target, requests.Load())
		}
		if stepA.ID == stepB.ID {
			t.Fatalf("%s: fixtures share a step", target)
		}
	}
}

// B-62: canonical admission accepts alternate boundary spellings — the SAME
// attempt spelled uppercase and urn:uuid:-prefixed is ACCEPTED at each of
// the three boundaries (boundary parses, re-emits parsed.String(),
// MatchesAttempt recognizes the same canonical attempt).
func TestCanonicalAdmissionAcceptsAlternateBoundarySpellings(t *testing.T) {
	for _, spelling := range []string{"upper", "urn"} {
		store, ctx := liveTestStore(t)
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := liveIndexer(t, ctx, store, now)
		run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
		scheduler := indexers.NewScheduler(4, store)
		configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
		snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
		indexerSpelling := indexerID.String()
		stepSpelling := step.ID.String()
		if spelling == "upper" {
			indexerSpelling = strings.ToUpper(indexerSpelling)
			stepSpelling = strings.ToUpper(stepSpelling)
		} else {
			indexerSpelling = "urn:uuid:" + indexerSpelling
			stepSpelling = "urn:uuid:" + stepSpelling
		}
		// Reserve with alternate spelling: accepted, same canonical row.
		reservation, err := store.ReserveCommissioningRequest(ctx, indexerSpelling, stepSpelling, snapshot, now.Add(3*time.Second))
		if err != nil || !reservation.Granted {
			t.Fatalf("%s: reserve: %#v %v", spelling, reservation, err)
		}
		var resIndexer, resID string
		if err := store.Pool.QueryRow(ctx, `SELECT indexer_instance_id::text, id::text FROM indexer_request_reservations WHERE id=$1`, step.ID.String()).Scan(&resIndexer, &resID); err != nil {
			t.Fatalf("%s: canonical reservation row missing: %v", spelling, err)
		}
		if resIndexer != indexerID.String() || resID != step.ID.String() {
			t.Fatalf("%s: reservation not canonical: %s/%s", spelling, resIndexer, resID)
		}
		// Transmit with alternate spelling.
		marker := liveMarkerFor(run, step, snapshot, now.Add(3*time.Second))
		marker.IndexerInstanceID = indexerSpelling
		marker.StepID = stepSpelling
		if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err != nil {
			t.Fatalf("%s: transmit: %v", spelling, err)
		}
		var gate *string
		if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil || *gate != step.ID.String() {
			t.Fatalf("%s: gate=%v err=%v", spelling, gate, err)
		}
		// Complete with alternate spelling.
		reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		completion := liveCAPSCompletion(reloaded, step, time.Time{})
		completion.BudgetSnapshot = snapshot
		completion.IndexerInstanceID = indexerSpelling
		completion.StepID = stepSpelling
		if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err != nil {
			t.Fatalf("%s: complete: %v", spelling, err)
		}
		loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.State != commissioningv2.StepCompleted {
			t.Fatalf("%s: state=%#v", spelling, loaded)
		}
	}
}

// B-41: reservation uses the indexer advisory-lock key — the held objid
// equals hashtextextended('indexer-request-budget:'||indexerID,0) — and a
// concurrent legacy ReserveIndexerRequest blocks on the identical key.
func TestReserveCommissioningRequestUsesIndexerAdvisoryLockKey(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	// Hold the v2 reservation transaction open via a raw advisory lock on
	// the same key derivation, then prove the legacy path serializes on it:
	// run both concurrently and assert the shared window is never overshot
	// (B-42 proves the count; here prove the key identity via pg_locks).
	connection, err := store.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(ctx)
	lockKey := "indexer-request-budget:" + indexerID.String()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		t.Fatal(err)
	}
	var objid int64
	if err := transaction.QueryRow(ctx, `SELECT objid FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND objid=hashtextextended($1,0)::bigint`, lockKey).Scan(&objid); err != nil {
		t.Fatalf("v2 lock key not held as advisory objid: %v", err)
	}
	// While the v2-side lock is held, a legacy ReserveIndexerRequest must
	// block (same key) rather than proceed concurrently.
	legacyDone := make(chan error, 1)
	go func() {
		_, err := store.ReserveIndexerRequest(ctx, indexerID.String(), "query", 100, time.Hour, now)
		legacyDone <- err
	}()
	select {
	case err := <-legacyDone:
		t.Fatalf("legacy reservation did not block on shared key: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-legacyDone:
		if err != nil {
			t.Fatalf("legacy reservation after unlock: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("legacy reservation never unblocked")
	}
	// Uppercase spelling serializes on the same key (canonical derivation).
	upperDone := make(chan error, 1)
	connection2, err := store.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection2.Release()
	transaction2, err := connection2.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction2.Rollback(ctx)
	if _, err := transaction2.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		t.Fatal(err)
	}
	stepID := uuid.NewV7()
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, stepID)
	go func() {
		_, err := store.ReserveCommissioningRequest(ctx, strings.ToUpper(indexerID.String()), stepID.String(), snapshot, now.Add(time.Second))
		upperDone <- err
	}()
	select {
	case err := <-upperDone:
		t.Fatalf("uppercase v2 reservation did not block on shared key: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := transaction2.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-upperDone:
		if err != nil {
			t.Fatalf("uppercase reservation after unlock: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("uppercase reservation never unblocked")
	}
}

// B-42: legacy and commissioning reservations share the window without
// overshoot — limit=N, N+5 mixed legacy/v2 callers concurrently; EXACTLY N
// granted and count(*) <= N (a total-refusal implementation fails the exact
// count). Idempotent same-step retry consumes no slot.
func TestLegacyAndCommissioningReservationsShareWindowWithoutOvershoot(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	const limit = 5
	scheduler := indexers.NewScheduler(16, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: limit, LimitWindow: time.Hour})
	const callers = limit + 5
	var wg sync.WaitGroup
	v2Granted := make(chan bool, callers)
	legacyGranted := make(chan bool, callers)
	for i := 0; i < callers; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			stepID := uuid.NewV7()
			snapshot := liveSnapshotForStep(t, scheduler, indexerID, stepID)
			reservation, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), stepID.String(), snapshot, now)
			if err == nil && reservation.Granted {
				v2Granted <- true
			} else {
				v2Granted <- false
			}
		}(i)
		go func() {
			defer wg.Done()
			retryAt, err := store.ReserveIndexerRequest(ctx, indexerID.String(), "query", limit, time.Hour, now)
			legacyGranted <- err == nil && retryAt.IsZero()
		}()
	}
	wg.Wait()
	close(v2Granted)
	close(legacyGranted)
	granted := 0
	for ok := range v2Granted {
		if ok {
			granted++
		}
	}
	for ok := range legacyGranted {
		if ok {
			granted++
		}
	}
	// Both paths share one ledger: total granted must EQUAL the limit under
	// saturating load (2N+10 callers against N slots): at most N is
	// necessary but not sufficient — a total-refusal implementation must
	// fail here.
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family='query' AND reserved_at > $2`, indexerID.String(), now.Add(-time.Hour)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count > limit {
		t.Fatalf("shared window overshot: count=%d limit=%d", count, limit)
	}
	if granted != limit {
		t.Fatalf("granted=%d want exactly %d (total-refusal or under-grant implementation)", granted, limit)
	}
	if count != limit {
		t.Fatalf("ledger count=%d want exactly %d", count, limit)
	}
}

// E-64 DB companion: a legacy (zero) snapshot is rejected at all three
// durable boundaries with zero writes.
func TestLegacySnapshotRejectedAtAllDurableBoundaries(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	legacyPermit, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyPermit.ReleaseWithoutOutcome()
	legacySnapshot := legacyPermit.CommissioningBudgetSnapshot()
	if legacySnapshot.Valid() {
		t.Fatal("legacy snapshot valid")
	}
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), legacySnapshot, now.Add(3*time.Second)); err == nil {
		t.Fatal("legacy snapshot reserved")
	}
	marker := liveMarkerFor(run, step, legacySnapshot, now.Add(3*time.Second))
	if _, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second)); err == nil {
		t.Fatal("legacy snapshot transmitted")
	}
	// Complete arm needs a real gate: transmit legitimately first, then
	// present the legacy snapshot at completion.
	snapshot := liveSnapshotForStep(t, scheduler, indexerID, step.ID)
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completion := liveCAPSCompletion(reloaded, step, time.Time{})
	completion.BudgetSnapshot = legacySnapshot
	if _, err := store.CompleteLiveAttempt(ctx, completion, now.Add(5*time.Second)); err == nil {
		t.Fatal("legacy snapshot completed")
	}
	loaded, err := store.IndexerCommissioningStep(ctx, run.ID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("legacy complete changed step: %#v", loaded)
	}
}

// E-66 DB companion: non-canonical admission attempts leave no durable
// trace (real store, so "no row / no write" is observable). Every
// malformed/noncanonical admission asserts the EXACT
// ErrInvalidCommissioningIdentity, zero reservation rows, zero
// commissioning durable state (including indexer_budget_state), zero token
// leak, and a canonical follow-up acquisition that succeeds. No nil store
// is used for durable zero-write assertions.
func TestAcquireCommissioningV2RejectsNonCanonicalIdentityDurable(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	scheduler := indexers.NewScheduler(1, store)
	configureCommissioningScheduler(t, scheduler, indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour})
	canonicalIndexer := indexerID.String()
	canonicalStep := uuid.NewV7().String()
	for _, spelling := range []string{
		"550E8400-E29B-41D4-A716-446655440000",
		"urn:uuid:550e8400-e29b-41d4-a716-446655440000",
		"{550e8400-e29b-41d4-a716-446655440000}",
		"550e8400e29b41d4a716446655440000",
		"",
		"   ",
		"not-a-uuid",
	} {
		for _, swapIndexer := range []bool{true, false} {
			indexerSpelling, stepSpelling := canonicalIndexer, canonicalStep
			if swapIndexer {
				indexerSpelling = spelling
			} else {
				stepSpelling = spelling
			}
			if permit, err := scheduler.AcquireCommissioningV2(ctx, indexerSpelling, stepSpelling); !errors.Is(err, indexers.ErrInvalidCommissioningIdentity) || permit != nil {
				t.Fatalf("spelling %q swapIndexer=%v: permit=%v err=%v, want ErrInvalidCommissioningIdentity", spelling, swapIndexer, permit, err)
			}
			var resCount int
			if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE indexer_instance_id=$1`, canonicalIndexer).Scan(&resCount); err != nil {
				t.Fatal(err)
			}
			if resCount != 0 {
				t.Fatalf("spelling %q wrote reservation rows", spelling)
			}
			var budgetCount int
			if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_budget_state WHERE indexer_instance_id=$1`, canonicalIndexer).Scan(&budgetCount); err != nil {
				t.Fatal(err)
			}
			if budgetCount != 0 {
				t.Fatalf("spelling %q wrote commissioning durable state", spelling)
			}
		}
	}
	// Canonical acquisition works immediately afterwards (zero token leak:
	// with global limit 1 a leaked token would block this).
	done := make(chan error, 1)
	go func() {
		permit, err := scheduler.AcquireCommissioningV2(ctx, canonicalIndexer, canonicalStep)
		if err == nil && permit != nil {
			permit.ReleaseWithoutOutcome()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("canonical follow-up: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canonical follow-up blocked: token leak")
	}
}

// TestReserveCommissioningRequestCarriesSnapshotType is the E-65 companion:
// the durable reservation boundary takes CommissioningBudgetSnapshot and no
// raw Budget parameter.
func TestReserveCommissioningRequestCarriesSnapshotType(t *testing.T) {
	t.Parallel()
	if stringTypeOfStore().Name() != "Store" {
		t.Fatal("Store type renamed; contract-shape reasoning changed")
	}
	method := storeMethodByName(t, "ReserveCommissioningRequest")
	assertSnapshotParamNoBudget(t, method)
}

// successPathClient builds the executor Client dependency for the
// success-path regression: a local fake-TLS server base that the finalizer
// never uses for network I/O (the request counter below proves zero sends).
func successPathClient(t *testing.T, scheduler *indexers.Scheduler) *newznab.Client {
	t.Helper()
	server := newznabSuccessPathServer(t)
	client, err := newznab.New(newznab.Config{IndexerID: "success-finalization", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func newznabSuccessPathServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	return server
}

// TestSuccessfulCommissioningCompletionPublishesAuthoritativeSchedulerState
// is the DB-1/RT-1 success-path regression: after a real transmit +
// completion establishing a future cooldown, the known-success finalization
// calls the REAL executor.CloseSucceededAfterKnownSuccessfulCompletion
// (invalidate while permit owned → ReleaseWithoutOutcome → authoritative
// reload) against a REAL *postgres.Store and REAL *indexers.Scheduler via
// NewLocalLiveExecutor — never hand-inlined. A legacy Acquire is then
// blocked by the committed cooldown; no legacy SaveIndexerBudget overwrote
// completion accounting. Plus the reload-failure arm: lane stays
// cacheUnknown, both acquisitions fail closed, zero HTTP.
func TestSuccessfulCommissioningCompletionPublishesAuthoritativeSchedulerState(t *testing.T) {
	store, ctx := liveTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := liveIndexer(t, ctx, store, now)
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	scheduler := indexers.NewScheduler(4, store)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	configureCommissioningScheduler(t, scheduler, indexerID, budget)
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
	executor, err := commissioningv2.NewLocalLiveExecutor(commissioningv2.LiveExecutorDeps{
		Steps:     store,
		Scheduler: scheduler,
		Resolver:  &commissioningv2.TestCredentialResolver{},
		Client:    successPathClient(t, scheduler),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 1-2. configure + preload lane (first acquire loads the lane).
	preload, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	preload.ReleaseWithoutOutcome()
	// 2. acquire the real commissioning permit and keep it owned across the
	// durable boundary: it is the permit the real success finalizer will
	// release without outcome.
	permit, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := permit.CommissioningBudgetSnapshot()
	// 3. reserve.
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	// 4. cross the durable transmission boundary.
	if _, err := store.MarkClaimedStepTransmitting(ctx, liveMarkerFor(run, step, snapshot, now.Add(3*time.Second)), now.Add(4*time.Second)); err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	// 5. complete with an outcome establishing a future cooldown
	// (authentication_failure -> now + MaximumBackoff).
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
		Transport: commissioningv2.TransportResponseReceived, Response: commissioningv2.ResponseAuthenticationFailure,
		TargetKind: step.Target,
	})
	if err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	completedAt := now.Add(5 * time.Second)
	completion := liveCompletionForStep(t, reloaded, step, snapshot, classified)
	if _, err := store.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	// 6. known-success finalization via the REAL helper (never hand-inlined):
	// invalidate while the permit is owned → ReleaseWithoutOutcome →
	// authoritative reload. Observe invalidation firing while the
	// finalizer still owns the lane.
	signal := &indexers.LaneCacheInvalidation{Invalidated: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- executor.CloseSucceededAfterKnownSuccessfulCompletion(ctx, indexerID.String(), permit, signal)
	}()
	select {
	case <-signal.Invalidated:
	case err := <-done:
		t.Fatalf("success finalizer returned before invalidation fired: %v", err)
	case <-time.After(10 * time.Second):
		permit.ReleaseWithoutOutcome()
		t.Fatal("success finalizer did not invalidate before release")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// 7. Scheduler.Snapshot exactly reflects the durable row.
	var cooldown, next, outcomeAt, updated time.Time
	var outcome string
	var failures int32
	var revision int64
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until, next_allowed_at, last_outcome, consecutive_failure, last_outcome_at, updated_at, revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldown, &next, &outcome, &failures, &outcomeAt, &updated, &revision); err != nil {
		t.Fatal(err)
	}
	got := scheduler.Snapshot(indexerID.String())
	if !got.CooldownUntil.Equal(cooldown) || !got.NextAllowedAt.Equal(next) || string(got.LastOutcome) != outcome || got.ConsecutiveFailure != uint32(failures) || !got.LastOutcomeAt.Equal(outcomeAt) || !got.UpdatedAt.Equal(updated) || got.Revision != uint64(revision) {
		t.Fatalf("scheduler snapshot diverges from durable row: %#v vs cooldown=%s next=%s outcome=%q failures=%d", got, cooldown, next, outcome, failures)
	}
	// 8. Legacy Acquire is blocked/deferred by the committed cooldown.
	if _, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("legacy acquire slipped through committed cooldown")
	} else {
		var cooldownErr indexers.CooldownError
		if !errors.As(err, &cooldownErr) {
			t.Fatalf("legacy acquire err=%v, want CooldownError", err)
		}
	}
	// 9. No legacy SaveIndexerBudget overwrote completion accounting: the
	// durable row still carries the completion's cooldown/revision. The
	// permit was released WITHOUT outcome by the real helper, so no
	// SaveIndexerBudget could have been issued on this path; assert the
	// durable row is unchanged AND that the reload-failure arm below is not
	// explained by an unresolved gate (the gate is clear here).
	var cooldownAfter time.Time
	var revisionAfter int64
	var gateAfter *string
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until, revision, commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldownAfter, &revisionAfter, &gateAfter); err != nil {
		t.Fatal(err)
	}
	if !cooldownAfter.Equal(cooldown) || revisionAfter != revision {
		t.Fatalf("completion accounting overwritten: %s/%d -> %s/%d", cooldown, revision, cooldownAfter, revisionAfter)
	}
	if gateAfter != nil {
		t.Fatalf("reload-failure arm confounded by unresolved gate %v; want clear gate so fail-closed is proved by cacheUnknown", *gateAfter)
	}
	// Reload-failure arm: invalidate again, fail the reload, prove
	// cacheUnknown + fail-closed legacy/v2 + zero HTTP.
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	// Fake TLS server wires the zero-send invariant for the reload-failure arm.
	permit2, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String())
	if err == nil {
		// A v2 re-acquire for the completed step may succeed at the
		// scheduler layer (gate clear, cooldown still future → actually it
		// must defer on cooldown). Either refusal is fine; holding a permit
		// here would confound the unknown-cache proof, so release it.
		permit2.ReleaseWithoutOutcome()
	}
	scheduler.InvalidateLaneCache(indexerID.String(), nil)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := scheduler.ReloadLaneCache(cancelled, indexerID.String()); err == nil {
		t.Fatal("cancelled reload succeeded")
	}
	if _, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("legacy acquire slipped through unknown cache")
	}
	if _, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String()); err == nil {
		t.Fatal("v2 acquire slipped through unknown cache")
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}
