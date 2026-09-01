package indexers

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reservationMemory struct {
	mu           sync.Mutex
	reservations map[string][]time.Time
	snapshot     Snapshot
}

func (store *reservationMemory) LoadIndexerBudget(_ context.Context, indexerID string) (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshot.IndexerID = indexerID
	return store.snapshot, nil
}

func (store *reservationMemory) SaveIndexerBudget(_ context.Context, snapshot Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.snapshot = snapshot
	return nil
}

func (store *reservationMemory) ReserveIndexerRequest(_ context.Context, indexerID, family string, limit int, window time.Duration, now time.Time) (time.Time, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := indexerID + ":" + family
	current := store.reservations[key]
	if len(current) >= limit {
		return current[0].Add(window), nil
	}
	store.reservations[key] = append(current, now)
	return time.Time{}, nil
}

func TestSchedulerAllowsOnlyOneInflightPerIndexer(t *testing.T) {
	scheduler := NewScheduler(4, nil)
	if err := scheduler.Configure("one", Budget{FailureBackoff: time.Millisecond, MaximumBackoff: time.Second}); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.Acquire(context.Background(), "one", WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	var acquired atomic.Bool
	done := make(chan struct{})
	go func() {
		second, acquireErr := scheduler.Acquire(context.Background(), "one", WorkloadCommissioning)
		if acquireErr == nil {
			acquired.Store(true)
			second.Close()
		}
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("second request bypassed the indexer lane")
	}
	if err := first.Record(context.Background(), OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second request never acquired")
	}
}

func TestRateLimitDoesNotBecomeUnsupportedAndHonorsRetryAfter(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	if err := scheduler.Configure("one", Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), "one", WorkloadCommissioning)
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("Retry-After", "30")
	if err := permit.Record(context.Background(), OutcomeRateLimited, headers); err != nil {
		t.Fatal(err)
	}
	snapshot := scheduler.Snapshot("one")
	if snapshot.LastOutcome != OutcomeRateLimited || time.Until(snapshot.CooldownUntil) < 29*time.Second {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestPersistedCooldownDefersWithoutHoldingSchedulerSlots(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	scheduler := NewScheduler(1, nil)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.Configure("one", Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	lane := scheduler.getLane("one")
	lane.mu.Lock()
	lane.loaded = true
	lane.snapshot = Snapshot{IndexerID: "one", CooldownUntil: now.Add(30 * time.Second)}
	lane.mu.Unlock()
	started := time.Now()
	_, err := scheduler.Acquire(context.Background(), "one", WorkloadRetrieval)
	var deferred CooldownError
	if !errors.As(err, &deferred) || deferred.RetryAt() != now.Add(30*time.Second) {
		t.Fatalf("deferred error=%v retry=%v", err, deferred.RetryAt())
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("cooldown blocked the caller")
	}
	lane.mu.Lock()
	lane.snapshot.CooldownUntil = now
	lane.mu.Unlock()
	permit, err := scheduler.Acquire(context.Background(), "one", WorkloadRetrieval)
	if err != nil {
		t.Fatalf("scheduler slots were retained after deferral: %v", err)
	}
	permit.Close()
}

func TestRollingBudgetsReserveBeforePermitAndSeparateQueriesFromGrabs(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	store := &reservationMemory{reservations: map[string][]time.Time{}}
	scheduler := NewScheduler(1, store)
	scheduler.now = func() time.Time { return now }
	budget := Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 1, GrabLimit: 1, LimitWindow: 24 * time.Hour}
	if err := scheduler.Configure("one", budget); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.Acquire(context.Background(), "one", WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Record(context.Background(), OutcomeNoResults, nil); err != nil {
		t.Fatal(err)
	}
	_, err = scheduler.Acquire(context.Background(), "one", WorkloadCommissioning)
	var deferred RequestBudgetError
	if !errors.As(err, &deferred) || deferred.RetryAt() != now.Add(24*time.Hour) {
		t.Fatalf("query budget error=%v retry=%v", err, deferred.RetryAt())
	}
	grab, err := scheduler.Acquire(context.Background(), "one", WorkloadRetrieval)
	if err != nil {
		t.Fatalf("grab did not use its independent budget: %v", err)
	}
	grab.Close()
}

func TestNewznabBudgetUsesConservativeFallbacks(t *testing.T) {
	budget := NewznabBudget(100, 0, 0, 0)
	if budget.MinimumInterval != 3*time.Second || budget.QueryLimit != ConservativeQueryLimit || budget.GrabLimit != ConservativeGrabLimit || budget.LimitWindow != 24*time.Hour {
		t.Fatalf("budget=%#v", budget)
	}
	configured := NewznabBudget(5000, 50, 10, 3600)
	if configured.MinimumInterval != 5*time.Second || configured.QueryLimit != 50 || configured.GrabLimit != 10 || configured.LimitWindow != time.Hour {
		t.Fatalf("configured budget=%#v", configured)
	}
}
