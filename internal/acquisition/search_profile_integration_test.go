package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestMovieSearchUsesActiveCapabilityProfileAndPacesProvider proves the movie
// search path is profile-backed: an active capability profile decides the
// exact request parameters, the shared paced client enforces the scheduler
// budget, and an eligible release advances the durable workflow.
func TestMovieSearchUsesActiveCapabilityProfileAndPacesProvider(t *testing.T) {
	store := openMovieSearchTestStore(t)
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("imdbid") == "1234567" {
			return movieSearchReleaseXML("Klaus.2019.1080p.WEB-DL")
		}
		return emptySearchXML()
	})
	defer server.Close()

	indexer := configureMovieSearchIndexer(t, store, server.URL, false)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)

	if err := service.Handle(context.Background(), postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("movie search: %v", err)
	}

	requests := captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want exactly one primary search", len(requests))
	}
	if got := requests[0].Get("imdbid"); got != "1234567" {
		t.Fatalf("profile primary binding imdbid=%q, want 1234567 (legacy dialect omits it)", got)
	}
	if got := requests[0].Get("t"); got != "movie" {
		t.Fatalf("search mode=%q, want movie", got)
	}
	if got := requests[0].Get("limit"); got != "50" {
		t.Fatalf("verified limit=%q, want 50 from the active profile", got)
	}
	if got := requests[0].Get("cat"); got != "2000" {
		t.Fatalf("category selection=%q, want 2000", got)
	}
	if requests[0].Get("apikey") != "test-api-key" {
		t.Fatal("provider request did not carry the resolved secret")
	}

	selected, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil {
		t.Fatalf("workflow after profile search=%+v", selected)
	}
	candidates, err := store.ListReleaseCandidates(context.Background(), 10)
	if err != nil || len(candidates) != 1 || candidates[0].Verdict != string(releases.VerdictEligible) || !candidates[0].Selected {
		t.Fatalf("persisted candidate evidence=%+v err=%v", candidates, err)
	}
	var retrievalEvents int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_type='release.retrieve_requested' AND aggregate_id=$1`, workflow.ID.String()).Scan(&retrievalEvents); err != nil || retrievalEvents != 1 {
		t.Fatalf("retrieval outbox events=%d err=%v", retrievalEvents, err)
	}

	// The request crossed the durable scheduler budget: the outcome is
	// recorded and the next paced slot is strictly in the future.
	var lastOutcome string
	var nextAllowed time.Time
	var minimumIntervalMS int64
	if err := store.Pool.QueryRow(context.Background(), `SELECT COALESCE(last_outcome,''),next_allowed_at,minimum_interval_ms FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexer.ID.String()).Scan(&lastOutcome, &nextAllowed, &minimumIntervalMS); err != nil {
		t.Fatal(err)
	}
	if lastOutcome != string(indexers.OutcomeSuccess) || minimumIntervalMS < 3000 {
		t.Fatalf("durable pacing state outcome=%q minimum_interval_ms=%d", lastOutcome, minimumIntervalMS)
	}
	if !nextAllowed.After(time.Now().UTC()) {
		t.Fatalf("next_allowed_at=%s is not in the future", nextAllowed)
	}
}

// TestMovieSearchFallsBackToLegacyDialectWithoutActiveProfile preserves the
// pre-cutover behavior: an enabled, legacy-commissioned indexer with no active
// capability profile is still searched with its commissioned dialect.
func TestMovieSearchFallsBackToLegacyDialectWithoutActiveProfile(t *testing.T) {
	store := openMovieSearchTestStore(t)
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("q") != "" || query.Get("imdbid") != "" {
			return movieSearchReleaseXML("Klaus.2019.1080p.WEB-DL")
		}
		return emptySearchXML()
	})
	defer server.Close()

	configureMovieSearchIndexer(t, store, server.URL, true)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)

	if err := service.Handle(context.Background(), postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("legacy movie search: %v", err)
	}
	requests := captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want one", len(requests))
	}
	if got := requests[0].Get("imdbid"); got != "" {
		t.Fatalf("legacy dialect unexpectedly used profile imdbid=%q", got)
	}
	if got := requests[0].Get("year"); got != "2019" {
		t.Fatalf("legacy dialect year=%q, want 2019", got)
	}
	selected, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected {
		t.Fatalf("legacy search workflow=%+v err=%v", selected, err)
	}
}

// TestMovieSearchProviderFailureStaysTruthful keeps a failing indexer from
// fabricating a selection or durable completion: the handler returns the
// operational error, the workflow remains searching, and no candidate or
// retrieval work is persisted.
func TestMovieSearchProviderFailureStaysTruthful(t *testing.T) {
	store := openMovieSearchTestStore(t)
	// A response of "" makes the local provider return HTTP 500 for every
	// request, exercising the operational failure path.
	server, _ := newMovieSearchServer(t, func(url.Values) string { return "" })
	defer server.Close()

	configureMovieSearchIndexer(t, store, server.URL, false)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)

	if err := service.Handle(context.Background(), postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err == nil {
		t.Fatal("provider failure did not surface an operational error")
	}
	failed, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || failed.State != workflows.StateSearching || failed.SelectedRelease != nil {
		t.Fatalf("workflow after provider failure=%+v err=%v", failed, err)
	}
	candidates, err := store.ListReleaseCandidates(context.Background(), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("provider failure fabricated candidates=%+v err=%v", candidates, err)
	}
	var completionEvents int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_type IN ('release.retrieve_requested','library.publish_requested','plex.scan_requested') AND aggregate_id=$1`, workflow.ID.String()).Scan(&completionEvents); err != nil || completionEvents != 0 {
		t.Fatalf("provider failure queued downstream work=%d err=%v", completionEvents, err)
	}
}

func movieSearchService(store *postgres.Store, server *httptest.Server) *SearchService {
	return &SearchService{
		Store:         store,
		Scheduler:     indexers.NewScheduler(2, store),
		ResolveSecret: func(string) (string, error) { return "test-api-key", nil },
		Transport:     server.Client().Transport,
	}
}

func configureMovieSearchIndexer(t *testing.T, store *postgres.Store, baseURL string, legacyOnly bool) postgres.Integration {
	t.Helper()
	ctx := context.Background()
	config, err := json.Marshal(map[string]any{
		"base_url":                           baseURL,
		"allow_http":                         true,
		"movie_category_ids":                 []int{2000},
		"series_category_ids":                []int{5000},
		"category_selection_review_required": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Profile indexer", Enabled: true, Config: config, SecretRef: "MOVIE_SEARCH_TEST_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "year"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "season"}},
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "caps-body", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if legacyOnly {
		return indexer
	}
	digest, err := store.IndexerEndpointConfigurationDigest(ctx, indexer.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateIndexerCapabilityProfile(ctx, indexer.ID, movieCapabilityProfileDocument(t), digest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexer.ID, profile.ID, "owner", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return indexer
}

func createMovieSearchWorkflow(t *testing.T, store *postgres.Store) workflows.Workflow {
	t.Helper()
	ctx := context.Background()
	plex, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Profile Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "MOVIE_SEARCH_PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "Profile AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true}`), SecretRef: "MOVIE_SEARCH_ALTMOUNT_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: catalog.KindMovie, Name: "Movies quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	year := uint16(2019)
	movie, err := catalog.New(catalog.KindMovie, "Klaus", &year, []catalog.ExternalIdentity{
		{Namespace: "tmdb", Value: "508965", State: catalog.IdentityVerified, Source: "test"},
		{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified, Source: "test"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManualMedia(ctx, movie, root.ID, profile.ID, altMount.ID, nil, true, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("created workflows=%d, want 1", len(created))
	}
	return created[0]
}

func openMovieSearchTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	schema := "mm_movie_search_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
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
	t.Cleanup(store.Close)
	if err := postgres.Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	return store
}

func newMovieSearchServer(t *testing.T, respond func(url.Values) string) (*httptest.Server, func() []url.Values) {
	t.Helper()
	var mutex sync.Mutex
	requests := make([]url.Values, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		mutex.Lock()
		requests = append(requests, query)
		mutex.Unlock()
		body := respond(query)
		if body == "" {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(body))
	}))
	return server, func() []url.Values {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]url.Values(nil), requests...)
	}
}

func emptySearchXML() string {
	return `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel></channel></rss>`
}

func movieSearchReleaseXML(title string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>%s</title><guid>native-movie-release</guid><newznab:attr name="resolution" value="1920x1080"/></item></channel></rss>`, title)
}

// movieCapabilityProfileDocument returns a valid activatable profile with one
// verified movie primary recipe. The CAPS category tree contains the selected
// movie category so the runtime planner can resolve it.
func movieCapabilityProfileDocument(t *testing.T) json.RawMessage {
	t.Helper()
	const evidenceKey = "evidence.00000000000000000000000000000001"
	profile := newznab.CapabilityProfile{
		SchemaVersion: newznab.CapabilityProfileSchemaVersion,
		CAPS: newznab.CAPSRecord{
			BodySHA256:   strings.Repeat("a", 64),
			ObservedAt:   time.Unix(1, 0).UTC(),
			DefaultLimit: 50,
			MaximumLimit: 100,
			Categories:   []newznab.Category{{ID: 2000, Name: "Movies"}, {ID: 5000, Name: "TV"}},
		},
		SearchRecipes: []newznab.SearchRecipe{{
			Key:           "movie-imdb",
			Scope:         newznab.SearchScopeMovie,
			Mode:          newznab.SearchModeMovie,
			Query:         newznab.QuerySpec{Style: newznab.QueryTitle},
			Bindings:      []newznab.WireBinding{{Parameter: newznab.WireIMDbID, Source: newznab.BindingIMDbNumeric}},
			Extended:      true,
			Categories:    newznab.CategorySpec{Set: newznab.CategorySelectedMovie},
			VerifiedLimit: 50,
			Role:          newznab.RecipePrimary,
			Priority:      1,
			Verification:  newznab.RecipeVerification{State: newznab.VerificationVerified, EvidenceKeys: []string{evidenceKey}},
		}},
		Operations:  []newznab.OperationCapability{},
		MappingRefs: []newznab.MappingRef{{Key: newznab.MappingNewznabSearchV1, Version: 1}},
		Evidence: []newznab.CommissionEvidence{{
			Key:                    evidenceKey,
			Kind:                   newznab.EvidenceKindControlledProbe,
			Target:                 newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "movie-imdb"},
			ObservedAt:             time.Unix(1, 0).UTC(),
			BodySHA256:             strings.Repeat("b", 64),
			SafeOutcome:            newznab.EvidenceOutcomeSearchSuccess,
			RawResultCount:         1,
			PlausibleIdentityCount: 1,
		}},
	}
	if err := newznab.ValidateCapabilityProfileForActivation(profile); err != nil {
		t.Fatalf("movie capability profile: %v", err)
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
