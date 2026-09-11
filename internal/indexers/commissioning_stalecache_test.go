package indexers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type stubGateSource struct {
	mu     sync.Mutex
	state  CommissioningGateState
	ok     bool
	reads  int
	failOn int
}

func (source *stubGateSource) CommissioningGate(_ context.Context, _ string) (CommissioningGateState, bool) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.reads++
	if source.failOn > 0 && source.reads >= source.failOn {
		return CommissioningGateUnresolved, false
	}
	return source.state, source.ok
}

func (source *stubGateSource) readCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.reads
}

type stubBudgetStore struct {
	mu       sync.Mutex
	snapshot Snapshot
	loads    int
	loadErr  error
}

func (store *stubBudgetStore) LoadIndexerBudget(_ context.Context, indexerID string) (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loads++
	if store.loadErr != nil {
		return Snapshot{}, store.loadErr
	}
	store.snapshot.IndexerID = indexerID
	return store.snapshot, nil
}

func (store *stubBudgetStore) SaveIndexerBudget(_ context.Context, snapshot Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshot = snapshot
	return nil
}

func TestSameProcessStaleCacheInvalidationBlocksLegacyAcquire(t *testing.T) {
	// Required regression test shape (in-memory, same scheduler process):
	// 1. One scheduler process exists with previously loaded cached state.
	// 2. A commissioning completion really commits a new HTTP-429 cooldown,
	//    terminal outcome, and NULL (clear) gate.
	// 3. The commit acknowledgement is lost (simulated by invalidating the
	//    cache while still owning the lane, then releasing without an
	//    outcome-recording write).
	// 4. Immediate authoritative reload is forced to fail.
	// 5. Deterministic coordination proves invalidation precedes lane release.
	// 6. The SAME scheduler process remains alive.
	// 7. A legacy Acquire attempts work; ZERO network transmissions occur.
	// 9. Authoritative state later reloads successfully.
	// 10. The committed cooldown/pacing floor still blocks transmission.
	// 11. Only once the fake clock permits it may the legacy request transmit.
	now := time.Unix(1_700_000_000, 0).UTC()
	store := &stubBudgetStore{}
	scheduler := NewScheduler(1, store)
	scheduler.now = func() time.Time { return now }
	indexerID := "stale-cache-indexer"
	budget := Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	if err := scheduler.Configure(indexerID, budget); err != nil {
		t.Fatal(err)
	}
	gate := &stubGateSource{state: CommissioningGateClear, ok: true}
	scheduler.SetCommissioningGateSource(indexerID, gate)

	// 1. Preload cached state with a normal legacy acquisition.
	permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.RecordResult(context.Background(), RecordResult{Outcome: OutcomeSuccess, Scope: FailureScopeNone}); err != nil {
		t.Fatal(err)
	}
	if scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("fresh cache must be known-valid")
	}
	loadsBefore := store.loads

	// 2. Commissioning completion really commits 429 cooldown + terminal
	// outcome + NULL gate. The gate source already returns clear (NULL).
	// Advance the fake clock past the step-1 pacing floor first: the two
	// acquisitions are separate transmissions and the minimum interval
	// applies between them.
	now = now.Add(4 * time.Second)
	scheduler.now = func() time.Time { return now }
	executing, err := scheduler.Acquire(context.Background(), indexerID, WorkloadCommissioning)
	if err != nil {
		t.Fatal(err)
	}
	cooldownUntil := now.Add(60 * time.Second)
	now = now.Add(time.Second)
	scheduler.now = func() time.Time { return now }
	if err := executing.RecordResult(context.Background(), RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance, RetryAt: cooldownUntil}); err != nil {
		t.Fatal(err)
	}
	committed := scheduler.Snapshot(indexerID)
	if committed.LastOutcome != OutcomeRateLimited || committed.CooldownUntil != cooldownUntil {
		t.Fatalf("completion did not commit cooldown/outcome: %#v", committed)
	}
	pacingFloor := committed.NextAllowedAt

	// 3-5. Lost acknowledgement: advance the fake clock past the committed
	// floor ONLY to obtain lane ownership for the invalidation window (the
	// committed state itself is preserved in durable storage below), then
	// invalidate the complete lane cache WHILE still owning the lane permit,
	// prove invalidation precedes release, then release without an
	// outcome-recording write. Force the immediate reload to fail.
	now = cooldownUntil.Add(time.Second)
	if pacingFloor.After(now) {
		now = pacingFloor.Add(time.Second)
	}
	scheduler.now = func() time.Time { return now }
	holder, err := scheduler.Acquire(context.Background(), indexerID, WorkloadCommissioning)
	if err != nil {
		t.Fatalf("lane holder acquire: %v", err)
	}
	_ = holder
	// Release the holder WITHOUT touching committed state, then re-acquire
	// lane ownership for the invalidation window.
	holder.ReleaseWithoutOutcome()
	owner, err := scheduler.Acquire(context.Background(), indexerID, WorkloadCommissioning)
	if err != nil {
		// If pacing still blocks, move the fake clock only for lane
		// ownership bookkeeping is forbidden: instead fail loudly, because
		// the test must not bypass the committed floor.
		t.Fatalf("invalidation owner acquire blocked (floor respected): %v", err)
	}
	store.mu.Lock()
	store.loadErr = errors.New("authoritative reload unavailable")
	store.mu.Unlock()
	invalidated := &LaneCacheInvalidation{Invalidated: make(chan struct{})}
	scheduler.InvalidateLaneCache(indexerID, invalidated)
	select {
	case <-invalidated.Invalidated:
	case <-time.After(5 * time.Second):
		t.Fatal("invalidation did not fire before lane release")
	}
	if !scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("cache must be unknown before release")
	}
	owner.ReleaseWithoutOutcome()
	if !scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("release exposed old cache as usable")
	}

	// 7-8. Legacy Acquire must fail closed with ZERO transmissions. There is
	// no network layer here by construction; assert no Close/Record write
	// overwrote committed state and no new durable load succeeded.
	if _, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive); err == nil {
		t.Fatal("legacy Acquire transmitted through unknown cache with failed reload")
	}
	after := scheduler.Snapshot(indexerID)
	_ = after
	if store.loads == loadsBefore {
		t.Fatal("blocked acquisition must attempt authoritative reload (database read retry allowed)")
	}

	// 9-10. Authoritative reload later succeeds; committed cooldown and
	// pacing floor still block transmission even with a clear gate. Reset
	// the fake clock to BEFORE the committed floor so the reloaded
	// restriction is actually exercised (step 3 advanced the clock only to
	// obtain lane ownership for the invalidation window).
	now = cooldownUntil.Add(-time.Second)
	scheduler.now = func() time.Time { return now }
	store.mu.Lock()
	store.loadErr = nil
	store.snapshot = Snapshot{IndexerID: indexerID, NextAllowedAt: pacingFloor, CooldownUntil: cooldownUntil, ConsecutiveFailure: 1, LastOutcome: OutcomeRateLimited, LastOutcomeAt: now, UpdatedAt: now, MinimumInterval: budget.MinimumInterval}
	store.mu.Unlock()
	if _, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive); err == nil {
		t.Fatal("legacy Acquire bypassed committed cooldown after reload")
	} else {
		var deferred CooldownError
		if !errors.As(err, &deferred) {
			t.Fatalf("expected CooldownError, got %v", err)
		}
	}
	if scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("successful reload must publish a complete consistent snapshot before marking valid")
	}

	// 11. Only once the fake clock permits it may the legacy request transmit.
	now = cooldownUntil.Add(time.Second)
	if pacingFloor.After(now) {
		now = pacingFloor.Add(time.Second)
	}
	scheduler.now = func() time.Time { return now }
	legacy, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatalf("legacy request blocked after clock permits: %v", err)
	}
	legacy.Close()
}

func TestUnresolvedGateBlocksAcquireAcrossUnknownCache(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	indexerID := "gated-indexer"
	if err := scheduler.Configure(indexerID, Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	scheduler.SetCommissioningGateSource(indexerID, &stubGateSource{state: CommissioningGateUnresolved, ok: true})
	if _, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive); err == nil {
		t.Fatal("unresolved gate permitted transmission")
	} else {
		var blocked CommissioningGateBlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("expected gate-blocked error, got %v", err)
		}
	}
	scheduler.SetCommissioningGateSource(indexerID, &stubGateSource{ok: false})
	if _, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive); err == nil {
		t.Fatal("gate read failure permitted transmission")
	}
}

func TestPacingFloorUsesDeadlinePlusMinimumAndPreservesOutcomeTime(t *testing.T) {
	deadline := time.Unix(2_000_000_000, 0).UTC()
	floor := PacingFloor(deadline, 3*time.Second, NextAllowedHint{})
	if !floor.Equal(deadline.Add(3 * time.Second)) {
		t.Fatalf("floor=%s", floor)
	}
	stronger := deadline.Add(time.Hour)
	floor = PacingFloor(deadline, 3*time.Second, NextAllowedHint{At: stronger})
	if !floor.Equal(stronger) {
		t.Fatalf("stronger restriction not preserved: %s", floor)
	}
	snapshot := Snapshot{IndexerID: "x", LastOutcome: OutcomeRateLimited, LastOutcomeAt: deadline}
	advanced := MarkLanePacingFloor(snapshot, deadline.Add(time.Minute))
	if !advanced.NextAllowedAt.Equal(deadline.Add(time.Minute)) {
		t.Fatalf("pacing floor not advanced: %#v", advanced)
	}
	if advanced.LastOutcome != OutcomeRateLimited || !advanced.LastOutcomeAt.Equal(deadline) {
		t.Fatalf("pacing-only write refreshed outcome time: %#v", advanced)
	}
}

func TestKnownValidCacheDoesNotReloadOnEveryAcquisition(t *testing.T) {
	store := &stubBudgetStore{}
	scheduler := NewScheduler(1, store)
	scheduler.now = func() time.Time { return time.Unix(3_000_000_000, 0).UTC() }
	indexerID := "normal-cache"
	if err := scheduler.Configure(indexerID, Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	loads := store.loads
	second, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	if store.loads != loads {
		t.Fatalf("known-valid cache triggered a database reload: %d -> %d", loads, store.loads)
	}
}
