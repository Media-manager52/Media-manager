package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestSeasonPackSearchUsesActiveCapabilityProfileAndPacesProvider proves the
// complete-season search path is profile-backed: the active capability profile
// decides the exact tvsearch parameters, the shared paced client enforces the
// scheduler budget, and one whole-season release advances the durable workflow.
// A complete season must be acquired as one pack job, never as per-episode
// submissions.
func TestSeasonPackSearchUsesActiveCapabilityProfileAndPacesProvider(t *testing.T) {
	store := openMovieSearchTestStore(t)
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("tvdbid") == "378992" && query.Get("season") == "1" {
			return seasonPackSearchReleaseXML("Example Series 2020 S01 COMPLETE 1080p WEB-DL")
		}
		return emptySearchXML()
	})
	defer server.Close()

	indexer := configureSeasonSearchIndexer(t, store, server.URL, false)
	workflow := createSeasonSearchWorkflow(t, store, true)
	service := movieSearchService(store, server)

	if err := service.Handle(context.Background(), postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("season-pack search: %v", err)
	}

	requests := captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want exactly one primary search", len(requests))
	}
	if got := requests[0].Get("t"); got != "tvsearch" {
		t.Fatalf("search mode=%q, want tvsearch", got)
	}
	if got := requests[0].Get("tvdbid"); got != "378992" {
		t.Fatalf("profile primary binding tvdbid=%q, want 378992 (legacy dialect omits it)", got)
	}
	if got := requests[0].Get("season"); got != "1" {
		t.Fatalf("profile primary binding season=%q, want 1", got)
	}
	if _, present := requests[0]["episode"]; present {
		t.Fatalf("season-pack primary search unexpectedly requested an episode: %v", requests[0])
	}
	if got := requests[0].Get("limit"); got != "100" {
		t.Fatalf("verified limit=%q, want 100 from the active profile", got)
	}
	if got := requests[0].Get("cat"); got != "5000" {
		t.Fatalf("category selection=%q, want 5000", got)
	}
	if requests[0].Get("apikey") != "test-api-key" {
		t.Fatal("provider request did not carry the resolved secret")
	}

	selected, err := store.GetWorkflow(context.Background(), workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil {
		t.Fatalf("workflow after season-pack profile search=%+v", selected)
	}
	candidates, err := store.ListReleaseCandidates(context.Background(), 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("persisted season-pack candidates=%+v err=%v", candidates, err)
	}
	if candidates[0].Verdict != string(releases.VerdictEligible) || !candidates[0].Selected {
		t.Fatalf("season-pack candidate evidence=%+v", candidates[0])
	}
	if candidates[0].Variant != "season:01" {
		t.Fatalf("season-pack candidate variant=%q, want season:01", candidates[0].Variant)
	}
	var retrievalEvents, jobCount int
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM outbox_events WHERE event_type='release.retrieve_requested' AND aggregate_id=$1),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&retrievalEvents, &jobCount); err != nil {
		t.Fatalf("season-pack downstream work: %v", err)
	}
	if retrievalEvents != 1 || jobCount != 0 {
		t.Fatalf("season-pack retrieval events=%d altmount jobs=%d, want one retrieval and no fan-out job", retrievalEvents, jobCount)
	}

	// The request crossed the durable scheduler budget: the outcome is
	// recorded and the next paced slot is strictly in the future.
	var lastOutcome string
	var nextAllowed time.Time
	var minimumIntervalMS int64
	if err := store.Pool.QueryRow(context.Background(), `SELECT COALESCE(last_outcome,''),next_allowed_at,minimum_interval_ms FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexer.ID.String()).Scan(&lastOutcome, &nextAllowed, &minimumIntervalMS); err != nil {
		t.Fatal(err)
	}
	if lastOutcome != "success" || minimumIntervalMS < 3000 {
		t.Fatalf("durable pacing state outcome=%q minimum_interval_ms=%d", lastOutcome, minimumIntervalMS)
	}
	if !nextAllowed.After(time.Now().UTC()) {
		t.Fatalf("next_allowed_at=%s is not in the future", nextAllowed)
	}
}

// TestSeasonPackSearchRejectsIndependentEpisodeJobs proves a season workflow
// cannot be satisfied by independently submitted episode releases. The fake
// provider returns several single-episode releases plus one complete-season
// pack: only the pack is eligible and selected, so the season still becomes a
// single acquisition job while the episode releases are rejected.
func TestSeasonPackSearchRejectsIndependentEpisodeJobs(t *testing.T) {
	store := openMovieSearchTestStore(t)
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("tvdbid") == "378992" && query.Get("season") == "1" {
			return seasonPackSearchReleaseXML(
				"Example Series 2020 S01E01 1080p WEB-DL",
				"Example Series 2020 S01E02 1080p WEB-DL",
				"Example Series 2020 S01E03 1080p WEB-DL",
				"Example Series 2020 S01 COMPLETE 1080p WEB-DL",
			)
		}
		return emptySearchXML()
	})
	defer server.Close()

	configureSeasonSearchIndexer(t, store, server.URL, false)
	workflow := createSeasonSearchWorkflow(t, store, true)
	service := movieSearchService(store, server)

	if err := service.Handle(context.Background(), postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("season-pack search: %v", err)
	}
	if len(captured()) != 1 {
		t.Fatalf("provider requests=%d, want one paced search", len(captured()))
	}

	candidates, err := store.ListReleaseCandidates(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	verdicts := make(map[string]string, len(candidates))
	selected := ""
	for _, candidate := range candidates {
		verdicts[candidate.Title] = candidate.Verdict
		if candidate.Selected {
			selected = candidate.Title
		}
	}
	for _, episode := range []string{
		"Example Series 2020 S01E01 1080p WEB-DL",
		"Example Series 2020 S01E02 1080p WEB-DL",
		"Example Series 2020 S01E03 1080p WEB-DL",
	} {
		if verdicts[episode] != string(releases.VerdictRejected) {
			t.Fatalf("episode release %q verdict=%q, want rejected", episode, verdicts[episode])
		}
	}
	if verdicts["Example Series 2020 S01 COMPLETE 1080p WEB-DL"] != string(releases.VerdictEligible) {
		t.Fatalf("season-pack verdict=%q, want eligible", verdicts["Example Series 2020 S01 COMPLETE 1080p WEB-DL"])
	}
	if selected != "Example Series 2020 S01 COMPLETE 1080p WEB-DL" {
		t.Fatalf("selected release=%q, want the complete season pack", selected)
	}

	// Exactly one workflow and one open round exist for the season; the
	// rejected episode observations never became independent acquisition work.
	var workflowCount, jobCount, retrievalEvents int
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM workflows WHERE media_id=$1),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$2),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=$2 AND event_type='release.retrieve_requested')`, workflow.MediaID.String(), workflow.ID.String()).Scan(&workflowCount, &jobCount, &retrievalEvents); err != nil {
		t.Fatal(err)
	}
	if workflowCount != 1 || jobCount != 0 || retrievalEvents != 1 {
		t.Fatalf("season episode fan-out workflows=%d jobs=%d retrievals=%d, want 1/0/1", workflowCount, jobCount, retrievalEvents)
	}
}

func configureSeasonSearchIndexer(t *testing.T, store *postgres.Store, baseURL string, legacyOnly bool) postgres.Integration {
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
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Season profile indexer", Enabled: true, Config: config, SecretRef: "SEASON_SEARCH_TEST_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "year"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "season"}},
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "season-caps-body", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if legacyOnly {
		return indexer
	}
	digest, err := store.IndexerEndpointConfigurationDigest(ctx, indexer.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateIndexerCapabilityProfile(ctx, indexer.ID, seasonCapabilityProfileDocument(t), digest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexer.ID, profile.ID, "owner", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return indexer
}

func createSeasonSearchWorkflow(t *testing.T, store *postgres.Store, searchImmediately bool) workflows.Workflow {
	t.Helper()
	ctx := context.Background()
	plex, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Season Profile Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "SEASON_SEARCH_PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "Season Profile AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true}`), SecretRef: "SEASON_SEARCH_ALTMOUNT_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: catalog.KindSeries, Name: "TV quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	year := uint16(2020)
	series, err := catalog.New(catalog.KindSeries, "Example Series", &year, []catalog.ExternalIdentity{
		{Namespace: "tvdb", Value: "378992", State: catalog.IdentityVerified, Source: "test"},
		{Namespace: "tmdb", Value: "110333", State: catalog.IdentityVerified, Source: "test"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManualMedia(ctx, series, root.ID, profile.ID, altMount.ID, []postgres.ManualSeason{{
		Number:            1,
		Monitored:         true,
		PreferSeasonPack:  true,
		SearchImmediately: searchImmediately,
		Episodes:          []postgres.ManualEpisode{{Number: 1, ProviderID: "tvdb:1001", Title: "Pilot"}, {Number: 2, ProviderID: "tvdb:1002", Title: "Second"}},
	}}, false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("season workflows=%d, want exactly one", len(created))
	}
	return created[0]
}

// seasonPackSearchReleaseXML builds a Newznab response with one item per
// supplied title. The runtime parser infers release scope and season number
// from the title exactly as it does for a live provider.
func seasonPackSearchReleaseXML(titles ...string) string {
	var items strings.Builder
	for index, title := range titles {
		fmt.Fprintf(&items, `<item><title>%s</title><guid>native-season-release-%d</guid><newznab:attr name="resolution" value="1920x1080"/></item>`, title, index)
	}
	return `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>` + items.String() + `</channel></rss>`
}

// TestSeasonCapabilityProfilePlansCompleteSeasonSearch is the non-database
// companion: it pins the profile document the DB-backed tests activate and the
// exact complete-season request the production planner emits for it.
func TestSeasonCapabilityProfilePlansCompleteSeasonSearch(t *testing.T) {
	t.Parallel()
	profile, err := newznab.DecodeCapabilityProfile(seasonCapabilityProfileDocument(t))
	if err != nil {
		t.Fatal(err)
	}
	season := uint16(1)
	year := uint16(2020)
	plan, err := PlanSearch(profile, SearchPlanInput{
		Season: &season,
		Media: catalog.MediaItem{
			Kind:  catalog.KindSeries,
			Title: "Example Series",
			Year:  &year,
			Identities: []catalog.ExternalIdentity{{
				Namespace: "tvdb",
				Value:     "378992",
				State:     catalog.IdentityVerified,
			}},
		},
	}, newznab.NewznabCategorySelection{Series: []int{5000}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Primary.RecipeKey != "season-tvdb" || plan.Primary.Request.Mode != "tvsearch" || len(plan.Primary.Request.Params) != 3 {
		t.Fatalf("complete-season plan=%+v", plan.Primary)
	}
}

// seasonCapabilityProfileDocument returns a valid activatable profile with one
// verified complete-season primary recipe. The CAPS category tree contains the
// selected series category so the runtime planner can resolve it.
func seasonCapabilityProfileDocument(t *testing.T) json.RawMessage {
	t.Helper()
	const evidenceKey = "evidence.00000000000000000000000000000002"
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
			Key:   "season-tvdb",
			Scope: newznab.SearchScopeCompleteSeason,
			Mode:  newznab.SearchModeTV,
			Query: newznab.QuerySpec{Style: newznab.QueryTitle},
			Bindings: []newznab.WireBinding{
				{Parameter: newznab.WireTVDbID, Source: newznab.BindingTVDbNumeric},
				{Parameter: newznab.WireSeason, Source: newznab.BindingSeasonNumber},
			},
			Extended:      true,
			Categories:    newznab.CategorySpec{Set: newznab.CategorySelectedSeries},
			VerifiedLimit: 100,
			Role:          newznab.RecipePrimary,
			Priority:      1,
			Verification:  newznab.RecipeVerification{State: newznab.VerificationVerified, EvidenceKeys: []string{evidenceKey}},
		}},
		Operations:  []newznab.OperationCapability{},
		MappingRefs: []newznab.MappingRef{{Key: newznab.MappingNewznabSearchV1, Version: 1}},
		Evidence: []newznab.CommissionEvidence{{
			Key:                    evidenceKey,
			Kind:                   newznab.EvidenceKindControlledProbe,
			Target:                 newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "season-tvdb"},
			ObservedAt:             time.Unix(1, 0).UTC(),
			BodySHA256:             strings.Repeat("b", 64),
			SafeOutcome:            newznab.EvidenceOutcomeSearchSuccess,
			RawResultCount:         1,
			PlausibleIdentityCount: 1,
		}},
	}
	if err := newznab.ValidateCapabilityProfileForActivation(profile); err != nil {
		t.Fatalf("season capability profile: %v", err)
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// A normal primary scan remains one request. The next existing scan removes
// unreliable season filters, maps only the exact frozen year/identity, and
// immediately dispatches the existing per-episode acquisition path.
func TestAnnualDiscoveryDispatchesEpisodesAfterOneAlternateScan(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	// The ID-only alternate is intentionally limited to recent targets. Use a
	// recent synthetic season so this test exercises the alternate scan rather
	// than the old-season path covered by TestAnnualSearchAlternateSkipsOldTargets.
	now := time.Now().UTC()
	seasonYear := now.Year()
	airDate1 := now.AddDate(0, 0, -14).Format("2006-01-02")
	airDate2 := now.AddDate(0, 0, -7).Format("2006-01-02")
	releaseTitle := fmt.Sprintf("Example.Series.S%dE01.1080p.WEB-DL", seasonYear)
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		if query.Get("tvdbid") == "378992" && query.Get("season") == "" {
			return strings.ReplaceAll(seasonPackSearchReleaseXML(releaseTitle), `<newznab:attr name="resolution"`, `<newznab:attr name="tvdbid" value="378992"/><newznab:attr name="season" value="20"/><newznab:attr name="episode" value="E00"/><newznab:attr name="resolution"`)
		}
		return emptySearchXML()
	})
	defer server.Close()
	configureSeasonSearchIndexer(t, store, server.URL, false)
	workflow := createSeasonSearchWorkflow(t, store, true)
	if _, err := store.Pool.Exec(ctx, `UPDATE pack_expected_episodes SET air_date=CASE WHEN episode=1 THEN $2::date ELSE $3::date END WHERE expectation_id IN (SELECT id FROM pack_expectation_sets WHERE workflow_id=$1)`, workflow.ID.String(), airDate1, airDate2); err != nil {
		t.Fatal(err)
	}
	count := uint16(1)
	if _, err := store.RefreshMediaMetadata(ctx, workflow.MediaID, &catalog.MediaMetadata{SeasonCount: &count}, nil, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	service := movieSearchService(store, server)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatal(err)
	}
	if len(captured()) != 1 || captured()[0].Get("season") != "1" {
		t.Fatal("primary shape changed")
	}
	// The same client must obey both its durable and in-memory pacing lane.
	time.Sleep(3100 * time.Millisecond)
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Handle(ctx, postgres.OutboxEvent{AggregateID: current.ID, AggregateRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	requests := captured()
	if len(requests) != 2 || requests[1].Get("season") != "" || requests[1].Get("q") != "" || requests[1].Get("tvdbid") != "378992" {
		t.Fatalf("requests=%v", requests)
	}
	var children, jobs int
	if err = store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflows WHERE media_id=$1 AND variant LIKE 'season:01:episode:%'),(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$2)`, workflow.MediaID.String(), workflow.ID.String()).Scan(&children, &jobs); err != nil {
		t.Fatal(err)
	}
	if children != 2 || jobs != 0 {
		t.Fatalf("children=%d parent jobs=%d", children, jobs)
	}
	current, err = store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateWaiting {
		t.Fatalf("coordinator=%+v err=%v", current, err)
	}
}
