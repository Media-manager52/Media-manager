package newznab

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectSearchDialectsPrefersAdvertisedSpecializedModes(t *testing.T) {
	caps := CapsSnapshot{Searches: []SearchCapability{
		{Mode: "search", Available: true, Params: []string{"q"}},
		{Mode: "movie-search", Available: true, Params: []string{"Q", "imdbid", "tmdbid", "q"}},
		{Mode: "tv-search", Available: true, Params: []string{"q", "tvdbid", "season"}},
	}}
	dialects, err := SelectSearchDialects(caps)
	if err != nil {
		t.Fatal(err)
	}
	if dialects.Movie.Mode != "movie" || dialects.Series.Mode != "tvsearch" {
		t.Fatalf("unexpected dialects: %#v", dialects)
	}
	if !reflect.DeepEqual(dialects.Movie.Params, []string{"imdbid", "q", "tmdbid"}) {
		t.Fatalf("movie params=%v", dialects.Movie.Params)
	}
}

func TestSelectSearchDialectsUsesGenericFallback(t *testing.T) {
	dialects, err := SelectSearchDialects(CapsSnapshot{Searches: []SearchCapability{{Mode: "search", Available: true, Params: []string{"q"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if dialects.Movie.Mode != "search" || dialects.Series.Mode != "search" {
		t.Fatalf("unexpected fallback: %#v", dialects)
	}
}

func TestSelectSearchDialectsRejectsUnusableCapabilities(t *testing.T) {
	_, err := SelectSearchDialects(CapsSnapshot{Searches: []SearchCapability{{Mode: "movie-search", Available: true, Params: []string{"imdbid"}}}})
	if !errors.Is(err, ErrSearchCapabilityRequired) {
		t.Fatalf("error=%v", err)
	}
}
