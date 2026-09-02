package newznab

import (
	"errors"
	"reflect"
	"testing"
	"time"
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

func TestSelectSearchDialectsKeepsPerMediaFallbackIndependent(t *testing.T) {
	dialects, err := SelectSearchDialects(CapsSnapshot{Searches: []SearchCapability{
		{Mode: "search", Available: true, Params: []string{"q", "year"}},
		{Mode: "movie-search", Available: true, Params: []string{"q", "imdbid"}},
		{Mode: "tv-search", Available: false, Params: []string{"q", "tvdbid", "season"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if dialects.Movie.Mode != "movie" || !reflect.DeepEqual(dialects.Movie.Params, []string{"imdbid", "q"}) {
		t.Fatalf("movie dialect=%#v", dialects.Movie)
	}
	if dialects.Series.Mode != "search" || !reflect.DeepEqual(dialects.Series.Params, []string{"q", "year"}) {
		t.Fatalf("series dialect=%#v", dialects.Series)
	}
}

func TestCAPSModeObservationsDoNotChangeLegacyDialectSelection(t *testing.T) {
	snapshot, err := ParseCaps([]byte(`<caps><searching><search available="yes" supportedParams="q,year"/><tv-search available="no" supportedParams="q,tvdbid,season"/></searching></caps>`), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	dialects, err := SelectSearchDialects(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	want := SearchDialects{
		Movie:  SearchDialect{Mode: "search", Params: []string{"q", "year"}},
		Series: SearchDialect{Mode: "search", Params: []string{"q", "year"}},
	}
	if !reflect.DeepEqual(dialects, want) {
		t.Fatalf("dialects=%#v, want %#v", dialects, want)
	}
	if len(snapshot.ModeObservations) != 3 || snapshot.ModeObservations[0].ElementState != CAPSElementPresentAvailable || snapshot.ModeObservations[1].ElementState != CAPSElementAbsent || snapshot.ModeObservations[2].ElementState != CAPSElementPresentUnavailable {
		t.Fatalf("mode observations=%#v", snapshot.ModeObservations)
	}
}

func TestSelectSearchDialectsRejectsUnusableCapabilities(t *testing.T) {
	_, err := SelectSearchDialects(CapsSnapshot{Searches: []SearchCapability{{Mode: "movie-search", Available: true, Params: []string{"imdbid"}}}})
	if !errors.Is(err, ErrSearchCapabilityRequired) {
		t.Fatalf("error=%v", err)
	}
}
