package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

const testOwnerToken = "test-only-owner-token-0123456789abcdef"

func TestCreateMovieAndSeriesThroughOwnerAPI(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	moviePayload := map[string]any{
		"kind":               "movie",
		"title":              "HTTP Integration Movie",
		"year":               2024,
		"search_immediately": false,
		"identities": []map[string]any{
			{"namespace": "tmdb", "value": "810001", "state": "verified", "source": "test_metadata"},
			{"namespace": "imdb", "value": "tt0810001", "state": "verified", "source": "test_metadata"},
		},
		"metadata": map[string]any{
			"overview":          "Persisted through the owner API.",
			"original_language": "da",
			"genres":            []string{"Drama"},
			"countries":         []string{"DK"},
			"networks":          []any{},
			"aliases":           []any{},
			"sources":           []any{},
			"ratings":           []any{},
			"events":            []any{},
			"observed_at":       "2026-09-01T00:00:00Z",
		},
	}
	unauthorizedBody, err := json.Marshal(moviePayload)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedRequest, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/media", bytes.NewReader(unauthorizedBody))
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	unauthorizedResponse, err := http.DefaultClient.Do(unauthorizedRequest)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized create status=%d, want %d", unauthorizedResponse.StatusCode, http.StatusUnauthorized)
	}
	var mediaBeforeOwnerCreate int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM media_items`).Scan(&mediaBeforeOwnerCreate); err != nil {
		t.Fatal(err)
	}
	if mediaBeforeOwnerCreate != 0 {
		t.Fatalf("unauthorized create persisted %d media items", mediaBeforeOwnerCreate)
	}
	movie := createMediaThroughAPI(t, testServer.URL, moviePayload, 1)
	if movie.Kind != catalog.KindMovie || movie.Title != "HTTP Integration Movie" || movie.Metadata == nil || movie.Metadata.Overview != "Persisted through the owner API." {
		t.Fatalf("movie response did not preserve the submitted media: %+v", movie)
	}
	movieDetail := getMediaDetailThroughAPI(t, testServer.URL, movie.ID)
	if len(movieDetail.Workflows) != 1 || movieDetail.Workflows[0].Variant != "default" || movieDetail.Workflows[0].State != workflows.StateMonitored || len(movieDetail.Seasons) != 0 {
		t.Fatalf("movie detail=%+v", movieDetail)
	}

	seriesPayload := map[string]any{
		"kind":               "series",
		"title":              "HTTP Integration Series",
		"year":               2023,
		"search_immediately": false,
		"identities": []map[string]any{
			{"namespace": "tmdb", "value": "820002", "state": "verified", "source": "test_metadata"},
			{"namespace": "tvdb", "value": "920002", "state": "verified", "source": "test_metadata"},
		},
		"metadata": map[string]any{
			"overview":          "Two exact season snapshots.",
			"original_language": "da",
			"season_count":      2,
			"episode_count":     5,
			"genres":            []string{"Drama"},
			"countries":         []string{"DK"},
			"networks":          []any{},
			"aliases":           []any{},
			"sources":           []any{},
			"ratings":           []any{},
			"events":            []any{},
			"observed_at":       "2026-09-01T00:00:00Z",
		},
		"seasons": []map[string]any{
			{
				"number": 1, "monitored": true, "search_immediately": false, "prefer_season_pack": true,
				"episodes": []map[string]any{
					{"number": 1, "provider_episode_id": "tvdb:920101", "title": "Pilot", "air_date": "2023-04-05"},
					{"number": 2, "provider_episode_id": "tvdb:920102", "title": "Second", "air_date": "2023-04-12"},
				},
			},
			{
				"number": 2, "monitored": true, "search_immediately": false, "prefer_season_pack": true,
				"episodes": []map[string]any{
					{"number": 1, "provider_episode_id": "tvdb:920201", "title": "Return", "air_date": "2024-05-01"},
					{"number": 2, "provider_episode_id": "tvdb:920202", "title": "Middle", "air_date": "2024-05-08"},
					{"number": 3, "provider_episode_id": "tvdb:920203", "title": "Finale", "air_date": "2024-05-15"},
				},
			},
		},
	}
	series := createMediaThroughAPI(t, testServer.URL, seriesPayload, 2)
	if series.Kind != catalog.KindSeries || series.Title != "HTTP Integration Series" || series.Metadata == nil || series.Metadata.SeasonCount == nil || *series.Metadata.SeasonCount != 2 {
		t.Fatalf("series response did not preserve the submitted media: %+v", series)
	}
	seriesDetail := getMediaDetailThroughAPI(t, testServer.URL, series.ID)
	if len(seriesDetail.Workflows) != 2 || len(seriesDetail.Seasons) != 2 {
		t.Fatalf("series detail workflows=%d seasons=%d", len(seriesDetail.Workflows), len(seriesDetail.Seasons))
	}
	if seriesDetail.Seasons[0].Number != 1 || seriesDetail.Seasons[0].ExpectedEpisodes != 2 || !seriesDetail.Seasons[0].PreferSeasonPack || seriesDetail.Seasons[0].SearchImmediately {
		t.Fatalf("season one=%+v", seriesDetail.Seasons[0])
	}
	if seriesDetail.Seasons[1].Number != 2 || seriesDetail.Seasons[1].ExpectedEpisodes != 3 || !seriesDetail.Seasons[1].PreferSeasonPack || seriesDetail.Seasons[1].SearchImmediately {
		t.Fatalf("season two=%+v", seriesDetail.Seasons[1])
	}

	var searchEvents int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_type='workflow.search_requested'`).Scan(&searchEvents); err != nil {
		t.Fatal(err)
	}
	if searchEvents != 0 {
		t.Fatalf("search events=%d, want zero", searchEvents)
	}
}

func createMediaThroughAPI(t *testing.T, baseURL string, payload any, wantWorkflows int) catalog.MediaItem {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/media", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d, want %d", response.StatusCode, http.StatusCreated)
	}
	if !strings.HasPrefix(response.Header.Get("Location"), "/api/v1/workflows/") {
		t.Fatalf("create Location=%q", response.Header.Get("Location"))
	}
	var envelope struct {
		Data struct {
			Media     catalog.MediaItem    `json:"media"`
			Workflows []workflows.Workflow `json:"workflows"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Workflows) != wantWorkflows {
		t.Fatalf("created workflows=%d, want %d", len(envelope.Data.Workflows), wantWorkflows)
	}
	return envelope.Data.Media
}

func getMediaDetailThroughAPI(t *testing.T, baseURL string, mediaID uuid.UUID) postgres.MediaDetail {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/media/"+mediaID.String()+"/detail", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	var envelope struct {
		Data postgres.MediaDetail `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func openHTTPTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	schema := "mm_http_test_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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

	plex, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Test Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "TEST_PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "Test AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true}`), SecretRef: "TEST_ALTMOUNT_SECRET"}); err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []catalog.MediaKind{catalog.KindMovie, catalog.KindSeries} {
		name, destination, visible, libraryID := "Test Movies", "/libraries/movies", "/movies", "1"
		if kind == catalog.KindSeries {
			name, destination, visible, libraryID = "Test TV", "/libraries/tv", "/tv", "2"
		}
		if _, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: kind, Name: name, DestinationRoot: destination, PlexVisibleRoot: visible, SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: libraryID, Default: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: kind, Name: fmt.Sprintf("%s quality", name), PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}
