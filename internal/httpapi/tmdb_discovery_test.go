package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/postgres"
)

type fakeTMDBDiscovery struct {
	configured bool
	searches   int
}

func (fake *fakeTMDBDiscovery) Configured(context.Context) bool { return fake.configured }
func (fake *fakeTMDBDiscovery) Search(_ context.Context, query string, page int) (seerr.SearchPage, error) {
	fake.searches++
	return seerr.SearchPage{Page: page, TotalPages: 1, TotalResults: 2, Results: []seerr.SearchResult{{ID: 61428, MediaType: "tv", Name: query}, {ID: 7, MediaType: "person", Name: "Someone"}}}, nil
}
func (fake *fakeTMDBDiscovery) Metadata(context.Context, string, int64) (seerr.Metadata, error) {
	return seerr.Metadata{}, nil
}
func (fake *fakeTMDBDiscovery) SeasonMetadata(context.Context, int64, int16) (seerr.SeasonMetadata, error) {
	return seerr.SeasonMetadata{}, nil
}

func TestTitleSearchUsesTMDBWhenNoSeerrIsConnected(t *testing.T) {
	tmdb := &fakeTMDBDiscovery{}
	server, err := New(Config{OwnerToken: testOwnerToken, TMDBDiscovery: tmdb}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	search := func() (int, map[string]any) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/discovery/search?query=1864", nil)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		var body map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder.Code, body
	}

	status, body := search()
	if status != http.StatusServiceUnavailable {
		t.Fatalf("without TMDB status=%d, want 503: %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "metadata_unavailable" {
		t.Fatalf("without TMDB code=%v, want metadata_unavailable", code)
	}
	if tmdb.searches != 0 {
		t.Fatal("an unconfigured TMDB was asked to search")
	}

	tmdb.configured = true
	status, body = search()
	if status != http.StatusOK || tmdb.searches != 1 {
		t.Fatalf("with TMDB status=%d searches=%d: %v", status, tmdb.searches, body)
	}
	results := body["data"].(map[string]any)["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["name"] != "1864" {
		t.Fatalf("results = %v, want only the series", results)
	}
}

func TestMetadataStatesReportTMDBLive(t *testing.T) {
	tmdb := &fakeTMDBDiscovery{}
	server := &Server{config: Config{TMDBDiscovery: tmdb, MetadataProviderStates: map[string]string{"tmdb": "not_configured", "tvdb": "direct_configured"}}}
	if state := server.metadataProviderStates(context.Background())["tmdb"]; state != "not_configured" {
		t.Fatalf("tmdb state without a token = %q", state)
	}
	tmdb.configured = true
	states := server.metadataProviderStates(context.Background())
	if states["tmdb"] != "direct_configured" || states["tvdb"] != "direct_configured" {
		t.Fatalf("states with a website token = %v", states)
	}
	if server.config.MetadataProviderStates["tmdb"] != "not_configured" {
		t.Fatal("the startup states were modified")
	}
}
