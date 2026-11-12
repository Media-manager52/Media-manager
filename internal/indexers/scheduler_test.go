package indexers

import (
	"context"
	"errors"
	"fmt"
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

func TestSuccessfulOutcomesResetFailuresAndAdvanceMinimumInterval(t *testing.T) {
	t.Parallel()
	for _, outcome := range []Outcome{OutcomeSuccess, OutcomeNoResults} {
		outcome := outcome
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			now := time.Unix(100, 0).UTC()
			minimum := 3 * time.Second
			scheduler := NewScheduler(1, nil)
			scheduler.now = func() time.Time { return now }
			if err := scheduler.Configure("reset-"+string(outcome), Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
				t.Fatal(err)
			}
			lane := scheduler.getLane("reset-" + string(outcome))
			lane.mu.Lock()
			lane.loaded = true
			lane.snapshot = Snapshot{IndexerID: "reset-" + string(outcome), ConsecutiveFailure: 3, LastOutcome: OutcomeSystemFailure}
			lane.mu.Unlock()

			permit, err := scheduler.Acquire(context.Background(), "reset-"+string(outcome), WorkloadInteractive)
			if err != nil {
				t.Fatal(err)
			}
			if err := permit.Record(context.Background(), outcome, nil); err != nil {
				t.Fatal(err)
			}
			snapshot := scheduler.Snapshot("reset-" + string(outcome))
			if snapshot.ConsecutiveFailure != 0 || snapshot.LastOutcome != outcome {
				t.Fatalf("outcome did not reset failures: %#v", snapshot)
			}
			if snapshot.NextAllowedAt != now.Add(minimum) {
				t.Fatalf("next allowed=%s want %s", snapshot.NextAllowedAt, now.Add(minimum))
			}
		})
	}
}

func TestRateLimitHonorsRetryAfter(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	minimum := 3 * time.Second
	scheduler := NewScheduler(1, nil)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.Configure("rate-limited", Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), "rate-limited", WorkloadCommissioning)
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("Retry-After", "30")
	if err := permit.Record(context.Background(), OutcomeRateLimited, headers); err != nil {
		t.Fatal(err)
	}
	snapshot := scheduler.Snapshot("rate-limited")
	if snapshot.LastOutcome != OutcomeRateLimited || snapshot.ConsecutiveFailure != 1 {
		t.Fatalf("unexpected rate-limit outcome: %#v", snapshot)
	}
	if snapshot.NextAllowedAt != now.Add(minimum) || snapshot.CooldownUntil != now.Add(30*time.Second) {
		t.Fatalf("unexpected rate-limit pacing: %#v", snapshot)
	}
}

func TestRateLimitRetryAfterIsBoundedByTheMaximumBackoff(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	budget := Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}
	for _, test := range []struct {
		name, retryAfter string
		want             func(string) time.Time
	}{
		{"a year is capped", "31536000", func(string) time.Time { return now.Add(budget.MaximumBackoff) }},
		{"zero uses the normal backoff", "0", func(id string) time.Time { return now.Add(backoff(id, 1, budget)) }},
	} {
		scheduler := NewScheduler(1, nil)
		scheduler.now = func() time.Time { return now }
		id := "bounded-" + test.retryAfter
		if err := scheduler.Configure(id, budget); err != nil {
			t.Fatal(err)
		}
		permit, err := scheduler.Acquire(context.Background(), id, WorkloadCommissioning)
		if err != nil {
			t.Fatal(err)
		}
		headers := make(http.Header)
		headers.Set("Retry-After", test.retryAfter)
		if err := permit.Record(context.Background(), OutcomeRateLimited, headers); err != nil {
			t.Fatal(err)
		}
		if got := scheduler.Snapshot(id).CooldownUntil; !got.Equal(test.want(id)) {
			t.Fatalf("%s: cooldown until %s, want %s", test.name, got, test.want(id))
		}
	}
}

func TestAuthenticationAndSystemFailuresBackOffInstance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		outcome Outcome
		until   func(time.Time, string, Budget) time.Time
	}{
		{
			name:    "authentication",
			outcome: OutcomeAuthFailed,
			until: func(now time.Time, _ string, budget Budget) time.Time {
				return now.Add(budget.MaximumBackoff)
			},
		},
		{
			name:    "system",
			outcome: OutcomeSystemFailure,
			until: func(now time.Time, indexerID string, budget Budget) time.Time {
				return now.Add(backoff(indexerID, 1, budget))
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			now := time.Unix(300, 0).UTC()
			indexerID := "failure-" + test.name
			budget := Budget{MinimumInterval: 3 * time.Second, FailureBackoff: 10 * time.Second, MaximumBackoff: time.Minute}
			scheduler := NewScheduler(1, nil)
			scheduler.now = func() time.Time { return now }
			if err := scheduler.Configure(indexerID, budget); err != nil {
				t.Fatal(err)
			}
			permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
			if err != nil {
				t.Fatal(err)
			}
			if err := permit.Record(context.Background(), test.outcome, nil); err != nil {
				t.Fatal(err)
			}
			snapshot := scheduler.Snapshot(indexerID)
			if snapshot.LastOutcome != test.outcome || snapshot.ConsecutiveFailure != 1 {
				t.Fatalf("unexpected failure outcome: %#v", snapshot)
			}
			if snapshot.CooldownUntil != test.until(now, indexerID, budget) {
				t.Fatalf("cooldown=%s want %s", snapshot.CooldownUntil, test.until(now, indexerID, budget))
			}
		})
	}
}

func TestRecordResultInstanceFailuresRetainGlobalBackoff(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		outcome Outcome
		retryAt func(time.Time) time.Time
		want    func(time.Time, string, Budget) time.Time
	}{
		{name: "rate limit", outcome: OutcomeRateLimited, retryAt: func(now time.Time) time.Time { return now.Add(45 * time.Second) }, want: func(now time.Time, _ string, _ Budget) time.Time { return now.Add(45 * time.Second) }},
		{name: "authentication", outcome: OutcomeAuthFailed, want: func(now time.Time, _ string, budget Budget) time.Time { return now.Add(budget.MaximumBackoff) }},
		{name: "system", outcome: OutcomeSystemFailure, want: func(now time.Time, indexerID string, budget Budget) time.Time {
			return now.Add(backoff(indexerID, 1, budget))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			now := time.Unix(350, 0).UTC()
			minimum := 3 * time.Second
			budget := Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute}
			indexerID := "scoped-instance-" + test.name
			scheduler := NewScheduler(1, nil)
			scheduler.now = func() time.Time { return now }
			if err := scheduler.Configure(indexerID, budget); err != nil {
				t.Fatal(err)
			}
			permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
			if err != nil {
				t.Fatal(err)
			}
			var retryAt time.Time
			if test.retryAt != nil {
				retryAt = test.retryAt(now)
			}
			if err := permit.RecordResult(context.Background(), RecordResult{Outcome: test.outcome, Scope: FailureScopeInstance, RetryAt: retryAt}); err != nil {
				t.Fatal(err)
			}
			snapshot := scheduler.Snapshot(indexerID)
			if snapshot.LastOutcome != test.outcome || snapshot.ConsecutiveFailure != 1 {
				t.Fatalf("unexpected instance failure outcome: %#v", snapshot)
			}
			if snapshot.NextAllowedAt != now.Add(minimum) || snapshot.CooldownUntil != test.want(now, indexerID, budget) {
				t.Fatalf("unexpected instance pacing: %#v", snapshot)
			}
		})
	}
}

func TestRecordResultOperationFailureDoesNotBackOffInstance(t *testing.T) {
	now := time.Unix(375, 0).UTC()
	minimum := 3 * time.Second
	priorCooldown := now.Add(-time.Minute)
	indexerID := "scoped-operation-system"
	scheduler := NewScheduler(1, nil)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.Configure(indexerID, Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	lane := scheduler.getLane(indexerID)
	lane.mu.Lock()
	lane.loaded = true
	lane.snapshot = Snapshot{IndexerID: indexerID, ConsecutiveFailure: 2, CooldownUntil: priorCooldown, LastOutcome: OutcomeSuccess}
	lane.mu.Unlock()

	permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.RecordResult(context.Background(), RecordResult{Outcome: OutcomeSystemFailure, Scope: FailureScopeOperation}); err != nil {
		t.Fatal(err)
	}
	snapshot := scheduler.Snapshot(indexerID)
	if snapshot.LastOutcome != OutcomeSuccess || snapshot.ConsecutiveFailure != 2 {
		t.Fatalf("operation failure changed instance-wide health: %#v", snapshot)
	}
	if snapshot.CooldownUntil != priorCooldown {
		t.Fatalf("operation failure changed global cooldown: got %s want %s", snapshot.CooldownUntil, priorCooldown)
	}
	if snapshot.NextAllowedAt != now.Add(minimum) {
		t.Fatalf("operation failure did not advance minimum pacing: got %s want %s", snapshot.NextAllowedAt, now.Add(minimum))
	}
}

func TestRecordResultRejectsInvalidScopeClassificationConservatively(t *testing.T) {
	t.Parallel()
	for _, result := range []RecordResult{
		{Outcome: OutcomeRateLimited, Scope: FailureScopeOperation},
		{Outcome: OutcomeAuthFailed, Scope: FailureScopeOperation},
		{Outcome: OutcomeSuccess, Scope: FailureScopeInstance},
		{Outcome: Outcome("unknown"), Scope: FailureScopeNone},
	} {
		result := result
		t.Run(string(result.Outcome)+"-"+string(result.Scope), func(t *testing.T) {
			t.Parallel()
			now := time.Unix(390, 0).UTC()
			indexerID := "invalid-result-" + string(result.Outcome) + "-" + string(result.Scope)
			scheduler := NewScheduler(1, nil)
			scheduler.now = func() time.Time { return now }
			budget := Budget{MinimumInterval: 2 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}
			if err := scheduler.Configure(indexerID, budget); err != nil {
				t.Fatal(err)
			}
			permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
			if err != nil {
				t.Fatal(err)
			}
			if err := permit.RecordResult(context.Background(), result); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("invalid result error=%v", err)
			}
			snapshot := scheduler.Snapshot(indexerID)
			if snapshot.LastOutcome != OutcomeSystemFailure || snapshot.ConsecutiveFailure != 1 || snapshot.NextAllowedAt != now.Add(budget.MinimumInterval) || !snapshot.CooldownUntil.After(now) {
				t.Fatalf("invalid result was not conservatively accounted: %#v", snapshot)
			}
			permit, err = scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
			var deferred CooldownError
			if !errors.As(err, &deferred) {
				if permit != nil {
					permit.Close()
				}
				t.Fatalf("invalid result did not release and defer its lane: %v", err)
			}
		})
	}
}

func TestBrowseWaitsForPacingSlotButNotFailureCooldown(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	minimum := 5 * time.Second
	scheduler := NewScheduler(1, nil)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.Configure("browse", Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), "browse", WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err = permit.Record(context.Background(), OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	waited := time.Duration(0)
	waitCalls := 0
	scheduler.wait = func(_ context.Context, delay time.Duration) error {
		waitCalls++
		waited = delay
		now = now.Add(delay)
		return nil
	}

	// Existing interactive acquisition traffic must keep its fail-fast behavior.
	_, err = scheduler.Acquire(context.Background(), "browse", WorkloadInteractive)
	var deferred CooldownError
	if !errors.As(err, &deferred) {
		t.Fatalf("interactive acquisition did not defer: %v", err)
	}
	if waitCalls != 0 {
		t.Fatal("interactive acquisition unexpectedly waited")
	}

	permit, err = scheduler.Acquire(context.Background(), "browse", WorkloadBrowse)
	if err != nil {
		t.Fatalf("browse did not wait for its pacing slot: %v", err)
	}
	if waitCalls != 1 || waited != minimum {
		t.Fatalf("browse wait calls=%d delay=%s, want one %s wait", waitCalls, waited, minimum)
	}
	permit.Close()

	lane := scheduler.getLane("browse")
	lane.mu.Lock()
	lane.snapshot.CooldownUntil = now.Add(30 * time.Second)
	lane.mu.Unlock()
	_, err = scheduler.Acquire(context.Background(), "browse", WorkloadBrowse)
	if !errors.As(err, &deferred) || deferred.RetryAt() != now.Add(30*time.Second) {
		t.Fatalf("failure cooldown error=%v retry=%v", err, deferred.RetryAt())
	}
	if waitCalls != 1 {
		t.Fatal("browse waited through a failure cooldown")
	}
}

func TestFeedWaitsForPacingSlotButNotFailureCooldown(t *testing.T) {
	now := time.Unix(600, 0).UTC()
	minimum := 5 * time.Second
	scheduler := NewScheduler(1, nil)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.Configure("feed", Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), "feed", WorkloadBackground)
	if err != nil {
		t.Fatal(err)
	}
	if err = permit.Record(context.Background(), OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	waitCalls := 0
	scheduler.wait = func(_ context.Context, delay time.Duration) error {
		waitCalls++
		now = now.Add(delay)
		return nil
	}
	// The feed read queues behind the search that just ran: it never shortens
	// the minimum interval between two requests to the provider.
	permit, err = scheduler.Acquire(context.Background(), "feed", WorkloadFeed)
	if err != nil {
		t.Fatalf("feed did not wait for its pacing slot: %v", err)
	}
	if waitCalls != 1 || now != time.Unix(600, 0).UTC().Add(minimum) {
		t.Fatalf("feed wait calls=%d now=%s", waitCalls, now)
	}
	permit.Close()

	lane := scheduler.getLane("feed")
	lane.mu.Lock()
	lane.snapshot.CooldownUntil = now.Add(30 * time.Second)
	lane.mu.Unlock()
	var deferred CooldownError
	if _, err = scheduler.Acquire(context.Background(), "feed", WorkloadFeed); !errors.As(err, &deferred) || deferred.RetryAt() != now.Add(30*time.Second) {
		t.Fatalf("failure cooldown error=%v", err)
	}
	if waitCalls != 1 {
		t.Fatal("feed waited through a failure cooldown")
	}
}

func TestMinimumIntervalDefersNextRequest(t *testing.T) {
	now := time.Unix(400, 0).UTC()
	minimum := 5 * time.Second
	scheduler := NewScheduler(1, nil)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.Configure("minimum", Budget{MinimumInterval: minimum, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), "minimum", WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Record(context.Background(), OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	_, err = scheduler.Acquire(context.Background(), "minimum", WorkloadInteractive)
	var deferred CooldownError
	if !errors.As(err, &deferred) || deferred.RetryAt() != now.Add(minimum) {
		t.Fatalf("minimum interval error=%v retry=%s", err, deferred.RetryAt())
	}
	now = now.Add(minimum)
	permit, err = scheduler.Acquire(context.Background(), "minimum", WorkloadInteractive)
	if err != nil {
		t.Fatalf("request remained deferred after minimum interval: %v", err)
	}
	permit.Close()
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

func TestBackgroundSearchUsesAtMostFirstSixtyPercentOfQueryBudget(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		queryLimit int
		want       int
	}{{queryLimit: 1000, want: 600}, {queryLimit: 9, want: 5}, {queryLimit: 1, want: 1}} {
		limit, family := requestLimit(WorkloadBackground, Budget{QueryLimit: test.queryLimit})
		if limit != test.want || family != "query" {
			t.Fatalf("query limit=%d got limit=%d family=%q want limit=%d family=query", test.queryLimit, limit, family, test.want)
		}
	}
}

// TestBrowseHasItsOwnInFlightSlots pins that a viewer stream list is not
// queued behind background work in the global pool, that its own pool is
// bounded, and that the per-indexer lane still holds one request at a time.
func TestBrowseHasItsOwnInFlightSlots(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	budget := Budget{MinimumInterval: time.Millisecond, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 1000, LimitWindow: time.Hour}
	for index := 0; index <= BrowseConcurrency; index++ {
		if err := scheduler.Configure(fmt.Sprintf("indexer-%d", index), budget); err != nil {
			t.Fatal(err)
		}
	}
	background, err := scheduler.Acquire(context.Background(), "indexer-0", WorkloadBackground)
	if err != nil {
		t.Fatal(err)
	}
	// The only global slot is taken; browse on other indexers still proceeds.
	var browse []*Permit
	for index := 1; index <= BrowseConcurrency; index++ {
		quick, cancel := context.WithTimeout(context.Background(), time.Second)
		permit, err := scheduler.Acquire(quick, fmt.Sprintf("indexer-%d", index), WorkloadBrowse)
		cancel()
		if err != nil {
			t.Fatalf("browse %d waited behind background work: %v", index, err)
		}
		browse = append(browse, permit)
	}
	// The browse pool is bounded: with every browse slot held, the next
	// browse request waits.
	if err := scheduler.Configure("indexer-extra", budget); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	if _, err := scheduler.Acquire(blocked, "indexer-extra", WorkloadBrowse); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("browse beyond its pool: err=%v", err)
	}
	cancel()
	// The lane is still exclusive: browse on the indexer background is using
	// waits for it.
	_ = browse[0].Record(context.Background(), OutcomeSuccess, nil)
	sameLane, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	if _, err := scheduler.Acquire(sameLane, "indexer-0", WorkloadBrowse); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("browse shared a lane with background work: err=%v", err)
	}
	cancel()
	// Releasing browse permits returns their slots to the browse pool, not
	// to the global one.
	for _, permit := range browse[1:] {
		if err := permit.Record(context.Background(), OutcomeSuccess, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(scheduler.browse) != 0 || len(scheduler.global) != 1 {
		t.Fatalf("browse pool=%d global pool=%d after release", len(scheduler.browse), len(scheduler.global))
	}
	if err := background.Record(context.Background(), OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	if len(scheduler.global) != 0 {
		t.Fatalf("global pool=%d after release", len(scheduler.global))
	}
}
