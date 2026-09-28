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
	"github.com/media-manager/media-manager/internal/servarr"
)

func TestSeerrLibraryRouteHonorsChosenRootFolder(t *testing.T) {
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
	defaultRoot, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	comedyRoot, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Comedy", DestinationRoot: "/libraries/comedy", PlexVisibleRoot: "/libraries/comedy", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	policy, _ := json.Marshal(map[string]any{"rules": rules, "required_subtitle_languages": []string{"da"}})
	defaultProfile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie 1080p", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	originalPolicy, _ := json.Marshal(map[string]any{"rules": rules, "original_language_routing": []string{"da"}})
	originalProfile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie Danish Original", Policy: originalPolicy, UpgradesEnabled: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	ingest := func(t *testing.T, requestID, providerID, folder, originalLanguage string, profileID int) (SeerrIntake, IntakeResult, error) {
		t.Helper()
		now := time.Now().UTC()
		year := uint16(2026)
		deliveryID := uuid.NewV7()
		intake := SeerrIntake{
			InstanceID: seerr.ID.String(), RequestID: requestID, UpdatedAt: now, RequestState: "approved",
			Kind: catalog.KindMovie, Title: "Routing " + requestID, Year: &year,
			Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: providerID, State: catalog.IdentityVerified, Source: "seerr"}},
			Metadata:   &catalog.MediaMetadata{OriginalLanguage: originalLanguage}, PayloadHash: []byte(requestID), DeliveryID: deliveryID, RootFolder: folder, ProfileID: profileID,
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,$4,$5,'{}',$6)`, deliveryID.String(), seerr.ID.String(), "event-"+requestID, requestID, intake.PayloadHash, now); err != nil {
			t.Fatal(err)
		}
		if changed, err := store.UpsertSeerrSnapshot(ctx, intake); err != nil || !changed {
			t.Fatalf("snapshot changed=%v err=%v", changed, err)
		}
		result, err := store.EnsureMediaFromSeerr(ctx, intake, now)
		return intake, result, err
	}

	// A route with no profile override keeps original-language profile routing.
	route, err := store.UpsertSeerrLibraryRoute(ctx, SeerrLibraryRoute{SeerrInstanceID: seerr.ID, MediaKind: catalog.KindMovie, RootFolder: "/media/Comedy", LibraryRootID: comedyRoot.ID}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if route.RootFolder != "/media/Comedy" || route.LibraryRootID != comedyRoot.ID || !route.Enabled {
		t.Fatalf("route=%+v", route)
	}

	_, result, err := ingest(t, "routed", "920001", "/media/Comedy", "en", 0)
	if err != nil || len(result.WorkflowIDs) != 1 {
		t.Fatalf("routed intake result=%+v err=%v", result, err)
	}
	var rootID, profileID string
	if err := store.Pool.QueryRow(ctx, `SELECT root_id::text,quality_profile_id::text FROM workflows WHERE id=$1`, result.WorkflowIDs[0].String()).Scan(&rootID, &profileID); err != nil {
		t.Fatal(err)
	}
	if rootID != comedyRoot.ID.String() || profileID != defaultProfile.ID.String() {
		t.Fatalf("routed root=%s profile=%s", rootID, profileID)
	}

	// A folder without a route pauses intake for owner configuration.
	if _, _, err := ingest(t, "unmapped", "920002", "/media/Unknown", "en", 0); !errors.Is(err, ErrSeerrRouteUnmapped) {
		t.Fatalf("unmapped error=%v, want ErrSeerrRouteUnmapped", err)
	}

	// No folder keeps the previous default behavior.
	_, result, err = ingest(t, "legacy", "920003", "", "en", 0)
	if err != nil || len(result.WorkflowIDs) != 1 {
		t.Fatalf("legacy intake result=%+v err=%v", result, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT root_id::text FROM workflows WHERE id=$1`, result.WorkflowIDs[0].String()).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if rootID != defaultRoot.ID.String() {
		t.Fatalf("legacy root=%s, want default %s", rootID, defaultRoot.ID)
	}

	// A route may override the quality profile for that folder.
	route, err = store.UpsertSeerrLibraryRoute(ctx, SeerrLibraryRoute{SeerrInstanceID: seerr.ID, MediaKind: catalog.KindMovie, RootFolder: "/media/Comedy", LibraryRootID: comedyRoot.ID, QualityProfileID: &originalProfile.ID}, "test")
	if err != nil {
		t.Fatal(err)
	}
	_, result, err = ingest(t, "profile-override", "920004", "/media/Comedy", "en", 0)
	if err != nil || len(result.WorkflowIDs) != 1 {
		t.Fatalf("override intake result=%+v err=%v", result, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT quality_profile_id::text FROM workflows WHERE id=$1`, result.WorkflowIDs[0].String()).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if profileID != originalProfile.ID.String() {
		t.Fatalf("override profile=%s, want %s", profileID, originalProfile.ID)
	}

	// An unmapped folder route can only be satisfied after the owner maps it:
	// requeued deliveries bypass the unchanged-snapshot dedup.
	requeued, _, err := ingest(t, "requeued", "920005", "/media/Unknown", "en", 0)
	if !errors.Is(err, ErrSeerrRouteUnmapped) {
		t.Fatalf("requeued precondition error=%v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE seerr_deliveries SET processed_at=NULL,outcome='requeued' WHERE id=$1`, requeued.DeliveryID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSeerrLibraryRoute(ctx, SeerrLibraryRoute{SeerrInstanceID: seerr.ID, MediaKind: catalog.KindMovie, RootFolder: "/media/Unknown", LibraryRootID: comedyRoot.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	accepted, err := store.UpsertSeerrSnapshot(ctx, requeued)
	if err != nil || !accepted {
		t.Fatalf("requeued snapshot accepted=%v err=%v", accepted, err)
	}
	result, err = store.EnsureMediaFromSeerr(ctx, requeued, time.Now().UTC())
	if err != nil || len(result.WorkflowIDs) != 1 {
		t.Fatalf("requeued intake result=%+v err=%v", result, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT root_id::text FROM workflows WHERE id=$1`, result.WorkflowIDs[0].String()).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if rootID != comedyRoot.ID.String() {
		t.Fatalf("requeued root=%s, want mapped %s", rootID, comedyRoot.ID)
	}

	// Media Manager can itself be the Radarr server Seerr talks to. Seerr then
	// sends the library root's destination path and an MM-issued profile id.
	servarrRoot, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Servarr Movies", DestinationRoot: "/libraries/servarr", PlexVisibleRoot: "/libraries/servarr", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "3", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	servarrProfile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie Servarr", Policy: policy, UpgradesEnabled: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	syntheticProfile := servarr.SyntheticID("profile", servarrProfile.ID.String())
	_, result, err = ingest(t, "servarr", "920006", "/libraries/servarr", "en", syntheticProfile)
	if err != nil || len(result.WorkflowIDs) != 1 {
		t.Fatalf("servarr intake result=%+v err=%v", result, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT root_id::text,quality_profile_id::text FROM workflows WHERE id=$1`, result.WorkflowIDs[0].String()).Scan(&rootID, &profileID); err != nil {
		t.Fatal(err)
	}
	if rootID != servarrRoot.ID.String() || profileID != servarrProfile.ID.String() {
		t.Fatalf("servarr root=%s profile=%s, want %s %s", rootID, profileID, servarrRoot.ID, servarrProfile.ID)
	}
	if _, _, err := ingest(t, "servarr-unmapped", "920007", "/libraries/missing", "en", 0); !errors.Is(err, ErrSeerrRouteUnmapped) {
		t.Fatalf("servarr unmapped error=%v, want ErrSeerrRouteUnmapped", err)
	}

	// Observed folders and route listing feed the owner UI.
	routes, err := store.ListSeerrLibraryRoutes(ctx, seerr.ID)
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	observed, err := store.ListSeerrObservedFolders(ctx, seerr.ID)
	if err != nil || len(observed) < 2 {
		t.Fatalf("observed=%+v err=%v", observed, err)
	}
	if err := store.DeleteSeerrLibraryRoute(ctx, seerr.ID, route.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSeerrLibraryRoute(ctx, seerr.ID, route.ID, "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error=%v, want ErrNotFound", err)
	}
}
