package main

import (
	"context"
	"testing"
	"time"

	metadataproviders "github.com/media-manager/media-manager/internal/adapters/metadataproviders"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

func TestNewMetadataServiceLeavesUnconfiguredProvidersNil(t *testing.T) {
	service := newMetadataService(nil, nil, nil)
	if service.TMDB != nil || service.TVDB != nil || service.Simkl != nil {
		t.Fatalf("unconfigured providers must stay nil interfaces: %#v", service)
	}
	// Seerr intake enriches through this path; with no direct provider it must
	// fall back to Seerr's TMDB data instead of calling a nil client.
	result, err := service.Build(context.Background(), catalog.KindMovie, 603, seerr.Metadata{ID: 603, Title: "The Matrix", ReleaseDate: "1999-03-31"}, nil, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.EnrichmentState != "tmdb_via_seerr" || result.ProviderStates["tmdb"] != "seerr_fallback" {
		t.Fatalf("unexpected enrichment: state=%q providers=%v", result.EnrichmentState, result.ProviderStates)
	}
}

func TestNewMetadataServiceKeepsConfiguredProviders(t *testing.T) {
	tmdb, err := metadataproviders.NewTMDB(metadataproviders.TMDBConfig{AccessToken: "token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := newMetadataService(tmdb, nil, nil)
	if service.TMDB == nil || service.TVDB != nil || service.Simkl != nil {
		t.Fatalf("only the configured provider should be wired: %#v", service)
	}
}
