package commissioningv2_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// autoFinalizeStore wraps the real store so a test can observe every automatic
// draft-finalization attempt and inject a failure between durable semantic
// evidence and local draft generation. It inherits every other LiveStepStore
// method from the embedded store.
type autoFinalizeStore struct {
	*postgres.Store
	calls atomic.Int32
	fail  atomic.Bool
}

func (store *autoFinalizeStore) FinalizeReadyCommissioningDraft(ctx context.Context, indexerID, runID string, expectedRevision uint64, now time.Time) (bool, error) {
	store.calls.Add(1)
	if store.fail.Load() {
		return false, errors.New("injected automatic finalization failure")
	}
	return store.Store.FinalizeReadyCommissioningDraft(ctx, indexerID, runID, expectedRevision, now)
}

type countingDraftTransport struct {
	calls atomic.Int32
}

func (transport *countingDraftTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, http.ErrHandlerTimeout
}

func autofinalizeResolver(indexerID uuid.UUID) *commissioningv2.TestCredentialResolver {
	resolver := &commissioningv2.TestCredentialResolver{}
	resolver.RegisterTestCredential(indexerID.String(), "C2_TEST_KEY", []byte("local-test-key"))
	return resolver
}

func autofinalizeExecutor(t *testing.T, store *postgres.Store, steps commissioningv2.LiveStepStore, indexerID uuid.UUID, server *httptest.Server, resolver *commissioningv2.TestCredentialResolver) *commissioningv2.LiveExecutor {
	t.Helper()
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
	executor, err := commissioningv2.NewLocalLiveExecutor(commissioningv2.LiveExecutorDeps{
		Steps: steps, Scheduler: scheduler, Resolver: resolver,
		Client:       searchClient(t, scheduler, server),
		Reservations: store, Authorizer: store, Destination: ownerCAPSDestination(server),
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func searchStepsOf(run postgres.IndexerCommissioningRun) []postgres.IndexerCommissioningStep {
	steps := make([]postgres.IndexerCommissioningStep, 0, commissioningv2.MaximumSearchCalls)
	for _, step := range run.Steps {
		if step.Phase == commissioningv2.StepPhaseSearch {
			steps = append(steps, step)
		}
	}
	return steps
}

func executeSearchStep(t *testing.T, ctx context.Context, store *postgres.Store, steps commissioningv2.LiveStepStore, indexerID uuid.UUID, step postgres.IndexerCommissioningStep, server *httptest.Server, resolver *commissioningv2.TestCredentialResolver) commissioningv2.LiveExecuteResult {
	t.Helper()
	clearDurablePacing(t, ctx, store, indexerID)
	executor := autofinalizeExecutor(t, store, steps, indexerID, server, resolver)
	result, err := executor.Execute(ctx, ownerCAPSCommand(t, ctx, store, step.ID))
	if err != nil {
		t.Fatalf("execute search step ordinal %d: %v", step.Ordinal, err)
	}
	return result
}

func countDrafts(t *testing.T, ctx context.Context, store *postgres.Store, indexerID uuid.UUID) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// malformedLastSearchServer returns valid results for the identity-bound
// probes but a syntactically malformed envelope for the final general season
// fallback, so the last semantic step closes unsafe.
func malformedLastSearchServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		query := request.URL.Query()
		if strings.Contains(query.Get("q"), "Series") && query.Get("tvdbid") == "" {
			writer.Header().Set("Content-Type", "application/xml")
			_, _ = writer.Write([]byte(`<rss><channel><item>`))
			return
		}
		var title, facts string
		switch {
		case query.Get("imdbid") != "":
			title, facts = "Commissioning Movie 2026", `<newznab:attr name="imdb" value="`+query.Get("imdbid")+`"/>`
		case query.Get("tmdbid") != "":
			title, facts = "Commissioning Movie 2026", `<newznab:attr name="tmdbid" value="`+query.Get("tmdbid")+`"/>`
		case query.Get("tvdbid") != "":
			title, facts = "Commissioning Series S02", `<newznab:attr name="tvdbid" value="`+query.Get("tvdbid")+`"/><newznab:attr name="season" value="2"/>`
		default:
			title, facts = "Commissioning Movie 2026", `<newznab:attr name="imdb" value="tt1234567"/>`
		}
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>%s</title>%s</item></channel></rss>`, title, facts)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// TestAutomaticDraftNotCreatedBeforeFinalSearchStep proves a successful
// non-final semantic probe does not produce a draft.
func TestAutomaticDraftNotCreatedBeforeFinalSearchStep(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	steps := searchStepsOf(authorized)
	if len(steps) < 2 {
		t.Fatalf("need at least two search steps, got %d", len(steps))
	}
	server, requests := searchResultServer(t)
	result := executeSearchStep(t, ctx, store, store, indexerID, steps[0], server, autofinalizeResolver(indexerID))
	if result.State != commissioningv2.StepCompleted {
		t.Fatalf("first search step state=%q", result.State)
	}
	if requests.Load() != 1 {
		t.Fatalf("first search step requests=%d", requests.Load())
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != commissioningv2.RunSearchesAuthorized {
		t.Fatalf("run state=%q, want searches_authorized", reloaded.State)
	}
	if reloaded.GeneratedProfileID != nil || countDrafts(t, ctx, store, indexerID) != 0 {
		t.Fatal("draft was generated before the final search step")
	}
}

// TestAutomaticDraftFinalizationOnFinalSafeSearchStep proves the final
// semantic step automatically advances ready_for_draft and creates and links
// exactly one immutable draft without an owner POST /draft.
func TestAutomaticDraftFinalizationOnFinalSafeSearchStep(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	steps := searchStepsOf(authorized)
	server, requests := searchResultServer(t)
	resolver := autofinalizeResolver(indexerID)
	for _, step := range steps {
		if result := executeSearchStep(t, ctx, store, store, indexerID, step, server, resolver); result.State != commissioningv2.StepCompleted {
			t.Fatalf("search step ordinal %d state=%q", step.Ordinal, result.State)
		}
	}
	if requests.Load() != int32(len(steps)) {
		t.Fatalf("search requests=%d, want %d", requests.Load(), len(steps))
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != commissioningv2.RunCompleted || reloaded.GeneratedProfileID == nil {
		t.Fatalf("auto-finalized run=%#v", reloaded)
	}
	if countDrafts(t, ctx, store, indexerID) != 1 {
		t.Fatalf("draft count=%d, want 1", countDrafts(t, ctx, store, indexerID))
	}
	profile, err := store.IndexerCapabilityProfile(ctx, *reloaded.GeneratedProfileID)
	if err != nil {
		t.Fatal(err)
	}
	document, err := newznab.DecodeCapabilityProfile(profile.Document)
	if err != nil {
		t.Fatal(err)
	}
	if document.CAPS.MaximumLimit != 100 || len(document.SearchRecipes) == 0 || profile.LifecycleState != "draft" {
		t.Fatalf("auto-generated draft=%#v recipes=%d", profile.LifecycleState, len(document.SearchRecipes))
	}
}

// TestAutomaticDraftFinalizationPerformsNoNetwork proves the automatic
// finalization step resolves no credential, constructs no client, and sends no
// HTTP beyond the probes themselves.
func TestAutomaticDraftFinalizationPerformsNoNetwork(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	steps := searchStepsOf(authorized)
	wrapper := &autoFinalizeStore{Store: store}
	server, requests := searchResultServer(t)
	resolver := autofinalizeResolver(indexerID)
	transport := &countingDraftTransport{}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, step := range steps {
		if result := executeSearchStep(t, ctx, store, wrapper, indexerID, step, server, resolver); result.State != commissioningv2.StepCompleted {
			t.Fatalf("search step ordinal %d state=%q", step.Ordinal, result.State)
		}
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != commissioningv2.RunCompleted {
		t.Fatalf("run state=%q", reloaded.State)
	}
	if wrapper.calls.Load() != int32(len(steps)) {
		t.Fatalf("automatic finalization calls=%d, want %d", wrapper.calls.Load(), len(steps))
	}
	if resolver.Calls != len(steps) {
		t.Fatalf("credential resolutions=%d, want %d (one per probe, none for finalization)", resolver.Calls, len(steps))
	}
	if requests.Load() != int32(len(steps)) {
		t.Fatalf("HTTP requests=%d, want %d probes", requests.Load(), len(steps))
	}
	if transport.calls.Load() != 0 {
		t.Fatalf("generic HTTP transport used %d times during automatic finalization", transport.calls.Load())
	}
}

// TestAutomaticDraftFinalizationFailureRecoversViaOwnerRetry proves that a
// failure between durable semantic evidence and local draft generation neither
// resends a probe nor loses the run: the owner-triggered retry finalizes it
// exactly once.
func TestAutomaticDraftFinalizationFailureRecoversViaOwnerRetry(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	steps := searchStepsOf(authorized)
	wrapper := &autoFinalizeStore{Store: store}
	wrapper.fail.Store(true)
	server, requests := searchResultServer(t)
	resolver := autofinalizeResolver(indexerID)
	for _, step := range steps {
		if result := executeSearchStep(t, ctx, store, wrapper, indexerID, step, server, resolver); result.State != commissioningv2.StepCompleted {
			t.Fatalf("search step ordinal %d state=%q", step.Ordinal, result.State)
		}
	}
	if requests.Load() != int32(len(steps)) {
		t.Fatalf("probe resend: requests=%d, want %d", requests.Load(), len(steps))
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != commissioningv2.RunSearchesAuthorized || reloaded.GeneratedProfileID != nil {
		t.Fatalf("failed finalization must leave the run recoverable: %#v", reloaded)
	}
	if countDrafts(t, ctx, store, indexerID) != 0 {
		t.Fatal("failed automatic finalization created a draft")
	}
	// The owner-triggered local retry succeeds and creates exactly one draft.
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, runID, reloaded.Revision, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("owner finalization retry: %v", err)
	}
	finalized, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if finalized.State != commissioningv2.RunCompleted || finalized.GeneratedProfileID == nil {
		t.Fatalf("owner retry run=%#v", finalized)
	}
	if countDrafts(t, ctx, store, indexerID) != 1 {
		t.Fatalf("owner retry draft count=%d, want 1", countDrafts(t, ctx, store, indexerID))
	}
}

// TestAutomaticDraftFinalizationReplayReturnsSameDraft proves a replay after
// automatic success returns the same linked draft and allocates no second
// revision.
func TestAutomaticDraftFinalizationReplayReturnsSameDraft(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	steps := searchStepsOf(authorized)
	server, _ := searchResultServer(t)
	resolver := autofinalizeResolver(indexerID)
	for _, step := range steps {
		executeSearchStep(t, ctx, store, store, indexerID, step, server, resolver)
	}
	completed, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.GeneratedProfileID == nil {
		t.Fatal("automatic finalization did not link a draft")
	}
	replayed, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, runID, completed.Revision, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.GeneratedProfileID == nil || *replayed.GeneratedProfileID != *completed.GeneratedProfileID {
		t.Fatal("replay allocated a different draft")
	}
	if countDrafts(t, ctx, store, indexerID) != 1 {
		t.Fatalf("replay draft count=%d, want 1", countDrafts(t, ctx, store, indexerID))
	}
}

// TestAutomaticDraftFinalizationUnsafeFinalStepCreatesNoDraft proves an unsafe
// final semantic step leaves no draft and no linked profile.
func TestAutomaticDraftFinalizationUnsafeFinalStepCreatesNoDraft(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	steps := searchStepsOf(authorized)
	if len(steps) < 2 {
		t.Fatalf("need at least two search steps, got %d", len(steps))
	}
	server, _ := malformedLastSearchServer(t)
	resolver := autofinalizeResolver(indexerID)
	for _, step := range steps {
		executeSearchStep(t, ctx, store, store, indexerID, step, server, resolver)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State == commissioningv2.RunCompleted || reloaded.GeneratedProfileID != nil {
		t.Fatalf("unsafe final step completed a draft: %#v", reloaded)
	}
	if countDrafts(t, ctx, store, indexerID) != 0 {
		t.Fatalf("unsafe final step draft count=%d, want 0", countDrafts(t, ctx, store, indexerID))
	}
}
