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
	plexadapter "github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/adoption"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/importlists"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
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
	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Commissioned test indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api","minimum_interval_ms":3000}`), SecretRef: "NEWZNAB_TEST_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{Movie: newznab.SearchDialect{Mode: "movie", Params: []string{"q", "imdbid"}}, Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season"}}}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "test-caps", DefaultLimit: 100, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	budgetNow := time.Now().UTC()
	if retryAt, err := store.ReserveIndexerRequest(ctx, indexer.ID.String(), "query", 1, time.Hour, budgetNow); err != nil || !retryAt.IsZero() {
		t.Fatalf("first request reservation retry=%v err=%v", retryAt, err)
	}
	if retryAt, err := store.ReserveIndexerRequest(ctx, indexer.ID.String(), "query", 1, time.Hour, budgetNow); err != nil || retryAt.Sub(budgetNow.Add(time.Hour)) < -time.Millisecond || retryAt.Sub(budgetNow.Add(time.Hour)) > time.Millisecond {
		t.Fatalf("spent query budget retry=%v err=%v", retryAt, err)
	}
	if retryAt, err := store.ReserveIndexerRequest(ctx, indexer.ID.String(), "grab", 1, time.Hour, budgetNow); err != nil || !retryAt.IsZero() {
		t.Fatalf("independent grab reservation retry=%v err=%v", retryAt, err)
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
	movie.Metadata, err = catalog.NormalizeMetadata(&catalog.MediaMetadata{Overview: "Persisted overview", PosterURL: "https://image.tmdb.org/t/p/w500/movie.jpg", Genres: []string{"Drama"}, Sources: []catalog.MetadataSource{{Provider: "tmdb", ProviderID: "123456", URL: "https://www.themoviedb.org/movie/123456", Role: "primary descriptive metadata"}}}, time.Now().UTC())
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
	persistedMovie, err := store.GetMedia(ctx, movie.ID)
	if err != nil || persistedMovie.Metadata == nil || persistedMovie.Metadata.Overview != "Persisted overview" {
		t.Fatalf("persisted movie metadata=%+v err=%v", persistedMovie.Metadata, err)
	}
	refreshedMovie, err := store.RefreshMediaMetadata(ctx, movie.ID, &catalog.MediaMetadata{Overview: "Refreshed overview", Sources: []catalog.MetadataSource{{Provider: "tmdb", ProviderID: "123456", URL: "https://www.themoviedb.org/movie/123456", Role: "primary descriptive metadata"}}}, nil, "test", time.Now().UTC())
	if err != nil || refreshedMovie.Metadata == nil || refreshedMovie.Metadata.Overview != "Refreshed overview" || refreshedMovie.Revision <= movie.Revision {
		t.Fatalf("refreshed movie=%+v err=%v", refreshedMovie, err)
	}
	assertWorkflowIntent(t, ctx, store, movieWorkflows[0].ID, "default", 0, 1)
	configuredList, err := store.CreateImportList(ctx, ImportList{Name: "IMDb owner export", Provider: "imdb_export", Enabled: true, MovieRootID: &movieRoot, MovieQualityProfileID: &movieProfile}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var mediaBeforeSnapshot, workflowsBeforeSnapshot int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_items),(SELECT count(*) FROM workflows)`).Scan(&mediaBeforeSnapshot, &workflowsBeforeSnapshot); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CreateImportListSnapshot(ctx, configuredList.ID, strings.Repeat("a", 64), []importlists.Item{{Ordinal: 1, MediaKind: "movie", Provider: "imdb", ProviderID: "tt1234567", Title: "Snapshot Movie", State: "pending_review"}, {Ordinal: 2, MediaKind: "series", Provider: "imdb", ProviderID: "tt7654321", Title: "Snapshot Series", State: "unsupported", Attention: "Exact TV season selection is required before monitoring."}}, time.Now().UTC())
	if err != nil || snapshot.ItemCount != 2 || snapshot.PendingCount != 1 || snapshot.UnsupportedCount != 1 {
		t.Fatalf("import-list snapshot=%+v err=%v", snapshot, err)
	}
	loadedList, err := store.ImportList(ctx, configuredList.ID)
	if err != nil || loadedList.LatestSnapshot == nil || len(loadedList.LatestSnapshot.Items) != 2 {
		t.Fatalf("loaded import list=%+v err=%v", loadedList, err)
	}
	if _, err := store.CreateImportListSnapshot(ctx, configuredList.ID, strings.Repeat("a", 64), []importlists.Item{{Ordinal: 1, MediaKind: "movie", Provider: "imdb", ProviderID: "tt1234567", Title: "Snapshot Movie", State: "pending_review"}}, time.Now().UTC()); !errors.Is(err, ErrDuplicateSnapshot) {
		t.Fatalf("duplicate snapshot error=%v", err)
	}
	var mediaAfterSnapshot, workflowsAfterSnapshot int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_items),(SELECT count(*) FROM workflows)`).Scan(&mediaAfterSnapshot, &workflowsAfterSnapshot); err != nil || mediaAfterSnapshot != mediaBeforeSnapshot || workflowsAfterSnapshot != workflowsBeforeSnapshot {
		t.Fatalf("snapshot changed monitoring state: media %d/%d workflows %d/%d err=%v", mediaBeforeSnapshot, mediaAfterSnapshot, workflowsBeforeSnapshot, workflowsAfterSnapshot, err)
	}

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
	seasonTwoWorkflow, err := store.CreateManualWorkflowForExisting(ctx, series, seriesRoot, seriesProfile, seriesClient, &seasonTwo, false, "", "test", time.Now().UTC())
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
	adoptionBatch, err := store.CreateAdoptionBatch(ctx, "/libraries/movies/existing", adoptionConfiguration, true, false, []adoption.Entry{{Relative: "Integration Movie.mkv", RecognizedCompanions: []string{"Integration Movie.en.srt"}, UnknownCompanions: []string{}}}, time.Now().UTC())
	if err != nil || len(adoptionBatch.Items) != 1 || adoptionBatch.AttentionItems != 1 {
		t.Fatalf("adoption discovery batch=%+v err=%v", adoptionBatch, err)
	}
	adoptionItem := adoptionBatch.Items[0]
	if err := store.ReviewAdoptionItem(ctx, adoptionBatch.ID, adoptionItem.ID, AdoptionReview{State: "approved", MediaID: &movie.ID, RootID: &movieRoot, QualityProfileID: &movieProfile, ProposedRelative: "Integration Movie (2026)/Integration Movie (2026).mkv", MonitoringEnabled: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.ApproveAdoptionBatch(ctx, adoptionBatch.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	approvedAdoption, err := store.AdoptionBatch(ctx, adoptionBatch.ID)
	if err != nil || approvedAdoption.State != "approved" || approvedAdoption.ApprovedItems != 1 || approvedAdoption.Items[0].ExecutionState != "pending" {
		t.Fatalf("approved adoption plan=%+v err=%v", approvedAdoption, err)
	}
	if _, err := store.TransitionWorkflow(ctx, movieWorkflows[0].ID, movieWorkflows[0].Revision, workflows.StateCancelled, "adoption monitoring creates a fresh post-verification workflow", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAdoptionExecution(ctx, adoptionBatch.ID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	executionContext, err := store.AdoptionExecutionContext(ctx, adoptionBatch.ID)
	if err != nil || len(executionContext.Items) != 1 || executionContext.Batch.ExecutionDeadline == nil {
		t.Fatalf("adoption execution context=%+v err=%v", executionContext, err)
	}
	operations, err := store.PrepareAdoptionOperations(ctx, adoptionBatch.ID, []AdoptionOperation{{ItemID: adoptionItem.ID, Ordinal: 1, Kind: "primary", SourceRootID: movieRoot, DestinationRootID: movieRoot, TransferMode: "rename", SourceRelative: "existing/Integration Movie.mkv", DestinationRelative: "Integration Movie (2026)/Integration Movie (2026).mkv", SourceSize: 1024, SourceModTimeNS: 1, SourceSHA256: strings.Repeat("a", 64)}})
	if err != nil || len(operations) != 1 {
		t.Fatalf("adoption operations=%+v err=%v", operations, err)
	}
	if err := store.MarkAdoptionOperation(ctx, operations[0].ID, "verified", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	mediaInfoEvidence := json.RawMessage(`{"schema_version":1,"container_names":["matroska"],"video_tracks":[{"index":0,"codec":"hevc","width":1920,"height":1080}],"audio_tracks":[],"subtitle_tracks":[],"facts":{"resolution":"1080p","video_codec":"hevc"}}`)
	if err := store.RecordAdoptionMediaInfo(ctx, operations[0].ID, mediaInfoEvidence, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAdoptionMediaInfo(ctx, operations[0].ID, mediaInfoEvidence, time.Now().UTC()); err != nil {
		t.Fatalf("idempotent adoption media-info record: %v", err)
	}
	if err := store.RecordAdoptionMediaInfo(ctx, operations[0].ID, json.RawMessage(`{"schema_version":2}`), time.Now().UTC()); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("conflicting adoption media-info error=%v", err)
	}
	operations, err = store.ListAdoptionOperations(ctx, adoptionBatch.ID)
	if err != nil || len(operations) != 1 || len(operations[0].MediaInfo) == 0 || len(operations[0].MediaInfoSHA256) != 64 || operations[0].MediaInfoRecordedAt == nil {
		t.Fatalf("persisted adoption media-info=%+v err=%v", operations, err)
	}
	if err := store.QueueAdoptionPlexConfirmation(ctx, adoptionBatch.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	confirmation := AdoptionPlexConfirmation{ItemID: adoptionItem.ID, ObservedPath: "/libraries/movies/Integration Movie (2026)/Integration Movie (2026).mkv", RatingKey: "123", ProviderGUID: "tmdb://123456", ConfirmedAt: time.Now().UTC()}
	if err := store.RecordAdoptionPlexConfirmation(ctx, adoptionBatch.ID, confirmation); err != nil {
		t.Fatal(err)
	}
	confirmedContext, err := store.AdoptionExecutionContext(ctx, adoptionBatch.ID)
	if err != nil || !confirmedContext.ConfirmedItems[adoptionItem.ID] {
		t.Fatalf("persisted adoption confirmation=%+v err=%v", confirmedContext.ConfirmedItems, err)
	}
	if err := store.CompleteAdoptionExecution(ctx, adoptionBatch.ID, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	completedAdoption, err := store.AdoptionBatch(ctx, adoptionBatch.ID)
	if err != nil || completedAdoption.State != "complete" || completedAdoption.Items[0].ExecutionState != "complete" {
		t.Fatalf("completed adoption=%+v err=%v", completedAdoption, err)
	}
	var adoptionMonitoringWorkflowText string
	if err := store.Pool.QueryRow(ctx, `SELECT workflow_id::text FROM adoption_monitoring_applications WHERE batch_id=$1 AND NOT search_immediately AND NOT replacement_authorized`, adoptionBatch.ID.String()).Scan(&adoptionMonitoringWorkflowText); err != nil {
		t.Fatalf("adoption monitoring application err=%v", err)
	}
	adoptionMonitoringWorkflowID, _ := uuid.Parse(adoptionMonitoringWorkflowText)
	files, err = store.ListManagedFiles(ctx, 100)
	if err != nil || len(files) != 1 || files[0].ID != adoptionItem.ID || files[0].Origin != "adoption" || files[0].Variant != "default" || files[0].State != "current" || !files[0].PlexConfirmed || files[0].QualityEvidenceState != "media_info" || !strings.Contains(string(files[0].QualityEvidence), `"resolution": "1080p"`) || files[0].SourceSizeBytes == nil || *files[0].SourceSizeBytes != 1024 || files[0].AdoptionBatchID == nil || *files[0].AdoptionBatchID != adoptionBatch.ID {
		t.Fatalf("adopted current-file read model=%+v err=%v", files, err)
	}
	movieDetail, err = store.MediaDetail(ctx, movie.ID)
	if err != nil || len(movieDetail.Files) != 1 || movieDetail.Files[0].Origin != "adoption" {
		t.Fatalf("adopted media detail=%+v err=%v", movieDetail, err)
	}
	dashboard, err = store.Dashboard(ctx, time.Now().UTC())
	if err != nil || dashboard.Counts.CurrentFiles != 1 {
		t.Fatalf("dashboard adopted current count=%+v err=%v", dashboard.Counts, err)
	}
	wanted, err = store.ListWanted(ctx, 100)
	if err != nil || len(wanted) != 3 {
		t.Fatalf("wanted after adoption=%+v err=%v", wanted, err)
	}
	var movieGap string
	for _, item := range wanted {
		if item.MediaID == movie.ID {
			movieGap = item.Gap
		}
	}
	if movieGap != "upgrade" {
		t.Fatalf("adopted movie wanted gap=%q, want upgrade", movieGap)
	}
	workflowSummaries, err := store.ListWorkflowSummaries(ctx, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	var adoptedConfirmationRequired bool
	for _, summary := range workflowSummaries {
		if summary.ID == adoptionMonitoringWorkflowID {
			adoptedConfirmationRequired = summary.AdoptedReplacementConfirmationRequired
		}
	}
	if !adoptedConfirmationRequired {
		t.Fatal("active workflow did not expose its adopted replacement confirmation gate")
	}
	if err := store.RequestWorkflowSearch(ctx, adoptionMonitoringWorkflowID, "", time.Now().UTC()); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("delayed search without adopted replacement confirmation error=%v", err)
	}
	if err := store.RequestWorkflowSearch(ctx, adoptionMonitoringWorkflowID, AdoptedReplacementConfirmation+" ", time.Now().UTC()); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("delayed search with inexact adopted replacement confirmation error=%v", err)
	}
	if err := store.RequestWorkflowSearch(ctx, adoptionMonitoringWorkflowID, AdoptedReplacementConfirmation, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var delayedAuthorizationCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM adopted_replacement_authorizations WHERE workflow_id=$1`, adoptionMonitoringWorkflowID.String()).Scan(&delayedAuthorizationCount); err != nil || delayedAuthorizationCount != 1 {
		t.Fatalf("delayed search adopted authorizations=%d err=%v", delayedAuthorizationCount, err)
	}
	delayedWorkflow, err := store.GetWorkflow(ctx, adoptionMonitoringWorkflowID)
	if err != nil || delayedWorkflow.State != workflows.StateSearching {
		t.Fatalf("delayed adopted search workflow=%+v err=%v", delayedWorkflow, err)
	}
	if _, err := store.TransitionWorkflow(ctx, delayedWorkflow.ID, delayedWorkflow.Revision, workflows.StateCancelled, "isolate adopted replacement test", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seerrDeliveryID := uuid.NewV7()
	seerrUpdatedAt := time.Now().UTC()
	seerrIntake := SeerrIntake{InstanceID: seerr.ID.String(), RequestID: "adopted-movie-request", UpdatedAt: seerrUpdatedAt, RequestState: "approved", Kind: catalog.KindMovie, Title: movie.Title, Year: movie.Year, Identities: movie.Identities, PayloadHash: []byte{8}, DeliveryID: seerrDeliveryID}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'poll','adopted-movie-event',$3,$4,'{}',$5)`, seerrDeliveryID.String(), seerr.ID.String(), seerrIntake.RequestID, seerrIntake.PayloadHash, seerrUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.UpsertSeerrSnapshot(ctx, seerrIntake); err != nil || !changed {
		t.Fatalf("adopted Seerr snapshot changed=%v err=%v", changed, err)
	}
	if _, err := store.EnsureMediaFromSeerr(ctx, seerrIntake, seerrUpdatedAt); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("Seerr bypassed adopted replacement confirmation: %v", err)
	}
	if _, err := store.CreateManualWorkflowForExisting(ctx, movie, movieRoot, movieProfile, movieClient, nil, false, "", "test", time.Now().UTC()); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("adopted replacement without exact confirmation error=%v", err)
	}
	if _, err := store.CreateManualWorkflowForExisting(ctx, movie, movieRoot, movieProfile, movieClient, nil, false, AdoptedReplacementConfirmation+" ", "test", time.Now().UTC()); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("adopted replacement with inexact confirmation error=%v", err)
	}
	replacementWorkflow, err := store.CreateManualWorkflowForExisting(ctx, movie, movieRoot, movieProfile, movieClient, nil, false, AdoptedReplacementConfirmation, "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var authorizationCount, recycleCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM adopted_replacement_authorizations WHERE workflow_id=$1`, replacementWorkflow.ID.String()).Scan(&authorizationCount); err != nil || authorizationCount != 1 {
		t.Fatalf("adopted replacement authorizations=%d err=%v", authorizationCount, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM recycle_entries WHERE adopted_file_id=$1`, adoptionItem.ID.String()).Scan(&recycleCount); err != nil || recycleCount != 0 {
		t.Fatalf("replacement workflow displaced a file before Plex confirmation: entries=%d err=%v", recycleCount, err)
	}
	displacementAt := time.Now().UTC()
	replacementRunID, replacementObservationID, replacementCandidateID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	replacementJobID, replacementInventoryID, replacementAssetID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	replacementLinkID, replacementVersionID, replacementScanID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	replacementRelative := "Integration Movie (2026) {tmdb-123456}/Integration Movie (2026) {tmdb-123456} - [1080p].mkv"
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,$4,$4)`, replacementRunID.String(), replacementWorkflow.ID.String(), []byte{3}, displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'replacement','replacement-guid','Integration.Movie.2026.REPACK.1080p-GROUP',$3,'{}','{}','test','[]',$4)`, replacementObservationID.String(), indexer.ID.String(), []byte{4}, displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,'replacement-guid','Integration.Movie.2026.REPACK.1080p-GROUP','native:replacement-guid',$6)`, replacementCandidateID.String(), replacementWorkflow.ID.String(), replacementRunID.String(), replacementObservationID.String(), indexer.ID.String(), displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,external_queue_id,submission_started_at,submitted_at,last_reconciled_at,revision) VALUES($1,$2,$3,$4,1,'mm-integration-replacement.nzb','import_complete',42,$5,$5,$5,1)`, replacementJobID.String(), replacementWorkflow.ID.String(), replacementCandidateID.String(), altMount.ID.String(), displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,1,'replacement',$3,$4,$4)`, replacementInventoryID.String(), replacementJobID.String(), []byte{5}, displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind,content_identity,media_info) VALUES($1,$2,'replacement.mkv',2048,'movie','tmdb:123456','{}')`, replacementAssetID.String(), replacementInventoryID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO library_links(id,root_id,destination_relative,created_at) VALUES($1,$2,$3,$4)`, replacementLinkID.String(), movieRoot.String(), replacementRelative, displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO library_link_versions(id,library_link_id,workflow_id,source_asset_id,altmount_job_id,generation,source_relative,plex_visible_target,state,verified_at) VALUES($1,$2,$3,$4,$5,1,'replacement/replacement.mkv','/mnt/altmount/replacement/replacement.mkv','verified',$6)`, replacementVersionID.String(), replacementLinkID.String(), replacementWorkflow.ID.String(), replacementAssetID.String(), replacementJobID.String(), displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='plex_scanning',revision=2,selected_release_id=$2,grab_attempt_id=$3,updated_at=$4 WHERE id=$1`, replacementWorkflow.ID.String(), replacementCandidateID.String(), replacementJobID.String(), displacementAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO plex_scan_requests(id,workflow_id,plex_instance_id,plex_library_id,altmount_job_id,import_generation,state,revision,poll_attempts,requested_at,deadline_at) VALUES($1,$2,$3,'1',$4,1,'polling',1,0,$5,$6)`, replacementScanID.String(), replacementWorkflow.ID.String(), plex.ID.String(), replacementJobID.String(), displacementAt, displacementAt.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	plexPath := "/libraries/movies/" + replacementRelative
	scan := PlexScanContext{ID: replacementScanID, Revision: 1, Workflow: replacementWorkflow, Integration: plex, Versions: []PlexVersion{{VersionID: replacementVersionID, DestinationRelative: replacementRelative, PlexVisiblePath: plexPath}}}
	if err := store.ConfirmPlexScan(ctx, scan, []plexadapter.Confirmation{{RatingKey: "456", Path: plexPath, ProviderGUID: "tmdb://123456"}}, displacementAt); err != nil {
		t.Fatal(err)
	}
	recycleEntries, err := store.ListRecycleEntries(ctx, 10)
	if err != nil || len(recycleEntries) != 1 {
		t.Fatalf("adopted recycle entries=%+v err=%v", recycleEntries, err)
	}
	recycleEntry := recycleEntries[0]
	if recycleEntry.EntryKind != "real_file" || recycleEntry.Purpose != "upgrade_displacement" || recycleEntry.AdoptedFileID == nil || *recycleEntry.AdoptedFileID != adoptionItem.ID || recycleEntry.ExpectedSize == nil || *recycleEntry.ExpectedSize != 1024 || !recycleEntry.ConfirmationRequired || recycleEntry.State != "planned" || recycleEntry.PurgeAfter.Sub(displacementAt) < 30*24*time.Hour-time.Second {
		t.Fatalf("adopted recycle evidence=%+v", recycleEntry)
	}
	files, err = store.ListManagedFiles(ctx, 100)
	var adoptedPlanned, replacementCurrent bool
	for _, file := range files {
		adoptedPlanned = adoptedPlanned || file.ID == adoptionItem.ID && file.State == "recycle_planned" && file.DisplacedAt != nil
		replacementCurrent = replacementCurrent || file.ID == replacementVersionID && file.State == "current" && file.PlexConfirmed
	}
	if err != nil || len(files) != 2 || !adoptedPlanned || !replacementCurrent {
		t.Fatalf("planned adopted-file read model=%+v err=%v", files, err)
	}
	if err := store.MarkRecycleRetained(ctx, recycleEntry.ID, displacementAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManualWorkflowForExisting(ctx, movie, movieRoot, movieProfile, movieClient, nil, false, AdoptedReplacementConfirmation, "test", displacementAt.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "lifecycle") {
		t.Fatalf("overlapping adopted lifecycle workflow error=%v", err)
	}
	if err := store.RequestRealFilePurge(ctx, recycleEntry.ID, RealFilePurgeConfirmation, displacementAt.Add(29*24*time.Hour)); err == nil || !strings.Contains(err.Error(), "30-day") {
		t.Fatalf("early retained-file purge error=%v", err)
	}
	if err := store.RequestRealFilePurge(ctx, recycleEntry.ID, "purge", displacementAt.Add(31*24*time.Hour)); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("inexact retained-file purge confirmation error=%v", err)
	}
	if err := store.RequestRealFilePurge(ctx, recycleEntry.ID, RealFilePurgeConfirmation, displacementAt.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	recycleEntry, err = store.LoadRecycleEntry(ctx, recycleEntry.ID)
	if err != nil || recycleEntry.State != "purge_planned" {
		t.Fatalf("owner-confirmed retained-file purge=%+v err=%v", recycleEntry, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE recycle_entries SET state='retained',confirmed_by=NULL,confirmed_at=NULL WHERE id=$1`, recycleEntry.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := store.RequestAdoptedRestore(ctx, recycleEntry.ID, AdoptedRestoreConfirmationPhrase+" ", displacementAt.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("inexact adopted restore confirmation error=%v", err)
	}
	if err := store.RequestAdoptedRestore(ctx, recycleEntry.ID, AdoptedRestoreConfirmationPhrase, displacementAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	restoreContext, err := store.LoadAdoptedRestoreContext(ctx, recycleEntry.ID)
	if err != nil || len(restoreContext.Entries) != 1 || restoreContext.Leader.State != "restore_planned" || restoreContext.Leader.RestoreDeadlineAt == nil {
		t.Fatalf("planned adopted restore=%+v err=%v", restoreContext, err)
	}
	if err := store.MarkAdoptedRestoreAwaitingPlex(ctx, recycleEntry.ID, displacementAt.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	restoreContext, err = store.LoadAdoptedRestoreContext(ctx, recycleEntry.ID)
	if err != nil || restoreContext.Leader.State != "awaiting_confirmation" || restoreContext.Leader.RestoreRevision != 1 {
		t.Fatalf("adopted restore awaiting Plex=%+v err=%v", restoreContext.Leader, err)
	}
	if err := store.MarkAdoptedRestoreRefreshed(ctx, recycleEntry.ID, 1, displacementAt.Add(4*time.Minute), displacementAt.Add(4*time.Minute+5*time.Second)); err != nil {
		t.Fatal(err)
	}
	restoreContext, err = store.LoadAdoptedRestoreContext(ctx, recycleEntry.ID)
	if err != nil || restoreContext.Leader.RestoreRevision != 2 || restoreContext.Leader.RestoreAttempts != 0 || restoreContext.Leader.RestoreRefreshAt == nil {
		t.Fatalf("adopted restore refresh state=%+v err=%v", restoreContext.Leader, err)
	}
	if err := store.RescheduleAdoptedRestoreConfirmation(ctx, recycleEntry.ID, 2, displacementAt.Add(5*time.Minute), displacementAt.Add(5*time.Minute+5*time.Second)); err != nil {
		t.Fatal(err)
	}
	restoredPath := "/libraries/movies/Integration Movie (2026)/Integration Movie (2026).mkv"
	if err := store.CompleteAdoptedRestore(ctx, recycleEntry.ID, []AdoptedRestoreConfirmation{{RecycleEntryID: recycleEntry.ID, ObservedPath: restoredPath, RatingKey: "789", ProviderGUID: "tmdb://123456", ConfirmedAt: displacementAt.Add(6 * time.Minute)}}, displacementAt.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recycleEntry, err = store.LoadRecycleEntry(ctx, recycleEntry.ID)
	if err != nil || recycleEntry.State != "restored" {
		t.Fatalf("completed adopted restore=%+v err=%v", recycleEntry, err)
	}
	files, err = store.ListManagedFiles(ctx, 100)
	var adoptedRestored, replacementDisplaced bool
	for _, file := range files {
		adoptedRestored = adoptedRestored || file.ID == adoptionItem.ID && file.State == "current" && file.DisplacedAt == nil
		replacementDisplaced = replacementDisplaced || file.ID == replacementVersionID && file.State == "displaced" && file.DisplacedAt != nil
	}
	if err != nil || !adoptedRestored || !replacementDisplaced {
		t.Fatalf("restored adopted-file read model=%+v err=%v", files, err)
	}
	var restoreConfirmations, displacedRemovalEvents int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM adopted_restore_confirmations WHERE recycle_entry_id=$1`, recycleEntry.ID.String()).Scan(&restoreConfirmations); err != nil || restoreConfirmations != 1 {
		t.Fatalf("adopted restore confirmations=%d err=%v", restoreConfirmations, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='library.displaced_remove_requested'`, replacementVersionID.String()).Scan(&displacedRemovalEvents); err != nil || displacedRemovalEvents != 1 {
		t.Fatalf("restored replacement removal events=%d err=%v", displacedRemovalEvents, err)
	}
	displacedLink, err := store.LoadDisplacedLink(ctx, replacementVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDisplacedLinkRemoved(ctx, displacedLink, displacementAt.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var displacedRefreshEvents int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='plex.displaced_refresh_requested'`, replacementVersionID.String()).Scan(&displacedRefreshEvents); err != nil || displacedRefreshEvents != 1 {
		t.Fatalf("restored replacement Plex refresh events=%d err=%v", displacedRefreshEvents, err)
	}
	if err := store.RecordDisplacedLinkRemoved(ctx, displacedLink, displacementAt.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='plex.displaced_refresh_requested'`, replacementVersionID.String()).Scan(&displacedRefreshEvents); err != nil || displacedRefreshEvents != 1 {
		t.Fatalf("duplicate replacement Plex refresh events=%d err=%v", displacedRefreshEvents, err)
	}
	destination, err := store.CreateNotificationDestination(ctx, NotificationDestination{Name: "Owner webhook", Endpoint: "https://notifications.invalid/hook", Enabled: true, Events: []string{"collection.created"}, SecretRef: "NOTIFICATION_TEST_SECRET"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	destinations, err := store.ListNotificationDestinations(ctx)
	if err != nil || len(destinations) != 1 || destinations[0].Endpoint != "https://notifications.invalid/…" {
		t.Fatalf("redacted notification destinations=%+v err=%v", destinations, err)
	}
	var notificationAudit string
	if err := store.Pool.QueryRow(ctx, `SELECT detail::text FROM audit_events WHERE action='notification.destination_created' AND aggregate_id=$1`, destination.ID.String()).Scan(&notificationAudit); err != nil || strings.Contains(notificationAudit, "/hook") {
		t.Fatalf("notification audit endpoint was not redacted: %q err=%v", notificationAudit, err)
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
	deliveries, err := store.ListNotificationDeliveries(ctx, 10)
	if err != nil || len(deliveries) != 1 || strings.Contains(string(deliveries[0].Payload), "detail") || strings.Contains(string(deliveries[0].Payload), "secret") {
		t.Fatalf("redacted notification deliveries=%+v err=%v", deliveries, err)
	}
	if err := store.MarkNotificationDelivery(ctx, deliveries[0].ID, true, 204, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNotificationDestinationEnabled(ctx, destination.ID, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	backup, err := store.CreateBackup(ctx, time.Now().UTC())
	if err != nil || backup.State != "queued" {
		t.Fatalf("create backup record=%+v err=%v", backup, err)
	}
	if _, err := store.CreateBackup(ctx, time.Now().UTC()); !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("concurrent backup error=%v", err)
	}
	if err := store.StartBackup(ctx, backup.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.FailBackup(ctx, backup.ID, "restore verification failed", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.StartBackup(ctx, backup.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteBackup(ctx, backup.ID, backup.ID.String()+".mmbackup", 4096, strings.Repeat("b", 64), CurrentSchemaVersion, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	backups, err := store.ListBackups(ctx, 10)
	if err != nil || len(backups) != 1 || backups[0].State != "verified" || backups[0].Attempts != 2 || backups[0].SchemaVersion == nil || *backups[0].SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("backup records=%+v err=%v", backups, err)
	}
	if _, err := store.TransitionWorkflow(ctx, seasonTwoWorkflow.ID, seasonTwoWorkflow.Revision, workflows.StateCancelled, "isolate reviewed adoption search test", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	cloneTransaction, err := store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clonedWorkflow := workflows.NewWithClient(series.ID, seriesRoot, seriesProfile, seriesClient, time.Now().UTC())
	if err := insertManualWorkflow(ctx, cloneTransaction, clonedWorkflow, "season:02", false); err != nil {
		_ = cloneTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	if err := cloneLatestAdoptionExpectation(ctx, cloneTransaction, adoptionMonitoringScope{MediaID: series.ID, MediaKind: catalog.KindSeries, Variant: "season:02", RootID: seriesRoot, QualityProfileID: seriesProfile}, clonedWorkflow.ID, time.Now().UTC()); err != nil {
		_ = cloneTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	var clonedEpisodes int
	if err := cloneTransaction.QueryRow(ctx, `SELECT count(*) FROM pack_expected_episodes episode JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id WHERE expectation.workflow_id=$1`, clonedWorkflow.ID.String()).Scan(&clonedEpisodes); err != nil || clonedEpisodes != 3 {
		_ = cloneTransaction.Rollback(ctx)
		t.Fatalf("cloned adoption season expectations=%d err=%v", clonedEpisodes, err)
	}
	if err := cloneTransaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	seriesAdoptionConfiguration, err := store.AdoptionConfiguration(ctx, seriesRoot, seriesProfile)
	if err != nil {
		t.Fatal(err)
	}
	immediateAdoption, err := store.CreateAdoptionBatch(ctx, "/libraries/tv/existing", seriesAdoptionConfiguration, true, true, []adoption.Entry{{Relative: "Integration Series S02E01.mkv"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReviewAdoptionItem(ctx, immediateAdoption.ID, immediateAdoption.Items[0].ID, AdoptionReview{State: "approved", MediaID: &series.ID, RootID: &seriesRoot, QualityProfileID: &seriesProfile, ProposedRelative: "Integration Series/Season 02/Integration Series - S02E01.mkv", Variant: "season:02", MonitoringEnabled: true, SearchImmediately: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.ApproveAdoptionBatch(ctx, immediateAdoption.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueAdoptionExecution(ctx, immediateAdoption.ID, "", time.Now().UTC()); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("immediate adopted search without exact confirmation error=%v", err)
	}
	if err := store.QueueAdoptionExecution(ctx, immediateAdoption.ID, AdoptedReplacementConfirmation+" ", time.Now().UTC()); !errors.Is(err, ErrAdoptedReplacementConfirmationRequired) {
		t.Fatalf("immediate adopted search with inexact confirmation error=%v", err)
	}
	if err := store.QueueAdoptionExecution(ctx, immediateAdoption.ID, AdoptedReplacementConfirmation, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	immediateAdoption, err = store.AdoptionBatch(ctx, immediateAdoption.ID)
	if err != nil || immediateAdoption.State != "executing" || !immediateAdoption.ReplacementSearchAuthorized {
		t.Fatalf("authorized immediate adoption search=%+v err=%v", immediateAdoption, err)
	}
	if _, err := store.CreateManualWorkflowForExisting(ctx, series, seriesRoot, seriesProfile, seriesClient, &seasonTwo, false, "", "test", time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "adoption monitoring owns") {
		t.Fatalf("concurrent workflow bypassed executing adoption monitoring: %v", err)
	}
	immediateTransaction, err := store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	immediateItem := immediateAdoption.Items[0]
	if _, err := immediateTransaction.Exec(ctx, `INSERT INTO adopted_files(id,adoption_batch_id,adoption_item_id,media_id,root_id,quality_profile_id,variant,original_source_root,original_relative,destination_relative,source_size,source_modtime_ns,plex_observed_path,plex_item_key,provider_guid,quality_evidence_state,quality_evidence,state,verified_at) VALUES($1,$2,$1,$3,$4,$5,'season:02','/libraries/tv/existing','Integration Series S02E01.mkv','Integration Series/Season 02/Integration Series - S02E01.mkv',2048,2,'/libraries/tv/Integration Series/Season 02/Integration Series - S02E01.mkv','series-2','tvdb://987654','media_info','{}','current',$6)`, immediateItem.ID.String(), immediateAdoption.ID.String(), series.ID.String(), seriesRoot.String(), seriesProfile.String(), time.Now().UTC()); err != nil {
		_ = immediateTransaction.Rollback(ctx)
		t.Fatal(err)
	}
	monitoringChanges, immediateSearches, err := store.applyAdoptionMonitoring(ctx, immediateTransaction, immediateAdoption.ID, time.Now().UTC())
	if err != nil || monitoringChanges != 1 || immediateSearches != 1 {
		_ = immediateTransaction.Rollback(ctx)
		t.Fatalf("apply immediate adoption monitoring changes=%d searches=%d err=%v", monitoringChanges, immediateSearches, err)
	}
	var immediateAuthorizations, immediateSearchEvents, immediateExpectedEpisodes int
	if err := immediateTransaction.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM adopted_replacement_authorizations replacement_auth JOIN adoption_monitoring_applications application ON application.workflow_id=replacement_auth.workflow_id WHERE application.batch_id=$1),
		(SELECT count(*) FROM outbox_events event JOIN adoption_monitoring_applications application ON application.workflow_id=event.aggregate_id WHERE application.batch_id=$1 AND event.event_type='workflow.search_requested'),
		(SELECT count(*) FROM pack_expected_episodes episode JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id JOIN adoption_monitoring_applications application ON application.workflow_id=expectation.workflow_id WHERE application.batch_id=$1)`, immediateAdoption.ID.String()).Scan(&immediateAuthorizations, &immediateSearchEvents, &immediateExpectedEpisodes); err != nil || immediateAuthorizations != 1 || immediateSearchEvents != 1 || immediateExpectedEpisodes != 3 {
		_ = immediateTransaction.Rollback(ctx)
		t.Fatalf("immediate adoption evidence authorizations=%d search_events=%d expected_episodes=%d err=%v", immediateAuthorizations, immediateSearchEvents, immediateExpectedEpisodes, err)
	}
	if err := immediateTransaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	sources, err := store.ListSourceStatuses(ctx)
	if err != nil || len(sources) != 4 {
		t.Fatalf("source read model: items=%d err=%v", len(sources), err)
	}
	health, err := store.SystemHealth(ctx, time.Now().UTC())
	if err != nil || health.DatabaseState != "ready" || health.SchemaVersion != CurrentSchemaVersion || len(health.Components) != 4 {
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
