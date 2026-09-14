package postgres

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

func openAiringTestStore(t *testing.T) (*Store, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	schema := "mm_airing_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
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
	t.Cleanup(store.Close)
	if err := Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`), SecretRef: "ALTMOUNT_SECRET"}); err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p", "2160p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "TV quality", PolicyVersion: 1, Policy: policy, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, clientID, err := store.ResolveMediaConfiguration(ctx, catalog.KindSeries, &root.ID, &profile.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return store, root.ID, profile.ID, clientID
}

func createAiringSeries(t *testing.T, ctx context.Context, store *Store, rootID, profileID, clientID uuid.UUID, title string, episodes []ManualEpisode) (catalog.MediaItem, uuid.UUID) {
	t.Helper()
	year := uint16(2026)
	series, err := catalog.New(catalog.KindSeries, title, &year, []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "481455", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	season := ManualSeason{Number: 1, Monitored: true, SearchImmediately: false, PreferSeasonPack: true, Episodes: episodes}
	created, err := store.CreateManualMedia(ctx, series, rootID, profileID, clientID, []ManualSeason{season}, false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("season workflows=%d", len(created))
	}
	return series, created[0].ID
}

func TestDispatchAiringSeasonAcquiresReleasedEpisodesAndSkipsFuture(t *testing.T) {
	store, rootID, profileID, clientID := openAiringTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	series, workflowID := createAiringSeries(t, ctx, store, rootID, profileID, clientID, "Rivalerne", []ManualEpisode{
		{Number: 1, ProviderID: "tmdb:7695209", Title: "Episode 1", AirDate: now.AddDate(0, 0, -1).Format("2006-01-02")},
		{Number: 2, ProviderID: "tmdb:7695853", Title: "Episode 2", AirDate: now.AddDate(0, 0, 6).Format("2006-01-02")},
		{Number: 3, ProviderID: "tmdb:7695854", Title: "Episode 3", AirDate: now.AddDate(0, 0, 13).Format("2006-01-02")},
	})

	dispatched, err := store.DispatchAiringSeason(ctx, workflowID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !dispatched {
		t.Fatal("airing season was not dispatched into individual episodes")
	}

	// E01 is released and missing: one episode workflow with exactly one frozen
	// expectation and one targeted search.
	assertEpisodeWorkflow(t, ctx, store, series.ID, 1, "monitored")
	if got := countEpisodeWorkflows(t, ctx, store, series.ID, 1); got != 1 {
		t.Fatalf("episode workflows for E01=%d, want 1", got)
	}
	// E02 and E03 are future and must never be searched prematurely.
	for _, episode := range []uint16{2, 3} {
		if got := countEpisodeWorkflows(t, ctx, store, series.ID, episode); got != 0 {
			t.Fatalf("future episode E%02d created %d workflow(s)", episode, got)
		}
	}
	// No season-pack search event is created while coordinating episodes.
	var packSearchEvents int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflowID.String()).Scan(&packSearchEvents); err != nil {
		t.Fatal(err)
	}
	if packSearchEvents != 1 {
		t.Fatalf("season dispatch events=%d, want one scheduled next dispatch", packSearchEvents)
	}
	var nextAvailable time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT available_at FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflowID.String()).Scan(&nextAvailable); err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	if !nextAvailable.Equal(wantNext) {
		t.Fatalf("next dispatch=%s, want %s", nextAvailable.UTC(), wantNext.UTC())
	}
	var seasonState string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1`, workflowID.String()).Scan(&seasonState); err != nil {
		t.Fatal(err)
	}
	if seasonState != "waiting" {
		t.Fatalf("season state=%q, want waiting", seasonState)
	}
}

func TestDispatchAiringSeasonLaterAirDateMakesEpisodeActionable(t *testing.T) {
	store, rootID, profileID, clientID := openAiringTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	series, workflowID := createAiringSeries(t, ctx, store, rootID, profileID, clientID, "Later Airing", []ManualEpisode{
		{Number: 1, ProviderID: "tmdb:1", Title: "Episode 1", AirDate: now.AddDate(0, 0, -1).Format("2006-01-02")},
		{Number: 2, ProviderID: "tmdb:2", Title: "Episode 2", AirDate: now.AddDate(0, 0, 5).Format("2006-01-02")},
	})

	if dispatched, err := store.DispatchAiringSeason(ctx, workflowID, now); err != nil || !dispatched {
		t.Fatalf("first dispatch dispatched=%v err=%v", dispatched, err)
	}
	if got := countEpisodeWorkflows(t, ctx, store, series.ID, 2); got != 0 {
		t.Fatalf("E02 workflows before air date=%d", got)
	}

	// The scheduler wakes after the air date: the monitored season must become
	// actionable without any owner action.
	later := now.AddDate(0, 0, 6)
	if dispatched, err := store.DispatchAiringSeason(ctx, workflowID, later); err != nil || !dispatched {
		t.Fatalf("later dispatch dispatched=%v err=%v", dispatched, err)
	}
	assertEpisodeWorkflow(t, ctx, store, series.ID, 2, "monitored")
}

func TestDispatchCompleteMissingSeasonKeepsSinglePackSearch(t *testing.T) {
	store, rootID, profileID, clientID := openAiringTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	series, workflowID := createAiringSeries(t, ctx, store, rootID, profileID, clientID, "Complete Missing", []ManualEpisode{
		{Number: 1, ProviderID: "tmdb:1", AirDate: now.AddDate(0, 0, -20).Format("2006-01-02")},
		{Number: 2, ProviderID: "tmdb:2", AirDate: now.AddDate(0, 0, -13).Format("2006-01-02")},
	})
	dispatched, err := store.DispatchAiringSeason(ctx, workflowID, now)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched {
		t.Fatal("complete entirely missing season must use the season-pack path")
	}
	for _, episode := range []uint16{1, 2} {
		if got := countEpisodeWorkflows(t, ctx, store, series.ID, episode); got != 0 {
			t.Fatalf("complete missing season created episode workflow E%02d", episode)
		}
	}
	assertEpisodeIntentUnchanged(t, ctx, store, workflowID, 2)
}

func TestDispatchPartiallyAcquiredCompleteSeasonAvoidsDuplicateEpisodes(t *testing.T) {
	store, rootID, profileID, clientID := openAiringTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	series, workflowID := createAiringSeries(t, ctx, store, rootID, profileID, clientID, "Partial Season", []ManualEpisode{
		{Number: 1, ProviderID: "tmdb:1", Title: "Episode 1", AirDate: now.AddDate(0, 0, -7).Format("2006-01-02")},
		{Number: 2, ProviderID: "tmdb:2", Title: "Episode 2", AirDate: now.AddDate(0, 0, 3).Format("2006-01-02")},
	})
	if dispatched, err := store.DispatchAiringSeason(ctx, workflowID, now); err != nil || !dispatched {
		t.Fatalf("first dispatch dispatched=%v err=%v", dispatched, err)
	}
	// The first episode is confirmed exactly as Plex publication would mark it.
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='plex_confirmed',terminal_at=$2 WHERE media_id=$1 AND variant='season:01:episode:01'`, series.ID.String(), now); err != nil {
		t.Fatal(err)
	}

	later := now.AddDate(0, 0, 4)
	if dispatched, err := store.DispatchAiringSeason(ctx, workflowID, later); err != nil || !dispatched {
		t.Fatalf("later dispatch dispatched=%v err=%v", dispatched, err)
	}
	if got := countEpisodeWorkflows(t, ctx, store, series.ID, 1); got != 1 {
		t.Fatalf("E01 workflow duplicated: %d", got)
	}
	assertEpisodeWorkflow(t, ctx, store, series.ID, 2, "monitored")
}

func assertEpisodeWorkflow(t *testing.T, ctx context.Context, store *Store, mediaID uuid.UUID, episode uint16, wantState string) {
	t.Helper()
	variant := episodeVariantForTest(1, episode)
	var state string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE media_id=$1 AND variant=$2 AND terminal_at IS NULL`, mediaID.String(), variant).Scan(&state); err != nil {
		t.Fatalf("load episode workflow %s: %v", variant, err)
	}
	if state != wantState {
		t.Fatalf("episode workflow %s state=%q, want %q", variant, state, wantState)
	}
	var episodes, events int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM pack_expected_episodes episode JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id WHERE expectation.workflow_id=(SELECT id FROM workflows WHERE media_id=$1 AND variant=$2)`, mediaID.String(), variant).Scan(&episodes); err != nil {
		t.Fatal(err)
	}
	if episodes != 1 {
		t.Fatalf("episode workflow %s expectation episodes=%d, want 1", variant, episodes)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=(SELECT id FROM workflows WHERE media_id=$1 AND variant=$2) AND event_type='workflow.search_requested'`, mediaID.String(), variant).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("episode workflow %s search events=%d, want 1", variant, events)
	}
}

func countEpisodeWorkflows(t *testing.T, ctx context.Context, store *Store, mediaID uuid.UUID, episode uint16) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE media_id=$1 AND variant=$2`, mediaID.String(), episodeVariantForTest(1, episode)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertEpisodeIntentUnchanged(t *testing.T, ctx context.Context, store *Store, workflowID uuid.UUID, wantEpisodes int) {
	t.Helper()
	var episodes int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM pack_expected_episodes episode JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id WHERE expectation.workflow_id=$1`, workflowID.String()).Scan(&episodes); err != nil {
		t.Fatal(err)
	}
	if episodes != wantEpisodes {
		t.Fatalf("season expectation episodes=%d, want %d", episodes, wantEpisodes)
	}
}

func episodeVariantForTest(season, episode uint16) string {
	return workflows.EpisodeVariant(season, episode)
}
