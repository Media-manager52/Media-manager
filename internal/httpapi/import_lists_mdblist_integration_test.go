package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/mdblist"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/intake"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

type fakeListFetcher struct {
	items []mdblist.Item
	err   error
	reads int
}

func (fetcher *fakeListFetcher) Fetch(_ context.Context, ref mdblist.ListRef) ([]mdblist.Item, error) {
	fetcher.reads++
	if ref.Owner != "someone" || ref.Slug != "test-list" {
		return nil, mdblist.ErrListNotFound
	}
	return fetcher.items, fetcher.err
}

// fakeTMDB answers like Seerr's TMDB proxy for a fixed set of titles.
type fakeTMDB struct {
	seasonCalls []int16
}

func (provider *fakeTMDB) Search(context.Context, string, int) (seerr.SearchPage, error) {
	return seerr.SearchPage{}, nil
}

func (provider *fakeTMDB) Metadata(_ context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error) {
	tvdb := int64(70004)
	switch {
	case mediaType == "movie" && tmdbID == 900003:
		return seerr.Metadata{ID: tmdbID, Title: "List Film", ReleaseDate: "2021-05-01", OriginalLanguage: "da", ExternalIDs: seerr.ExternalIDs{IMDbID: "tt9000030"}}, nil
	case mediaType == "tv" && tmdbID == 900004:
		return seerr.Metadata{ID: tmdbID, Name: "List Series", FirstAirDate: "2019-01-10", OriginalLanguage: "da", ExternalIDs: seerr.ExternalIDs{TVDBID: &tvdb}, Seasons: []seerr.SeasonSummary{{SeasonNumber: 0}, {SeasonNumber: 1}, {SeasonNumber: 2}, {SeasonNumber: 3}}}, nil
	}
	return seerr.Metadata{}, errors.New("not found")
}

func (provider *fakeTMDB) SeasonMetadata(_ context.Context, tmdbID int64, season int16) (seerr.SeasonMetadata, error) {
	provider.seasonCalls = append(provider.seasonCalls, season)
	if tmdbID != 900004 {
		return seerr.SeasonMetadata{}, errors.New("not found")
	}
	switch season {
	case 1:
		return seerr.SeasonMetadata{SeasonNumber: 1, Episodes: []seerr.EpisodeMetadata{{ID: 1, EpisodeNumber: 1, Name: "One", AirDate: "2019-01-10"}, {ID: 2, EpisodeNumber: 2, Name: "Two", AirDate: "2019-01-17"}}}, nil
	case 2:
		return seerr.SeasonMetadata{SeasonNumber: 2, Episodes: []seerr.EpisodeMetadata{{ID: 3, EpisodeNumber: 1, Name: "Three", AirDate: "2020-01-10"}}}, nil
	case 3:
		// Announced, but TMDB has no episodes yet.
		return seerr.SeasonMetadata{SeasonNumber: 3}, nil
	}
	return seerr.SeasonMetadata{}, errors.New("unexpected season")
}

func TestFollowedMDBListAddsNewTitlesLikeAdd(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	if _, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "List indexer", SecretRef: "LIST_KEY", Config: json.RawMessage(`{"base_url":"https://indexer.example/api","mode_verified":true,"mode":"movie","minimum_interval_ms":1,"query_limit":100,"grab_limit":10,"extended":true,"search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=true,config=config || '{"mode_verified":true,"mode":"movie","search_dialects":{"movie":{"mode":"movie","params":["q","imdbid","year"]}}}'::jsonb WHERE kind='newznab'`); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) (int, []byte) {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		request, _ := http.NewRequest(method, testServer.URL+path, reader)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var buffer bytes.Buffer
		_, _ = buffer.ReadFrom(response.Body)
		return response.StatusCode, buffer.Bytes()
	}
	identities := func(tmdb, imdb string) []map[string]any {
		return []map[string]any{{"namespace": "tmdb", "value": tmdb, "state": "verified", "source": "test"}, {"namespace": "imdb", "value": imdb, "state": "verified", "source": "test"}}
	}
	existing := createMediaThroughAPI(t, testServer.URL, map[string]any{"kind": "movie", "title": "Already Here", "year": 2010, "search_immediately": false, "identities": identities("900001", "tt9000010")}, 1)
	removed := createMediaThroughAPI(t, testServer.URL, map[string]any{"kind": "movie", "title": "Removed Earlier", "year": 2011, "search_immediately": false, "identities": identities("900002", "tt9000020")}, 1)
	if status, body := call(http.MethodPost, "/api/v1/media/"+removed.ID.String()+"/archive", nil); status != http.StatusOK {
		t.Fatalf("archive = %d %s", status, body)
	}

	if status, body := call(http.MethodPost, "/api/v1/import-lists", map[string]any{"provider": "mdblist", "url": "https://example.com/lists/someone/test-list", "enabled": true}); status != http.StatusUnprocessableEntity || !strings.Contains(string(body), "invalid_list_url") {
		t.Fatalf("foreign address = %d %s", status, body)
	}
	status, body := call(http.MethodPost, "/api/v1/import-lists", map[string]any{"provider": "mdblist", "url": "https://mdblist.com/lists/someone/test-list/", "enabled": true})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}
	var created struct{ Data postgres.ImportList }
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	list := created.Data
	if list.Name != "test-list" || list.SourceURL != "https://mdblist.com/lists/someone/test-list" || list.Entries == nil {
		t.Fatalf("created list = %+v", list)
	}
	if status, body := call(http.MethodPost, "/api/v1/import-lists", map[string]any{"provider": "mdblist", "url": "someone/TEST-LIST", "name": "Other name", "enabled": true}); status != http.StatusConflict {
		t.Fatalf("same list again = %d %s", status, body)
	}
	countEvents := func(eventType string) int {
		t.Helper()
		var count int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type=$1`, eventType).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := countEvents("import_list.sync_requested"); got != 1 {
		t.Fatalf("queued reads = %d, want 1", got)
	}
	if status, body := call(http.MethodPost, "/api/v1/import-lists/"+list.ID.String()+"/sync", nil); status != http.StatusAccepted || !strings.Contains(string(body), `"already_waiting":true`) {
		t.Fatalf("sync while waiting = %d %s", status, body)
	}
	if queued, err := store.QueueDueImportListSyncs(ctx, time.Now().UTC()); err != nil || queued != 0 {
		t.Fatalf("due lists while a read waits = %d, %v", queued, err)
	}

	fetcher := &fakeListFetcher{items: []mdblist.Item{
		{Rank: 1, Kind: "movie", TMDBID: 900001, Title: "Already Here", Year: 2010},
		{Rank: 2, Kind: "movie", TMDBID: 900002, IMDbID: "tt9000020", Title: "Removed Earlier"},
		{Rank: 3, Kind: "movie", TMDBID: 900003, Title: "List Film", Year: 2021},
		{Rank: 4, Kind: "series", TMDBID: 900004, TVDBID: 70004, Title: "List Series"},
		{Rank: 5, Kind: "movie", TMDBID: 900005, Title: "Unknown To TMDB"},
	}}
	tmdb := &fakeTMDB{}
	service := &intake.ImportListService{Store: store, Lists: fetcher, MetadataEnricher: &metadataservice.Service{}, Discovery: func(context.Context) (metadataservice.DiscoveryProvider, bool) { return tmdb, true }}
	event := postgres.OutboxEvent{Type: "import_list.sync_requested", Payload: json.RawMessage(`{"import_list_id":"` + list.ID.String() + `"}`)}
	searchesBefore := countEvents("workflow.search_requested")
	if err := service.Handle(ctx, event); err != nil {
		t.Fatal(err)
	}
	entries := func() map[int64]postgres.ImportListEntry {
		t.Helper()
		status, body := call(http.MethodGet, "/api/v1/import-lists/"+list.ID.String()+"/entries", nil)
		if status != http.StatusOK {
			t.Fatalf("entries = %d %s", status, body)
		}
		var envelope struct{ Data []postgres.ImportListEntry }
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		result := map[int64]postgres.ImportListEntry{}
		for _, entry := range envelope.Data {
			result[entry.TMDBID] = entry
		}
		return result
	}
	got := entries()
	if len(got) != 5 {
		t.Fatalf("entries = %+v", got)
	}
	if entry := got[900001]; entry.State != "in_library" || entry.MediaID == nil || *entry.MediaID != existing.ID {
		t.Fatalf("existing title = %+v", entry)
	}
	if entry := got[900002]; entry.State != "removed_earlier" || entry.MediaID == nil || *entry.MediaID != removed.ID {
		t.Fatalf("removed title = %+v", entry)
	}
	if entry := got[900005]; entry.State != "failed" || entry.Detail != "metadata_failed" || entry.Attempts != 1 {
		t.Fatalf("unknown title = %+v", entry)
	}
	film, series := got[900003], got[900004]
	if film.State != "added" || film.MediaID == nil || series.State != "added" || series.MediaID == nil {
		t.Fatalf("new titles = %+v / %+v", film, series)
	}
	filmDetail := getMediaDetailThroughAPI(t, testServer.URL, *film.MediaID)
	if filmDetail.Media.Title != "List Film" || len(filmDetail.Workflows) != 1 || filmDetail.Workflows[0].Variant != "default" {
		t.Fatalf("film detail = %+v", filmDetail)
	}
	seriesDetail := getMediaDetailThroughAPI(t, testServer.URL, *series.MediaID)
	variants := []string{}
	for _, workflow := range seriesDetail.Workflows {
		variants = append(variants, workflow.Variant)
	}
	if strings.Join(variants, ",") != "season:01,season:02" {
		t.Fatalf("series workflows = %v, want season 1 and 2 (no specials, no season without episodes)", variants)
	}
	for _, season := range tmdb.seasonCalls {
		if season == 0 {
			t.Fatal("specials were looked up")
		}
	}
	if added := countEvents("workflow.search_requested") - searchesBefore; added < 3 {
		t.Fatalf("searches queued = %d, want one per new film and season", added)
	}
	var stored postgres.ImportList
	if stored, err = store.ImportList(ctx, list.ID); err != nil {
		t.Fatal(err)
	}
	if stored.LastSyncState != "ok" || stored.LastSyncedAt == nil || stored.NextSyncAt == nil || time.Until(*stored.NextSyncAt) < 5*time.Hour || stored.Entries.Added != 2 || stored.Entries.Failed != 1 || stored.Entries.Total != 5 {
		t.Fatalf("list after read = %+v entries %+v", stored, stored.Entries)
	}

	// A second read adds nothing twice and tries the failed title again.
	var mediaCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM media_items`).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	// A title that leaves the list stays in the library.
	fetcher.items = fetcher.items[1:3:3]
	fetcher.items = append(fetcher.items, mdblist.Item{Rank: 5, Kind: "movie", TMDBID: 900005, Title: "Unknown To TMDB"})
	if err := service.Handle(ctx, event); err != nil {
		t.Fatal(err)
	}
	var mediaAfter int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM media_items`).Scan(&mediaAfter); err != nil {
		t.Fatal(err)
	}
	got = entries()
	if mediaAfter != mediaCount || len(got) != 5 || got[900004].State != "added" || got[900001].State != "in_library" || got[900005].Attempts != 2 {
		t.Fatalf("second read: media %d -> %d, entries %+v", mediaCount, mediaAfter, got)
	}

	// A list that cannot be read keeps its titles and is tried again soon.
	fetcher.err = mdblist.ErrUnavailable
	if err := service.Handle(ctx, event); err != nil {
		t.Fatal(err)
	}
	if stored, err = store.ImportList(ctx, list.ID); err != nil || stored.LastSyncState != "failed" || stored.LastSyncDetail != "mdblist_unavailable" || time.Until(*stored.NextSyncAt) > 2*time.Hour || stored.Entries.Total != 5 {
		t.Fatalf("list after a failed read = %+v, %v", stored, err)
	}
	fetcher.err = nil

	// Folders and profiles must belong to the right kind.
	var seriesRoot, movieRoot string
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT id::text FROM library_roots WHERE media_kind='series'),(SELECT id::text FROM library_roots WHERE media_kind='movie')`).Scan(&seriesRoot, &movieRoot); err != nil {
		t.Fatal(err)
	}
	if status, body := call(http.MethodPatch, "/api/v1/import-lists/"+list.ID.String(), map[string]any{"name": "Wrong", "enabled": true, "movie_root_id": seriesRoot}); status != http.StatusUnprocessableEntity || !strings.Contains(string(body), "invalid_import_list") {
		t.Fatalf("series folder as film folder = %d %s", status, body)
	}
	if status, body := call(http.MethodPatch, "/api/v1/import-lists/"+list.ID.String(), map[string]any{"name": "Chosen", "enabled": true, "movie_root_id": movieRoot, "series_root_id": seriesRoot}); status != http.StatusOK || !strings.Contains(string(body), `"movie_root_name":"Test Movies"`) || !strings.Contains(string(body), `"series_root_name":"Test TV"`) {
		t.Fatalf("choose folders = %d %s", status, body)
	}

	// Turned off, the list is not read; deleting it keeps every added title.
	if status, body := call(http.MethodPatch, "/api/v1/import-lists/"+list.ID.String(), map[string]any{"name": "Renamed", "enabled": false}); status != http.StatusOK || !strings.Contains(string(body), `"name":"Renamed"`) {
		t.Fatalf("turn off = %d %s", status, body)
	}
	reads := fetcher.reads
	if err := service.Handle(ctx, event); err != nil || fetcher.reads != reads {
		t.Fatalf("a list turned off was read: %v", err)
	}
	if status, body := call(http.MethodPost, "/api/v1/import-lists/"+list.ID.String()+"/sync", nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("sync of a list turned off = %d %s", status, body)
	}
	if status, body := call(http.MethodDelete, "/api/v1/import-lists/"+list.ID.String(), nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", status, body)
	}
	if err := service.Handle(ctx, event); err != nil {
		t.Fatalf("a read of a deleted list = %v", err)
	}
	var remaining, entryRows int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM media_items WHERE deleted_at IS NULL AND id=ANY($1::uuid[])`, []string{film.MediaID.String(), series.MediaID.String()}).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM import_list_entries`).Scan(&entryRows); err != nil {
		t.Fatal(err)
	}
	if remaining != 2 || entryRows != 0 {
		t.Fatalf("after delete: %d added titles left (want 2), %d entries (want 0)", remaining, entryRows)
	}
	if status, _ := call(http.MethodGet, "/api/v1/import-lists/"+uuid.NewV7().String()+"/entries", nil); status != http.StatusNotFound {
		t.Fatalf("entries of unknown list = %d", status)
	}
}
