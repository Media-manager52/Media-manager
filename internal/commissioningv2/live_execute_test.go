package commissioningv2_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

const ownerCAPSTestBody = `<caps><server title="local" version="1"/><limits default="20" max="100"/><registration available="yes" open="no"/><searching><search available="yes" supportedParams="q"/></searching></caps>`

func ownerCAPSServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(ownerCAPSTestBody))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func ownerCAPSDestination(server *httptest.Server) newznab.CommissioningDestination {
	return newznab.CommissioningDestination{
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		Roots:        c2Roots(server),
		TestLoopback: true,
		Timeout:      5 * time.Second,
	}
}

func ownerCAPSBudget() indexers.Budget {
	return indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
}

func ownerCAPSRun(t *testing.T, ctx context.Context, store *postgres.Store, indexerID uuid.UUID, now time.Time, authorize bool) postgres.IndexerCommissioningRun {
	t.Helper()
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
	if authorize {
		run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	return run
}

func ownerCAPSCommand(t *testing.T, ctx context.Context, store *postgres.Store, stepID uuid.UUID) commissioningv2.LiveExecuteCommand {
	t.Helper()
	snapshot, err := store.LoadAuthorizedStep(ctx, stepID.String())
	if err != nil {
		t.Fatal(err)
	}
	return commissioningv2.LiveExecuteCommand{
		RunID:             snapshot.RunID,
		StepID:            snapshot.StepID,
		ExpectedRunRev:    snapshot.RunRevision,
		ExpectedStepState: commissioningv2.StepPlanned,
		Fingerprint:       snapshot.Fingerprint,
		PlannerVersion:    snapshot.PlannerVersion,
		LeaseOwner:        "owner-caps",
	}
}

func ownerCAPSExecutor(t *testing.T, store *postgres.Store, steps commissioningv2.LiveStepStore, indexerID uuid.UUID, client *newznab.Client, destination newznab.CommissioningDestination) (*commissioningv2.LiveExecutor, *indexers.Scheduler) {
	t.Helper()
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
	resolver := &commissioningv2.TestCredentialResolver{}
	resolver.RegisterTestCredential(indexerID.String(), "C2_TEST_KEY", []byte("local-test-key"))
	executor, err := commissioningv2.NewLocalLiveExecutor(commissioningv2.LiveExecutorDeps{
		Steps:        steps,
		Scheduler:    scheduler,
		Resolver:     resolver,
		Client:       client,
		Reservations: store,
		Authorizer:   store,
		Destination:  destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor, scheduler
}

// TestOwnerAuthorizedCAPSExecutesExactlyOnce is the positive Milestone-B
// acceptance: one explicit owner authorization followed by exactly one
// execute action produces exactly one CAPS HTTP request and one durable
// completed/awaiting_category_review outcome.
func TestOwnerAuthorizedCAPSExecutesExactlyOnce(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	run := ownerCAPSRun(t, ctx, store, indexerID, now, true)
	server, requests := ownerCAPSServer(t)
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	client, err := newznab.New(newznab.Config{IndexerID: indexerID.String(), BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	executor, _ := ownerCAPSExecutor(t, store, store, indexerID, client, ownerCAPSDestination(server))
	command := ownerCAPSCommand(t, ctx, store, run.Steps[0].ID)

	result, err := executor.Execute(ctx, command)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Sent || result.State != commissioningv2.StepCompleted || result.SafeOutcome != commissioningv2.OutcomeCAPSObserved {
		t.Fatalf("result=%#v", result)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want exactly 1", requests.Load())
	}
	after, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != commissioningv2.RunAwaitingCategoryReview {
		t.Fatalf("run=%q, want awaiting_category_review", after.State)
	}
	step, err := store.IndexerCommissioningStep(ctx, run.ID, run.Steps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if step.State != commissioningv2.StepCompleted || step.SafeOutcome != commissioningv2.OutcomeCAPSObserved {
		t.Fatalf("step=%#v", step)
	}
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil {
		t.Fatal(err)
	}
	if gate != nil {
		t.Fatalf("gate=%v, want cleared", *gate)
	}
}

// TestOwnerAuthorizationRequiredForCAPSTransmission proves that a run that
// merely exists authorizes nothing: no owner authorization means zero HTTP.
func TestOwnerAuthorizationRequiredForCAPSTransmission(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	run := ownerCAPSRun(t, ctx, store, indexerID, now, false)
	server, requests := ownerCAPSServer(t)
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	client, err := newznab.New(newznab.Config{IndexerID: indexerID.String(), BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	executor, _ := ownerCAPSExecutor(t, store, store, indexerID, client, ownerCAPSDestination(server))
	command := ownerCAPSCommand(t, ctx, store, run.Steps[0].ID)

	if _, err := executor.Execute(ctx, command); !errors.Is(err, commissioningv2.ErrLiveExecutorNotAuthorized) {
		t.Fatalf("unauthorized execute err=%v, want ErrLiveExecutorNotAuthorized", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unauthorized requests=%d, want 0", requests.Load())
	}
	step, err := store.IndexerCommissioningStep(ctx, run.ID, run.Steps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if step.State != commissioningv2.StepPlanned {
		t.Fatalf("unauthorized execute changed step: %#v", step)
	}
}

// TestOwnerAuthorizedCAPSReplaySendsNothing proves duplicate execution of an
// already-completed authorized attempt sends zero additional HTTP.
func TestOwnerAuthorizedCAPSReplaySendsNothing(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	run := ownerCAPSRun(t, ctx, store, indexerID, now, true)
	server, requests := ownerCAPSServer(t)
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	client, err := newznab.New(newznab.Config{IndexerID: indexerID.String(), BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	executor, _ := ownerCAPSExecutor(t, store, store, indexerID, client, ownerCAPSDestination(server))
	command := ownerCAPSCommand(t, ctx, store, run.Steps[0].ID)
	if _, err := executor.Execute(ctx, command); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("first requests=%d", requests.Load())
	}
	if _, err := executor.Execute(ctx, command); err == nil {
		t.Fatal("replayed execute succeeded")
	}
	if requests.Load() != 1 {
		t.Fatalf("replay requests=%d, want still 1", requests.Load())
	}
}

// TestAuthorizeCAPSReplayIsDurableNoOp proves the durable owner
// authorization is a one-time transition: a replayed authorization fails
// closed without changing the run and without enabling a second send.
func TestAuthorizeCAPSReplayIsDurableNoOp(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	run := ownerCAPSRun(t, ctx, store, indexerID, now, true)
	authorized, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.State != commissioningv2.RunCAPSAuthorized {
		t.Fatalf("state=%q", authorized.State)
	}
	if _, err := store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, authorized.Revision, "owner", now.Add(2*time.Millisecond)); !errors.Is(err, postgres.ErrInvalidIndexerCommissioning) {
		t.Fatalf("replayed authorization err=%v, want ErrInvalidIndexerCommissioning", err)
	}
	after, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != commissioningv2.RunCAPSAuthorized || after.Revision != authorized.Revision {
		t.Fatalf("replayed authorization changed run: %#v", after)
	}
}

type completeFailingStore struct {
	*postgres.Store
	fail atomic.Bool
}

func (store *completeFailingStore) CompleteLiveAttempt(ctx context.Context, completion commissioningv2.LiveCompletion, now time.Time) (commissioningv2.LiveStepState, error) {
	if store.fail.Load() {
		return commissioningv2.LiveStepState{}, errors.New("injected uncertain completion")
	}
	return store.Store.CompleteLiveAttempt(ctx, completion, now)
}

// TestCAPSUncertainFinalizationDoesNotResend proves that an uncertain
// post-transmission completion leaves the durable unresolved state in place,
// invalidates the lane cache, and never causes an automatic resend on a
// subsequent execute.
func TestCAPSUncertainFinalizationDoesNotResend(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	run := ownerCAPSRun(t, ctx, store, indexerID, now, true)
	server, requests := ownerCAPSServer(t)
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	client, err := newznab.New(newznab.Config{IndexerID: indexerID.String(), BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	failing := &completeFailingStore{Store: store}
	failing.fail.Store(true)
	executor, _ := ownerCAPSExecutor(t, store, failing, indexerID, client, ownerCAPSDestination(server))
	command := ownerCAPSCommand(t, ctx, store, run.Steps[0].ID)

	if _, err := executor.Execute(ctx, command); err == nil {
		t.Fatal("uncertain completion reported success")
	}
	if requests.Load() != 1 {
		t.Fatalf("uncertain finalization requests=%d, want 1", requests.Load())
	}
	step, err := store.IndexerCommissioningStep(ctx, run.ID, run.Steps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if step.State != commissioningv2.StepTransmitting {
		t.Fatalf("uncertain step=%q, want transmitting", step.State)
	}
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil {
		t.Fatal(err)
	}
	if gate == nil || *gate != run.Steps[0].ID.String() {
		t.Fatalf("uncertain gate=%v, want held by step", gate)
	}
	// A retry (even with the same command) must not send again.
	if _, err := executor.Execute(ctx, command); err == nil {
		t.Fatal("retry after uncertain finalization succeeded")
	}
	if requests.Load() != 1 {
		t.Fatalf("retry requests=%d, want still 1", requests.Load())
	}
}
