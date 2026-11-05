package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/quicksetup"
)

// TestRecommendedSetupCreatesProvenRootsAndProfiles runs "Brug anbefalet
// opsætning" against PostgreSQL and a Plex that sees the disk under other
// paths than Media Manager, through the real Plex client.
func TestRecommendedSetupCreatesProvenRootsAndProfiles(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	if _, err := store.Pool.Exec(ctx, `DELETE FROM library_roots`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=false`); err != nil {
		t.Fatal(err)
	}
	secrets, err := integrationsecrets.New(store, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	host := t.TempDir()
	for _, folder := range []string{"movies", "tv", "altmount/content"} {
		if err := os.MkdirAll(filepath.Join(host, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mounts := map[string]string{"/data/movies": filepath.Join(host, "movies"), "/data/tv": filepath.Join(host, "tv"), "/mnt/altmount": filepath.Join(host, "altmount")}
	const token = "plex-recommended-token"
	plexServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Plex-Token") != token {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.URL.Path == "/library/sections":
			_, _ = io.WriteString(writer, `<MediaContainer><Directory key="4" title="Film" type="movie"><Location path="/data/movies"/></Directory><Directory key="5" title="Serier" type="show"><Location path="/data/tv"/></Directory></MediaContainer>`)
		case strings.HasPrefix(request.URL.Path, "/services/browse/"):
			if request.URL.Query().Get("includeFiles") != "0" {
				t.Errorf("browse must not list files")
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(request.URL.Path, "/services/browse/"))
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			type entry struct {
				Path string `xml:"path,attr"`
			}
			var document struct {
				XMLName xml.Name `xml:"MediaContainer"`
				Paths   []entry  `xml:"Path"`
			}
			plexPath := string(decoded)
			if host, ok := mounts[plexPath]; ok {
				entries, _ := os.ReadDir(host)
				for _, item := range entries {
					document.Paths = append(document.Paths, entry{Path: path.Join(plexPath, item.Name())})
				}
			}
			encoded, _ := xml.Marshal(document)
			_, _ = writer.Write(encoded)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer plexServer.Close()

	plexConfig, _ := json.Marshal(map[string]any{"base_url": plexServer.URL, "allow_http": true})
	plexIntegration, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Plex anbefalet", Enabled: true, Default: true, Config: plexConfig})
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Save(ctx, plexIntegration.ID, token, plexIntegration.CreatedAt); err != nil {
		t.Fatal(err)
	}
	altConfig, _ := json.Marshal(map[string]any{"base_url": "http://altmount:8080", "allow_http": true, "local_read_only_root": filepath.Join(host, "altmount")})
	if _, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "AltMount anbefalet", Enabled: true, Default: true, Config: altConfig}); err != nil {
		t.Fatal(err)
	}

	resolve := func(reference string) (string, error) { return secrets.Resolve(ctx, reference) }
	service := &quicksetup.Service{Store: store, OpenPlex: quicksetup.OpenPlex(resolve), Bases: map[catalog.MediaKind][]string{catalog.KindMovie: {filepath.Join(host, "movies")}, catalog.KindSeries: {filepath.Join(host, "tv")}}, SourceCandidates: []string{"/mnt/altmount"}}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", IntegrationSecrets: secrets, ResolveSecret: resolve, RecommendedSetup: service}, store)
	if err != nil {
		t.Fatal(err)
	}
	run := func(apply bool) quicksetup.Report {
		t.Helper()
		body, _ := json.Marshal(map[string]bool{"apply": apply})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/recommended", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), token) {
			t.Fatal("the Plex token reached the response")
		}
		var envelope struct {
			Data quicksetup.Report `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}

	if report := run(false); len(report.Steps) != 4 || report.Steps[0].State != quicksetup.StateCreate {
		t.Fatalf("preview = %+v", report)
	}
	report := run(true)
	for _, step := range report.Steps {
		if step.State != quicksetup.StateCreated {
			t.Fatalf("step %s = %+v", step.ID, step)
		}
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[catalog.MediaKind]postgres.LibraryRoot{}
	for _, root := range roots {
		byKind[root.MediaKind] = root
	}
	if movie := byKind[catalog.KindMovie]; movie.DestinationRoot != filepath.Join(host, "movies") || movie.PlexVisibleRoot != "/data/movies" || movie.SourceVisibleRoot != "/mnt/altmount" || movie.PlexLibraryID != "4" || !movie.Default {
		t.Fatalf("movie root = %+v", movie)
	}
	if series := byKind[catalog.KindSeries]; series.DestinationRoot != filepath.Join(host, "tv") || series.PlexLibraryID != "5" || !series.Default {
		t.Fatalf("series root = %+v", series)
	}
	profiles, err := store.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, profile := range profiles {
		if profile.Default && profile.Enabled && profile.Name == quicksetup.ProfileName {
			defaults++
		}
	}
	if defaults != 2 {
		t.Fatalf("default Danish profiles = %d", defaults)
	}
	for _, folder := range []string{"movies", "tv"} {
		entries, _ := os.ReadDir(filepath.Join(host, folder))
		if len(entries) != 0 {
			t.Fatalf("markers left in %s: %v", folder, entries)
		}
	}
	for _, step := range run(true).Steps {
		if step.State != quicksetup.StateExists {
			t.Fatalf("second run step %s = %+v", step.ID, step)
		}
	}
}
