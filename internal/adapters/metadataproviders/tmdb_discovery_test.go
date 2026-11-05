package metadataproviders

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func fakeTMDB(t *testing.T, token string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"success":false,"status_code":7}`))
			return
		}
		if request.URL.Path != "/3/authentication" && request.URL.Query().Get("language") != "en-US" {
			t.Errorf("%s asked for language %q, want en-US like Seerr", request.URL.Path, request.URL.Query().Get("language"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/3/authentication":
			_, _ = writer.Write([]byte(`{"success":true}`))
		case "/3/search/multi":
			if request.URL.Query().Get("query") != "Borgen" || request.URL.Query().Get("include_adult") != "false" {
				t.Errorf("unexpected search query %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"page":1,"total_pages":1,"total_results":2,"results":[{"id":1234,"media_type":"tv","name":"Borgen","first_air_date":"2010-09-26","overview":"Danish politics","poster_path":"/b.jpg"},{"id":99,"media_type":"person","name":"Someone"}]}`))
		case "/3/tv/1234":
			if request.URL.Query().Get("append_to_response") != "external_ids" {
				t.Errorf("series lookup must append external_ids, got %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"id":1234,"name":"Borgen","original_name":"Borgen","original_language":"da","first_air_date":"2010-09-26","status":"Ended","episode_run_time":[58,0],"origin_country":["DK"],"networks":[{"id":119,"name":"DR1","origin_country":"DK"}],"genres":[{"id":18,"name":"Drama"}],"seasons":[{"season_number":0},{"season_number":1},{"season_number":2}],"external_ids":{"imdb_id":"tt1526318","tvdb_id":94551}}`))
		case "/3/movie/603":
			_, _ = writer.Write([]byte(`{"id":603,"title":"The Matrix","original_title":"The Matrix","release_date":"1999-03-31","runtime":136,"imdb_id":"tt0133093","external_ids":{"imdb_id":"","tvdb_id":null},"production_countries":[{"iso_3166_1":"US","name":"United States of America"}]}`))
		case "/3/tv/1234/season/1":
			_, _ = writer.Write([]byte(`{"id":5555,"season_number":1,"episodes":[{"id":71,"episode_number":1,"name":"Decency in the Middle","air_date":"2010-09-26"},{"id":72,"episode_number":2,"name":"Count to 90","air_date":"2010-10-03"}]}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testTMDB(t *testing.T, server *httptest.Server, token string) *TMDBClient {
	t.Helper()
	client, err := NewTMDB(TMDBConfig{AccessToken: token, BaseURL: server.URL, AllowHTTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestTMDBDiscoveryAnswersLikeSeerr(t *testing.T) {
	server := fakeTMDB(t, "read-token")
	client := testTMDB(t, server, "read-token")
	ctx := context.Background()

	if err := client.Check(ctx); err != nil {
		t.Fatalf("Check() = %v", err)
	}
	page, err := client.Search(ctx, "Borgen", 1)
	if err != nil || len(page.Results) != 2 || page.Results[0].ID != 1234 || page.Results[0].MediaType != "tv" || page.Results[0].Name != "Borgen" || page.Results[0].PosterPath != "/b.jpg" {
		t.Fatalf("Search() = %+v, %v", page, err)
	}

	series, err := client.Metadata(ctx, "tv", 1234)
	if err != nil {
		t.Fatal(err)
	}
	if series.Name != "Borgen" || series.OriginalLanguage != "da" || series.FirstAirDate != "2010-09-26" || len(series.EpisodeRunTime) != 1 || series.EpisodeRunTime[0] != 58 {
		t.Fatalf("series fields = %+v", series)
	}
	if series.ExternalIDs.IMDbID != "tt1526318" || series.ExternalIDs.TVDBID == nil || *series.ExternalIDs.TVDBID != 94551 {
		t.Fatalf("series identities = %+v", series.ExternalIDs)
	}
	if len(series.Networks) != 1 || series.Networks[0].ID != 119 || series.Networks[0].Name != "DR1" || series.Networks[0].OriginCountry != "DK" {
		t.Fatalf("series networks = %+v", series.Networks)
	}
	if len(series.Seasons) != 3 || series.Seasons[2].SeasonNumber != 2 || len(series.Genres) != 1 {
		t.Fatalf("series seasons/genres = %+v %+v", series.Seasons, series.Genres)
	}

	movie, err := client.Metadata(ctx, "movie", 603)
	if err != nil {
		t.Fatal(err)
	}
	if movie.Title != "The Matrix" || movie.Runtime == nil || *movie.Runtime != 136 || movie.ExternalIDs.IMDbID != "tt0133093" || movie.ExternalIDs.TVDBID != nil {
		t.Fatalf("movie = %+v", movie)
	}

	season, err := client.SeasonMetadata(ctx, 1234, 1)
	if err != nil || season.SeasonNumber != 1 || len(season.Episodes) != 2 || season.Episodes[1].ID != 72 || season.Episodes[1].EpisodeNumber != 2 || season.Episodes[1].AirDate != "2010-10-03" {
		t.Fatalf("SeasonMetadata() = %+v, %v", season, err)
	}
}

func TestTMDBDiscoveryRefusesAWrongTokenAndAMismatchedID(t *testing.T) {
	server := fakeTMDB(t, "read-token")
	if err := testTMDB(t, server, "wrong").Check(context.Background()); err == nil {
		t.Fatal("a refused token passed Check")
	}
	if _, err := testTMDB(t, server, "read-token").Metadata(context.Background(), "tv", 4321); err == nil {
		t.Fatal("a missing title was returned")
	}
}

func TestTMDBSourcePicksUpAWebsiteTokenWithoutRestart(t *testing.T) {
	server := fakeTMDB(t, "website-token")
	var token atomic.Value
	token.Store("")
	var reads atomic.Int32
	source := &TMDBSource{BaseURL: server.URL, AllowHTTP: true, Resolve: func(context.Context) (string, error) {
		reads.Add(1)
		return token.Load().(string), nil
	}}
	ctx := context.Background()
	if source.Configured(ctx) {
		t.Fatal("configured without any token")
	}
	if _, err := source.Search(ctx, "Borgen", 1); !errors.Is(err, ErrTMDBNotConfigured) {
		t.Fatalf("Search() without token = %v, want ErrTMDBNotConfigured", err)
	}
	token.Store("website-token")
	if source.Configured(ctx) {
		t.Fatal("the cached answer should hold until the refresh interval or Invalidate")
	}
	source.Invalidate()
	page, err := source.Search(ctx, "Borgen", 1)
	if err != nil || len(page.Results) == 0 {
		t.Fatalf("Search() after saving the token = %+v, %v", page, err)
	}
	before := reads.Load()
	for range 5 {
		_ = source.Configured(ctx)
	}
	if reads.Load() != before {
		t.Fatalf("the token was re-read %d times within the refresh interval", reads.Load()-before)
	}
	token.Store("")
	source.Invalidate()
	if source.Configured(ctx) {
		t.Fatal("still configured after the connection was removed")
	}
}

func TestTMDBSourceEnvironmentTokenWins(t *testing.T) {
	server := fakeTMDB(t, "env-token")
	source := &TMDBSource{EnvironmentToken: "env-token", BaseURL: server.URL, AllowHTTP: true, Resolve: func(context.Context) (string, error) {
		t.Fatal("the website token must not be read when TMDB_ACCESS_TOKEN is set")
		return "", nil
	}}
	if _, err := source.Metadata(context.Background(), "tv", 1234); err != nil {
		t.Fatal(err)
	}
	if source.MetadataSourceLabel() != "tmdb_metadata" {
		t.Fatalf("label = %q", source.MetadataSourceLabel())
	}
}
