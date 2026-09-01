package catalog

import (
	"errors"
	"testing"
	"time"
)

func TestVerifiedProviderIdentityValidation(t *testing.T) {
	_, err := New(KindMovie, "Example", pointer(uint16(2026)), []ExternalIdentity{{Namespace: "tmdb", Value: "../1", State: IdentityVerified, Source: "test"}}, time.Now())
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("unsafe provider identity was accepted: %v", err)
	}
	if _, err := New(KindSeries, "Example", pointer(uint16(2026)), []ExternalIdentity{{Namespace: "tvdb", Value: "12345", State: IdentityVerified, Source: "test"}}, time.Now()); err != nil {
		t.Fatalf("valid TVDB identity was rejected: %v", err)
	}
}

func TestNormalizeMetadataBoundsAndPreservesProviderEvidence(t *testing.T) {
	runtime := uint16(29)
	value, err := NormalizeMetadata(&MediaMetadata{
		Overview:       "  Real overview  ",
		Tagline:        " Evidence, not guesses. ",
		ReleaseDate:    "2026-09-01",
		Certification:  "15",
		RuntimeMinutes: &runtime,
		PosterURL:      "https://image.tmdb.org/t/p/w500/poster.jpg",
		Genres:         []string{"Comedy", "comedy", "Drama"},
		Keywords:       []string{"Newsroom", "newsroom"},
		Companies:      []MetadataCompany{{Name: "Studio", Country: "DK", Kind: "Production", Source: "tmdb"}},
		Credits:        []MetadataCredit{{Name: "Director", Role: "Director", ProfileURL: "https://image.tmdb.org/t/p/w185/person.jpg", Source: "tmdb"}},
		Sources:        []MetadataSource{{Provider: "SIMKL", ProviderID: "88", URL: "https://simkl.com/tv/88/show", Role: "schedule evidence"}},
		Ratings:        []MetadataRating{{Source: "IMDb", Value: 7.5, Votes: 100, Provenance: "Simkl catalog evidence"}},
		Aliases:        []MetadataAlias{{Name: "Dansk titel", Language: "da", Source: "tvdb"}},
		Events:         []MetadataEvent{{Type: "series_premiere", Date: "2026-09-01", Confidence: 0.8, Sources: []string{"simkl"}}},
	}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if value.Overview != "Real overview" || value.Tagline != "Evidence, not guesses." || len(value.Genres) != 2 || len(value.Keywords) != 1 || len(value.Companies) != 1 || len(value.Credits) != 1 || value.Ratings[0].Provenance == "" || value.Sources[0].Provider != "simkl" || value.ObservedAt.IsZero() {
		t.Fatalf("normalized metadata=%+v", value)
	}

	zero := uint16(0)
	for _, invalid := range []*MediaMetadata{
		{PosterURL: "http://image.tmdb.org/poster.jpg"},
		{PosterURL: "https://example.test/poster.jpg"},
		{PosterURL: "https://image.tmdb.org/t/p/w500/poster.jpg?token=secret"},
		{PosterURL: "https://image.tmdb.org:8443/t/p/w500/poster.jpg"},
		{Sources: []MetadataSource{{Provider: "tvdb", URL: "https://thetvdb.com/series/example?apikey=secret", Role: "supplementary metadata"}}},
		{ReleaseDate: "01-09-2026"},
		{RuntimeMinutes: &zero},
	} {
		if _, err := NormalizeMetadata(invalid, time.Now()); !errors.Is(err, ErrInvalidMetadata) {
			t.Fatalf("invalid metadata accepted: %+v err=%v", invalid, err)
		}
	}
}

func pointer[T any](value T) *T { return &value }
