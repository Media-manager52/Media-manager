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
	"time"

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

// TestStreamingPrepareSubmitsToAltMountAndReturnsOnlyTheMountPath covers the
// whole viewer playback preparation: Media Manager fetches the chosen NZB with
// the indexer key, uploads it to AltMount's stream endpoint with the AltMount
// API key, and the player token gets back nothing but the file's mount path.
func TestStreamingPrepareSubmitsToAltMountAndReturnsOnlyTheMountPath(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Prepare indexer", SecretRef: "PREPARE_KEY", Config: json.RawMessage(`{"base_url":"https://indexer.example/api","mode_verified":true,"mode":"movie","minimum_interval_ms":1,"query_limit":100,"grab_limit":10,"extended":true,"search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=true,config=config || '{"mode_verified":true,"mode":"movie","search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}'::jsonb WHERE id=$1`, indexer.ID.String()); err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "AltMount", Config: json.RawMessage(`{"base_url":"http://altmount:8080","authentication":"anonymous_admin","allow_http":true,"category":"media-manager"}`)})
	if err != nil {
		t.Fatal(err)
	}
	// The test schema seeds a default AltMount client; this one replaces it.
	if _, err = store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=(id=$1) WHERE kind='altmount'`, altMount.ID.String()); err != nil {
		t.Fatal(err)
	}
	var uploads atomic.Int32
	var altMountKey, category, uploaded string
	transport := browseTransport(func(r *http.Request) (*http.Response, error) {
		respond := func(body string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		switch {
		case r.URL.Host == "altmount:8080" && r.URL.Path == "/api/nzb/streams":
			uploads.Add(1)
			altMountKey = r.Header.Get("X-Api-Key")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			category = r.FormValue("category")
			file, _, err := r.FormFile("file")
			if err == nil {
				body, _ := io.ReadAll(file)
				uploaded = string(body)
			}
			return respond(`{"streams":[{"url":"http://altmount:8080/api/files/stream?path=%2Fnuvio%2FExample.2020.1080p.WEB-DL%2FExample.2020.1080p.WEB-DL.mkv&download_key=secret-download-key","name":"Example.2020.1080p.WEB-DL.mkv"}],"_queue_item_id":7}`)
		case r.URL.Host == "indexer.example" && r.URL.Query().Get("t") == "get":
			return respond(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="x" date="0" subject="Example.2020.1080p.WEB-DL.mkv"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="1" number="1">x@y</segment></segments></file></nzb>`)
		case r.URL.Host == "indexer.example":
			return respond(`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Example.2020.1080p.WEB-DL</title><guid>release-one</guid><newznab:attr name="imdbid" value="0000123"/></item></channel></rss>`)
		}
		t.Errorf("unexpected request to %s", r.URL.Redacted())
		return nil, io.ErrUnexpectedEOF
	})
	player := strings.Repeat("p", 32)
	browse := &acquisition.BrowseService{Search: &acquisition.SearchService{Store: store, Scheduler: indexers.NewScheduler(4, store), ResolveSecret: func(string) (string, error) { return "indexer-key", nil }, Transport: transport}}
	call := func(server *Server, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+player)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}

	unconfigured, err := New(Config{OwnerToken: testOwnerToken, PlayerToken: player, Streaming: browse, StreamPreparer: &acquisition.StreamPreparer{Browse: browse}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(unconfigured, "/api/v1/streaming/releases/anything/prepare", ""); w.Code != 503 || !strings.Contains(w.Body.String(), "streaming_preparation_unavailable") {
		t.Fatalf("without an AltMount key: %d %s", w.Code, w.Body)
	}

	server, err := New(Config{OwnerToken: testOwnerToken, PlayerToken: player, Streaming: browse, StreamPreparer: &acquisition.StreamPreparer{Browse: browse, APIKey: "altmount-api-key", Category: acquisition.DefaultStreamCategory, Timeout: 10 * time.Second, Transport: transport}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(server, "/api/v1/streaming/releases/expired/prepare", ""); w.Code != 410 {
		t.Fatalf("expired choice status=%d", w.Code)
	}
	w := call(server, "/api/v1/streaming/search", `{"kind":"movie","imdb_id":"tt0000123","title":"Example","year":2020}`)
	if w.Code != 200 {
		t.Fatalf("search %d %s", w.Code, w.Body)
	}
	var search struct {
		Data acquisition.BrowseResult `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &search); err != nil || len(search.Data.Releases) != 1 {
		t.Fatalf("search result %s", w.Body)
	}
	// The NZB request shares the indexer's pacing with the search just made
	// (at least 3 s apart), so the first attempt is deferred with 503 and the
	// client retries, as the addon does.
	for attempt := 0; ; attempt++ {
		w = call(server, "/api/v1/streaming/releases/"+search.Data.Releases[0].ID+"/prepare", "")
		if w.Code != 503 || attempt == 2 {
			break
		}
		time.Sleep(3200 * time.Millisecond)
	}
	if w.Code != 200 {
		t.Fatalf("prepare %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{"secret-download-key", "download_key", "altmount-api-key", "indexer-key", "altmount:8080", "indexer.example"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("prepare response exposes %q: %s", secret, w.Body)
		}
	}
	var prepared struct {
		Data acquisition.PreparedStream `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Data.Path != "/nuvio/Example.2020.1080p.WEB-DL/Example.2020.1080p.WEB-DL.mkv" || prepared.Data.QueueItemID != 7 {
		t.Fatalf("prepared=%+v", prepared.Data)
	}
	if uploads.Load() != 1 || altMountKey != "altmount-api-key" || category != "nuvio" || !strings.Contains(uploaded, "<segment") {
		t.Fatalf("AltMount upload count=%d key=%q category=%q", uploads.Load(), altMountKey, category)
	}
	var grabs, workflows int
	_ = store.Pool.QueryRow(ctx, "SELECT count(*) FROM workflows").Scan(&workflows)
	_ = store.Pool.QueryRow(ctx, "SELECT count(*) FROM altmount_jobs").Scan(&grabs)
	if grabs != 0 || workflows != 0 {
		t.Fatal("viewer playback preparation created acquisition work")
	}
}
