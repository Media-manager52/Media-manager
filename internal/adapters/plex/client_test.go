package plex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfirmationRequiresPathAndIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Plex-Token") != "secret" {
			t.Fatal("token missing")
		}
		_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="7" title="Movie"><Guid id="tmdb://42"/><Media><Part file="/library/movies/Movie/Movie.mkv"/></Media></Video></MediaContainer>`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Movie", PlexVisiblePath: "/library/movies/Movie/Movie.mkv", ProviderGUIDs: []string{"tmdb://42"}})
	if err != nil || confirmation.RatingKey != "7" {
		t.Fatalf("unexpected confirmation: %#v %v", confirmation, err)
	}
	if _, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Movie", PlexVisiblePath: "/wrong.mkv", ProviderGUIDs: []string{"tmdb://42"}}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("wrong path confirmed: %v", err)
	}
}

func TestConfirmationLoadsExactItemIdentityWhenSectionOmitsGuids(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/1/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="7" title="Movie" guid="plex://movie/internal"><Media><Part file="/library/movies/Movie/Movie.mkv"/></Media></Video></MediaContainer>`))
		case "/library/metadata/7":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="7" title="Movie" guid="plex://movie/internal"><Guid id="imdb://tt0000042"/><Guid id="tmdb://42"/></Video></MediaContainer>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Movie", PlexVisiblePath: "/library/movies/Movie/Movie.mkv", ProviderGUIDs: []string{"tmdb://42"}})
	if err != nil || confirmation.RatingKey != "7" || confirmation.ProviderGUID != "tmdb://42" {
		t.Fatalf("unexpected confirmation: %#v %v", confirmation, err)
	}
}

func TestConfirmSeriesRequiresEveryEpisodeLeafForMultiEpisodeFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/library/metadata/10/allLeaves" {
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="101" parentIndex="1" index="1"><Media><Part file="/tv/Show/Season 01/Show - S01E01-E02.mkv"/></Media></Video></MediaContainer>`))
			return
		}
		_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="10" title="Show"><Guid id="tvdb://123"/></Directory></MediaContainer>`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	path := "/tv/Show/Season 01/Show - S01E01-E02.mkv"
	if _, err := client.ConfirmSeries(context.Background(), ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{path}, ExpectedEpisodes: map[string][]EpisodeLeaf{path: {{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}}}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("one Plex leaf confirmed two expected episodes: %v", err)
	}
}

func TestConfirmSeriesRequiresEveryExpectedPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/4/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99" title="Show"><Guid id="tvdb://123"/></Directory></MediaContainer>`))
		case "/library/metadata/99/allLeaves":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="101" parentIndex="1" index="1"><Media><Part file="/tv/Show/Season 01/Show - S01E01.mkv"/></Media></Video><Video ratingKey="102" parentIndex="1" index="2"><Media><Part file="/tv/Show/Season 01/Show - S01E02.mkv"/></Media></Video></MediaContainer>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	confirmations, err := client.ConfirmSeries(context.Background(), ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{"/tv/Show/Season 01/Show - S01E01.mkv", "/tv/Show/Season 01/Show - S01E02.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmations) != 2 {
		t.Fatalf("expected 2 confirmations, got %d", len(confirmations))
	}
}

func TestConfirmSeriesLoadsProviderIdentityFromShowDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/4/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99" title="Show" guid="plex://show/internal"/></MediaContainer>`))
		case "/library/metadata/99":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99" title="Show" guid="plex://show/internal"><Guid id="tmdb://42"/><Guid id="tvdb://123"/></Directory></MediaContainer>`))
		case "/library/metadata/99/allLeaves":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="101" parentIndex="1" index="1"><Media><Part file="/tv/Show/Season 01/Show - S01E01.mkv"/></Media></Video></MediaContainer>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	path := "/tv/Show/Season 01/Show - S01E01.mkv"
	confirmations, err := client.ConfirmSeries(context.Background(), ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{path}, ExpectedEpisodes: map[string][]EpisodeLeaf{path: {{Season: 1, Episode: 1}}}})
	if err != nil || len(confirmations) != 1 || confirmations[0].ProviderGUID != "tvdb://123" || confirmations[0].Path != path {
		t.Fatalf("confirmations=%+v err=%v", confirmations, err)
	}
}

// TestConfirmationRequiresMatchingProviderIdentity proves a correct path with a
// different provider GUID is never accepted as confirmation, even when Plex's
// summary item omits the expected GUID and requires an exact detail lookup.
func TestConfirmationRequiresMatchingProviderIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/1/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="7" title="Movie" guid="plex://movie/internal"><Media><Part file="/library/movies/Movie/Movie.mkv"/></Media></Video></MediaContainer>`))
		case "/library/metadata/7":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="7" guid="plex://movie/internal"><Guid id="imdb://tt0000042"/></Video></MediaContainer>`))
		case "/library/sections":
			_, _ = writer.Write([]byte(`<MediaContainer/>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Movie", PlexVisiblePath: "/library/movies/Movie/Movie.mkv", ProviderGUIDs: []string{"tmdb://42"}}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("wrong provider identity error=%v, want ErrNotConfirmed", err)
	}
}

// TestConfirmSeriesRejectsWrongShowIdentity proves episode paths alone never
// confirm a series whose provider identity is different.
func TestConfirmSeriesRejectsWrongShowIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/4/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99" title="Show"><Guid id="tvdb://999"/></Directory></MediaContainer>`))
		case "/library/metadata/99":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99"><Guid id="tvdb://999"/></Directory></MediaContainer>`))
		case "/library/sections":
			_, _ = writer.Write([]byte(`<MediaContainer/>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	path := "/tv/Show/Season 01/Show - S01E01.mkv"
	if _, err := client.ConfirmSeries(context.Background(), ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{path}, ExpectedEpisodes: map[string][]EpisodeLeaf{path: {{Season: 1, Episode: 1}}}}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("wrong show identity error=%v, want ErrNotConfirmed", err)
	}
}

// TestConfirmSeriesRejectsMissingExpectedPath proves a partial season pack is
// never confirmed: every expected path must appear exactly.
func TestConfirmSeriesRejectsMissingExpectedPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/4/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99" title="Show"><Guid id="tvdb://123"/></Directory></MediaContainer>`))
		case "/library/metadata/99/allLeaves":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="101" parentIndex="1" index="1"><Media><Part file="/tv/Show/Season 01/Show - S01E01.mkv"/></Media></Video></MediaContainer>`))
		case "/library/sections":
			_, _ = writer.Write([]byte(`<MediaContainer/>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	first := "/tv/Show/Season 01/Show - S01E01.mkv"
	second := "/tv/Show/Season 01/Show - S01E02.mkv"
	expectations := map[string][]EpisodeLeaf{first: {{Season: 1, Episode: 1}}, second: {{Season: 1, Episode: 2}}}
	if _, err := client.ConfirmSeries(context.Background(), ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{first, second}, ExpectedEpisodes: expectations}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("partial season pack error=%v, want ErrNotConfirmed", err)
	}
}

// TestConfirmSeriesAcceptsMultiEpisodeFileWithEveryLeaf proves a Plex
// multi-episode file satisfies a pack asset only when Plex exposes one leaf for
// each covered episode against that exact path.
func TestConfirmSeriesAcceptsMultiEpisodeFileWithEveryLeaf(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/library/sections/4/all":
			_, _ = writer.Write([]byte(`<MediaContainer><Directory ratingKey="99" title="Show"><Guid id="tvdb://123"/></Directory></MediaContainer>`))
		case "/library/metadata/99/allLeaves":
			_, _ = writer.Write([]byte(`<MediaContainer><Video ratingKey="101" parentIndex="1" index="1"><Media><Part file="/tv/Show/Season 01/Show - S01E01-E02.mkv"/></Media></Video><Video ratingKey="102" parentIndex="1" index="2"><Media><Part file="/tv/Show/Season 01/Show - S01E01-E02.mkv"/></Media></Video></MediaContainer>`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	path := "/tv/Show/Season 01/Show - S01E01-E02.mkv"
	confirmations, err := client.ConfirmSeries(context.Background(), ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{path}, ExpectedEpisodes: map[string][]EpisodeLeaf{path: {{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}}})
	if err != nil || len(confirmations) != 1 || confirmations[0].Path != path || confirmations[0].ProviderGUID != "tvdb://123" {
		t.Fatalf("multi-episode confirmation=%+v err=%v", confirmations, err)
	}
}

// TestRefreshValidatesInputs proves the Plex client never builds a request for
// a malformed library key or a path that is not absolute.
func TestRefreshValidatesInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatalf("unexpected request to %s", request.URL.Path)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		libraryKey string
		path       string
	}{{"abc", "/library/movies"}, {"1", "relative/movies"}, {"0", "/library/movies"}} {
		if err := client.Refresh(context.Background(), expected.libraryKey, expected.path); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Refresh(%q, %q) error=%v, want ErrInvalidConfig", expected.libraryKey, expected.path, err)
		}
	}
}

// localizedLibrary serves a Plex library whose metadata titles are localized, so
// the title filter finds nothing and only the folder tree reveals the exact path.
func localizedLibrary(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		key := request.URL.Path
		if request.URL.RawQuery != "" {
			key += "?" + request.URL.RawQuery
		}
		if body, ok := routes[key]; ok {
			_, _ = writer.Write([]byte(body))
			return
		}
		if request.URL.Path == "/library/sections/1/all" || request.URL.Path == "/library/sections/4/all" {
			_, _ = writer.Write([]byte(`<MediaContainer size="0"/>`))
			return
		}
		t.Fatalf("unexpected request %s", key)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

const localizedMoviePath = "/media/Danske Film/Unraveled (2026) {tmdb-1477664}/Unraveled (2026) {tmdb-1477664} - [1080p].mkv"

func localizedMovieRoutes(detailGUIDs string) map[string]string {
	return map[string]string{
		"/library/sections":                      `<MediaContainer><Directory key="1" type="movie" title="Danske Film"><Location path="/media/Danske Film"/></Directory><Directory key="2" type="movie" title="Other"><Location path="/media/Other"/></Directory></MediaContainer>`,
		"/library/sections/1/folder":             `<MediaContainer><Directory key="/library/sections/1/folder?parent=7048" title="Other (2020)"/><Directory key="/library/sections/1/folder?parent=7049" title="Unraveled (2026) {tmdb-1477664}"/></MediaContainer>`,
		"/library/sections/1/folder?parent=7049": `<MediaContainer><Video ratingKey="179744" title="Vores L&#248;fte" guid="plex://movie/internal"><Media><Part file="` + localizedMoviePath + `"/></Media></Video></MediaContainer>`,
		"/library/metadata/179744":               `<MediaContainer><Video ratingKey="179744" guid="plex://movie/internal">` + detailGUIDs + `</Video></MediaContainer>`,
	}
}

// TestConfirmationFindsLocalizedTitleByExactPath proves a movie whose Plex
// title is localized is still confirmed by exact path plus provider identity.
func TestConfirmationFindsLocalizedTitleByExactPath(t *testing.T) {
	client := localizedLibrary(t, localizedMovieRoutes(`<Guid id="imdb://tt39335022"/><Guid id="tmdb://1477664"/>`))
	confirmation, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Unraveled", PlexVisiblePath: localizedMoviePath, ProviderGUIDs: []string{"tmdb://1477664"}})
	if err != nil || confirmation.RatingKey != "179744" || confirmation.ProviderGUID != "tmdb://1477664" || confirmation.Path != localizedMoviePath {
		t.Fatalf("confirmation=%+v err=%v", confirmation, err)
	}
}

// TestConfirmationByPathStillRequiresProviderIdentity proves the folder lookup
// never turns a path match into confirmation without the expected identity.
func TestConfirmationByPathStillRequiresProviderIdentity(t *testing.T) {
	client := localizedLibrary(t, localizedMovieRoutes(`<Guid id="tmdb://999"/>`))
	if _, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Unraveled", PlexVisiblePath: localizedMoviePath, ProviderGUIDs: []string{"tmdb://1477664"}}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("wrong identity error=%v, want ErrNotConfirmed", err)
	}
}

// TestConfirmationByPathIgnoresForeignFolderKeys proves the walk follows only
// this section's own folder keys, never another section or an absolute URL.
func TestConfirmationByPathIgnoresForeignFolderKeys(t *testing.T) {
	routes := localizedMovieRoutes(`<Guid id="tmdb://1477664"/>`)
	routes["/library/sections/1/folder"] = `<MediaContainer><Directory key="http://attacker.invalid/library/sections/1/folder?parent=7049" title="Unraveled (2026) {tmdb-1477664}"/><Directory key="/library/sections/2/folder?parent=7049" title="Unraveled (2026) {tmdb-1477664}"/><Directory key="/library/sections/1/folder?parent=7049&amp;extra=1" title="Unraveled (2026) {tmdb-1477664}"/></MediaContainer>`
	client := localizedLibrary(t, routes)
	if _, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Unraveled", PlexVisiblePath: localizedMoviePath, ProviderGUIDs: []string{"tmdb://1477664"}}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("foreign folder key error=%v, want ErrNotConfirmed", err)
	}
}

// TestConfirmationByPathRequiresContainingLocation proves a path outside every
// section location (including a sibling with a shared name prefix) makes no
// folder request at all.
func TestConfirmationByPathRequiresContainingLocation(t *testing.T) {
	routes := localizedMovieRoutes(`<Guid id="tmdb://1477664"/>`)
	delete(routes, "/library/sections/1/folder")
	client := localizedLibrary(t, routes)
	for _, path := range []string{"/media/Danske Film 2/Unraveled/Unraveled.mkv", "/media/Other/Unraveled/Unraveled.mkv", "/media/Danske Film"} {
		if _, err := client.Confirm(context.Background(), Expected{LibraryKey: "1", Title: "Unraveled", PlexVisiblePath: path, ProviderGUIDs: []string{"tmdb://1477664"}}); !errors.Is(err, ErrNotConfirmed) {
			t.Fatalf("path %q error=%v, want ErrNotConfirmed", path, err)
		}
	}
}

// TestConfirmSeriesFindsLocalizedShowByEpisodePath proves a series whose Plex
// title is localized is confirmed through the episode's owning show, with the
// same show identity and every-path leaf checks.
func TestConfirmSeriesFindsLocalizedShowByEpisodePath(t *testing.T) {
	first := "/tv/Show (2006) {tvdb-123}/Season 01/Show - S01E01.mkv"
	second := "/tv/Show (2006) {tvdb-123}/Season 01/Show - S01E02.mkv"
	leaves := `<MediaContainer><Video ratingKey="101" grandparentRatingKey="99" parentIndex="1" index="1"><Media><Part file="` + first + `"/></Media></Video><Video ratingKey="102" grandparentRatingKey="99" parentIndex="1" index="2"><Media><Part file="` + second + `"/></Media></Video></MediaContainer>`
	routes := map[string]string{
		"/library/sections":                    `<MediaContainer><Directory key="4" type="show" title="TV"><Location path="/other"/><Location path="/tv"/></Directory></MediaContainer>`,
		"/library/sections/4/folder":           `<MediaContainer><Directory key="/library/sections/4/folder?parent=10" title="Show (2006) {tvdb-123}"/></MediaContainer>`,
		"/library/sections/4/folder?parent=10": `<MediaContainer><Directory key="/library/sections/4/folder?parent=11" title="Season 01"/></MediaContainer>`,
		"/library/sections/4/folder?parent=11": leaves,
		"/library/metadata/99":                 `<MediaContainer><Directory ratingKey="99" guid="plex://show/internal"><Guid id="tvdb://123"/></Directory></MediaContainer>`,
		"/library/metadata/99/allLeaves":       leaves,
	}
	client := localizedLibrary(t, routes)
	expected := ExpectedSeries{LibraryKey: "4", Title: "Show", ProviderGUIDs: []string{"tvdb://123"}, PlexVisiblePaths: []string{first, second}, ExpectedEpisodes: map[string][]EpisodeLeaf{first: {{Season: 1, Episode: 1}}, second: {{Season: 1, Episode: 2}}}}
	confirmations, err := client.ConfirmSeries(context.Background(), expected)
	if err != nil || len(confirmations) != 2 || confirmations[0].ProviderGUID != "tvdb://123" {
		t.Fatalf("confirmations=%+v err=%v", confirmations, err)
	}
	routes["/library/metadata/99"] = `<MediaContainer><Directory ratingKey="99"><Guid id="tvdb://999"/></Directory></MediaContainer>`
	if _, err := client.ConfirmSeries(context.Background(), expected); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("wrong show identity error=%v, want ErrNotConfirmed", err)
	}
}

func TestRelativeComponentsFailsClosed(t *testing.T) {
	for _, test := range []struct {
		root, path string
		ok         bool
	}{
		{"/media/Comedy", "/media/Comedy/Movie (1995)/Movie.mkv", true},
		{"/media/Comedy/", "/media/Comedy/Movie.mkv", true},
		{"/media/Comedy", "/media/Comedy", false},
		{"/media/Comedy", "/media/Comedy2/Movie.mkv", false},
		{"/media/Comedy", "/media/Comedy/../Horror/Movie.mkv", false},
		{"media/Comedy", "/media/Comedy/Movie.mkv", false},
		{"/media", "/media/a/b/c/d/e/f/g/h/i.mkv", false},
	} {
		if _, ok := relativeComponents(test.root, test.path); ok != test.ok {
			t.Fatalf("relativeComponents(%q, %q) ok=%v, want %v", test.root, test.path, ok, test.ok)
		}
	}
}
