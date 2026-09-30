package commissioningv2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

// recordingSnapshotStore counts SaveIndexerBudget (must stay zero on the
// success path) and LoadAuthoritativeIndexerState calls, with an injectable
// reload failure for the fail-closed test.
type recordingSnapshotStore struct {
	mu       sync.Mutex
	saves    int
	loads    int
	loadErr  error
	snapshot indexers.Snapshot
}

func (store *recordingSnapshotStore) LoadIndexerBudget(_ context.Context, indexerID string) (indexers.Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loads++
	if store.loadErr != nil {
		return indexers.Snapshot{}, store.loadErr
	}
	store.snapshot.IndexerID = indexerID
	return store.snapshot, nil
}

func (store *recordingSnapshotStore) SaveIndexerBudget(_ context.Context, snapshot indexers.Snapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saves++
	store.snapshot = snapshot
	return nil
}

func (store *recordingSnapshotStore) LoadAuthoritativeIndexerState(_ context.Context, indexerID string) (indexers.AuthoritativeSnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loads++
	if store.loadErr != nil {
		return indexers.AuthoritativeSnapshot{}, store.loadErr
	}
	return indexers.AuthoritativeSnapshot{
		Snapshot: indexers.Snapshot{IndexerID: indexerID},
		Gate:     indexers.CommissioningGateClear,
	}, nil
}

func (store *recordingSnapshotStore) saveCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.saves
}

func (store *recordingSnapshotStore) loadCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loads
}

func (store *recordingSnapshotStore) failReload() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loadErr = context.DeadlineExceeded
}

func testLiveClient(t *testing.T) *newznab.Client {
	t.Helper()
	// Local fake TLS server base; the client is never used for network I/O
	// in these finalization tests, only as a constructor dependency.
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	client, err := newznab.New(newznab.Config{IndexerID: "success-finalization", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func successTestScheduler() (*indexers.Scheduler, *recordingSnapshotStore) {
	store := &recordingSnapshotStore{}
	scheduler := indexers.NewScheduler(1, store)
	return scheduler, store
}

func TestCloseSucceededAfterKnownSuccessfulCompletionInvalidatesBeforeRelease(t *testing.T) {
	t.Parallel()
	scheduler, store := successTestScheduler()
	executor, err := NewLocalLiveExecutor(LiveExecutorDeps{
		Steps:     &stubLiveStepStore{},
		Scheduler: scheduler,
		Resolver:  &TestCredentialResolver{},
		Client:    testLiveClient(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	if err := scheduler.Configure(indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		_, err := scheduler.Acquire(context.Background(), indexerID, indexers.WorkloadInteractive)
		blocked <- err
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-blocked:
		t.Fatalf("second acquire returned early: %v", err)
	default:
	}
	signal := &indexers.LaneCacheInvalidation{Invalidated: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- executor.CloseSucceededAfterKnownSuccessfulCompletion(context.Background(), indexerID, permit, signal)
	}()
	select {
	case <-signal.Invalidated:
	case err := <-done:
		// Both channels may be ready when select runs, and select picks
		// among ready cases at random. Finalization returning is only a
		// failure when invalidation had not fired by then.
		select {
		case <-signal.Invalidated:
			done <- err
		default:
			t.Fatalf("finalization returned before invalidation fired: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invalidation did not fire before release")
	}
	// Invalidation fired; the lane permit is still owned at that instant by
	// construction (ReleaseWithoutOutcome runs after InvalidateLaneCache
	// inside the finalizer). Drain the finalizer, then prove no legacy
	// SaveIndexerBudget was issued and the authoritative reload was
	// attempted. The blocked second acquisition is drained after the lane
	// is released by the finalizer.
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if store.saveCount() != 0 {
		t.Fatalf("success-path finalization issued legacy SaveIndexerBudget: saves=%d", store.saveCount())
	}
	if store.loadCount() == 0 {
		t.Fatal("authoritative reload not attempted")
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

func TestCloseSucceededAfterKnownSuccessfulCompletionReloadFailureFailClosed(t *testing.T) {
	t.Parallel()
	scheduler, store := successTestScheduler()
	executor, err := NewLocalLiveExecutor(LiveExecutorDeps{
		Steps:     &stubLiveStepStore{},
		Scheduler: scheduler,
		Resolver:  &TestCredentialResolver{},
		Client:    testLiveClient(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	if err := scheduler.Configure(indexerID, indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	store.failReload()
	if err := executor.CloseSucceededAfterKnownSuccessfulCompletion(context.Background(), indexerID, permit, nil); err == nil {
		t.Fatal("failed reload succeeded")
	}
	if store.saveCount() != 0 {
		t.Fatalf("fail-closed path issued legacy SaveIndexerBudget: saves=%d", store.saveCount())
	}
	// A waiting same-indexer acquisition cannot slip through stale state.
	if _, err := scheduler.Acquire(context.Background(), indexerID, indexers.WorkloadInteractive); err == nil {
		t.Fatal("same-indexer acquisition slipped through stale state")
	}
}
