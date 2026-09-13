package newznab

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

func fakeCAPSServer(t *testing.T, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(`<caps><server title="local" version="1"/><limits default="20" max="100"/><registration available="yes" open="no"/><searching><search available="yes" supportedParams="q"/></searching></caps>`))
	}))
}

func loopbackDialer(t *testing.T, server *httptest.Server) func(ctx context.Context, network, address string) (net.Conn, error) {
	t.Helper()
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
}

func mustCommissioningClient(t *testing.T, server *httptest.Server, indexerID string) *Client {
	t.Helper()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = serverURL
	client, err := New(Config{IndexerID: indexerID, BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type fakeAuthorizer struct {
	transmitting map[string]bool
}

func (authorizer *fakeAuthorizer) AuthorizedTransmittingCommissioningStep(_ context.Context, indexerID, stepID string) bool {
	if authorizer == nil {
		return false
	}
	return authorizer.transmitting[indexerID+"\x00"+stepID]
}

func transmittingAuthorizer(indexerID, stepID string) *fakeAuthorizer {
	return &fakeAuthorizer{transmitting: map[string]bool{indexerID + "\x00" + stepID: true}}
}

// mintRealSnapshot acquires a REAL commissioning snapshot from an actual
// Scheduler.AcquireCommissioningV2 setup and returns the permit (caller must
// ReleaseWithoutOutcome when done) plus the snapshot. The scheduler is
// configured with a valid commissioning budget so admission succeeds.
func mintRealSnapshot(t *testing.T, indexerID, stepID string, transmitting bool) (*indexers.Permit, indexers.CommissioningBudgetSnapshot, *fakeAuthorizer) {
	t.Helper()
	scheduler := indexers.NewScheduler(4, nil)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	if err := scheduler.Configure(indexerID, budget); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := permit.CommissioningBudgetSnapshot()
	if !snapshot.Valid() || !snapshot.MatchesAttempt(indexerID, stepID) {
		permit.ReleaseWithoutOutcome()
		t.Fatal("real snapshot invalid")
	}
	var authorizer *fakeAuthorizer
	if transmitting {
		authorizer = transmittingAuthorizer(indexerID, stepID)
	} else {
		authorizer = &fakeAuthorizer{transmitting: map[string]bool{}}
	}
	return permit, snapshot, authorizer
}

func mintCapability(t *testing.T, indexerID, stepID string, transmitting bool) (*CommissioningTransmission, *indexers.Permit) {
	t.Helper()
	permit, snapshot, authorizer := mintRealSnapshot(t, indexerID, stepID, transmitting)
	capability := NewCommissioningTransmissionFromSnapshot(snapshot, authorizer, indexerID, stepID)
	if capability == nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal("snapshot-bound mint failed")
	}
	return capability, permit
}

func releasePermit(permit *indexers.Permit) {
	if permit != nil {
		permit.ReleaseWithoutOutcome()
	}
}

const (
	oneuseIndexerID = "550e8400-e29b-41d4-a716-446655440000"
	oneuseStepID    = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	oneuseStepIDB   = "6ba7b812-9dad-11d1-80b4-00c04fd430c8"
)

func TestCommissioningTransmissionRejectsInvalidSnapshot(t *testing.T) {
	t.Parallel()
	// Invalid/foreign snapshots mint nothing: zero snapshot and
	// mismatched-attempt mint both return nil.
	if got := NewCommissioningTransmissionFromSnapshot(indexers.CommissioningBudgetSnapshot{}, transmittingAuthorizer(oneuseIndexerID, oneuseStepID), oneuseIndexerID, oneuseStepID); got != nil {
		t.Fatal("invalid snapshot minted authority")
	}
	permit, snapshot, authorizer := mintRealSnapshot(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	if got := NewCommissioningTransmissionFromSnapshot(snapshot, authorizer, oneuseIndexerID, oneuseStepIDB); got != nil {
		t.Fatal("foreign snapshot minted authority")
	}
}

func TestCommissioningTransmissionOneUseBinding(t *testing.T) {
	t.Parallel()
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	if capability.Consumed() {
		t.Fatal("fresh capability consumed")
	}
	if err := capability.consume(context.Background(), CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if !capability.Consumed() {
		t.Fatal("capability not consumed after use")
	}
}

func TestCommissioningTransmissionRejectsForeignIdentity(t *testing.T) {
	t.Parallel()
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	otherIndexer := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	if err := capability.consume(context.Background(), CommissioningTransmitClaim{IndexerID: otherIndexer, StepID: oneuseStepID}); err == nil {
		t.Fatal("foreign indexer accepted")
	}
	if !capability.Consumed() {
		t.Fatal("failed claim did not consume capability")
	}
	if err := capability.consume(context.Background(), CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}); err == nil {
		t.Fatal("second use accepted after consumption")
	}
}

func TestCommissioningTransmissionRejectsDurableNonTransmitting(t *testing.T) {
	t.Parallel()
	// Durable non-transmitting step: authorizer reports false -> zero requests
	// and the capability is consumed on the validation error.
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-durable")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, false)
	defer releasePermit(permit)
	claim := CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), capability, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("non-transmitting durable step accepted")
	}
	if requests.Load() != 0 {
		t.Fatalf("non-transmitting claim transmitted: requests=%d", requests.Load())
	}
	if !capability.Consumed() {
		t.Fatal("validation error did not consume capability")
	}
	// Second sequential use of the same consumed capability is rejected.
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), capability, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("second sequential use accepted")
	}
	if requests.Load() != 0 {
		t.Fatalf("second use transmitted: requests=%d", requests.Load())
	}
}

func TestCommissioningTransmissionSecondUseRejected(t *testing.T) {
	t.Parallel()
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	if err := capability.consume(context.Background(), CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := capability.consume(context.Background(), CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}); err == nil {
		t.Fatal("second use accepted")
	}
}

func TestCommissioningTransmissionValueCopySharesOneCell(t *testing.T) {
	t.Parallel()
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	copied := *capability
	if err := capability.consume(context.Background(), CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}); err != nil {
		t.Fatalf("first use: %v", err)
	}
	// A copied value aliases the same consumed cell: total uses remain one.
	if err := copied.consume(context.Background(), CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}); err == nil {
		t.Fatal("value copy yielded a second use")
	}
	if !copied.Consumed() {
		t.Fatal("copy does not observe shared consumption")
	}
}

// TestCommissioningMultipleMintSharesOneAttemptCell is CP1-1 requirement A:
// one legitimate snapshot -> mint capability A and capability B
// independently -> concurrent/sequential use across both produces EXACTLY
// ONE fake-TLS request.
func TestCommissioningMultipleMintSharesOneAttemptCell(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-multimint")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	permit, snapshot, authorizer := mintRealSnapshot(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	first := NewCommissioningTransmissionFromSnapshot(snapshot, authorizer, oneuseIndexerID, oneuseStepID)
	second := NewCommissioningTransmissionFromSnapshot(snapshot, authorizer, oneuseIndexerID, oneuseStepID)
	if first == nil || second == nil {
		t.Fatal("multiple mint from one snapshot failed")
	}
	claim := CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	capabilities := []*CommissioningTransmission{first, second}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = base.FetchCommissioningWithTransmission(context.Background(), capabilities[i], claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("multiple-mint concurrent successes=%d, want exactly 1 (errs=%v)", succeeded, errs)
	}
	if requests.Load() != 1 {
		t.Fatalf("multiple-mint concurrent requests=%d, want exactly 1", requests.Load())
	}
	// Sequential aftermath: both are now consumed, zero further requests.
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), first, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("consumed first mint accepted a retry")
	}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), second, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("consumed second mint accepted a retry")
	}
	if requests.Load() != 1 {
		t.Fatalf("multiple-mint total requests=%d, want exactly 1", requests.Load())
	}
}

// TestCommissioningSnapshotCopySharesOneAttemptCell is CP1-1 requirement B:
// copy snapshot -> mint from original and copy -> total requests EXACTLY ONE.
func TestCommissioningSnapshotCopySharesOneAttemptCell(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-snapcopy")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	permit, snapshot, authorizer := mintRealSnapshot(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	copied := snapshot
	first := NewCommissioningTransmissionFromSnapshot(snapshot, authorizer, oneuseIndexerID, oneuseStepID)
	second := NewCommissioningTransmissionFromSnapshot(copied, authorizer, oneuseIndexerID, oneuseStepID)
	if first == nil || second == nil {
		t.Fatal("mint from snapshot copy failed")
	}
	claim := CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), first, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err != nil {
		t.Fatalf("first transmission: %v", err)
	}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), second, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("snapshot-copy mint transmitted a second time")
	}
	if requests.Load() != 1 {
		t.Fatalf("snapshot-copy total requests=%d, want exactly 1", requests.Load())
	}
}

func TestFetchCommissioningWithTransmissionSendsAtMostOnce(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-once")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	claim := CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}
	result, err := base.FetchCommissioningWithTransmission(context.Background(), capability, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	if err != nil {
		t.Fatalf("first transmission: %v", err)
	}
	if len(result.Body) == 0 || requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), capability, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("second transmission accepted")
	}
	if requests.Load() != 1 {
		t.Fatalf("second transmission sent: requests=%d", requests.Load())
	}
}

func TestFetchCommissioningWithTransmissionRejectsWrongStep(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-wrong")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	_, err := base.FetchCommissioningWithTransmission(context.Background(), capability, CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepIDB}, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	if err == nil {
		t.Fatal("wrong step accepted")
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected claim transmitted: requests=%d", requests.Load())
	}
	if !capability.Consumed() {
		t.Fatal("validation error did not consume capability")
	}
}

func TestFetchCommissioningWithTransmissionConcurrentDoubleUseSendsOnce(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-race")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	claim := CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = base.FetchCommissioningWithTransmission(context.Background(), capability, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent double use successes=%d, want exactly 1 (errs=%v)", succeeded, errs)
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent double use requests=%d, want exactly 1", requests.Load())
	}
}

func TestFetchCommissioningWithTransmissionCopiedValueSendsOnce(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := fakeCAPSServer(t, &requests)
	defer server.Close()
	base := mustCommissioningClient(t, server, "one-use-copy")
	dest := localLoopbackDest(t, server, loopbackDialer(t, server))
	capability, permit := mintCapability(t, oneuseIndexerID, oneuseStepID, true)
	defer releasePermit(permit)
	copied := *capability
	claim := CommissioningTransmitClaim{IndexerID: oneuseIndexerID, StepID: oneuseStepID}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), capability, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err != nil {
		t.Fatalf("first transmission: %v", err)
	}
	if _, err := base.FetchCommissioningWithTransmission(context.Background(), &copied, claim, CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second)); err == nil {
		t.Fatal("copied capability transmitted a second time")
	}
	if requests.Load() != 1 {
		t.Fatalf("value-copy total requests=%d, want exactly 1", requests.Load())
	}
}
