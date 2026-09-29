package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/servarr"
	"github.com/media-manager/media-manager/internal/workflows"
)

const testServarrKey = "servarr-test-key-0123456789abcdef"

// TestServarrCompatibilityAPI exercises the Radarr/Sonarr surface Seerr uses
// when Media Manager replaces the *arr applications.
func TestServarrCompatibilityAPI(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", ServarrAPIKey: testServarrKey}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	call := func(method, path string, body any, key string) (int, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			reader = bytes.NewReader(encoded)
		}
		request, requestErr := http.NewRequest(method, testServer.URL+path, reader)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			request.Header.Set("X-Api-Key", key)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		payload, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response.StatusCode, payload
	}

	if status, _ := call(http.MethodGet, "/radarr/api/v3/system/status", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("missing key status=%d, want 401", status)
	}
	if status, _ := call(http.MethodGet, "/radarr/api/v3/system/status?apikey=wrong-key", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("wrong key status=%d, want 401", status)
	}

	plex, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	movieRoot, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/media/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/media/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	policy, _ := json.Marshal(map[string]any{"rules": rules})
	movieProfile, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie HD", Policy: policy, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: catalog.KindSeries, Name: "TV HD", Policy: policy, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	status, payload := call(http.MethodGet, "/radarr/api/v3/system/status?apikey="+testServarrKey, nil, "")
	if status != http.StatusOK {
		t.Fatalf("radarr status=%d body=%s", status, payload)
	}
	var radarrStatus servarr.SystemStatus
	if err := json.Unmarshal(payload, &radarrStatus); err != nil {
		t.Fatal(err)
	}
	if radarrStatus.AppName != "Radarr" || !strings.HasPrefix(radarrStatus.Version, "5.") {
		t.Fatalf("radarr status=%+v", radarrStatus)
	}
	if status, payload = call(http.MethodGet, "/sonarr/api/v3/system/status?apikey="+testServarrKey, nil, ""); status != http.StatusOK {
		t.Fatalf("sonarr status=%d body=%s", status, payload)
	}
	var sonarrStatus servarr.SystemStatus
	if err := json.Unmarshal(payload, &sonarrStatus); err != nil {
		t.Fatal(err)
	}
	if sonarrStatus.AppName != "Sonarr" || !strings.HasPrefix(sonarrStatus.Version, "4.") {
		t.Fatalf("sonarr status=%+v", sonarrStatus)
	}

	status, payload = call(http.MethodGet, "/radarr/api/v3/qualityProfile?apikey="+testServarrKey, nil, "")
	if status != http.StatusOK {
		t.Fatalf("profiles status=%d body=%s", status, payload)
	}
	var profiles []servarr.QualityProfile
	if err := json.Unmarshal(payload, &profiles); err != nil {
		t.Fatal(err)
	}
	wantProfileID := servarr.SyntheticID("profile", movieProfile.ID.String())
	var addedProfile servarr.QualityProfile
	for _, profile := range profiles {
		if profile.ID == wantProfileID {
			addedProfile = profile
		}
	}
	if addedProfile.ID == 0 || addedProfile.Name != "Movie HD" {
		t.Fatalf("profiles=%+v, missing profile %d", profiles, wantProfileID)
	}

	status, payload = call(http.MethodGet, "/radarr/api/v3/rootfolder?apikey="+testServarrKey, nil, "")
	if status != http.StatusOK {
		t.Fatalf("rootfolder status=%d body=%s", status, payload)
	}
	var roots []servarr.RootFolder
	if err := json.Unmarshal(payload, &roots); err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].ID != servarr.SyntheticID("root", "auto") || roots[0].Path != servarr.AutoRootPath || !roots[0].Accessible {
		t.Fatalf("roots=%+v, want the single Auto root", roots)
	}

	status, payload = call(http.MethodGet, "/radarr/api/v3/movie/lookup?apikey="+testServarrKey+"&term=tmdb:555001", nil, "")
	if status != http.StatusOK {
		t.Fatalf("lookup status=%d body=%s", status, payload)
	}
	var lookup []servarr.Movie
	if err := json.Unmarshal(payload, &lookup); err != nil {
		t.Fatal(err)
	}
	if len(lookup) != 1 || lookup[0].ID != 0 || lookup[0].TMDBID != 555001 {
		t.Fatalf("lookup=%+v", lookup)
	}

	status, payload = call(http.MethodPost, "/radarr/api/v3/movie?apikey="+testServarrKey, map[string]any{
		"title": "Servarr Added Movie", "qualityProfileId": addedProfile.ID, "profileId": addedProfile.ID,
		"titleSlug": "555001", "minimumAvailability": "released", "tmdbId": 555001, "year": 2026,
		"rootFolderPath": "/libraries/movies", "monitored": true, "tags": []any{},
		"addOptions": map[string]any{"searchForMovie": true},
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("add movie status=%d body=%s", status, payload)
	}
	var added servarr.Movie
	if err := json.Unmarshal(payload, &added); err != nil {
		t.Fatal(err)
	}
	if added.ID != servarr.SyntheticID("movie", "555001") || !added.Monitored {
		t.Fatalf("added movie=%+v", added)
	}

	year := uint16(2026)
	item, err := catalog.New(catalog.KindMovie, "Servarr Catalog Movie", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "555002", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	workflow := workflows.New(item.ID, movieRoot.ID, movieProfile.ID, time.Now().UTC())
	if err := store.CreateMediaWorkflow(ctx, item, workflow, "test"); err != nil {
		t.Fatal(err)
	}
	status, payload = call(http.MethodGet, "/radarr/api/v3/movie?apikey="+testServarrKey+"&tmdbId=555002", nil, "")
	if status != http.StatusOK {
		t.Fatalf("library movie status=%d body=%s", status, payload)
	}
	var library []servarr.Movie
	if err := json.Unmarshal(payload, &library); err != nil {
		t.Fatal(err)
	}
	if len(library) != 1 || library[0].TMDBID != 555002 || library[0].HasFile {
		t.Fatalf("library movie=%+v", library)
	}

	status, payload = call(http.MethodGet, "/sonarr/api/v3/languageprofile?apikey="+testServarrKey, nil, "")
	if status != http.StatusOK || !strings.Contains(string(payload), "English") {
		t.Fatalf("language profiles status=%d body=%s", status, payload)
	}
	status, payload = call(http.MethodPost, "/sonarr/api/v3/series?apikey="+testServarrKey, map[string]any{
		"title": "Servarr Added Series", "qualityProfileId": 1, "profileId": 0, "tvdbId": 555003,
		"rootFolderPath": "/libraries/tv", "seasons": []any{1}, "seasonFolder": true,
		"seriesType": "standard", "monitored": true, "searchNow": true, "tags": []any{},
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("add series status=%d body=%s", status, payload)
	}
	var addedSeries servarr.Series
	if err := json.Unmarshal(payload, &addedSeries); err != nil {
		t.Fatal(err)
	}
	if addedSeries.ID != servarr.SyntheticID("series", "555003") || !addedSeries.Monitored {
		t.Fatalf("added series=%+v", addedSeries)
	}
	status, payload = call(http.MethodPost, "/sonarr/api/v3/command?apikey="+testServarrKey, map[string]any{
		"name": "SeriesSearch", "seriesId": 555003, "seasonNumber": 1,
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("series search command status=%d body=%s", status, payload)
	}
}

// TestDecodeServarrJSONAcceptsRealClientPayloads pins the exact bodies Seerr
// sends. Radarr/Sonarr clients include fields Media Manager does not consume;
// strict decoding rejected them and Seerr marked the request FAILED.
func TestDecodeServarrJSONAcceptsRealClientPayloads(t *testing.T) {
	t.Parallel()
	radarrBody := `{"title":"Minions & Monsters","qualityProfileId":100399145,"profileId":100399145,"titleSlug":"1315772","minimumAvailability":"released","tmdbId":1315772,"year":2026,"rootFolderPath":"/srv/media/Børn Film","monitored":true,"tags":[],"addOptions":{"searchForMovie":true}}`
	var movie struct {
		TMDBID int64  `json:"tmdbId"`
		Title  string `json:"title"`
	}
	if err := decodeServarrJSON(httptest.NewRequest(http.MethodPost, "/movie", strings.NewReader(radarrBody)), &movie, 256<<10); err != nil || movie.TMDBID != 1315772 || movie.Title == "" {
		t.Fatalf("radarr payload movie=%+v err=%v", movie, err)
	}
	sonarrBody := `{"title":"Lanterns","profileId":543223483,"tvdbId":376098,"rootFolderPath":"/libraries/tv","seasons":[1],"seasonFolder":true,"seriesType":"standard","monitored":true,"searchNow":true,"tags":[],"qualityProfileId":543223483}`
	var series struct {
		TVDBID int64  `json:"tvdbId"`
		Title  string `json:"title"`
	}
	if err := decodeServarrJSON(httptest.NewRequest(http.MethodPost, "/series", strings.NewReader(sonarrBody)), &series, 256<<10); err != nil || series.TVDBID != 376098 || series.Title == "" {
		t.Fatalf("sonarr payload series=%+v err=%v", series, err)
	}
	commandBody := `{"name":"MoviesSearch","movieIds":[123]}`
	var command struct {
		Name string `json:"name"`
	}
	if err := decodeServarrJSON(httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(commandBody)), &command, 64<<10); err != nil || command.Name != "MoviesSearch" {
		t.Fatalf("command payload command=%+v err=%v", command, err)
	}
	if err := decodeServarrJSON(httptest.NewRequest(http.MethodPost, "/movie", strings.NewReader(`{`)), &movie, 256<<10); err == nil {
		t.Fatal("malformed body was accepted")
	}
	if err := decodeServarrJSON(httptest.NewRequest(http.MethodPost, "/movie", strings.NewReader(`{} {}`)), &movie, 256<<10); err == nil {
		t.Fatal("multi-document body was accepted")
	}
}
