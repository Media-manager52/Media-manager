package postgres

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

var errRelocationInterrupted = errors.New("interrupted")

// TestInterruptedMoveFinishesFromThePlan stops a whole-title move at every
// step in turn and proves the next run, by the owner or by recovery after a
// restart, leaves every video and subtitle in the new library, every record
// agreeing, and exactly one Plex refresh per folder.
func TestInterruptedMoveFinishesFromThePlan(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	store := fixture.store
	defer func() { store.relocateCheckpoint = nil }()
	for stop := 1; ; stop++ {
		root := func(name string) LibraryRoot {
			created, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: fmt.Sprintf("Move %s %d", name, stop), DestinationRoot: t.TempDir(), PlexVisibleRoot: fmt.Sprintf("/media/Move%s%d", name, stop), SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &fixture.plex.ID, PlexLibraryID: "80", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			return created
		}
		rootA, rootB := root("A"), root("B")
		item := fixture.movieItem(t, fmt.Sprintf("Interrupted %d", stop), fmt.Sprintf("97%04d", stop))
		if _, err := store.CreateManualMedia(ctx, item, rootA.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test"); err != nil {
			t.Fatal(err)
		}
		var files []string
		for index, name := range []string{"one", "two"} {
			versionID := seedCurrentSubtitleVersion(t, ctx, store, item.ID, rootA.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
			if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET upload_name=id::text`); err != nil {
				t.Fatal(err)
			}
			relative := "Subtitle Integration (2026)/" + name + ".mkv"
			if _, err := store.Pool.Exec(ctx, `UPDATE library_links l SET destination_relative=$2 FROM library_link_versions v WHERE v.library_link_id=l.id AND v.id=$1`, versionID.String(), relative); err != nil {
				t.Fatal(err)
			}
			subtitle := "Subtitle Integration (2026)/" + name + ".da.srt"
			if _, err := store.Pool.Exec(ctx, `INSERT INTO library_link_companions(id,link_version_id,source_asset_id,language,source_relative,destination_relative,plex_visible_target,state,verified_at) SELECT $1,v.id,v.source_asset_id,'da','old/Old.da.srt',$3,$4,'verified',now() FROM library_link_versions v WHERE v.id=$2`, uuid.NewV7().String(), versionID.String(), subtitle, fmt.Sprintf("/mnt/altmount/old/%d.da.srt", index)); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(rootA.DestinationRoot, filepath.Dir(relative)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/mnt/altmount/old/Old.mkv", filepath.Join(rootA.DestinationRoot, relative)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(fmt.Sprintf("/mnt/altmount/old/%d.da.srt", index), filepath.Join(rootA.DestinationRoot, subtitle)); err != nil {
				t.Fatal(err)
			}
			files = append(files, relative, subtitle)
		}

		calls := 0
		store.relocateCheckpoint = func() error {
			calls++
			if calls == stop {
				return errRelocationInterrupted
			}
			return nil
		}
		first, err := store.RelocateMedia(ctx, item.ID, rootB.ID, time.Now().UTC())
		store.relocateCheckpoint = nil
		final := first
		finished := err == nil
		if finished {
			if stop < 8 {
				t.Fatalf("the move finished after %d interruption points, want every file and workflow covered", stop-1)
			}
		} else {
			if !errors.Is(err, errRelocationInterrupted) {
				t.Fatalf("stop %d: %v", stop, err)
			}
			if refreshes := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE event_type='plex.folder_refresh_requested' AND payload->>'root_id' IN ($1,$2)`, rootA.ID.String(), rootB.ID.String()); refreshes != 0 {
				t.Fatalf("stop %d: Plex was asked to refresh an unfinished move", stop)
			}
			if stop == 1 {
				var refusal ActionRefusal
				other := root("C")
				if _, err := store.RelocateMedia(ctx, item.ID, other.ID, time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "relocation_in_progress" {
					t.Fatalf("redirecting an unfinished move: %v", err)
				}
				if _, err := store.Pool.Exec(ctx, `UPDATE media_relocations SET lease_until=now()+interval '1 minute' WHERE media_id=$1`, item.ID.String()); err != nil {
					t.Fatal(err)
				}
				if _, err := store.RelocateMedia(ctx, item.ID, rootB.ID, time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "relocation_in_progress" {
					t.Fatalf("second run during a held lease: %v", err)
				}
				if _, err := store.Pool.Exec(ctx, `UPDATE media_relocations SET created_at=created_at-interval '2 hours' WHERE media_id=$1`, item.ID.String()); err != nil {
					t.Fatal(err)
				}
				feed, err := store.Problems(ctx, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, problem := range feed.Problems {
					found = found || problem.Code == "library.move_unfinished"
				}
				if !found {
					t.Fatal("an unfinished move is not visible to the owner")
				}
			}
			// A crash leaves the lease behind until it expires.
			if _, err := store.Pool.Exec(ctx, `UPDATE media_relocations SET lease_until=now()-interval '1 second' WHERE media_id=$1`, item.ID.String()); err != nil {
				t.Fatal(err)
			}
			if stop%2 == 0 {
				if final, err = store.RelocateMedia(ctx, item.ID, rootB.ID, time.Now().UTC()); err != nil {
					t.Fatalf("stop %d owner retry: %v", stop, err)
				}
			} else {
				var payload []byte
				if err := store.Pool.QueryRow(ctx, `SELECT e.payload FROM outbox_events e JOIN media_relocations r ON r.id=e.aggregate_id WHERE e.event_type='media.relocation_resume_requested' AND r.media_id=$1`, item.ID.String()).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				if err := store.ResumeMediaRelocation(ctx, OutboxEvent{Payload: payload}); err != nil {
					t.Fatalf("stop %d recovery: %v", stop, err)
				}
				var id string
				if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM media_relocations WHERE media_id=$1`, item.ID.String()).Scan(&id); err != nil {
					t.Fatal(err)
				}
				if final, err = store.runMediaRelocation(ctx, uuid.MustParse(id), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
		}

		if final.Workflows != 3 || final.Moved != 4 {
			t.Fatalf("stop %d: result %+v, want 3 workflows and 4 files", stop, final)
		}
		for _, relative := range files {
			if _, err := os.Lstat(filepath.Join(rootB.DestinationRoot, relative)); err != nil {
				t.Fatalf("stop %d: %s missing from the new library: %v", stop, relative, err)
			}
			if _, err := os.Lstat(filepath.Join(rootA.DestinationRoot, relative)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stop %d: %s left behind: %v", stop, relative, err)
			}
		}
		if n := fixture.count(t, `SELECT count(*) FROM workflows WHERE media_id=$1 AND root_id<>$2`, item.ID.String(), rootB.ID.String()); n != 0 {
			t.Fatalf("stop %d: %d workflows point at the old library", stop, n)
		}
		if n := fixture.count(t, `SELECT count(*) FROM library_links WHERE root_id=$1`, rootA.ID.String()); n != 0 {
			t.Fatalf("stop %d: %d links recorded in the old library", stop, n)
		}
		if n := fixture.count(t, `SELECT count(*) FROM media_relocations WHERE media_id=$1 AND state='complete'`, item.ID.String()); n != 1 {
			t.Fatalf("stop %d: %d finished moves", stop, n)
		}
		if n := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE event_type='plex.folder_refresh_requested' AND payload->>'root_id' IN ($1,$2)`, rootA.ID.String(), rootB.ID.String()); n != 2 {
			t.Fatalf("stop %d: %d Plex refreshes, want one for each library", stop, n)
		}
		if finished {
			return
		}
	}
}
