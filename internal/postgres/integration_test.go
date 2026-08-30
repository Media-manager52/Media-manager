package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
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
	season := ManualSeason{Number: 1, Monitored: true, SearchImmediately: true, PreferSeasonPack: true, Episodes: []ManualEpisode{{Number: 1, ProviderID: "tvdb:1001", Title: "Pilot"}, {Number: 2, ProviderID: "tvdb:1002", Title: "Second"}}}
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
	candidates, err := store.ListReleaseCandidates(ctx, 100)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("release read model before search worker: items=%d err=%v", len(candidates), err)
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
