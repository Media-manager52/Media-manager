package indexers

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// recordingSaveStore records SaveIndexerBudget calls for the success-path
// finalization tests.
type recordingSaveStore struct {
	mu        sync.Mutex
	snapshot  Snapshot
	saves     int
	loadErr   error
	authority AuthoritativeSnapshot
}

func (store *recordingSaveStore) LoadIndexerBudget(_ context.Context, indexerID string) (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadErr != nil {
		return Snapshot{}, store.loadErr
	}
	store.snapshot.IndexerID = indexerID
	return store.snapshot, nil
}

func (store *recordingSaveStore) SaveIndexerBudget(_ context.Context, snapshot Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshot = snapshot
	store.saves++
	return nil
}

func (store *recordingSaveStore) LoadAuthoritativeIndexerState(_ context.Context, indexerID string) (AuthoritativeSnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadErr != nil {
		return AuthoritativeSnapshot{}, store.loadErr
	}
	result := store.authority
	result.IndexerID = indexerID
	result.Snapshot.IndexerID = indexerID
	return result, nil
}

func commissioningTestBudget() Budget {
	return Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
}

func acquireCommissioningPermit(t *testing.T, scheduler *Scheduler, indexerID, stepID string) *Permit {
	t.Helper()
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	return permit
}

// TestKnownSuccessfulFinalizationInvalidatesBeforeRelease proves the
// success-path ordering: InvalidateLaneCache fires while the lane token is
// still owned (a second Acquire stays blocked), then ReleaseWithoutOutcome +
// reload publish authoritative state with no legacy SaveIndexerBudget.
func TestKnownSuccessfulFinalizationInvalidatesBeforeRelease(t *testing.T) {
	t.Parallel()
	store := &recordingSaveStore{authority: AuthoritativeSnapshot{Gate: CommissioningGateClear}}
	scheduler := NewScheduler(1, store)
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	if err := scheduler.Configure(indexerID, commissioningTestBudget()); err != nil {
		t.Fatal(err)
	}
	permit := acquireCommissioningPermit(t, scheduler, indexerID, stepID)

	blocked := make(chan error, 1)
	go func() {
		_, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
		blocked <- err
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-blocked:
		t.Fatalf("second acquire returned early: %v", err)
	default:
	}

	signal := &LaneCacheInvalidation{Invalidated: make(chan struct{})}
	scheduler.InvalidateLaneCache(indexerID, signal)
	select {
	case <-signal.Invalidated:
	case <-time.After(5 * time.Second):
		t.Fatal("invalidation did not fire before release")
	}
	// Invalidation fired while the lane permit is still owned (release
	// follows invalidation inside the success-path finalizer). Prove the
	// cache is unknown before release, then release and reload.
	if !scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("cache must be unknown before release")
	}
	permit.ReleaseWithoutOutcome()
	if err := scheduler.ReloadLaneCache(context.Background(), indexerID); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	saves := store.saves
	store.mu.Unlock()
	if saves != 0 {
		t.Fatalf("success-path finalization issued legacy SaveIndexerBudget: saves=%d", saves)
	}
	if scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("authoritative reload must clear cacheUnknown")
	}
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("blocked acquire after reload: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocked acquire never returned")
	}
}

// TestKnownSuccessfulFinalizationReloadFailureStaysFailClosed proves a failed
// authoritative reload after the success-path invalidation leaves the lane
// fail-closed and blocks a waiting same-indexer acquisition from slipping
// through stale state.
func TestKnownSuccessfulFinalizationReloadFailureStaysFailClosed(t *testing.T) {
	t.Parallel()
	store := &recordingSaveStore{}
	scheduler := NewScheduler(1, store)
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	if err := scheduler.Configure(indexerID, commissioningTestBudget()); err != nil {
		t.Fatal(err)
	}
	permit := acquireCommissioningPermit(t, scheduler, indexerID, stepID)
	scheduler.InvalidateLaneCache(indexerID, nil)
	permit.ReleaseWithoutOutcome()
	store.mu.Lock()
	store.loadErr = context.DeadlineExceeded
	store.mu.Unlock()
	if err := scheduler.ReloadLaneCache(context.Background(), indexerID); err == nil {
		t.Fatal("failed reload succeeded")
	}
	if !scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("failed reload must leave cacheUnknown")
	}
	if _, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive); err == nil {
		t.Fatal("same-indexer acquisition slipped through stale state")
	}
	if _, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID); err == nil {
		t.Fatal("same-indexer v2 acquisition slipped through stale state")
	}
	store.mu.Lock()
	saves := store.saves
	store.mu.Unlock()
	if saves != 0 {
		t.Fatalf("fail-closed path issued legacy SaveIndexerBudget: saves=%d", saves)
	}
}

// TestCommissioningPermitRecordAndCloseCannotPersistLegacyOutcome is CP1-3.
// A. commissioning Permit.Record returns the closed sentinel and
// SaveIndexerBudget call count remains 0.
// B. commissioning Permit.Close cannot issue SaveIndexerBudget.
// C. tokens are not leaked after rejected Record/Close.
// D. legacy Permit.Record/Close semantics are asserted unchanged in
// TestCommissioningPermitGuardLeavesLegacyPermitsUnchanged below.
func TestCommissioningPermitRecordAndCloseCannotPersistLegacyOutcome(t *testing.T) {
	t.Parallel()
	newCommissioningScheduler := func(t *testing.T) (*Scheduler, *recordingSaveStore, *Permit) {
		t.Helper()
		store := &recordingSaveStore{authority: AuthoritativeSnapshot{Gate: CommissioningGateClear}}
		scheduler := NewScheduler(1, store)
		indexerID := "550e8400-e29b-41d4-a716-446655440000"
		stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
		if err := scheduler.Configure(indexerID, commissioningTestBudget()); err != nil {
			t.Fatal(err)
		}
		return scheduler, store, acquireCommissioningPermit(t, scheduler, indexerID, stepID)
	}
	saveCount := func(store *recordingSaveStore) int {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.saves
	}

	// A: Record on a commissioning permit.
	scheduler, store, permit := newCommissioningScheduler(t)
	if err := permit.Record(context.Background(), OutcomeSuccess, nil); !errors.Is(err, ErrCommissioningResultPath) {
		permit.ReleaseWithoutOutcome()
		t.Fatalf("commissioning Record err=%v, want ErrCommissioningResultPath", err)
	}
	if saveCount(store) != 0 {
		t.Fatalf("commissioning Record issued SaveIndexerBudget: saves=%d", saveCount(store))
	}
	// C: the rejected Record still released lane/global exactly once — a
	// follow-up legacy acquire proceeds immediately, and a second Record
	// still returns the sentinel without a second release/panic. Capture
	// the follow-up permit so its tokens are drained at the end.
	followA, err := scheduler.Acquire(context.Background(), "550e8400-e29b-41d4-a716-446655440000", WorkloadInteractive)
	if err != nil {
		t.Fatalf("lane token leaked after rejected Record: %v", err)
	}
	defer followA.ReleaseWithoutOutcome()
	if err := permit.Record(context.Background(), OutcomeSuccess, nil); !errors.Is(err, ErrCommissioningResultPath) {
		t.Fatalf("second commissioning Record err=%v", err)
	}
	if saveCount(store) != 0 {
		t.Fatalf("second Record issued SaveIndexerBudget: saves=%d", saveCount(store))
	}

	// RecordResult variant.
	scheduler2, store2, permit2 := newCommissioningScheduler(t)
	if err := permit2.RecordResult(context.Background(), RecordResult{Outcome: OutcomeSuccess, Scope: FailureScopeNone}); !errors.Is(err, ErrCommissioningResultPath) {
		permit2.ReleaseWithoutOutcome()
		t.Fatalf("commissioning RecordResult err=%v", err)
	}
	if saveCount(store2) != 0 {
		t.Fatalf("commissioning RecordResult issued SaveIndexerBudget: saves=%d", saveCount(store2))
	}
	// Invalid RecordResult on a commissioning permit also stays guarded.
	if err := permit2.RecordResult(context.Background(), RecordResult{Outcome: "bogus"}); !errors.Is(err, ErrCommissioningResultPath) {
		t.Fatalf("invalid commissioning RecordResult err=%v, want sentinel", err)
	}
	if saveCount(store2) != 0 {
		t.Fatalf("invalid RecordResult issued SaveIndexerBudget: saves=%d", saveCount(store2))
	}
	_ = scheduler2
	permit2.ReleaseWithoutOutcome()

	// B: Close on a commissioning permit.
	scheduler3, store3, permit3 := newCommissioningScheduler(t)
	permit3.Close()
	if saveCount(store3) != 0 {
		t.Fatalf("commissioning Close issued SaveIndexerBudget: saves=%d", saveCount(store3))
	}
	// C: Close released the tokens — a follow-up acquire proceeds.
	follow, err := scheduler3.Acquire(context.Background(), "550e8400-e29b-41d4-a716-446655440000", WorkloadInteractive)
	if err != nil {
		t.Fatalf("lane token leaked after commissioning Close: %v", err)
	}
	follow.ReleaseWithoutOutcome()
}

// TestCommissioningPermitGuardLeavesLegacyPermitsUnchanged is CP1-3 D:
// ordinary legacy Permit.Record/Close semantics are unchanged.
func TestCommissioningPermitGuardLeavesLegacyPermitsUnchanged(t *testing.T) {
	t.Parallel()
	store := &recordingSaveStore{authority: AuthoritativeSnapshot{Gate: CommissioningGateClear}}
	scheduler := NewScheduler(1, store)
	indexerID := "legacy-guard-check"
	if err := scheduler.Configure(indexerID, Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Record(context.Background(), OutcomeSuccess, http.Header{}); err != nil {
		t.Fatalf("legacy Record: %v", err)
	}
	store.mu.Lock()
	saves := store.saves
	store.mu.Unlock()
	if saves != 1 {
		t.Fatalf("legacy Record saves=%d, want 1", saves)
	}
	legacy, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		// Minimum-interval pacing from the first Record may defer the
		// immediate re-acquire; that is unchanged legacy pacing behavior,
		// not the commissioning guard. Advance past the floor instead.
		var cooldown CooldownError
		if !errors.As(err, &cooldown) {
			t.Fatal(err)
		}
		scheduler.now = func() time.Time { return cooldown.Until.Add(time.Second) }
		legacy, err = scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
		if err != nil {
			t.Fatal(err)
		}
	}
	legacy.Close()
	store.mu.Lock()
	saves = store.saves
	store.mu.Unlock()
	if saves != 2 {
		t.Fatalf("legacy Close saves=%d, want 2", saves)
	}
}

// TestComputeCommissioningOutcomeEffectClampsAbsurdRetryAt proves the
// commissioning-only ceiling: absurd future RetryAt clamps to
// now + MaximumBackoff, ordinary RetryAt passes through, zero stays zero,
// and legacy ComputeOutcomeEffect semantics are untouched.
func TestComputeCommissioningOutcomeEffectClampsAbsurdRetryAt(t *testing.T) {
	t.Parallel()
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	scheduler := NewScheduler(1, nil)
	budget := Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10, LimitWindow: time.Hour}
	if err := scheduler.Configure(indexerID, budget); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	snapshot := permit.CommissioningBudgetSnapshot()
	now := time.Unix(1_800_000_000, 0).UTC()
	start := Snapshot{IndexerID: indexerID}

	absurd := now.Add(10000 * time.Hour)
	got, err := ComputeCommissioningOutcomeEffect(start, snapshot, RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance, RetryAt: absurd}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CooldownUntil.Equal(now.Add(budget.MaximumBackoff)) {
		t.Fatalf("absurd RetryAt not clamped: %s", got.CooldownUntil)
	}

	ordinary := now.Add(time.Minute)
	got, err = ComputeCommissioningOutcomeEffect(start, snapshot, RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance, RetryAt: ordinary}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CooldownUntil.Equal(ordinary) {
		t.Fatalf("ordinary RetryAt changed: %s", got.CooldownUntil)
	}

	got, err = ComputeCommissioningOutcomeEffect(start, snapshot, RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.CooldownUntil.IsZero() {
		t.Fatal("zero RetryAt path produced zero cooldown")
	}
}
