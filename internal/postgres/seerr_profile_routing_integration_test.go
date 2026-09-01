package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestSeerrRoutesReliableOriginalLanguageMetadataToMatchingProfile(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`)}); err != nil {
		t.Fatal(err)
	}
	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "imdbid"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season"}},
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "test", DefaultLimit: 100, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	defaultPolicy, _ := json.Marshal(map[string]any{"rules": rules, "required_subtitle_languages": []string{"da"}})
	defaultProfile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie 1080p Danish", Policy: defaultPolicy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	originalPolicy, _ := json.Marshal(map[string]any{"rules": rules, "original_language_routing": []string{"dansk"}})
	originalProfile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie 1080p Danish Original", Policy: originalPolicy, UpgradesEnabled: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		requestID        string
		providerID       string
		originalLanguage string
		wantProfile      string
	}{
		{requestID: "danish", providerID: "910001", originalLanguage: "da-DK", wantProfile: originalProfile.ID.String()},
		{requestID: "non-danish", providerID: "910002", originalLanguage: "en", wantProfile: defaultProfile.ID.String()},
		{requestID: "unknown", providerID: "910003", originalLanguage: "", wantProfile: defaultProfile.ID.String()},
	}
	for index, test := range tests {
		t.Run(test.requestID, func(t *testing.T) {
			now := time.Now().UTC().Add(time.Duration(index) * time.Second)
			year := uint16(2026)
			deliveryID := uuid.NewV7()
			intake := SeerrIntake{
				InstanceID: seerr.ID.String(), RequestID: test.requestID, UpdatedAt: now, RequestState: "approved",
				Kind: catalog.KindMovie, Title: "Routing " + test.requestID, Year: &year,
				Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: test.providerID, State: catalog.IdentityVerified, Source: "seerr"}},
				Metadata:   &catalog.MediaMetadata{OriginalLanguage: test.originalLanguage}, PayloadHash: []byte{byte(index + 1)}, DeliveryID: deliveryID,
			}
			if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,$4,$5,'{}',$6)`, deliveryID.String(), seerr.ID.String(), "event-"+test.requestID, test.requestID, intake.PayloadHash, now); err != nil {
				t.Fatal(err)
			}
			if changed, err := store.UpsertSeerrSnapshot(ctx, intake); err != nil || !changed {
				t.Fatalf("snapshot changed=%v err=%v", changed, err)
			}
			result, err := store.EnsureMediaFromSeerr(ctx, intake, now)
			if err != nil || len(result.WorkflowIDs) != 1 {
				t.Fatalf("intake result=%+v err=%v", result, err)
			}
			var selected string
			if err := store.Pool.QueryRow(ctx, `SELECT quality_profile_id::text FROM workflows WHERE id=$1`, result.WorkflowIDs[0].String()).Scan(&selected); err != nil {
				t.Fatal(err)
			}
			if selected != test.wantProfile {
				t.Fatalf("selected profile=%s, want %s", selected, test.wantProfile)
			}
		})
	}
	if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Duplicate Danish route", Policy: originalPolicy, UpgradesEnabled: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(ctx)
	if _, err := selectSeerrQualityProfile(ctx, transaction, catalog.KindMovie, &catalog.MediaMetadata{OriginalLanguage: "da"}); !errors.Is(err, ErrProfileRoutingAmbiguous) {
		t.Fatalf("duplicate routing error=%v, want ErrProfileRoutingAmbiguous", err)
	}
}
