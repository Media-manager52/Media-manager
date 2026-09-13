package commissioningv2_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioning"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

const searchCapableCAPSBody = `<caps><server title="local" version="1"/><limits default="20" max="100"/><registration available="yes" open="no"/><categories><category id="2000" name="Movies"/><category id="5000" name="TV"/></categories><searching><search available="yes" supportedParams="q"/><movie-search available="yes" supportedParams="q,imdbid,tmdbid"/><tv-search available="yes" supportedParams="q,tvdbid,tvmazeid,season"/></searching></caps>`

func searchCapableCAPSServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(searchCapableCAPSBody))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// searchResultServer echoes a bounded, deterministic result item that matches
// exactly the fixture identity carried by the request.
func searchResultServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		query := request.URL.Query()
		var title, facts string
		switch {
		case query.Get("imdbid") != "":
			title, facts = "Commissioning Movie 2026", `<newznab:attr name="imdb" value="`+query.Get("imdbid")+`"/>`
		case query.Get("tmdbid") != "":
			title, facts = "Commissioning Movie 2026", `<newznab:attr name="tmdbid" value="`+query.Get("tmdbid")+`"/>`
		case query.Get("tvdbid") != "":
			title, facts = "Commissioning Series S02", `<newznab:attr name="tvdbid" value="`+query.Get("tvdbid")+`"/><newznab:attr name="season" value="2"/>`
		case strings.Contains(query.Get("q"), "Series"):
			title, facts = "Commissioning Series S02", ""
		default:
			title, facts = "Commissioning Movie 2026", `<newznab:attr name="imdb" value="tt1234567"/>`
		}
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>%s</title>%s</item></channel></rss>`, title, facts)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func searchClient(t *testing.T, scheduler *indexers.Scheduler, server *httptest.Server) *newznab.Client {
	t.Helper()
	client, err := newznab.New(newznab.Config{IndexerID: "commissioning-search", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func commissioningCAPSExecutor(t *testing.T, store *postgres.Store, steps commissioningv2.LiveStepStore, indexerID uuid.UUID, client *newznab.Client, destination newznab.CommissioningDestination) *commissioningv2.LiveExecutor {
	t.Helper()
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	scheduler.SetCommissioningGateSource(indexerID.String(), store)
	resolver := &commissioningv2.TestCredentialResolver{}
	resolver.RegisterTestCredential(indexerID.String(), "C2_TEST_KEY", []byte("local-test-key"))
	executor, err := commissioningv2.NewLocalLiveExecutor(commissioningv2.LiveExecutorDeps{
		Steps: steps, Scheduler: scheduler, Resolver: resolver, Client: client,
		Reservations: store, Authorizer: store, Destination: destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

// runCAPSToAwaitingReview executes the owner-authorized CAPS step and returns
// the run parked at awaiting_category_review.
func runCAPSToAwaitingReview(t *testing.T, ctx context.Context, store *postgres.Store, indexerID uuid.UUID) uuid.UUID {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	run := ownerCAPSRun(t, ctx, store, indexerID, now, true)
	server, requests := searchCapableCAPSServer(t)
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	executor := commissioningCAPSExecutor(t, store, store, indexerID, searchClient(t, scheduler, server), ownerCAPSDestination(server))
	if _, err := executor.Execute(ctx, ownerCAPSCommand(t, ctx, store, run.Steps[0].ID)); err != nil {
		t.Fatalf("caps execute: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("caps requests=%d", requests.Load())
	}
	return run.ID
}

func sealAndAuthorizeSearches(t *testing.T, ctx context.Context, store *postgres.Store, indexerID, runID uuid.UUID) postgres.IndexerCommissioningRun {
	t.Helper()
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	service := &commissioning.LiveCAPSService{Store: store, Scheduler: scheduler, ResolveSecret: func(string) (string, error) { return "local-test-key", nil }}
	reviewed, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := service.SealSearchPlan(ctx, indexerID, runID, reviewed.Revision, []int{2000}, []int{5000}, true, true)
	if err != nil {
		t.Fatalf("seal search plan: %v", err)
	}
	if sealed.State != commissioningv2.RunAwaitingSearchAuthorization {
		t.Fatalf("sealed run=%q", sealed.State)
	}
	authorized, err := service.AuthorizeSearches(ctx, indexerID, runID, sealed.Revision)
	if err != nil {
		t.Fatalf("authorize searches: %v", err)
	}
	if authorized.State != commissioningv2.RunSearchesAuthorized {
		t.Fatalf("authorized run=%q", authorized.State)
	}
	return authorized
}

// clearDurablePacing advances the durable pacing floor into the past so a
// fresh scheduler admits the next probe immediately. This isolates the probe
// control path from the wall-clock pacing floor established by the CAPS
// boundary; it does not bypass the scheduler (the executor still acquires a
// real permit and reservation).
func clearDurablePacing(t *testing.T, ctx context.Context, store *postgres.Store, indexerID uuid.UUID) {
	t.Helper()
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET next_allowed_at=$2, cooldown_until=NULL WHERE indexer_instance_id=$1`, indexerID.String(), time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
}

func firstSearchStep(t *testing.T, run postgres.IndexerCommissioningRun) postgres.IndexerCommissioningStep {
	t.Helper()
	for _, step := range run.Steps {
		if step.Phase == commissioningv2.StepPhaseSearch {
			return step
		}
	}
	t.Fatal("no search step in sealed plan")
	return postgres.IndexerCommissioningStep{}
}

// TestSemanticProbeRequiresAuthorizationAndSendsExactlyOnce proves that a
// search probe cannot transmit before owner authorization, transmits at most
// once when eligible, never resends on replay, and persists only the closed
// semantic proof as evidence.
func TestSemanticProbeRequiresAuthorizationAndSendsExactlyOnce(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)

	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	service := &commissioning.LiveCAPSService{Store: store, Scheduler: scheduler, ResolveSecret: func(string) (string, error) { return "local-test-key", nil }}
	reviewed, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := service.SealSearchPlan(ctx, indexerID, runID, reviewed.Revision, []int{2000}, []int{5000}, true, true)
	if err != nil {
		t.Fatalf("seal search plan: %v", err)
	}
	searchStep := firstSearchStep(t, sealed)

	searchServer, searchRequests := searchResultServer(t)
	executor := commissioningCAPSExecutor(t, store, store, indexerID, searchClient(t, scheduler, searchServer), ownerCAPSDestination(searchServer))

	// Prior-state gating: the run is awaiting_search_authorization, so the
	// exact search step is not authorized and must send nothing.
	command := ownerCAPSCommand(t, ctx, store, searchStep.ID)
	if _, err := executor.Execute(ctx, command); !errors.Is(err, commissioningv2.ErrLiveExecutorNotAuthorized) {
		t.Fatalf("pre-authorization execute err=%v, want ErrLiveExecutorNotAuthorized", err)
	}
	if searchRequests.Load() != 0 {
		t.Fatalf("pre-authorization requests=%d, want 0", searchRequests.Load())
	}

	authorized, err := service.AuthorizeSearches(ctx, indexerID, runID, sealed.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_ = authorized
	clearDurablePacing(t, ctx, store, indexerID)
	command = ownerCAPSCommand(t, ctx, store, searchStep.ID)
	result, err := executor.Execute(ctx, command)
	if err != nil {
		t.Fatalf("authorized probe: %v", err)
	}
	if !result.Sent {
		t.Fatalf("probe result=%#v", result)
	}
	if searchRequests.Load() != 1 {
		t.Fatalf("eligible probe requests=%d, want 1", searchRequests.Load())
	}
	// Replay must send zero additional requests.
	if _, err := executor.Execute(ctx, command); err == nil {
		t.Fatal("replayed probe succeeded")
	}
	if searchRequests.Load() != 1 {
		t.Fatalf("replay requests=%d, want still 1", searchRequests.Load())
	}
	// Safe evidence only: the persisted document carries the closed proof and
	// no raw body.
	loaded, err := store.IndexerCommissioningStep(ctx, runID, searchStep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Evidence == nil || loaded.Evidence.Search == nil {
		t.Fatalf("search evidence missing: %#v", loaded.Evidence)
	}
	encoded, err := json.Marshal(loaded.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "<rss") || strings.Contains(string(encoded), "<item") || strings.Contains(string(encoded), "http") {
		t.Fatalf("raw provider body persisted: %s", encoded)
	}
	if bytesContainCredential(encoded, "local-test-key") {
		t.Fatal("credential persisted in evidence")
	}
}

func bytesContainCredential(body []byte, credential string) bool {
	return strings.Contains(string(body), credential)
}

func mixedSearchResultServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		query := request.URL.Query()
		matching := `<item><title>Commissioning Movie 2026</title><newznab:attr name="imdb" value="` + query.Get("imdbid") + `"/></item>`
		if query.Get("imdbid") == "" {
			matching = `<item><title>Commissioning Series S02</title><newznab:attr name="tvdbid" value="` + query.Get("tvdbid") + `"/><newznab:attr name="season" value="2"/></item>`
		}
		unrelated := `<item><title>Unrelated Release 1999</title><newznab:attr name="imdb" value="tt9999999"/></item>`
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>%s%s</channel></rss>`, matching, unrelated)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// TestSemanticProbeMixedResultVerifiesThroughDurableCompletion proves the
// Section-20 mixed-result rule end to end through the real repository
// completion predicates: a qualifying plausible result plus an unrelated
// result verifies with raw > plausible.
func TestSemanticProbeMixedResultVerifiesThroughDurableCompletion(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	var searchStep postgres.IndexerCommissioningStep
	found := false
	for _, step := range authorized.Steps {
		if step.Phase == commissioningv2.StepPhaseSearch && step.Scope == newznab.SearchScopeMovie {
			searchStep, found = step, true
			break
		}
	}
	if !found {
		t.Fatal("no movie search step in sealed plan")
	}
	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	searchServer, searchRequests := mixedSearchResultServer(t)
	executor := commissioningCAPSExecutor(t, store, store, indexerID, searchClient(t, scheduler, searchServer), ownerCAPSDestination(searchServer))
	clearDurablePacing(t, ctx, store, indexerID)
	result, err := executor.Execute(ctx, ownerCAPSCommand(t, ctx, store, searchStep.ID))
	if err != nil {
		t.Fatalf("mixed probe: %v", err)
	}
	if result.SafeOutcome != commissioningv2.OutcomeRecipeVerified {
		t.Fatalf("mixed result outcome=%q want recipe_verified", result.SafeOutcome)
	}
	if searchRequests.Load() != 1 {
		t.Fatalf("mixed result requests=%d", searchRequests.Load())
	}
	loaded, err := store.IndexerCommissioningStep(ctx, runID, searchStep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Evidence == nil || loaded.Evidence.Search == nil || loaded.Evidence.Search.RawResultCount != 2 || loaded.Evidence.Search.PlausibleIdentityCount != 1 {
		t.Fatalf("mixed durable evidence wrong: %#v", loaded.Evidence)
	}
}

// TestSemanticProbeUncertainFinalizationDoesNotResend proves an uncertain
// post-send completion leaves the durable gate and never resends.
func TestSemanticProbeUncertainFinalizationDoesNotResend(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	searchStep := firstSearchStep(t, authorized)

	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	searchServer, searchRequests := searchResultServer(t)
	failing := &completeFailingStore{Store: store}
	failing.fail.Store(true)
	executor := commissioningCAPSExecutor(t, store, failing, indexerID, searchClient(t, scheduler, searchServer), ownerCAPSDestination(searchServer))
	clearDurablePacing(t, ctx, store, indexerID)
	command := ownerCAPSCommand(t, ctx, store, searchStep.ID)
	if _, err := executor.Execute(ctx, command); err == nil {
		t.Fatal("uncertain probe reported success")
	}
	if searchRequests.Load() != 1 {
		t.Fatalf("uncertain probe requests=%d, want 1", searchRequests.Load())
	}
	loaded, err := store.IndexerCommissioningStep(ctx, runID, searchStep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepTransmitting {
		t.Fatalf("uncertain step=%q, want transmitting", loaded.State)
	}
	if _, err := executor.Execute(ctx, command); err == nil {
		t.Fatal("retry after uncertain probe succeeded")
	}
	if searchRequests.Load() != 1 {
		t.Fatalf("retry requests=%d, want still 1", searchRequests.Load())
	}
}

// TestSemanticProbePlanCeiling proves the durable plan never exceeds the hard
// four-probe ceiling and that a nonexistent fifth step cannot be executed.
func TestSemanticProbePlanCeiling(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	searchSteps := make([]postgres.IndexerCommissioningStep, 0, 4)
	for _, step := range authorized.Steps {
		if step.Phase == commissioningv2.StepPhaseSearch {
			searchSteps = append(searchSteps, step)
		}
	}
	if len(searchSteps) == 0 || len(searchSteps) > int(commissioningv2.MaximumSearchCalls) {
		t.Fatalf("search steps=%d, want 1..%d", len(searchSteps), commissioningv2.MaximumSearchCalls)
	}
	if int(authorized.PlannedAPICalls) != len(searchSteps)+1 {
		t.Fatalf("planned calls=%d steps=%d", authorized.PlannedAPICalls, len(searchSteps))
	}

	searchServer, searchRequests := searchResultServer(t)
	// Execute every planned step exactly once; total requests must equal the
	// planned search-step count and never exceed four. A fresh scheduler is
	// created per step so the durable pacing floor established by the prior
	// step is re-read after the test advances it.
	for _, step := range searchSteps {
		clearDurablePacing(t, ctx, store, indexerID)
		executor := commissioningCAPSExecutor(t, store, store, indexerID, searchClient(t, indexers.NewScheduler(4, store), searchServer), ownerCAPSDestination(searchServer))
		command := ownerCAPSCommand(t, ctx, store, step.ID)
		if _, err := executor.Execute(ctx, command); err != nil {
			t.Fatalf("planned probe %s (ordinal %d): %v", step.ID, step.Ordinal, err)
		}
	}
	if int(searchRequests.Load()) != len(searchSteps) || searchRequests.Load() > int32(commissioningv2.MaximumSearchCalls) {
		t.Fatalf("planned probes requests=%d, steps=%d", searchRequests.Load(), len(searchSteps))
	}
	// A nonexistent extra step cannot transmit.
	phantom := ownerCAPSCommand(t, ctx, store, searchSteps[0].ID)
	phantom.StepID = uuid.NewV7().String()
	before := searchRequests.Load()
	phantomExecutor := commissioningCAPSExecutor(t, store, store, indexerID, searchClient(t, indexers.NewScheduler(4, store), searchServer), ownerCAPSDestination(searchServer))
	if _, err := phantomExecutor.Execute(ctx, phantom); err == nil {
		t.Fatal("phantom step executed")
	}
	if searchRequests.Load() != before {
		t.Fatalf("phantom step sent %d requests", searchRequests.Load()-before)
	}
}

// TestSemanticProbeConcurrentExecuteSendsOnce proves two racing executions of
// the same admitted probe yield at most one HTTP request.
func TestSemanticProbeConcurrentExecuteSendsOnce(t *testing.T) {
	store, ctx := c2TestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := c2Indexer(t, ctx, store, now)
	runID := runCAPSToAwaitingReview(t, ctx, store, indexerID)
	authorized := sealAndAuthorizeSearches(t, ctx, store, indexerID, runID)
	searchStep := firstSearchStep(t, authorized)

	scheduler := indexers.NewScheduler(4, store)
	if err := scheduler.Configure(indexerID.String(), ownerCAPSBudget()); err != nil {
		t.Fatal(err)
	}
	searchServer, searchRequests := searchResultServer(t)
	executor := commissioningCAPSExecutor(t, store, store, indexerID, searchClient(t, scheduler, searchServer), ownerCAPSDestination(searchServer))
	clearDurablePacing(t, ctx, store, indexerID)
	command := ownerCAPSCommand(t, ctx, store, searchStep.ID)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for index := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = executor.Execute(ctx, command)
		}(index)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent probes succeeded=%d errs=%v", succeeded, errs)
	}
	if searchRequests.Load() != 1 {
		t.Fatalf("concurrent probe requests=%d, want 1", searchRequests.Load())
	}
}
