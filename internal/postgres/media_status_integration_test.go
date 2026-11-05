package postgres

import (
	"errors"
	"testing"
	"time"
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
