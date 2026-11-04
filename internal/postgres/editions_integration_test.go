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

// TestStandardAnd4KEditionsCoexist proves a title can be kept twice: a
// standard Seerr request and a 4K Seerr request for the same movie each get
// their own active workflow in their own edition's library root, a 4K root can
// never become a default, a workflow cannot move into the other edition's root,
// and the standard edition's search knows a 4K edition exists.
func TestStandardAnd4KEditionsCoexist(t *testing.T) {
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
	standardRoot, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Film", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Film", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if standardRoot.Edition != EditionStandard {
		t.Fatalf("root without an edition = %q, want standard", standardRoot.Edition)
	}
	if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Film 4K default", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Film 4K default", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "9", Edition: Edition4K, Default: true, Enabled: true}); err == nil {
		t.Fatal("a 4K root was created as the default")
	}
	fourKRoot, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Film 4K", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Film 4K", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "3", Edition: Edition4K, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	var refusal ActionRefusal
	if _, err := store.UpdateLibraryRoot(ctx, fourKRoot.ID, LibraryRootPatch{Default: &yes}, time.Now().UTC()); !errors.As(err, &refusal) {
		t.Fatalf("making the 4K root default: err=%v, want a refusal", err)
	}
	if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "Serier", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Serier", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Preferred resolution", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: releases.RulePreferred, Weight: 10}}
	policy, _ := json.Marshal(map[string]any{"rules": rules})
	for _, kind := range []catalog.MediaKind{catalog.KindMovie, catalog.KindSeries} {
		if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: kind, Name: string(kind) + " best", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	year := uint16(2026)
	request := func(t *testing.T, requestID string, kind catalog.MediaKind, providerID string, is4K bool, rootFolder string, offset int) (IntakeResult, error) {
		t.Helper()
		now := time.Now().UTC().Add(time.Duration(offset) * time.Second)
		deliveryID := uuid.NewV7()
		identity := catalog.ExternalIdentity{Namespace: "tmdb", Value: providerID, State: catalog.IdentityVerified, Source: "seerr"}
		if kind == catalog.KindSeries {
			identity.Namespace = "tvdb"
		}
		intake := SeerrIntake{
			InstanceID: seerr.ID.String(), RequestID: requestID, UpdatedAt: now, RequestState: "approved",
			Kind: kind, Title: "Edition " + providerID, Year: &year,
			Identities:  []catalog.ExternalIdentity{identity},
			PayloadHash: []byte(requestID), DeliveryID: deliveryID, Is4K: is4K, RootFolder: rootFolder,
		}
		if kind == catalog.KindSeries {
			intake.Seasons = []SeerrSeason{{Number: 1}}
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,$4,$5,'{}',$6)`, deliveryID.String(), seerr.ID.String(), "event-"+requestID, requestID, intake.PayloadHash, now); err != nil {
			t.Fatal(err)
		}
		if changed, err := store.UpsertSeerrSnapshot(ctx, intake); err != nil || !changed {
			t.Fatalf("snapshot changed=%v err=%v", changed, err)
		}
		return store.EnsureMediaFromSeerr(ctx, intake, now)
	}
	workflowEdition := func(t *testing.T, id uuid.UUID) (string, string) {
		t.Helper()
		var edition, root string
		if err := store.Pool.QueryRow(ctx, `SELECT edition,root_id::text FROM workflows WHERE id=$1`, id.String()).Scan(&edition, &root); err != nil {
			t.Fatal(err)
		}
		return edition, root
	}

	standard, err := request(t, "standard", catalog.KindMovie, "920001", false, "", 0)
	if err != nil || len(standard.WorkflowIDs) != 1 {
		t.Fatalf("standard intake=%+v err=%v", standard, err)
	}
	if edition, root := workflowEdition(t, standard.WorkflowIDs[0]); edition != EditionStandard || root != standardRoot.ID.String() {
		t.Fatalf("standard workflow edition=%s root=%s", edition, root)
	}
	search, err := store.LoadSearchContext(ctx, standard.WorkflowIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if search.Edition != EditionStandard || search.FourKEditionExists {
		t.Fatalf("standard search before 4K: edition=%s fourK=%v", search.Edition, search.FourKEditionExists)
	}

	fourK, err := request(t, "uhd", catalog.KindMovie, "920001", true, "", 1)
	if err != nil || len(fourK.WorkflowIDs) != 1 {
		t.Fatalf("4K intake=%+v err=%v", fourK, err)
	}
	if fourK.WorkflowIDs[0] == standard.WorkflowIDs[0] {
		t.Fatal("the 4K request reused the standard workflow")
	}
	if edition, root := workflowEdition(t, fourK.WorkflowIDs[0]); edition != Edition4K || root != fourKRoot.ID.String() {
		t.Fatalf("4K workflow edition=%s root=%s", edition, root)
	}
	var active int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE media_id=$1 AND terminal_at IS NULL`, standard.MediaID.String()).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active workflows=%d err=%v, want one per edition", active, err)
	}
	again, err := request(t, "uhd-again", catalog.KindMovie, "920001", true, "", 2)
	if err != nil || len(again.WorkflowIDs) != 1 || again.WorkflowIDs[0] != fourK.WorkflowIDs[0] {
		t.Fatalf("second 4K request=%+v err=%v, want the existing 4K workflow", again, err)
	}
	search, err = store.LoadSearchContext(ctx, standard.WorkflowIDs[0])
	if err != nil || search.Edition != EditionStandard || !search.FourKEditionExists {
		t.Fatalf("standard search with 4K: edition=%s fourK=%v err=%v", search.Edition, search.FourKEditionExists, err)
	}
	search, err = store.LoadSearchContext(ctx, fourK.WorkflowIDs[0])
	if err != nil || search.Edition != Edition4K {
		t.Fatalf("4K search: edition=%s err=%v", search.Edition, err)
	}

	if _, err := store.RelocateWorkflowLinks(ctx, standard.WorkflowIDs[0], fourKRoot.ID, time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "library_root_wrong_edition" {
		t.Fatalf("relocating into the 4K root: err=%v, want library_root_wrong_edition", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET root_id=$2 WHERE id=$1`, standard.WorkflowIDs[0].String(), fourKRoot.ID.String()); err == nil {
		t.Fatal("the database let a standard workflow point at a 4K root")
	}

	if _, err := request(t, "series-uhd", catalog.KindSeries, "930001", true, "", 3); !errors.Is(err, ErrSeerr4KRootMissing) {
		t.Fatalf("4K series without a 4K root: err=%v, want ErrSeerr4KRootMissing", err)
	}
}

// addLegacyEditionShim lets a test seed a pre-54 schema with today's store
// code, which reads and scopes by edition. It adds bare standard edition
// columns; dropLegacyEditionShim removes them again before the test applies
// the real migrations, so migration 54 still runs against the true old shape.
func addLegacyEditionShim(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE library_roots ADD COLUMN edition text NOT NULL DEFAULT 'standard';
		ALTER TABLE workflows ADD COLUMN edition text NOT NULL DEFAULT 'standard'`); err != nil {
		t.Fatal(err)
	}
}

func dropLegacyEditionShim(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE workflows DROP COLUMN edition; ALTER TABLE library_roots DROP COLUMN edition`); err != nil {
		t.Fatal(err)
	}
}
