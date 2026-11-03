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

// TestStreamingChoiceSurvivesARestart covers a stream list the player still
// shows after Media Manager restarts (every deploy): the release id keeps
// working from the database, the stored row holds no credential, and an
// edited indexer or an expired list still asks the viewer to refresh.
func TestStreamingChoiceSurvivesARestart(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Restart indexer", SecretRef: "RESTART_KEY", Config: json.RawMessage(`{"base_url":"https://indexer.example/api","mode_verified":true,"mode":"movie","minimum_interval_ms":1,"query_limit":100,"grab_limit":10,"extended":true,"search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=true,config=config || '{"mode_verified":true,"mode":"movie","search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}'::jsonb WHERE id=$1`, indexer.ID.String()); err != nil {
		t.Fatal(err)
	}
	var searches, downloads atomic.Int32
	transport := browseTransport(func(r *http.Request) (*http.Response, error) {
		respond := func(body string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		if r.URL.Host != "indexer.example" {
			t.Errorf("unexpected request to %s", r.URL.Redacted())
			return nil, io.ErrUnexpectedEOF
		}
		if r.URL.Query().Get("t") == "get" {
			downloads.Add(1)
			if r.URL.Query().Get("id") != "release-one" || r.URL.Query().Get("apikey") != "indexer-key" {
				t.Errorf("download for the wrong release or without the key: id=%q", r.URL.Query().Get("id"))
			}
			return respond(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="x" date="0" subject="Example.2020.1080p.WEB-DL.mkv"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="1" number="1">x@y</segment></segments></file></nzb>`)
		}
		searches.Add(1)
		return respond(`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Example.2020.1080p.WEB-DL</title><guid>release-one</guid><newznab:attr name="imdbid" value="0000123"/></item></channel></rss>`)
	})
	// Each call builds a fresh BrowseService: a new process with no memory of
	// earlier stream lists, sharing only the database.
	restart := func() *Server {
		browse := &acquisition.BrowseService{Search: &acquisition.SearchService{Store: store, Scheduler: indexers.NewScheduler(4, store), ResolveSecret: func(string) (string, error) { return "indexer-key", nil }, Transport: transport}}
		server, err := New(Config{OwnerToken: testOwnerToken, Streaming: browse}, store)
		if err != nil {
			t.Fatal(err)
		}
		return server
	}
	call := func(server *Server, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testOwnerToken)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	// The NZB request shares the indexer's pacing (at least 3 s apart), so an
	// attempt right after the search is deferred with 503; retry as the addon
	// does.
	download := func(server *Server, id string) *httptest.ResponseRecorder {
		for attempt := 0; ; attempt++ {
			w := call(server, "/api/v1/streaming/releases/"+id+"/nzb", "")
			if w.Code != 503 || attempt == 2 {
				return w
			}
			time.Sleep(3200 * time.Millisecond)
		}
	}

	w := call(restart(), "/api/v1/streaming/search", `{"kind":"movie","imdb_id":"tt0000123","title":"Example","year":2020}`)
	if w.Code != 200 {
		t.Fatalf("search %d %s", w.Code, w.Body)
	}
	var search struct {
		Data acquisition.BrowseResult `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &search); err != nil || len(search.Data.Releases) != 1 {
		t.Fatalf("search result %s", w.Body)
	}
	id := search.Data.Releases[0].ID

	var stored string
	if err = store.Pool.QueryRow(ctx, `SELECT row_to_json(streaming_choices)::text FROM streaming_choices WHERE token=$1`, id).Scan(&stored); err != nil {
		t.Fatalf("choice was not stored: %v", err)
	}
	for _, secret := range []string{"indexer-key", "apikey", "RESTART_KEY"} {
		if strings.Contains(stored, secret) {
			t.Fatalf("stored choice holds %q: %s", secret, stored)
		}
	}

	w = download(restart(), id)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<segment") || w.Header().Get("X-Media-Kind") != "movie" {
		t.Fatalf("after restart: %d %s", w.Code, w.Body)
	}
	if searches.Load() != 1 || downloads.Load() != 1 {
		t.Fatalf("searches=%d downloads=%d, want one each", searches.Load(), downloads.Load())
	}

	// An expired list asks for a refresh, also after a restart.
	if _, err = store.Pool.Exec(ctx, `UPDATE streaming_choices SET created_at=now()-interval '7 hours',expires_at=now()-interval '1 hour' WHERE token=$1`, id); err != nil {
		t.Fatal(err)
	}
	if w = call(restart(), "/api/v1/streaming/releases/"+id+"/nzb", ""); w.Code != 410 {
		t.Fatalf("expired stored choice status=%d %s", w.Code, w.Body)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE streaming_choices SET created_at=now(),expires_at=now()+interval '1 hour' WHERE token=$1`, id); err != nil {
		t.Fatal(err)
	}

	// Editing the indexer invalidates its open lists, as it does in memory.
	if _, err = store.Pool.Exec(ctx, `UPDATE integration_instances SET config=config || '{"extended":false}'::jsonb WHERE id=$1`, indexer.ID.String()); err != nil {
		t.Fatal(err)
	}
	if w = call(restart(), "/api/v1/streaming/releases/"+id+"/nzb", ""); w.Code != 410 {
		t.Fatalf("choice from an edited indexer status=%d %s", w.Code, w.Body)
	}
	if w = call(restart(), "/api/v1/streaming/releases/not-a-uuid/nzb", ""); w.Code != 410 {
		t.Fatalf("malformed id status=%d", w.Code)
	}
	if downloads.Load() != 1 {
		t.Fatalf("an invalid choice reached the indexer: downloads=%d", downloads.Load())
	}
}
