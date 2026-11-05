package postgres

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
)

// TestMoveDeleteAndRestoreWholeTitle drives the owner's "Flyt" and "Slet"
// buttons end to end on real symlinks: a title moves to another root with its
// open work, Plex is asked to rescan both folders, a delete first refuses and
// then cancels the open work, the link goes to the recycle bin, and a restore
// brings the title and its link back unchanged.
func TestMoveDeleteAndRestoreWholeTitle(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	rootA, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Whole A", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Whole A", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "80", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Whole B", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Whole B", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "81", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := fixture.movieItem(t, "Whole Title", "900931")
	created, err := fixture.store.CreateManualMedia(ctx, item, rootA.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("create=%v err=%v", created, err)
	}
	openWorkflow := created[0].ID
	versionID := seedCurrentSubtitleVersion(t, ctx, fixture.store, item.ID, rootA.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
	var relative, target string
	if err := fixture.store.Pool.QueryRow(ctx, `SELECT l.destination_relative,v.plex_visible_target FROM library_link_versions v JOIN library_links l ON l.id=v.library_link_id WHERE v.id=$1`, versionID.String()).Scan(&relative, &target); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(rootA.DestinationRoot, relative)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(rootA.DestinationRoot, relative)); err != nil {
		t.Fatal(err)
	}

	// Nothing moves while a part of the title is in flight.
	var state string
	if err := fixture.store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1`, openWorkflow.String()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(ctx, `UPDATE workflows SET state='searching' WHERE id=$1`, openWorkflow.String()); err != nil {
		t.Fatal(err)
	}
	var refusal ActionRefusal
	if _, err := fixture.store.RelocateMedia(ctx, item.ID, rootB.ID, time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "workflow_in_flight" {
		t.Fatalf("relocate while searching: %v, want workflow_in_flight", err)
	}
	if _, err := os.Lstat(filepath.Join(rootA.DestinationRoot, relative)); err != nil {
		t.Fatalf("a refused move touched the link: %v", err)
	}
	if _, err := fixture.store.Pool.Exec(ctx, `UPDATE workflows SET state=$2 WHERE id=$1`, openWorkflow.String(), state); err != nil {
		t.Fatal(err)
	}

	moved, err := fixture.store.RelocateMedia(ctx, item.ID, rootB.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if moved.Workflows != 2 || moved.Moved != 1 {
		t.Fatalf("relocation=%+v, want both workflows and one link", moved)
	}
	if _, err := os.Lstat(filepath.Join(rootB.DestinationRoot, relative)); err != nil {
		t.Fatalf("moved link missing: %v", err)
	}
	if roots := fixture.count(t, `SELECT count(*) FROM workflows WHERE media_id=$1 AND root_id<>$2`, item.ID.String(), rootB.ID.String()); roots != 0 {
		t.Fatalf("%d workflows still point at the old root", roots)
	}
	rows, err := fixture.store.Pool.Query(ctx, `SELECT payload::text FROM outbox_events WHERE event_type='plex.folder_refresh_requested' ORDER BY payload->>'root_id'`)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	rows.Close()
	if len(payloads) != 2 {
		t.Fatalf("plex folder refreshes=%v, want the old and the new folder", payloads)
	}
	for _, payload := range payloads {
		folder, ok, err := fixture.store.PlexForFolderRefresh(ctx, []byte(payload))
		if err != nil || !ok || folder.Relative != filepath.Dir(relative) || folder.Integration.ID != fixture.plex.ID {
			t.Fatalf("folder refresh %s resolved to %+v ok=%v err=%v", payload, folder, ok, err)
		}
	}
	if _, err := fixture.store.RelocateMedia(ctx, item.ID, rootB.ID, time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "nothing_to_move" {
		t.Fatalf("second relocate: %v, want nothing_to_move", err)
	}

	// Delete: open work blocks a plain delete, and is cancelled on request.
	if _, err := fixture.store.PlanMediaRecycle(ctx, item.ID, time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "media_has_open_work" {
		t.Fatalf("delete with open work: %v, want media_has_open_work", err)
	}
	if cancelled, err := fixture.store.CancelOpenWorkOfMedia(ctx, item.ID, time.Now().UTC()); err != nil || cancelled != 1 {
		t.Fatalf("cancelled=%d err=%v, want 1", cancelled, err)
	}
	entries, err := fixture.store.PlanMediaRecycle(ctx, item.ID, time.Now().UTC())
	if err != nil || len(entries) != 1 || entries[0].RootID != rootB.ID {
		t.Fatalf("recycle entries=%+v err=%v", entries, err)
	}
	entry := entries[0]
	if err := library.MoveSymlink(entry.DestinationRoot, entry.OriginalRelative, entry.RecycleRelative, entry.ExpectedTarget); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkRecycleRetained(ctx, entry.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(rootB.DestinationRoot, relative)); !os.IsNotExist(err) {
		t.Fatalf("deleted link still in the library: %v", err)
	}
	if deleted := fixture.count(t, `SELECT count(*) FROM media_items WHERE id=$1 AND deleted_at IS NOT NULL`, item.ID.String()); deleted != 1 {
		t.Fatal("media is not marked deleted")
	}

	// Restore brings the title and its link back.
	if err := fixture.store.RequestRecycleRestore(ctx, entry.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	restoring, err := fixture.store.LoadRecycleEntry(ctx, entry.ID)
	if err != nil || restoring.State != "restore_planned" {
		t.Fatalf("restore entry=%+v err=%v", restoring, err)
	}
	if err := library.MoveSymlink(restoring.DestinationRoot, restoring.RecycleRelative, restoring.OriginalRelative, restoring.ExpectedTarget); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkRecycleRestored(ctx, restoring, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(rootB.DestinationRoot, relative)); err != nil || got != target {
		t.Fatalf("restored link=%q err=%v, want %q", got, err, target)
	}
	if active := fixture.count(t, `SELECT count(*) FROM media_items WHERE id=$1 AND deleted_at IS NULL`, item.ID.String()); active != 1 {
		t.Fatal("restored media is still marked deleted")
	}
}
