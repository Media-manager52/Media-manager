package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// TestAltMountStatusShowsDriveServersStreamsAndPlexView reads AltMount's live
// status through Media Manager: servers in AltMount's order with their pool
// state and quota, streams named by the title Media Manager uploaded them
// for, without the viewer's address; a part AltMount does not answer for is
// named while the rest is still shown. System health then says whether Plex
// lists AltMount's files, and warns only when a second look agrees.
func TestAltMountStatusShowsDriveServersStreamsAndPlexView(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	fake := &fakeUsenetAltMount{providers: []map[string]any{
		{"id": "provider_1", "name": "Hoved", "host": "news.example.test", "enabled": true, "quota_bytes": 1000},
		{"id": "provider_2", "host": "backup.example.test", "enabled": true, "is_backup_provider": true},
		{"id": "provider_3", "host": "old.example.test", "enabled": false},
	}}
	altMount := httptest.NewServer(fake.handler(t))
	defer altMount.Close()
	resolve := func(string) (string, error) { return `{"username":"mm-user","password":"mm-pass"}`, nil }
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", ResolveSecret: resolve}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	get := func(path string, target any) (int, string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, testServer.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		_ = json.Unmarshal(raw, &struct {
			Data any `json:"data"`
		}{Data: target})
		return response.StatusCode, string(raw)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$1, secret_ref='TEST_ALTMOUNT_SECRET' WHERE kind='altmount'`, `{"base_url":"`+altMount.URL+`","allow_http":true,"local_read_only_root":"/mnt/altmount"}`); err != nil {
		t.Fatal(err)
	}

	// A real upload: the stream's file name carries its job ID.
	movie := createMediaThroughAPI(t, testServer.URL, map[string]any{
		"kind": "movie", "title": "Stream Movie", "year": 2021, "search_immediately": false,
		"identities": []map[string]any{{"namespace": "tmdb", "value": "810777", "state": "verified", "source": "test_metadata"}},
		"metadata": map[string]any{
			"overview": "Streams.", "original_language": "en", "genres": []string{"Drama"}, "countries": []string{"DK"},
			"networks": []any{}, "aliases": []any{}, "sources": []any{}, "ratings": []any{}, "events": []any{}, "observed_at": "2026-09-01T00:00:00Z",
		},
	}, 1)
	workflowID := getMediaDetailThroughAPI(t, testServer.URL, movie.ID).Workflows[0].ID
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Stream Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	candidate := &storedDryRunCandidate{key: "stream", title: "Stream.Movie.2021.1080p.WEB-DL-GRP", facts: riFacts("resolution", "1080p"), observedAgo: time.Minute}
	seedStoredCandidate(t, store, workflowID, indexer.ID, candidate, time.Now().UTC().Truncate(time.Second))
	var roundID, altMountID string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1 ORDER BY round_ordinal LIMIT 1`, workflowID.String()).Scan(&roundID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE kind='altmount'`).Scan(&altMountID); err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot) VALUES($1,$2,$3,$4,1,'stream.nzb','needs_attention',now(),1,$5,1)`, jobID.String(), workflowID.String(), candidate.id.String(), altMountID, roundID); err != nil {
		t.Fatal(err)
	}

	fake.drive = map[string]any{"status": "running", "healthy": true, "path": "/mnt/altmount"}
	fake.pool = map[string]any{"bytes_downloaded_24h": 5000, "download_speed_bytes_per_sec": 1200.5, "providers": []map[string]any{
		{"id": "provider_1", "host": "news.example.test", "state": "active", "used_connections": 3, "max_connections": 10, "quota_bytes": 1000, "quota_used": 400, "quota_reset_at": "2026-12-01T00:00:00Z", "missing_warning": true},
		{"id": "provider_2", "host": "backup.example.test", "state": "active", "quota_reset_at": "0001-01-01T00:00:00Z"},
	}}
	fake.streams = []map[string]any{
		{"file_path": "media-manager/stream-movie--mm-" + jobID.String() + "/Stream.Movie.mkv", "source": "FUSE", "status": "Streaming", "total_size": 1000, "current_offset": 250, "bytes_per_second": 77, "client_ip": "192.0.2.44", "user_agent": "SecretPlayer"},
		{"file_path": "complete/Other.File.mkv", "source": "WebDAV", "status": "Buffering"},
	}

	var status altMountStatus
	code, body := get("/api/v1/system/altmount", &status)
	if code != http.StatusOK || status.Drive == nil || !status.Drive.Healthy || len(status.Unavailable) != 0 || status.BytesDownloaded24h != 5000 {
		t.Fatalf("status: %d %s", code, body)
	}
	if len(status.Servers) != 3 || status.Servers[0].ID != "provider_1" || status.Servers[0].Name != "Hoved" || !status.Servers[0].QuotaSet || status.Servers[0].Live == nil ||
		status.Servers[0].Live.QuotaUsed != 400 || status.Servers[0].Live.UsedConnections != 3 || !status.Servers[0].Live.MissingWarning || status.Servers[0].Live.QuotaResetAt == nil {
		t.Fatalf("first server: %s", body)
	}
	if !status.Servers[1].Backup || status.Servers[1].Live == nil || status.Servers[1].Live.QuotaResetAt != nil || status.Servers[2].Enabled || status.Servers[2].Live != nil {
		t.Fatalf("other servers: %s", body)
	}
	if len(status.Streams) != 2 || status.Streams[0].Title == nil || status.Streams[0].Title.Title != "Stream Movie" || status.Streams[0].Title.MediaID != movie.ID ||
		status.Streams[0].File != "Stream.Movie.mkv" || status.Streams[0].CurrentOffset != 250 || status.Streams[1].Title != nil {
		t.Fatalf("streams: %s", body)
	}
	if strings.Contains(body, "192.0.2.44") || strings.Contains(body, "SecretPlayer") || strings.Contains(body, "altmount-own-api-key") {
		t.Fatalf("status leaks viewer details or AltMount's key: %s", body)
	}

	fake.mu.Lock()
	fake.streams, fake.drive = nil, nil
	fake.mu.Unlock()
	status = altMountStatus{}
	if code, body := get("/api/v1/system/altmount", &status); code != http.StatusOK || strings.Join(status.Unavailable, ",") != "drive,streams" || len(status.Servers) != 3 {
		t.Fatalf("partial status: %d %s", code, body)
	}

	// Plex's view: the test store's roots say Plex sees AltMount at
	// /mnt/altmount. First Plex lists a dead drive, then a working one.
	plexListings := [][]string{{}, {}, {"/mnt/altmount/media-manager"}}
	asked := 0
	server.plexCheck.listLocal = func(root string) ([]string, error) {
		if root != "/mnt/altmount" {
			return nil, errors.New("unexpected root " + root)
		}
		return []string{"media-manager"}, nil
	}
	server.plexCheck.listPlex = func(_ context.Context, plex postgres.Integration, path string) ([]string, error) {
		if path != "/mnt/altmount" || plex.Name != "Test Plex" {
			t.Errorf("Plex asked for %s on %s", path, plex.Name)
		}
		listing := plexListings[min(asked, len(plexListings)-1)]
		asked++
		return listing, nil
	}
	server.plexCheck.recheck = 0
	var health systemHealthResponse
	if code, body := get("/api/v1/system/health", &health); code != http.StatusOK || len(health.PlexAltMount) != 1 || health.PlexAltMount[0].State != acquisition.PlexAltMountMissing ||
		health.PlexAltMountCheckedAt == nil || asked != 2 || len(health.Mounts) != 1 {
		t.Fatalf("health with a dead Plex drive (asked %d): %d %s", asked, code, body)
	}
	// Within two minutes the result is reused; Plex is not asked again.
	if _, body := get("/api/v1/system/health", &health); asked != 2 || !strings.Contains(body, `"state":"missing"`) {
		t.Fatalf("cached health (asked %d): %s", asked, body)
	}
	server.plexCheck.mu.Lock()
	server.plexCheck.checkedAt = time.Now().Add(-3 * time.Minute)
	server.plexCheck.mu.Unlock()
	health = systemHealthResponse{}
	if code, body := get("/api/v1/system/health", &health); code != http.StatusOK || len(health.PlexAltMount) != 1 || health.PlexAltMount[0].State != acquisition.PlexAltMountOK || asked != 3 {
		t.Fatalf("health with a working Plex drive (asked %d): %d %s", asked, code, body)
	}
}
