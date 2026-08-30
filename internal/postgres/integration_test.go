package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/adoption"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestManualMovieAndSeasonPackPersistence(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := "mm_test_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	}()

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`), SecretRef: "ALTMOUNT_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Commissioned test indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api","minimum_interval_ms":3000}`), SecretRef: "NEWZNAB_TEST_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "test-caps"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []catalog.MediaKind{catalog.KindMovie, catalog.KindSeries} {
		rootName, destination, libraryID := "Movies", "/libraries/movies", "1"
		if kind == catalog.KindSeries {
			rootName, destination, libraryID = "TV", "/libraries/tv", "2"
		}
		if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: kind, Name: rootName, DestinationRoot: destination, PlexVisibleRoot: destination, SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: libraryID, Default: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p", "2160p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: kind, Name: rootName + " quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	movieRoot, movieProfile, movieClient, err := store.ResolveMediaConfiguration(ctx, catalog.KindMovie, nil, nil, nil)
	if err != nil || movieClient != altMount.ID {
		t.Fatalf("resolve movie defaults: client=%s err=%v", movieClient, err)
	}
	movieYear := uint16(2026)
	movie, err := catalog.New(catalog.KindMovie, "Integration Movie", &movieYear, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "123456", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	movieWorkflows, err := store.CreateManualMedia(ctx, movie, movieRoot, movieProfile, movieClient, nil, true, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(movieWorkflows) != 1 {
		t.Fatalf("movie workflows=%d, want 1", len(movieWorkflows))
	}
	assertWorkflowIntent(t, ctx, store, movieWorkflows[0].ID, "default", 0, 1)

	seriesRoot, seriesProfile, seriesClient, err := store.ResolveMediaConfiguration(ctx, catalog.KindSeries, nil, nil, nil)
	if err != nil || seriesClient != altMount.ID {
		t.Fatalf("resolve series defaults: client=%s err=%v", seriesClient, err)
	}
	seriesYear := uint16(2025)
	series, err := catalog.New(catalog.KindSeries, "Integration Series", &seriesYear, []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "987654", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	airDate := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	season := ManualSeason{Number: 1, Monitored: true, SearchImmediately: true, PreferSeasonPack: true, Episodes: []ManualEpisode{{Number: 1, ProviderID: "tvdb:1001", Title: "Pilot", AirDate: airDate}, {Number: 2, ProviderID: "tvdb:1002", Title: "Second"}}}
	seasonWorkflows, err := store.CreateManualMedia(ctx, series, seriesRoot, seriesProfile, seriesClient, []ManualSeason{season}, false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasonWorkflows) != 1 {
		t.Fatalf("season workflows=%d, want 1", len(seasonWorkflows))
	}
	assertWorkflowIntent(t, ctx, store, seasonWorkflows[0].ID, "season:01", 2, 1)

	seasonTwo := ManualSeason{Number: 2, Monitored: true, SearchImmediately: false, PreferSeasonPack: true, Episodes: []ManualEpisode{{Number: 1}, {Number: 2}, {Number: 3}}}
	seasonTwoWorkflow, err := store.CreateManualWorkflowForExisting(ctx, series, seriesRoot, seriesProfile, seriesClient, &seasonTwo, false, "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowIntent(t, ctx, store, seasonTwoWorkflow.ID, "season:02", 3, 0)

	dashboard, err := store.Dashboard(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Counts.Movies != 1 || dashboard.Counts.Series != 1 || dashboard.Counts.ActiveWorkflows != 3 {
		t.Fatalf("dashboard counts=%+v, want one movie, one series, and three active workflows", dashboard.Counts)
	}
	if len(dashboard.RecentWorkflow) != 3 || len(dashboard.RecentActivity) == 0 {
		t.Fatalf("dashboard read model omitted persisted workflow/activity: workflows=%d activity=%d", len(dashboard.RecentWorkflow), len(dashboard.RecentActivity))
	}
	activity, err := store.ListActivity(ctx, 100)
	if err != nil || len(activity) == 0 {
		t.Fatalf("activity read model: items=%d err=%v", len(activity), err)
	}
	files, err := store.ListManagedFiles(ctx, 100)
	if err != nil || len(files) != 0 {
		t.Fatalf("managed-file read model before publication: items=%d err=%v", len(files), err)
	}
	movieDetail, err := store.MediaDetail(ctx, movie.ID)
	if err != nil || len(movieDetail.Workflows) != 1 || len(movieDetail.Seasons) != 0 || len(movieDetail.Files) != 0 {
		t.Fatalf("movie detail read model: detail=%+v err=%v", movieDetail, err)
	}
	seriesDetail, err := store.MediaDetail(ctx, series.ID)
	if err != nil || len(seriesDetail.Workflows) != 2 || len(seriesDetail.Seasons) != 2 || len(seriesDetail.Files) != 0 {
		t.Fatalf("series detail read model: workflows=%d seasons=%d files=%d err=%v", len(seriesDetail.Workflows), len(seriesDetail.Seasons), len(seriesDetail.Files), err)
	}
	if seriesDetail.Seasons[0].Number != 1 || seriesDetail.Seasons[0].ExpectedEpisodes != 2 || seriesDetail.Seasons[1].Number != 2 || seriesDetail.Seasons[1].ExpectedEpisodes != 3 {
		t.Fatalf("series season snapshots=%+v", seriesDetail.Seasons)
	}
	wanted, err := store.ListWanted(ctx, 100)
	if err != nil || len(wanted) != 3 {
		t.Fatalf("wanted read model: items=%d err=%v", len(wanted), err)
	}
	for _, item := range wanted {
		if item.Gap != "missing" {
			t.Fatalf("wanted item before publication has gap=%q, want missing", item.Gap)
		}
	}
	calendar, err := store.ListCalendar(ctx, time.Now().UTC().AddDate(0, 0, -1), time.Now().UTC().AddDate(0, 0, 30), 100)
	if err != nil || len(calendar) != 1 || calendar[0].Episode != 1 || calendar[0].EpisodeTitle != "Pilot" {
		t.Fatalf("calendar read model=%+v err=%v", calendar, err)
	}
	tasks, err := store.ListTaskStatuses(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].EventType != "workflow.search_requested" || tasks[0].Pending != 2 || tasks[0].Due != 2 {
		t.Fatalf("task read model=%+v err=%v", tasks, err)
	}
	candidates, err := store.ListReleaseCandidates(ctx, 100)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("release read model before search worker: items=%d err=%v", len(candidates), err)
	}
	blockedCandidateID := uuid.NewV7()
	runID, observationID := uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,now(),now())`, runID.String(), movieWorkflows[0].ID.String(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test','native-guid','Integration.Movie.2026.1080p-GROUP',$3,'{}','{}','test','[]',now())`, observationID.String(), indexer.ID.String(), []byte{2}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,'native-guid','Integration.Movie.2026.1080p-GROUP','native:native-guid',now())`, blockedCandidateID.String(), movieWorkflows[0].ID.String(), runID.String(), observationID.String(), indexer.ID.String()); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(90 * 24 * time.Hour)
	block, err := store.CreateReleaseBlockFromCandidate(ctx, blockedCandidateID, "native_release", "passworded payload", &expires, time.Now().UTC())
	if err != nil || !block.Matches(indexer.ID, "native-guid", movie.ID, "default", "ignored") {
		t.Fatalf("create native release block=%+v err=%v", block, err)
	}
	if _, err := store.CreateReleaseBlockFromCandidate(ctx, blockedCandidateID, "native_release", "duplicate block", &expires, time.Now().UTC()); !errors.Is(err, ErrReleaseBlockExists) {
		t.Fatalf("duplicate native release block error=%v", err)
	}
	activeBlocks, err := store.ListActiveReleaseBlocks(ctx, movie.ID, "default", time.Now().UTC())
	if err != nil || len(activeBlocks) != 1 {
		t.Fatalf("active release blocks=%+v err=%v", activeBlocks, err)
	}
	if err := store.DeleteReleaseBlock(ctx, block.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	adoptionConfiguration, err := store.AdoptionConfiguration(ctx, movieRoot, movieProfile)
	if err != nil {
		t.Fatal(err)
	}
	adoptionBatch, err := store.CreateAdoptionBatch(ctx, "/libraries/movies/existing", adoptionConfiguration, false, false, []adoption.Entry{{Relative: "Integration Movie.mkv", RecognizedCompanions: []string{"Integration Movie.en.srt"}, UnknownCompanions: []string{}}}, time.Now().UTC())
	if err != nil || len(adoptionBatch.Items) != 1 || adoptionBatch.AttentionItems != 1 {
		t.Fatalf("adoption discovery batch=%+v err=%v", adoptionBatch, err)
	}
	adoptionItem := adoptionBatch.Items[0]
	if err := store.ReviewAdoptionItem(ctx, adoptionBatch.ID, adoptionItem.ID, AdoptionReview{State: "approved", MediaID: &movie.ID, RootID: &movieRoot, QualityProfileID: &movieProfile, ProposedRelative: "Integration Movie (2026)/Integration Movie (2026).mkv"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.ApproveAdoptionBatch(ctx, adoptionBatch.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	approvedAdoption, err := store.AdoptionBatch(ctx, adoptionBatch.ID)
	if err != nil || approvedAdoption.State != "approved" || approvedAdoption.ApprovedItems != 1 || approvedAdoption.Items[0].ExecutionState != "pending" {
		t.Fatalf("approved adoption plan=%+v err=%v", approvedAdoption, err)
	}
	collection, err := store.CreateCollection(ctx, "Integration favorites", "Reviewed manual grouping", []uuid.UUID{movie.ID, series.ID}, time.Now().UTC())
	if err != nil || len(collection.Members) != 2 {
		t.Fatalf("create collection=%+v err=%v", collection, err)
	}
	collections, err := store.ListCollections(ctx, 10)
	if err != nil || len(collections) != 1 || len(collections[0].Members) != 2 {
		t.Fatalf("list collections=%+v err=%v", collections, err)
	}
	if err := store.DeleteCollection(ctx, collection.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sources, err := store.ListSourceStatuses(ctx)
	if err != nil || len(sources) != 3 {
		t.Fatalf("source read model: items=%d err=%v", len(sources), err)
	}
	health, err := store.SystemHealth(ctx, time.Now().UTC())
	if err != nil || health.DatabaseState != "ready" || health.SchemaVersion != CurrentSchemaVersion || len(health.Components) != 3 {
		t.Fatalf("system health=%+v err=%v", health, err)
	}
}

func assertWorkflowIntent(t *testing.T, ctx context.Context, store *Store, workflowID uuid.UUID, wantVariant string, wantEpisodes, wantSearchEvents int) {
	t.Helper()
	var variant string
	var episodes, events int
	if err := store.Pool.QueryRow(ctx, `SELECT variant FROM workflows WHERE id=$1`, workflowID.String()).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM pack_expected_episodes episode JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id WHERE expectation.workflow_id=$1`, workflowID.String()).Scan(&episodes); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflowID.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if variant != wantVariant || episodes != wantEpisodes || events != wantSearchEvents {
		t.Fatalf("workflow %s: got variant=%q episodes=%d search_events=%d; want %q/%d/%d", fmt.Sprint(workflowID), variant, episodes, events, wantVariant, wantEpisodes, wantSearchEvents)
	}
}
