package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
)

type browseTransport func(*http.Request) (*http.Response, error)

func (f browseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestStreamingOwnerBoundary(t *testing.T) {
	s := catalogServer(t)
	for _, path := range []string{"/api/v1/streaming/search", "/api/v1/streaming/releases/anything/nzb"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", path, w.Code)
		}
	}
}
func TestStreamingBrowseUsesSharedBudgetAndNeverGrabs(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	i, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Browse indexer", SecretRef: "BROWSE_KEY", Config: json.RawMessage(`{"base_url":"https://indexer.example/api","mode_verified":true,"mode":"movie","minimum_interval_ms":3000,"query_limit":100,"grab_limit":10,"extended":true,"search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=true,config=config || '{"mode_verified":true,"mode":"movie","search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}'::jsonb WHERE id=$1`, i.ID.String()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	transport := browseTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Query().Get("t") != "movie" || r.URL.Query().Get("extended") != "1" || r.URL.Query().Get("apikey") != "private-key" {
			t.Errorf("incorrect request shape")
		}
		body := `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Example.2020.1080p.WEB-DL</title><guid>release-one</guid><newznab:attr name="imdbid" value="0000123"/><newznab:attr name="subs" value="Danish"/><newznab:attr name="video" value="AVC"/></item><item><title>Other.2020.1080p</title><guid>release-two</guid></item></channel></rss>`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	service := &acquisition.BrowseService{Search: &acquisition.SearchService{Store: store, Scheduler: indexers.NewScheduler(4, store), ResolveSecret: func(string) (string, error) { return "private-key", nil }, Transport: transport}}
	server, err := New(Config{OwnerToken: testOwnerToken, Streaming: service}, store)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"kind":"movie","imdb_id":"tt0000123","title":"Example","year":2020}`
	var previous string
	for range 2 {
		r := httptest.NewRequest("POST", "/api/v1/streaming/search", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testOwnerToken)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "private-key") || strings.Contains(w.Body.String(), "https://indexer") {
			t.Fatal("private locator exposed")
		}
		var envelope struct {
			Data acquisition.BrowseResult `json:"data"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data.Releases) != 1 {
			t.Fatalf("wrong results: %s", w.Body)
		}
		id := envelope.Data.Releases[0].ID
		if previous != "" && previous != id {
			t.Fatal("cache lost exact choice")
		}
		previous = id
	}
	if calls.Load() != 1 {
		t.Fatalf("browsing made %d requests instead of one cached search", calls.Load())
	}
	var grabs, workflows int
	_ = store.Pool.QueryRow(ctx, "SELECT count(*) FROM workflows").Scan(&workflows)
	_ = store.Pool.QueryRow(ctx, "SELECT count(*) FROM altmount_jobs").Scan(&grabs)
	if grabs != 0 || workflows != 0 {
		t.Fatal("browsing created acquisition work")
	}
	r := httptest.NewRequest("POST", "/api/v1/streaming/releases/expired/nzb", nil)
	r.Header.Set("Authorization", "Bearer "+testOwnerToken)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != 410 {
		t.Fatalf("expired token status=%d", w.Code)
	}
}
