package indexers

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

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
