package metadataproviders

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

func verified(namespace, value string) catalog.ExternalIdentity {
	return catalog.ExternalIdentity{Namespace: namespace, Value: value, State: catalog.IdentityVerified}
}

func TestTMDBLookupUsesExactIdentityAndRichBoundedDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/3/movie/42" || !strings.Contains(request.URL.Query().Get("append_to_response"), "credits") || request.Header.Get("Authorization") != "Bearer private-token" {
			t.Fatalf("unexpected request")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":42,"title":"Example","original_title":"Eksempel","overview":"Real overview","tagline":"Real tagline","original_language":"da","release_date":"2026-08-31","runtime":104,"poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":8.2,"vote_count":321,"genres":[{"id":18,"name":"Drama"}],"production_countries":[{"iso_3166_1":"DK","name":"Denmark"}],"production_companies":[{"name":"Nordisk Film","origin_country":"DK"}],"external_ids":{"imdb_id":"tt1234567","tvdb_id":77},"credits":{"cast":[{"name":"Actor","character":"Lead","profile_path":"/actor.jpg"}],"crew":[{"name":"Director","job":"Director","profile_path":"/director.jpg"}]},"keywords":{"keywords":[{"name":"copenhagen"}]},"release_dates":{"results":[{"iso_3166_1":"DK","release_dates":[{"certification":"15","type":3}]}]}}`))
	}))
	defer server.Close()
	client, err := NewTMDB(TMDBConfig{AccessToken: "private-token", BaseURL: server.URL, AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Lookup(context.Background(), catalog.KindMovie, []catalog.ExternalIdentity{verified("tmdb", "42")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Tagline != "Real tagline" || result.Metadata.Certification != "15" || len(result.Metadata.Credits) != 2 || len(result.Metadata.Companies) != 1 || len(result.Metadata.Keywords) != 1 || len(result.Identities) != 3 {
		t.Fatalf("incomplete TMDB result: %+v", result)
	}
}

func TestTVDBLookupLogsInOnceAndRejectsIdentityDrift(t *testing.T) {
	loginCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v4/login":
			loginCalls++
			_, _ = writer.Write([]byte(`{"data":{"token":"bounded-token"}}`))
		case "/v4/series/77/extended":
			if request.Header.Get("Authorization") != "Bearer bounded-token" || request.URL.Query().Get("meta") != "translations" {
				t.Fatal("authorization or bounded query missing")
			}
			_, _ = writer.Write([]byte(`{"data":{"id":77,"name":"Series","slug":"series","overview":"TVDB overview","image":"/poster.jpg","originalCountry":"dk","originalLanguage":"dan","firstAired":"2026-01-02","averageRuntime":45,"score":7.4,"genres":[{"name":"Drama"}],"aliases":[{"name":"Serien","language":"da"}],"originalNetwork":{"name":"DR","country":"DK"},"companies":[{"name":"Studio","country":"DK","type":"Production"}],"remoteIds":[{"sourceName":"TheMovieDB.com","id":42},{"sourceName":"IMDB","id":"tt1234567"}]}}`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewTVDB(TVDBConfig{APIKey: "private-key", BaseURL: server.URL, AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	identities := []catalog.ExternalIdentity{verified("tvdb", "77"), verified("tmdb", "42")}
	result, err := client.Lookup(context.Background(), catalog.KindSeries, identities, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if loginCalls != 1 || result.Metadata.Overview != "TVDB overview" || result.Metadata.PosterURL != "https://artworks.thetvdb.com/poster.jpg" || len(result.Metadata.Networks) != 1 {
		t.Fatalf("unexpected TVDB result: %+v logins=%d", result, loginCalls)
	}
	_, err = client.Lookup(context.Background(), catalog.KindSeries, []catalog.ExternalIdentity{verified("tvdb", "77"), verified("tmdb", "999")}, time.Now())
	if !errors.Is(err, ErrIdentityConflict) || loginCalls != 1 {
		t.Fatalf("identity drift accepted or token not cached: %v logins=%d", err, loginCalls)
	}
}

func TestSimklManualResolutionPreservesProvenanceAndDoesNotLeakClientID(t *testing.T) {
	const secret = "private-client-id"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			if request.URL.Query().Get("tmdb") != "42" || request.URL.Query().Get("type") != "movie" || request.URL.Query().Get("client_id") != secret {
				t.Fatal("exact redirect resolution missing")
			}
			writer.Header().Set("Location", "//simkl.com/movies/99/example?client_id=must-not-persist&ticket=discarded")
			writer.WriteHeader(http.StatusMovedPermanently)
		case "/movies/99":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"title":"Example","year":2026,"type":"movie","runtime":104,"certification":"15","first_aired":"2026-08-31T00:00:00Z","ids":{"simkl":99,"tmdb":42,"imdb":"tt1234567"},"ratings":{"simkl":{"rating":8.0,"votes":50},"imdb":{"rating":7.7,"votes":500}}}`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewSimkl(SimklConfig{ClientID: secret, BaseURL: server.URL, AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Lookup(context.Background(), catalog.KindMovie, []catalog.ExternalIdentity{verified("tmdb", "42")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Metadata.Ratings) != 2 || result.Metadata.Ratings[1].Source != "imdb" || !strings.Contains(result.Metadata.Ratings[1].Provenance, "not a direct IMDb") || result.Metadata.Sources[0].URL != "https://simkl.com/movies/99/example" {
		t.Fatalf("provenance missing: %+v", result)
	}

	failing, _ := NewSimkl(SimklConfig{ClientID: secret, BaseURL: "https://api.simkl.com"}, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport included " + secret) }))
	_, err = failing.Lookup(context.Background(), catalog.KindMovie, []catalog.ExternalIdentity{verified("tmdb", "42")}, time.Now())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("credential leaked in error: %v", err)
	}
}

func TestProviderClientsRejectAutomaticRedirectsAndUnsafeConfiguration(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("redirect followed") }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	client, _ := NewTMDB(TMDBConfig{AccessToken: "token", BaseURL: redirector.URL, AllowHTTP: true}, redirector.Client().Transport)
	_, err := client.Lookup(context.Background(), catalog.KindMovie, []catalog.ExternalIdentity{verified("tmdb", "42")}, time.Now())
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("redirect was not rejected: %v", err)
	}
	if _, err := NewTVDB(TVDBConfig{APIKey: "key", BaseURL: "http://example.test"}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("plain HTTP accepted: %v", err)
	}
	unsafeSimkl := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Location", "//private.example/movies/99/example")
		writer.WriteHeader(http.StatusMovedPermanently)
	}))
	defer unsafeSimkl.Close()
	simkl, _ := NewSimkl(SimklConfig{ClientID: "client", BaseURL: unsafeSimkl.URL, AllowHTTP: true}, unsafeSimkl.Client().Transport)
	_, err = simkl.Lookup(context.Background(), catalog.KindMovie, []catalog.ExternalIdentity{verified("tmdb", "42")}, time.Now())
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("cross-origin Simkl redirect was accepted: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
