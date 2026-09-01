package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/gmd"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

type fakeGMD struct {
	detail  gmd.Detail
	matched bool
	err     error
}

func (fake fakeGMD) Lookup(context.Context, string, []catalog.ExternalIdentity) (gmd.Detail, bool, error) {
	return fake.detail, fake.matched, fake.err
}

func TestBuildUsesTMDBPrimaryAndGMDForExactSupplement(t *testing.T) {
	runtime := uint16(45)
	service := &Service{GMD: fakeGMD{matched: true, detail: gmd.Detail{
		EventID:   "event_primary",
		Title:     gmd.Title{OriginalName: "Papiret", Overview: "GMD fallback", Language: "da", Format: "Scripted", Status: "ongoing", RuntimeMinutes: &runtime, PosterURL: "https://artworks.thetvdb.com/banners/poster.jpg"},
		Countries: []string{"DK"},
		Genres:    []string{"Drama"},
		Networks:  []gmd.Network{{Name: "DR", Country: "DK", Type: "Broadcaster", Source: "tvdb"}, {Name: "DR", Country: "DK", Type: "Schedule evidence", Source: "simkl"}},
		Aliases:   []gmd.Alias{{Name: "Papiret", Language: "da", Source: "tvdb"}},
		ExternalIDs: []gmd.ExternalID{
			{Source: "tmdb", ID: "42", URL: "https://www.themoviedb.org/tv/42"},
			{Source: "tvdb", ID: "77", URL: "https://thetvdb.com/series/test"},
			{Source: "simkl", ID: "88", URL: "https://simkl.com/tv/88/test"},
		},
		Evidence: []gmd.Evidence{{Source: "tmdb"}, {Source: "tvdb"}, {Source: "simkl"}},
		Events:   []gmd.Event{{ID: "event_primary", Type: "series_premiere", Date: "2026-09-01", Confidence: 0.9}},
	}}}
	result, err := service.Build(context.Background(), catalog.KindSeries, 42, seerr.Metadata{
		Name:             "The Paper",
		OriginalName:     "The Paper",
		Overview:         "TMDB primary overview",
		OriginalLanguage: "en",
		PosterPath:       "/poster.jpg",
		BackdropPath:     "/backdrop.jpg",
		VoteAverage:      8.25,
		VoteCount:        120,
		Genres:           []seerr.Genre{{Name: "Comedy"}},
		OriginCountry:    []string{"US"},
	}, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified, Source: "seerr_metadata"}}, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.EnrichmentState != "gmd_exact_identity_match" || result.Metadata.Overview != "TMDB primary overview" || result.Metadata.PosterURL != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("unexpected precedence: %+v", result)
	}
	if result.Metadata.Format != "Scripted" || result.Metadata.RuntimeMinutes == nil || *result.Metadata.RuntimeMinutes != 45 {
		t.Fatalf("GMD supplement missing: %+v", result.Metadata)
	}
	if len(result.Metadata.Ratings) != 1 || result.Metadata.Ratings[0].Source != "tmdb" || len(result.Metadata.Events) != 1 || len(result.Metadata.Events[0].Sources) != 3 {
		t.Fatalf("rating/event evidence missing: %+v", result.Metadata)
	}
	if !hasIdentity(result.Identities, "simkl", "88") || !hasSource(result.Metadata.Sources, "tvdb", "supplementary TV metadata") || !hasSource(result.Metadata.Sources, "simkl", "schedule evidence") {
		t.Fatalf("provider evidence missing: identities=%+v sources=%+v", result.Identities, result.Metadata.Sources)
	}
}

func TestBuildNeverUsesGMDForMovieOrFailedMatch(t *testing.T) {
	service := &Service{GMD: fakeGMD{matched: true, detail: gmd.Detail{Title: gmd.Title{Overview: "must not apply"}}}}
	movie, err := service.Build(context.Background(), catalog.KindMovie, 7, seerr.Metadata{Title: "Movie", Overview: "TMDB"}, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7", State: catalog.IdentityVerified}}, time.Now())
	if err != nil || movie.EnrichmentState != "tmdb_only" || movie.Metadata.Overview != "TMDB" {
		t.Fatalf("movie enrichment=%+v err=%v", movie, err)
	}
	series, err := (&Service{GMD: fakeGMD{matched: false}}).Build(context.Background(), catalog.KindSeries, 8, seerr.Metadata{Name: "Series"}, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "8", State: catalog.IdentityVerified}}, time.Now())
	if err != nil || series.EnrichmentState != "gmd_no_exact_identity_match" || len(series.Identities) != 1 {
		t.Fatalf("failed exact match changed data: %+v err=%v", series, err)
	}
}

func hasIdentity(values []catalog.ExternalIdentity, namespace, value string) bool {
	for _, identity := range values {
		if identity.Namespace == namespace && identity.Value == value {
			return true
		}
	}
	return false
}

func hasSource(values []catalog.MetadataSource, provider, role string) bool {
	for _, source := range values {
		if source.Provider == provider && source.Role == role {
			return true
		}
	}
	return false
}
