package postgres

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

func refusalCode(err error) string {
	var refusal ActionRefusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

// TestDeleteLibraryRootAndIntegrationOnlyWhenUnused proves the settings
// deletes cannot lose history: a root or integration that anything refers to,
// the default root and an enabled integration are refused; unused ones go.
func TestDeleteLibraryRootAndIntegrationOnlyWhenUnused(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()
	fixture.movieWorkflow(t, "Root In Use", "590003")

	if code := refusalCode(store.DeleteLibraryRoot(ctx, fixture.root.ID, "owner", now)); code != "library_root_is_default" {
		t.Fatalf("deleting the default root: code=%q", code)
	}
	spare, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Spare root", DestinationRoot: t.TempDir(), PlexVisibleRoot: t.TempDir(), SourceVisibleRoot: t.TempDir(), PlexInstanceID: fixture.root.PlexInstanceID, PlexLibraryID: "9", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateLibraryRoot(ctx, spare.ID, LibraryRootPatch{Default: boolPointer(true)}, now); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(store.DeleteLibraryRoot(ctx, fixture.root.ID, "owner", now)); code != "library_root_in_use" {
		t.Fatalf("deleting a root a workflow uses: code=%q", code)
	}
	if _, err := store.UpdateLibraryRoot(ctx, fixture.root.ID, LibraryRootPatch{Default: boolPointer(true)}, now); err != nil {
		t.Fatal(err)
	}
	taken := fixture.root.Name
	if _, err := store.UpdateLibraryRoot(ctx, spare.ID, LibraryRootPatch{Name: &taken}, now); refusalCode(err) != "invalid_name" {
		t.Fatalf("renaming to a taken name: err=%v", err)
	}
	fresh := "  Renamed root "
	renamed, err := store.UpdateLibraryRoot(ctx, spare.ID, LibraryRootPatch{Name: &fresh}, now)
	if err != nil || renamed.Name != "Renamed root" || renamed.DestinationRoot != spare.DestinationRoot {
		t.Fatalf("rename: root=%+v err=%v", renamed, err)
	}
	if err := store.DeleteLibraryRoot(ctx, spare.ID, "owner", now); err != nil {
		t.Fatalf("deleting an unused root: %v", err)
	}
	if err := store.DeleteLibraryRoot(ctx, spare.ID, "owner", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice: err=%v", err)
	}

	// The Plex integration is enabled and the library root points at it.
	plexID := *fixture.root.PlexInstanceID
	if code := refusalCode(store.DeleteIntegration(ctx, plexID, "owner", now)); code != "integration_enabled" {
		t.Fatalf("deleting an enabled integration: code=%q", code)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=false WHERE id=$1`, plexID.String()); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(store.DeleteIntegration(ctx, plexID, "owner", now)); code != "integration_in_use" {
		t.Fatalf("deleting an integration a root uses: code=%q", code)
	}
	unused, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Unused Seerr", Config: json.RawMessage(`{"base_url":"https://seerr.invalid"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteIntegration(ctx, unused.ID, "owner", now); err != nil {
		t.Fatalf("deleting an unused integration: %v", err)
	}
	var left, audited int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM integration_instances WHERE id=$1),(SELECT count(*) FROM audit_events WHERE action IN ('integration.deleted','library.root_deleted'))`, unused.ID.String()).Scan(&left, &audited); err != nil {
		t.Fatal(err)
	}
	if left != 0 || audited != 2 {
		t.Fatalf("left=%d audited=%d, want the integration gone and two audit events", left, audited)
	}
}

// TestSettingsDeleteKnowsEveryCascade fails when a migration adds a foreign
// key that would silently delete rows together with a library root or an
// integration. Such a table must either be checked in DeleteLibraryRoot /
// DeleteIntegration or be configuration that belongs to the deleted row, and
// then be listed here.
func TestSettingsDeleteKnowsEveryCascade(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	rows, err := fixture.store.Pool.Query(fixture.ctx, `SELECT confrelid::regclass::text||' <- '||conrelid::regclass::text||' '||confdeltype::text FROM pg_constraint
		WHERE contype='f' AND confrelid::regclass::text IN ('library_roots','integration_instances','quality_profiles') AND confdeltype NOT IN ('a','r')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	sort.Strings(got)
	want := []string{
		"integration_instances <- indexer_feed_matches c",          // newest-uploads reader notes; belong to the indexer
		"integration_instances <- indexer_feed_state c",            // newest-uploads reader notes; belong to the indexer
		"integration_instances <- indexer_request_reservations c",  // checked
		"integration_instances <- integration_connection_checks c", // belongs to the integration
		"integration_instances <- integration_secrets c",           // belongs to the integration
		"integration_instances <- seerr_library_routes c",          // checked
		"integration_instances <- streaming_choices c",             // checked while unexpired
		"library_roots <- seerr_library_routes c",                  // checked
		"quality_profiles <- seerr_library_routes n",               // checked / moved
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cascading references changed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func boolPointer(value bool) *bool { return &value }
