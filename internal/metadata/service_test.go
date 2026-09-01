package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	metadataproviders "github.com/media-manager/media-manager/internal/adapters/metadataproviders"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

type fakeProvider struct {
	contribution metadataproviders.Contribution
	err          error
	calls        int
}

func (provider *fakeProvider) Lookup(context.Context, catalog.MediaKind, []catalog.ExternalIdentity, time.Time) (metadataproviders.Contribution, error) {
	provider.calls++
	return provider.contribution, provider.err
}

func TestBuildMergesDirectProvidersWithoutReplacingTMDBPrimary(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	runtime := uint16(47)
	tmdb := &fakeProvider{contribution: metadataproviders.Contribution{Metadata: &catalog.MediaMetadata{Overview: "Direct TMDB overview", Tagline: "The newsroom never sleeps.", RuntimeMinutes: &runtime, Genres: []string{"Comedy"}, Keywords: []string{"newsroom"}, Sources: []catalog.MetadataSource{{Provider: "tmdb", ProviderID: "42", URL: "https://www.themoviedb.org/tv/42", Role: "primary descriptive metadata"}}, Ratings: []catalog.MetadataRating{{Source: "tmdb", Value: 8.1, Votes: 123, Provenance: "direct TMDB API"}}, ObservedAt: now}, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}, {Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified}}}}
	tvdb := &fakeProvider{contribution: metadataproviders.Contribution{Metadata: &catalog.MediaMetadata{Overview: "TVDB must not replace TMDB", Certification: "15", Companies: []catalog.MetadataCompany{{Name: "Studio", Source: "tvdb"}}, Sources: []catalog.MetadataSource{{Provider: "tvdb", ProviderID: "77", URL: "https://thetvdb.com/series/example", Role: "supplementary TV metadata"}}, ObservedAt: now}, Identities: []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "77", State: catalog.IdentityVerified}}}}
	simkl := &fakeProvider{contribution: metadataproviders.Contribution{Metadata: &catalog.MediaMetadata{Ratings: []catalog.MetadataRating{{Source: "simkl", Value: 7.8, Votes: 50}, {Source: "imdb", Value: 7.5, Votes: 500, Provenance: "Simkl catalog evidence; not a direct IMDb API response"}}, Sources: []catalog.MetadataSource{{Provider: "simkl", ProviderID: "88", URL: "https://simkl.com/tv/88/example", Role: "ratings and lifecycle evidence"}}, ObservedAt: now}, Identities: []catalog.ExternalIdentity{{Namespace: "simkl", Value: "88", State: catalog.IdentityVerified}}}}
	service := &Service{TMDB: tmdb, TVDB: tvdb, Simkl: simkl}
	result, err := service.Build(context.Background(), catalog.KindSeries, 42, seerr.Metadata{ID: 42, Name: "The Paper", Overview: "Seerr fallback"}, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}, {Namespace: "tvdb", Value: "77", State: catalog.IdentityVerified}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.EnrichmentState != "direct_provider_enriched" || result.Metadata.Overview != "Direct TMDB overview" || result.Metadata.Tagline == "" || result.Metadata.Certification != "15" {
		t.Fatalf("unexpected merge: %+v", result)
	}
	if result.ProviderStates["tmdb"] != "direct" || result.ProviderStates["tvdb"] != "direct" || result.ProviderStates["simkl"] != "direct" || result.ProviderStates["imdb"] != "via_simkl" {
		t.Fatalf("provider states=%v", result.ProviderStates)
	}
	if !hasIdentity(result.Identities, "imdb", "tt1234567") || !hasIdentity(result.Identities, "simkl", "88") || len(result.Metadata.Ratings) != 3 {
		t.Fatalf("provider evidence missing: %+v", result)
	}
}

func TestBuildKeepsTruthfulFallbackAndRejectsConflictingContribution(t *testing.T) {
	conflict := &fakeProvider{contribution: metadataproviders.Contribution{Metadata: &catalog.MediaMetadata{Overview: "must not apply"}, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "999", State: catalog.IdentityVerified}}}}
	unavailable := &fakeProvider{err: errors.New("bounded failure")}
	result, err := (&Service{TVDB: conflict, Simkl: unavailable}).Build(context.Background(), catalog.KindMovie, 7, seerr.Metadata{ID: 7, Title: "Movie", Overview: "TMDB through Seerr"}, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7", State: catalog.IdentityVerified}, {Namespace: "tvdb", Value: "77", State: catalog.IdentityVerified}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.EnrichmentState != "tmdb_via_seerr" || result.Metadata.Overview != "TMDB through Seerr" || result.ProviderStates["tvdb"] != "identity_conflict" || result.ProviderStates["simkl"] != "unavailable" {
		t.Fatalf("fallback was not truthful: %+v", result)
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
