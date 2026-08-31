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
