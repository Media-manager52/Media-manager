package mdblist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseItemsKeepsFilmsAndSeriesWithTheirNumbers(t *testing.T) {
	items, err := parseItems([]byte(`[
		{"id":76600,"rank":1,"adult":0,"title":"Avatar: The Way of Water","tvdbid":5483,"imdb_id":"tt1630029","mediatype":"movie","release_year":2022},
		{"id":1399,"rank":2,"title":"Game of Thrones","tvdbid":"121361","imdb_id":"TT0944947","mediatype":"show","release_year":"2011"},
		{"id":42,"rank":3,"title":"Someone","mediatype":"person"},
		{"id":0,"rank":4,"title":"No id","mediatype":"movie"},
		{"id":76600,"rank":5,"title":"Duplicate","mediatype":"movie"},
		{"id":76600,"rank":6,"title":"Same number, other kind","mediatype":"show","tvdbid":null,"imdb_id":"not-an-id","release_year":null},
		{"id":550,"title":"No rank","mediatype":"movie"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{Rank: 1, Kind: "movie", TMDBID: 76600, IMDbID: "tt1630029", TVDBID: 5483, Title: "Avatar: The Way of Water", Year: 2022},
		{Rank: 2, Kind: "series", TMDBID: 1399, IMDbID: "tt0944947", TVDBID: 121361, Title: "Game of Thrones", Year: 2011},
		{Rank: 6, Kind: "series", TMDBID: 76600, Title: "Same number, other kind"},
		{Rank: 7, Kind: "movie", TMDBID: 550, Title: "No rank"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for index := range want {
		if items[index] != want[index] {
			t.Errorf("item %d = %+v, want %+v", index, items[index], want[index])
		}
	}
}

func TestParseItemsReportsMissingAndOverlongLists(t *testing.T) {
	if _, err := parseItems([]byte(`{"error":"List not found"}`)); !errors.Is(err, ErrListNotFound) {
		t.Fatalf("error object = %v, want ErrListNotFound", err)
	}
	if _, err := parseItems([]byte(`<html>`)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("html = %v, want ErrUnavailable", err)
	}
	long := []byte("[")
	for index := 0; index <= MaxItems; index++ {
		if index > 0 {
			long = append(long, ',')
		}
		long = append(long, []byte(`{"id":1,"mediatype":"movie","title":"x"}`)...)
	}
	long = append(long, ']')
	if _, err := parseItems(long); !errors.Is(err, ErrListTooLong) {
		t.Fatalf("long list = %v, want ErrListTooLong", err)
	}
}

func TestFetchReadsTheListJSONAndFollowsNoRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/lists/someone/films/json":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"id":550,"rank":1,"title":"Fight Club","imdb_id":"tt0137523","mediatype":"movie","release_year":1999}]`))
		case "/lists/someone/moved/json":
			http.Redirect(writer, request, "https://elsewhere.invalid/list.json", http.StatusFound)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.Fetch(context.Background(), ListRef{Owner: "someone", Slug: "films"})
	if err != nil || len(items) != 1 || items[0].TMDBID != 550 || items[0].IMDbID != "tt0137523" || items[0].Year != 1999 {
		t.Fatalf("Fetch = %+v, %v", items, err)
	}
	if _, err := client.Fetch(context.Background(), ListRef{Owner: "someone", Slug: "missing"}); !errors.Is(err, ErrListNotFound) {
		t.Fatalf("missing list = %v, want ErrListNotFound", err)
	}
	if _, err := client.Fetch(context.Background(), ListRef{Owner: "someone", Slug: "moved"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirected list = %v, want ErrUnavailable", err)
	}
	if _, err := client.Fetch(context.Background(), ListRef{Owner: "../x", Slug: "films"}); !errors.Is(err, ErrInvalidListURL) {
		t.Fatalf("bad owner = %v, want ErrInvalidListURL", err)
	}
}

func TestFetchSendsDanishLettersEncodedOnce(t *testing.T) {
	var rawPath, path string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		rawPath, path = request.URL.EscapedPath(), request.URL.Path
		_, _ = writer.Write([]byte(`[{"id":550,"rank":1,"title":"Fight Club","mediatype":"movie"}]`))
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), ListRef{Owner: "søren", Slug: "julefilm-på-dansk"}); err != nil {
		t.Fatal(err)
	}
	if rawPath != "/lists/s%C3%B8ren/julefilm-p%C3%A5-dansk/json" || path != "/lists/søren/julefilm-på-dansk/json" {
		t.Fatalf("request path = %q (%q)", rawPath, path)
	}
}
