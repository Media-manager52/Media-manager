package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCreateSectionAndAddLocationSendPlexsOwnParameters(t *testing.T) {
	type call struct {
		Method, Path string
		Query        map[string][]string
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, call{request.Method, request.URL.Path, request.URL.Query()})
		writer.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Token: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CreateSection(context.Background(), "show", "Serier", "da-DK", "/libraries/tv"); err != nil {
		t.Fatal(err)
	}
	section := Section{Key: "4", Title: "Film", Agent: "tv.plex.agents.movie", Language: "da-DK", Locations: []Location{{Path: "/data/movies"}}}
	if err := client.AddLocation(context.Background(), section, "/libraries/movies"); err != nil {
		t.Fatal(err)
	}
	// Already there: nothing is sent.
	section.Locations = append(section.Locations, Location{Path: "/libraries/movies"})
	if err := client.AddLocation(context.Background(), section, "/libraries/movies"); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{"POST", "/library/sections", map[string][]string{"name": {"Serier"}, "type": {"show"}, "agent": {"tv.plex.agents.series"}, "scanner": {"Plex TV Series"}, "language": {"da-DK"}, "location": {"/libraries/tv"}}},
		{"PUT", "/library/sections/4", map[string][]string{"agent": {"tv.plex.agents.movie"}, "language": {"da-DK"}, "name": {"Film"}, "location": {"/data/movies", "/libraries/movies"}}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v", calls)
	}
	for _, bad := range []error{
		client.CreateSection(context.Background(), "artist", "Musik", "da-DK", "/music"),
		client.CreateSection(context.Background(), "movie", "Film", "da-DK", "relative"),
		client.AddLocation(context.Background(), Section{Key: "4", Title: "Film", Language: "da-DK", Locations: section.Locations}, "/x"),
		client.AddLocation(context.Background(), Section{Key: "4", Title: "Film", Agent: "a", Language: "da-DK"}, "/x"),
	} {
		if bad != ErrInvalidConfig {
			t.Fatalf("invalid input accepted: %v", bad)
		}
	}
}
