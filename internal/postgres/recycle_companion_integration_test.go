package postgres

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
)

// TestDeletingATitleRecyclesItsSubtitleLinks proves a deleted title's Danish
// subtitle symlink leaves the Plex folder with its video and comes back with
// it on restore. Before the fix only the video was recycled, so the .srt link
// stayed behind in the library after Slet.
func TestDeletingATitleRecyclesItsSubtitleLinks(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	root, err := fixture.store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Recycle Subtitles", DestinationRoot: t.TempDir(), PlexVisibleRoot: "/media/Recycle Subtitles", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "74", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := fixture.movieItem(t, "Recycle Subtitle Links", "900987")
	created, err := fixture.store.CreateManualMedia(ctx, item, root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("create=%v err=%v", created, err)
	}
	if _, err := fixture.store.CancelWorkflowForOwner(ctx, created[0].ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	versionID := seedCurrentSubtitleVersion(t, ctx, fixture.store, item.ID, root.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
	var relative, target string
	if err := fixture.store.Pool.QueryRow(ctx, `SELECT l.destination_relative,v.plex_visible_target
		FROM library_link_versions v JOIN library_links l ON l.id=v.library_link_id WHERE v.id=$1`, versionID.String()).Scan(&relative, &target); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root.DestinationRoot, filepath.Dir(relative)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root.DestinationRoot, relative)); err != nil {
		t.Fatal(err)
	}
	subtitleAsset := uuid.NewV7()
	if _, err := fixture.store.Pool.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind)
		SELECT $1,a.inventory_id,'Old.da.srt',10,'movie' FROM library_link_versions v JOIN altmount_output_assets a ON a.id=v.source_asset_id WHERE v.id=$2`, subtitleAsset.String(), versionID.String()); err != nil {
		t.Fatal(err)
	}
	subtitleRelative := filepath.Join(filepath.Dir(relative), "old.da.srt")
	subtitleTarget := "/mnt/altmount/old/Old.da.srt"
	if _, err := fixture.store.Pool.Exec(ctx, `INSERT INTO library_link_companions(id,link_version_id,source_asset_id,language,source_relative,destination_relative,plex_visible_target,state,verified_at)
		VALUES($1,$2,$3,'da','Old.da.srt',$4,$5,'current',now())`, uuid.NewV7().String(), versionID.String(), subtitleAsset.String(), subtitleRelative, subtitleTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(subtitleTarget, filepath.Join(root.DestinationRoot, subtitleRelative)); err != nil {
		t.Fatal(err)
	}

	entries, err := fixture.store.PlanMediaRecycle(ctx, item.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("recycle entries=%+v, want the video and its subtitle", entries)
	}
	// Apply what the recycle worker does for each entry.
	for _, entry := range entries {
		if err := library.MoveSymlink(entry.DestinationRoot, entry.OriginalRelative, entry.RecycleRelative, entry.ExpectedTarget); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.MarkRecycleRetained(ctx, entry.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root.DestinationRoot, subtitleRelative)); !os.IsNotExist(err) {
		t.Fatalf("the subtitle link is still in the library after delete: %v", err)
	}
	if displaced := fixture.count(t, `SELECT count(*) FROM library_link_companions WHERE link_version_id=$1 AND state='displaced'`, versionID.String()); displaced != 1 {
		t.Fatalf("displaced companions=%d, want 1", displaced)
	}

	if err := fixture.store.RequestRecycleRestore(ctx, entries[0].ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		restoring, err := fixture.store.LoadRecycleEntry(ctx, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := library.MoveSymlink(restoring.DestinationRoot, restoring.RecycleRelative, restoring.OriginalRelative, restoring.ExpectedTarget); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.MarkRecycleRestored(ctx, restoring, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root.DestinationRoot, subtitleRelative)); err != nil {
		t.Fatalf("restored subtitle link missing: %v", err)
	}
	if current := fixture.count(t, `SELECT count(*) FROM library_link_companions WHERE link_version_id=$1 AND state='current'`, versionID.String()); current != 1 {
		t.Fatalf("current companions after restore=%d, want 1", current)
	}
	if deleted := fixture.count(t, `SELECT count(*) FROM media_items WHERE id=$1 AND deleted_at IS NOT NULL`, item.ID.String()); deleted != 0 {
		t.Fatal("the title is still deleted after a full restore")
	}
}
