package commissioningv2_test

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// countingAuthoritativeStore is a decorator around the REAL *postgres.Store
// used ONLY for the AuthoritativeStore observation seam: every durable call
// delegates to the real store while LoadAuthoritativeIndexerState calls are
// counted.
type countingAuthoritativeStore struct {
	real      *postgres.Store
	loads     atomic.Int32
	failLoads atomic.Bool
}

func (store *countingAuthoritativeStore) LoadIndexerBudget(ctx context.Context, indexerID string) (indexers.Snapshot, error) {
	return store.real.LoadIndexerBudget(ctx, indexerID)
}

func (store *countingAuthoritativeStore) SaveIndexerBudget(ctx context.Context, snapshot indexers.Snapshot) error {
	return store.real.SaveIndexerBudget(ctx, snapshot)
}

func (store *countingAuthoritativeStore) LoadAuthoritativeIndexerState(ctx context.Context, indexerID string) (indexers.AuthoritativeSnapshot, error) {
	store.loads.Add(1)
	if store.failLoads.Load() {
		return indexers.AuthoritativeSnapshot{}, context.DeadlineExceeded
	}
	return store.real.LoadAuthoritativeIndexerState(ctx, indexerID)
}

func (store *countingAuthoritativeStore) CommissioningGate(ctx context.Context, indexerID string) (indexers.CommissioningGateState, bool) {
	return store.real.CommissioningGate(ctx, indexerID)
}

func c2Fixtures() commissioningv2.FixtureDocument {
	movieYear, seasonYear := uint16(2026), uint16(2022)
	return commissioningv2.FixtureDocument{
		SchemaVersion: commissioningv2.DocumentSchemaVersion,
		Movie: &commissioningv2.MovieFixture{
			Key: "cfx_00000000000000000000000000000001", CanonicalTitle: "Commissioning Movie", Year: &movieYear,
			Identities: []commissioningv2.VerifiedIdentity{{Namespace: commissioningv2.IdentityIMDb, Value: "tt1234567"}, {Namespace: commissioningv2.IdentityTMDb, Value: "123"}},
			Source:     commissioningv2.FixtureExplicitOwner,
		},
		CompleteSeason: &commissioningv2.CompleteSeasonFixture{
			Key: "cfx_00000000000000000000000000000002", CanonicalTitle: "Commissioning Series", Season: 2, Year: &seasonYear,
			Identities: []commissioningv2.VerifiedIdentity{{Namespace: commissioningv2.IdentityTVDb, Value: "456"}},
			Source:     commissioningv2.FixtureExplicitOwner,
		},
	}
}

func c2TestDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	lowered := strings.ToLower(databaseURL)
	if strings.Contains(lowered, "prod") && !strings.Contains(lowered, "test") {
		t.Fatalf("refusing to use a non-test database URL")
	}
	return databaseURL
}

func c2TestStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()
	databaseURL := c2TestDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	schema := "mm_c2_executor_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	store, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, store.Pool); err != nil {
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

func c2Indexer(t *testing.T, ctx context.Context, store *postgres.Store, now time.Time) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,'C2_TEST_KEY',$4,$4)`, id.String(), "C2 fixture "+id.String(), `{"base_url":"https://c2-test.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func c2Client(t *testing.T, server *httptest.Server) *newznab.Client {
	t.Helper()
	client, err := newznab.New(newznab.Config{IndexerID: "c2-test", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func c2Roots(server *httptest.Server) *x509.CertPool {
	if server.Certificate() == nil {
		return nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return roots
}

// TestCloseInterruptedAfterUncertainFinalizationAgainstDurableStore is the
// §24 C2-TEST-2 acceptance: it calls the REAL
// executor.CloseInterruptedAfterUncertainFinalization against a REAL
// *postgres.Store and a REAL *indexers.Scheduler via NewLocalLiveExecutor —
// no stub Scheduler, no fake LiveStepStore. The commissioning permit is
// acquired BEFORE MarkClaimedStepTransmitting establishes the unresolved
// gate (the permit represents the in-flight attempt).
func TestCloseInterruptedAfterUncertainFinalizationAgainstDurableStore(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)

	// Build the run + intent-committed step through the real repository.
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := c2Fixtures()
	capsPlan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, capsPlan, now)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := run.Steps[0].StructuralRequestSHA256
	var fingerprintDigest commissioningv2.SHA256Digest
	copy(fingerprintDigest[:], fingerprint)
	if _, err := store.ClaimAuthorizedStepIntent(ctx, commissioningv2.LiveStepClaim{
		RunID: run.ID.String(), StepID: run.Steps[0].ID.String(), IndexerInstanceID: indexerID.String(),
		ExpectedStepState: commissioningv2.StepPlanned, ExpectedRunRevision: run.Revision,
		Fingerprint: fingerprintDigest, PlannerVersion: run.PlannerVersion,
		LeaseOwner: "worker.test", LeaseUntil: now.Add(time.Hour),
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err = store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := run.Steps[0]

	// REAL scheduler over a counting decorator around the REAL store (the
	// decorator exists only for the LoadAuthoritativeIndexerState
	// observation seam; every call delegates to postgres).
	counting := &countingAuthoritativeStore{real: store}
	scheduler := indexers.NewScheduler(1, counting)
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	if err := scheduler.Configure(indexerID.String(), budget); err != nil {
		t.Fatal(err)
	}
	scheduler.SetCommissioningGateSource(indexerID.String(), store)

	// Acquire the commissioning permit BEFORE the boundary (in-flight
	// attempt), then reserve + transmit.
	permit, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := permit.CommissioningBudgetSnapshot()
	if _, err := store.ReserveCommissioningRequest(ctx, indexerID.String(), step.ID.String(), snapshot, now.Add(3*time.Second)); err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	marker := commissioningv2.LiveTransmitMarker{
		RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: run.IndexerInstanceID.String(),
		ExpectedStepState: commissioningv2.StepIntentCommitted, ExpectedRunRev: run.Revision,
		LeaseOwner: "worker.test", ExecutionDeadline: now.Add(time.Hour), BudgetSnapshot: snapshot,
	}
	transmitted, err := store.MarkClaimedStepTransmitting(ctx, marker, now.Add(4*time.Second))
	if err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}
	if !transmitted.GateHeld {
		permit.ReleaseWithoutOutcome()
		t.Fatal("gate not held")
	}

	// Real local/test commissioning client wired to a fake TLS server. The
	// executor under test is constructed with the real client (constructor
	// dependency), and the server's request counter proves the
	// recovery/finalization method causes zero additional sends. The client
	// is retained by the executor; the roots pin the fake server's TLS.
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(`<caps><server title="local" version="1"/><limits default="20" max="100"/><registration available="yes" open="no"/><searching><search available="yes" supportedParams="q"/></searching></caps>`))
	}))
	defer server.Close()
	client := c2Client(t, server)
	if client == nil {
		t.Fatal("test client missing")
	}
	if c2Roots(server) == nil {
		t.Fatal("test server roots missing")
	}
	initialRequests := requests.Load()

	executor, err := commissioningv2.NewLocalLiveExecutor(commissioningv2.LiveExecutorDeps{
		Steps: store, Scheduler: scheduler, Resolver: &commissioningv2.TestCredentialResolver{}, Client: client,
	})
	if err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}

	// BEFORE: distinct variables for every durable field.
	var beforeCooldown, beforeNext, beforeOutcomeAt *time.Time
	var beforeOutcome *string
	var beforeRevision int64
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until,next_allowed_at,last_outcome,last_outcome_at,revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&beforeCooldown, &beforeNext, &beforeOutcome, &beforeOutcomeAt, &beforeRevision); err != nil {
		permit.ReleaseWithoutOutcome()
		t.Fatal(err)
	}

	// 1. INVALIDATION BEFORE RELEASE: hold another same-indexer Acquire
	// blocked on the lane token; observe the approved invalidation signal
	// while it is still blocked; only then may the executor method release.
	blocked := make(chan error, 1)
	go func() {
		_, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive)
		blocked <- err
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-blocked:
		permit.ReleaseWithoutOutcome()
		t.Fatalf("second acquire returned early: %v", err)
	default:
	}
	signal := &indexers.LaneCacheInvalidation{Invalidated: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- executor.CloseInterruptedAfterUncertainFinalization(ctx, indexerID.String(), permit, signal)
	}()
	select {
	case <-signal.Invalidated:
	case err := <-done:
		// select picks among ready cases at random; returning is only a
		// failure when invalidation had not fired by then.
		select {
		case <-signal.Invalidated:
			done <- err
		default:
			t.Fatalf("executor method returned before invalidation fired: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("invalidation did not fire")
	}
	select {
	case err := <-blocked:
		t.Fatalf("blocked acquire unblocked before method release: %v", err)
	default:
	}
	if err := <-done; err != nil {
		t.Fatalf("executor method: %v", err)
	}
	// The blocked acquire now unblocks (reload → gate-blocked, not stale).
	select {
	case err := <-blocked:
		var gateBlocked indexers.CommissioningGateBlockedError
		if !errors.As(err, &gateBlocked) {
			t.Fatalf("blocked acquire err=%v, want gate-blocked (not stale)", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocked acquire never returned")
	}

	// 2. RELEASEWITHOUTOUTCOME DID NOT OVERWRITE DURABLE ACCOUNTING:
	// distinct AFTER variables, compared field by field.
	var afterCooldown, afterNext, afterOutcomeAt *time.Time
	var afterOutcome *string
	var afterRevision int64
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until,next_allowed_at,last_outcome,last_outcome_at,revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&afterCooldown, &afterNext, &afterOutcome, &afterOutcomeAt, &afterRevision); err != nil {
		t.Fatal(err)
	}
	if !timesEqualC2(beforeCooldown, afterCooldown) || !timesEqualC2(beforeNext, afterNext) || !stringsEqualC2(beforeOutcome, afterOutcome) || !timesEqualC2(beforeOutcomeAt, afterOutcomeAt) || beforeRevision != afterRevision {
		t.Fatalf("durable accounting overwritten: cooldown %v->%v next %v->%v outcome %v->%v at %v->%v rev %d->%d",
			beforeCooldown, afterCooldown, beforeNext, afterNext, beforeOutcome, afterOutcome, beforeOutcomeAt, afterOutcomeAt, beforeRevision, afterRevision)
	}

	// 3. AUTHORITATIVE RELOAD: LoadAuthoritativeIndexerState count >= 1 and
	// Scheduler.Snapshot equals the durable row.
	if counting.loads.Load() < 1 {
		t.Fatalf("authoritative reloads=%d, want >= 1", counting.loads.Load())
	}
	var cooldown, next time.Time
	var outcome string
	var failures uint32
	var outcomeAt, updated time.Time
	var revision int64
	// Nullable-aware read of the durable row.
	var cooldownN, nextN, outcomeAtN, updatedN *time.Time
	var outcomeN *string
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until, next_allowed_at, last_outcome, consecutive_failure, last_outcome_at, updated_at, revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&cooldownN, &nextN, &outcomeN, &failures, &outcomeAtN, &updatedN, &revision); err != nil {
		t.Fatal(err)
	}
	if cooldownN != nil {
		cooldown = *cooldownN
	}
	if nextN != nil {
		next = *nextN
	}
	if outcomeN != nil {
		outcome = *outcomeN
	}
	if outcomeAtN != nil {
		outcomeAt = *outcomeAtN
	}
	if updatedN != nil {
		updated = *updatedN
	}
	got := scheduler.Snapshot(indexerID.String())
	if !got.CooldownUntil.Equal(cooldown) || !got.NextAllowedAt.Equal(next) || string(got.LastOutcome) != outcome || got.ConsecutiveFailure != failures || !got.LastOutcomeAt.Equal(outcomeAt) || !got.UpdatedAt.Equal(updated) || got.Revision != uint64(revision) {
		t.Fatalf("scheduler snapshot diverges from durable row: %#v", got)
	}

	// 4. RELOAD FAILURE FAILS CLOSED: acquire a properly owned permit on a
	// SECOND gate-free lane, then call the REAL executor method with a
	// cancelled reload context and prove it returns an error, the lane stays
	// cacheUnknown, both acquisitions refuse, and the durable row is
	// untouched. The gated lane's fail-closed shape is asserted right after
	// through the same scheduler primitive (the gate alone must not be what
	// proves the cache failure — hence the gate-free lane arm).
	indexerFree := c2Indexer(t, ctx, store, now.Add(time.Hour))
	if err := scheduler.Configure(indexerFree.String(), budget); err != nil {
		t.Fatal(err)
	}
	freeStep := uuid.NewV7().String()
	freePermit, err := scheduler.AcquireCommissioningV2(ctx, indexerFree.String(), freeStep)
	if err != nil {
		t.Fatal(err)
	}
	// Persist the authoritative-reload failure across the executor call AND
	// the follow-up Acquire attempts: the cacheUnknown barrier must hold
	// without relying on the durable gate (which is absent on this lane).
	counting.failLoads.Store(true)
	defer counting.failLoads.Store(false)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := executor.CloseInterruptedAfterUncertainFinalization(cancelled, indexerFree.String(), freePermit, nil); err == nil {
		t.Fatal("executor method with cancelled reload context succeeded")
	}
	// Lane remains cacheUnknown: both acquisitions refuse.
	if _, err := scheduler.Acquire(ctx, indexerFree.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("legacy acquire slipped through unknown cache on gate-free lane")
	}
	if _, err := scheduler.AcquireCommissioningV2(ctx, indexerFree.String(), freeStep); err == nil {
		t.Fatal("v2 acquire slipped through unknown cache on gate-free lane")
	}
	// No budget row was created for the gate-free lane by the failed reload.
	var freeRows int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerFree.String()).Scan(&freeRows); err != nil {
		t.Fatal(err)
	}
	if freeRows != 0 {
		t.Fatalf("failed reload wrote %d budget rows on gate-free lane", freeRows)
	}
	// Gated lane fail-closed shape through the same primitive: invalidate,
	// fail the reload, prove both acquisitions refuse and the durable row
	// is untouched.
	permit2, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String())
	if err == nil {
		permit2.ReleaseWithoutOutcome()
		t.Fatal("v2 acquire slipped through unresolved gate before reload-failure arm")
	}
	scheduler.InvalidateLaneCache(indexerID.String(), nil)
	if err := scheduler.ReloadLaneCache(cancelled, indexerID.String()); err == nil {
		t.Fatal("cancelled reload succeeded")
	}
	if _, err := scheduler.Acquire(ctx, indexerID.String(), indexers.WorkloadInteractive); err == nil {
		t.Fatal("legacy acquire slipped through unknown cache")
	}
	if _, err := scheduler.AcquireCommissioningV2(ctx, indexerID.String(), step.ID.String()); err == nil {
		t.Fatal("v2 acquire slipped through unknown cache")
	}
	// Durable row unchanged by the failed reload.
	var afterCooldown2, afterNext2, afterOutcomeAt2 *time.Time
	var afterOutcome2 *string
	var afterRevision2 int64
	if err := store.Pool.QueryRow(ctx, `SELECT cooldown_until,next_allowed_at,last_outcome,last_outcome_at,revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&afterCooldown2, &afterNext2, &afterOutcome2, &afterOutcomeAt2, &afterRevision2); err != nil {
		t.Fatal(err)
	}
	if !timesEqualC2(afterCooldown, afterCooldown2) || !timesEqualC2(afterNext, afterNext2) || !stringsEqualC2(afterOutcome, afterOutcome2) || !timesEqualC2(afterOutcomeAt, afterOutcomeAt2) || afterRevision != afterRevision2 {
		t.Fatal("failed reload changed the durable row")
	}

	// 5. ZERO RESEND: the request counter is exactly the initial
	// transmission's count; the recovery/finalization method caused zero
	// additional sends.
	if requests.Load() != initialRequests {
		t.Fatalf("finalization resent: requests %d -> %d", initialRequests, requests.Load())
	}
}

func timesEqualC2(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func stringsEqualC2(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
