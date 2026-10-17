package postgres

import (
	"errors"
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
	// A published Danish subtitle sidecar is a 'current' companion. It must
	// move with its video; before the fix only 'verified' companions moved and
	// Plex lost the subtitles of every relocated title.
	subtitleAsset := uuid.NewV7()
	if _, err := fixture.store.Pool.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind)
		SELECT $1,a.inventory_id,'Old.da.srt',10,'movie' FROM library_link_versions v JOIN altmount_output_assets a ON a.id=v.source_asset_id WHERE v.id=$2`, subtitleAsset.String(), versionID.String()); err != nil {
		t.Fatal(err)
	}
	subtitleRelative := filepath.Join(filepath.Dir(relative), "old.da.srt")
	if _, err := fixture.store.Pool.Exec(ctx, `INSERT INTO library_link_companions(id,link_version_id,source_asset_id,language,source_relative,destination_relative,plex_visible_target,state,verified_at)
		VALUES($1,$2,$3,'da','Old.da.srt',$4,'/mnt/altmount/old/Old.da.srt','current',now())`, uuid.NewV7().String(), versionID.String(), subtitleAsset.String(), subtitleRelative); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/mnt/altmount/old/Old.da.srt", filepath.Join(rootA.DestinationRoot, subtitleRelative)); err != nil {
		t.Fatal(err)
	}

	moved, err := fixture.store.RelocateWorkflowLinks(ctx, uuid.MustParse(workflowID), rootB.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Fatalf("moved=%d, want 2 (video and subtitle)", moved)
	}
	if _, err := os.Lstat(filepath.Join(rootB.DestinationRoot, subtitleRelative)); err != nil {
		t.Fatalf("relocated subtitle missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(rootA.DestinationRoot, subtitleRelative)); !os.IsNotExist(err) {
		t.Fatalf("source subtitle still present: %v", err)
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

// TestRelocateWorkflowLinksKeepsScheduledSearch proves that relocating a
// waiting workflow carries its pending search forward. Relocation bumps the
// workflow revision, and the search handler drops events whose revision no
// longer matches, so without the carry-over the workflow silently stopped
// searching.
func TestRelocateWorkflowLinksKeepsScheduledSearch(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	rootA, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Pending A", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Pending A", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "72", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Pending B", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Pending B", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "73", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := fixture.movieItem(t, "Relocate Pending Search", "900906")
	created, err := fixture.store.CreateManualMedia(ctx, item, rootA.ID, fixture.profile.ID, fixture.altMount.ID, nil, true, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("create=%v err=%v", created, err)
	}
	workflowID := created[0].ID.String()
	before := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_type='workflow' AND aggregate_id=$1 AND completed_at IS NULL AND aggregate_revision=(SELECT revision FROM workflows WHERE id=$1)`, workflowID)
	if before == 0 {
		t.Fatal("fixture has no pending event at the current revision")
	}
	if _, err := fixture.store.RelocateWorkflowLinks(ctx, created[0].ID, rootB.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if after := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_type='workflow' AND aggregate_id=$1 AND completed_at IS NULL AND aggregate_revision=(SELECT revision FROM workflows WHERE id=$1)`, workflowID); after != before {
		t.Fatalf("pending events at the new revision=%d, want %d", after, before)
	}
	if root := fixture.count(t, `SELECT count(*) FROM workflows WHERE id=$1 AND root_id=$2`, workflowID, rootB.ID.String()); root != 1 {
		t.Fatal("workflow root was not updated")
	}
}

// TestUpdateLibraryRootDefaultAndDisable covers the owner setting that moves
// the default root of a media kind and retires an unused root.
func TestUpdateLibraryRootDefaultAndDisable(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC()
	oldDefault, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "Old Default", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Old", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "74", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	next, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "Next Default", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Next", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "75", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	var refusal ActionRefusal
	if _, err := fixture.store.UpdateLibraryRoot(ctx, oldDefault.ID, LibraryRootPatch{Enabled: &no}, now); !errors.As(err, &refusal) {
		t.Fatalf("disabling the default root: err=%v, want a refusal", err)
	}
	if _, err := fixture.store.UpdateLibraryRoot(ctx, oldDefault.ID, LibraryRootPatch{Default: &no}, now); !errors.As(err, &refusal) || refusal.Code != "invalid_default_change" {
		t.Fatalf("clearing the only default: err=%v, want invalid_default_change", err)
	}
	updated, err := fixture.store.UpdateLibraryRoot(ctx, next.ID, LibraryRootPatch{Default: &yes}, now)
	if err != nil || !updated.Default {
		t.Fatalf("make default: root=%+v err=%v", updated, err)
	}
	if defaults := fixture.count(t, `SELECT count(*) FROM library_roots WHERE media_kind='series' AND is_default`); defaults != 1 {
		t.Fatalf("series defaults=%d, want 1", defaults)
	}
	retired, err := fixture.store.UpdateLibraryRoot(ctx, oldDefault.ID, LibraryRootPatch{Enabled: &no}, now)
	if err != nil || retired.Enabled || retired.Default {
		t.Fatalf("disable unused root: root=%+v err=%v", retired, err)
	}
	if audit := fixture.count(t, `SELECT count(*) FROM audit_events WHERE action='library.root_updated'`); audit != 2 {
		t.Fatalf("audit events=%d, want 2", audit)
	}
}
