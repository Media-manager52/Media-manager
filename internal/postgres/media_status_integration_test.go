package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestLibraryStatusAndStartingWorkAgain drives the title page's "Hent" and the
// library's status: a wanted film is missing, a second Hent while work is open
// is refused with a stable code, and once the film is in Plex it is no longer
// missing and a Hent looks for a better copy.
func TestLibraryStatusAndStartingWorkAgain(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	item := fixture.movieItem(t, "Status Title", "900941")
	created, err := fixture.store.CreateManualMedia(ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("create=%v err=%v", created, err)
	}
	status := func() MediaStatus {
		t.Helper()
		statuses, err := fixture.store.MediaStatuses(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range statuses {
			if entry.MediaID == item.ID {
				return entry
			}
		}
		t.Fatal("title missing from the status list")
		return MediaStatus{}
	}
	if got := status(); got.InPlex || got.Missing != 1 || got.Attention {
		t.Fatalf("wanted film status = %+v, want missing 1", got)
	}
	var refusal ActionRefusal
	if _, err := fixture.store.CreateManualWorkflowForExisting(ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "", "test", time.Now().UTC()); !errors.As(err, &refusal) || refusal.Code != "workflow_already_open" {
		t.Fatalf("second Hent: %v, want workflow_already_open", err)
	}
	if _, err := fixture.store.CancelOpenWorkOfMedia(ctx, item.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seedCurrentSubtitleVersion(t, ctx, fixture.store, item.ID, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
	if got := status(); !got.InPlex || got.Missing != 0 {
		t.Fatalf("film in Plex status = %+v, want in Plex and nothing missing", got)
	}
	upgrade, err := fixture.store.CreateManualWorkflowForExisting(ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "", "test", time.Now().UTC())
	if err != nil {
		t.Fatalf("Hent on a film in Plex: %v", err)
	}
	if got := status(); !got.InPlex || got.Missing != 0 {
		t.Fatalf("upgrade %s counted as missing: %+v", upgrade.ID, got)
	}
}

// TestLibraryFoldersRecentAdditionsAndIndexerDownloads covers the reads behind
// the library's folder filter and "recently added" sort, the overview's
// recently added row and the 30-day download count per indexer.
func TestLibraryFoldersRecentAdditionsAndIndexerDownloads(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx, store := fixture.ctx, fixture.store
	exec := func(query string, arguments ...any) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, query, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	older := fixture.movieItem(t, "Recent Older", "900951")
	newer := fixture.movieItem(t, "Recent Newer", "900952")
	for _, item := range []catalog.MediaItem{older, newer} {
		if _, err := store.CreateManualMedia(ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CancelOpenWorkOfMedia(ctx, item.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	status := func(media uuid.UUID) MediaStatus {
		t.Helper()
		statuses, err := store.MediaStatuses(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range statuses {
			if entry.MediaID == media {
				return entry
			}
		}
		t.Fatal("title missing from the status list")
		return MediaStatus{}
	}
	if got := status(older.ID); got.RootIDs == nil || len(got.RootIDs) != 0 || got.AddedAt != nil {
		t.Fatalf("title without a file = %+v, want no folders and no added time", got)
	}
	// The seeding helper uses fixed release names, so the first title's rows
	// are renamed before the second title is seeded.
	olderVersion := seedCurrentSubtitleVersion(t, ctx, store, older.ID, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
	exec(`UPDATE release_observations SET native_guid='older-guid',operation_key='older' WHERE native_guid='old-guid'`)
	exec(`UPDATE altmount_jobs SET upload_name='older.nzb' WHERE upload_name='old.nzb'`)
	exec(`UPDATE library_links SET destination_relative='Recent Older (2026)/old.mkv' WHERE id=(SELECT library_link_id FROM library_link_versions WHERE id=$1)`, olderVersion.String())
	exec(`UPDATE library_link_versions SET verified_at=now()-interval '2 days' WHERE id=$1`, olderVersion.String())
	newerVersion := seedCurrentSubtitleVersion(t, ctx, store, newer.ID, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
	exec(`INSERT INTO media_metadata(media_id,data,revision,created_at,updated_at) VALUES($1,'{"poster_url":"https://image.invalid/newer.jpg","backdrop_url":"https://image.invalid/newer-wide.jpg"}',1,now(),now())
		ON CONFLICT(media_id) DO UPDATE SET data=media_metadata.data || EXCLUDED.data`, newer.ID.String())

	got := status(older.ID)
	if len(got.RootIDs) != 1 || got.RootIDs[0] != fixture.root.ID || got.AddedAt == nil || time.Since(*got.AddedAt) < 47*time.Hour || time.Since(*got.AddedAt) > 49*time.Hour {
		t.Fatalf("status of a title with a file = %+v, want its folder and the file's verified time", got)
	}
	recent := func() []RecentAddition {
		t.Helper()
		items, err := store.RecentAdditions(ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		mine := []RecentAddition{}
		for _, item := range items {
			if item.MediaID == older.ID || item.MediaID == newer.ID {
				mine = append(mine, item)
			}
		}
		return mine
	}
	items := recent()
	if len(items) != 2 || items[0].MediaID != newer.ID || items[1].MediaID != older.ID {
		t.Fatalf("recent additions = %+v, want the newer title first and each title once", items)
	}
	if first := items[0]; first.Title != "Recent Newer" || first.MediaKind != "movie" || first.Variant != "default" || first.Edition != "standard" || first.PosterURL != "https://image.invalid/newer.jpg" || first.BackdropURL != "https://image.invalid/newer-wide.jpg" || first.AddedAt.IsZero() {
		t.Fatalf("recent addition = %+v", first)
	}
	// A displaced file is no longer in the library.
	exec(`UPDATE library_link_versions SET state='displaced',displaced_at=now() WHERE id=$1`, newerVersion.String())
	if items := recent(); len(items) != 1 || items[0].MediaID != older.ID {
		t.Fatalf("after displacing the newer file: %+v, want only the older title", items)
	}
	if got := status(newer.ID); len(got.RootIDs) != 0 || got.AddedAt != nil {
		t.Fatalf("status after displacing = %+v, want no folder", got)
	}
	// A deleted title is not listed.
	exec(`UPDATE media_items SET deleted_at=now() WHERE id=$1`, older.ID.String())
	if items := recent(); len(items) != 0 {
		t.Fatalf("after deleting the title: %+v, want nothing", items)
	}

	downloads := func() IndexerDownloads {
		t.Helper()
		usages, err := store.IndexerUsages(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		for _, usage := range usages {
			if usage.ID == fixture.indexer.ID {
				return usage.Downloads30d
			}
		}
		t.Fatal("indexer missing from usage")
		return IndexerDownloads{}
	}
	exec(`UPDATE altmount_jobs SET submitted_at=now()-interval '1 day',acquisition_outcome=NULL,acquisition_closed_at=NULL WHERE upload_name IN ('older.nzb','old.nzb')`)
	if got := downloads(); got != (IndexerDownloads{}) {
		t.Fatalf("downloads without an outcome = %+v, want none counted", got)
	}
	exec(`UPDATE altmount_jobs SET acquisition_outcome='output_verified',acquisition_closed_at=now() WHERE upload_name='older.nzb'`)
	exec(`UPDATE altmount_jobs SET acquisition_outcome='release_incomplete',acquisition_closed_at=now() WHERE upload_name='old.nzb'`)
	if got := downloads(); got != (IndexerDownloads{Finished: 2, Verified: 1}) {
		t.Fatalf("downloads = %+v, want 2 finished and 1 verified", got)
	}
	exec(`UPDATE altmount_jobs SET submitted_at=now()-interval '31 days' WHERE upload_name='older.nzb'`)
	if got := downloads(); got != (IndexerDownloads{Finished: 1, Verified: 0}) {
		t.Fatalf("downloads after 30 days = %+v, want only the recent failed one", got)
	}
}
