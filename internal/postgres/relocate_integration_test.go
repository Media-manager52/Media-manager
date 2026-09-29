package postgres

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestRelocateWorkflowLinksMovesSymlinkAndRecordsRoot proves the supported
// relocation path used to fix movies published into the wrong library root:
// the Plex-facing symlink moves between roots, the link and workflow records
// follow it, and the change is audited.
func TestRelocateWorkflowLinksMovesSymlinkAndRecordsRoot(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	rootA, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Relocate A", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Relocate A", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "70", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Relocate B", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Relocate B", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "71", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := fixture.movieItem(t, "Relocate Links", "900905")
	created, err := fixture.store.CreateManualMedia(ctx, item, rootA.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("create=%v err=%v", created, err)
	}
	if _, err := fixture.store.TransitionWorkflow(ctx, created[0].ID, created[0].Revision, workflows.StateCancelled, "relocate fixture", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	versionID := seedCurrentSubtitleVersion(t, ctx, fixture.store, item.ID, rootA.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
	var workflowID, relative, target string
	if err := fixture.store.Pool.QueryRow(ctx, `SELECT v.workflow_id::text,l.destination_relative,v.plex_visible_target
		FROM library_link_versions v JOIN library_links l ON l.id=v.library_link_id WHERE v.id=$1`, versionID.String()).Scan(&workflowID, &relative, &target); err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(rootA.DestinationRoot, relative)
	if err := os.MkdirAll(filepath.Dir(physical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, physical); err != nil {
		t.Fatal(err)
	}

	moved, err := fixture.store.RelocateWorkflowLinks(ctx, uuid.MustParse(workflowID), rootB.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("moved=%d, want 1", moved)
	}
	if _, err := os.Lstat(filepath.Join(rootB.DestinationRoot, relative)); err != nil {
		t.Fatalf("relocated link missing: %v", err)
	}
	if _, err := os.Lstat(physical); !os.IsNotExist(err) {
		t.Fatalf("source link still present: %v", err)
	}
	var linkRoot, workflowRoot string
	if err := fixture.store.Pool.QueryRow(ctx, `SELECT l.root_id::text,w.root_id::text FROM library_links l
		JOIN library_link_versions v ON v.library_link_id=l.id JOIN workflows w ON w.id=v.workflow_id WHERE v.id=$1`, versionID.String()).Scan(&linkRoot, &workflowRoot); err != nil {
		t.Fatal(err)
	}
	if linkRoot != rootB.ID.String() || workflowRoot != rootB.ID.String() {
		t.Fatalf("link root=%s workflow root=%s, want %s", linkRoot, workflowRoot, rootB.ID)
	}
	if audit := fixture.count(t, `SELECT count(*) FROM audit_events WHERE action='library.links_relocated' AND aggregate_id=$1`, workflowID); audit != 1 {
		t.Fatalf("relocation audit events=%d, want 1", audit)
	}
	// A rerun is a no-op because the workflow now lives in the target root.
	if moved, err := fixture.store.RelocateWorkflowLinks(ctx, uuid.MustParse(workflowID), rootB.ID, time.Now().UTC()); err != nil || moved != 0 {
		t.Fatalf("rerun moved=%d err=%v, want 0 nil", moved, err)
	}
}
