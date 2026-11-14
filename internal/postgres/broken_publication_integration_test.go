package postgres

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"uuid"

	plexadapter "github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
)

// removeDisplacedLinks runs what the library.displaced_remove_requested
// handler does for every queued removal of one workflow.
func removeDisplacedLinks(t *testing.T, fixture *plexPublicationFixture, workflowID uuid.UUID) int {
	t.Helper()
	rows, err := fixture.store.Pool.Query(fixture.ctx, `SELECT event.aggregate_id::text FROM outbox_events event JOIN library_link_versions version ON version.id=event.aggregate_id
		WHERE event.event_type='library.displaced_remove_requested' AND version.workflow_id=$1`, workflowID.String())
	if err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		id, _ := uuid.Parse(text)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		link, err := fixture.store.LoadDisplacedLink(fixture.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := library.RemoveSymlink(link.DestinationRoot, link.DestinationRelative, link.ExpectedTarget); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.RecordDisplacedLinkRemoved(fixture.ctx, link, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return len(ids)
}

func assertReleaseRejected(t *testing.T, fixture *plexPublicationFixture, workflowID uuid.UUID, version PublicationVersion, failureKind, failureCode, actor string) {
	t.Helper()
	var state string
	var attention *string
	var grab *string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,attention_code,grab_attempt_id::text FROM workflows WHERE id=$1`, workflowID.String()).Scan(&state, &attention, &grab); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" || grab != nil {
		t.Fatalf("workflow after rejection state=%q attention=%v grab=%v, want waiting without an attempt", state, attention, grab)
	}
	var versionState string
	var exclusions, packages, audits, searches int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT
		(SELECT state FROM library_link_versions WHERE id=$2),
		(SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind=$3),
		(SELECT count(*) FROM workflow_package_exclusions WHERE workflow_id=$1 AND failure_kind=$4),
		(SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.published_release_rejected' AND actor=$5),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested')`,
		workflowID.String(), version.ID.String(), failureKind, failureCode, actor).Scan(&versionState, &exclusions, &packages, &audits, &searches); err != nil {
		t.Fatal(err)
	}
	if versionState != "displaced" || exclusions != 1 || packages != 1 || audits != 1 || searches != 1 {
		t.Fatalf("version=%q exclusions=%d packages=%d audits=%d searches=%d", versionState, exclusions, packages, audits, searches)
	}
}

// TestOwnerRejectsAReleasePlexNeverConfirmed proves the owner can reject a
// published release whose Plex confirmation timed out: its link leaves the
// library, the exact release and package are excluded, and the workflow
// searches for another release. A repeat, or a workflow in any other state,
// is refused.
func TestOwnerRejectsAReleasePlexNeverConfirmed(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	workflowID, _, version, _ := publishTimedOutMovie(t, fixture, "Wrong Release Movie", "570001", "wrong-release-movie")
	link := filepath.Join(fixture.destinationRoot, mustDestination(t, fixture, version.ID))
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("published link missing before rejection: %v", err)
	}

	result, err := fixture.store.RejectPublishedRelease(fixture.ctx, workflowID, "owner", "output_verification", OwnerRejectedPublicationCode, "", time.Now().UTC())
	if err != nil || result.LinksRemoved != 1 || !result.RetryQueued || result.RoundExhausted {
		t.Fatalf("rejection=%+v err=%v", result, err)
	}
	assertReleaseRejected(t, fixture, workflowID, version, "output_verification", OwnerRejectedPublicationCode, "owner")
	if removed := removeDisplacedLinks(t, fixture, workflowID); removed != 1 {
		t.Fatalf("removed links=%d, want 1", removed)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected link still in the library: %v", err)
	}

	if _, err := fixture.store.RejectPublishedRelease(fixture.ctx, workflowID, "owner", "output_verification", OwnerRejectedPublicationCode, "", time.Now().UTC()); !errors.Is(err, ErrPublicationNotRejectable) {
		t.Fatalf("repeat rejection error=%v, want ErrPublicationNotRejectable", err)
	}
	_, unpublished := fixture.movieWorkflow(t, "Unpublished Reject Movie", "570002")
	if _, err := fixture.store.RejectPublishedRelease(fixture.ctx, unpublished, "owner", "output_verification", OwnerRejectedPublicationCode, "", time.Now().UTC()); !errors.Is(err, ErrPublicationNotRejectable) {
		t.Fatalf("unpublished workflow error=%v, want ErrPublicationNotRejectable", err)
	}
	if _, err := fixture.store.RejectPublishedRelease(fixture.ctx, uuid.NewV7(), "owner", "output_verification", OwnerRejectedPublicationCode, "", time.Now().UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing workflow error=%v, want ErrNotFound", err)
	}
}

// TestBrokenFileBeforePlexConfirmationRejectsTheRelease proves the missing-file
// sweep covers a publication that waits on the owner after a Plex timeout:
// when AltMount drops the file, the release is rejected automatically instead
// of the workflow waiting for a confirmation that can never come. A confirmed
// publication still gets a repair, never a rejection.
func TestBrokenFileBeforePlexConfirmationRejectsTheRelease(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	workflowID, _, version, _ := publishTimedOutMovie(t, fixture, "Broken Before Plex Movie", "580001", "broken-before-plex")
	var filePath string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT inventory.root_relative || '/' || asset.relative_path FROM library_link_versions version
		JOIN altmount_output_assets asset ON asset.id=version.source_asset_id JOIN altmount_output_inventories inventory ON inventory.id=asset.inventory_id
		WHERE version.id=$1`, version.ID.String()).Scan(&filePath); err != nil {
		t.Fatal(err)
	}
	listed, err := fixture.store.CurrentAltMountPublications(fixture.ctx, fixture.altMount.ID)
	if err != nil || !contains(listed, filePath) {
		t.Fatalf("timed-out publication not checked by the missing-file sweep: %v err=%v", listed, err)
	}

	outcome, err := fixture.store.PlanMissingAltMountFile(fixture.ctx, fixture.altMount.ID, filePath, time.Now().UTC())
	if err != nil || outcome != RepairReleaseRejected {
		t.Fatalf("missing file outcome=%q err=%v, want %q", outcome, err, RepairReleaseRejected)
	}
	assertReleaseRejected(t, fixture, workflowID, version, "release_incomplete", BrokenPublicationCode, "worker")
	var repairs int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM altmount_repairs WHERE altmount_file_path=$1`, filePath).Scan(&repairs); err != nil || repairs != 0 {
		t.Fatalf("rejection opened repairs=%d err=%v", repairs, err)
	}
	if listed, err = fixture.store.CurrentAltMountPublications(fixture.ctx, fixture.altMount.ID); err != nil || contains(listed, filePath) {
		t.Fatalf("rejected publication still listed: %v err=%v", listed, err)
	}
	if outcome, err = fixture.store.PlanAltMountRepair(fixture.ctx, fixture.altMount.ID, 1, filePath, time.Now().UTC()); err != nil || outcome != RepairUnmanaged {
		t.Fatalf("second report outcome=%q err=%v, want %q", outcome, err, RepairUnmanaged)
	}

	// A confirmed publication is never rejected: its broken file is repaired.
	confirmedID, scanID, confirmed, plexPath := publishTimedOutMovie(t, fixture, "Confirmed Movie", "580002", "confirmed-before-break")
	retry, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, confirmedID, "owner", time.Now().UTC())
	if err != nil || retry == scanID {
		t.Fatalf("reconfirm=%s err=%v", retry, err)
	}
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if scan, err = fixture.store.LoadPlexScan(fixture.ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, []plexadapter.Confirmation{{RatingKey: "movie-2", Path: plexPath, ProviderGUID: "tmdb://580002"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertPublicationState(t, fixture, confirmed.ID, "current", "plex_confirmed")
	var confirmedPath string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT inventory.root_relative || '/' || asset.relative_path FROM library_link_versions version
		JOIN altmount_output_assets asset ON asset.id=version.source_asset_id JOIN altmount_output_inventories inventory ON inventory.id=asset.inventory_id
		WHERE version.id=$1`, confirmed.ID.String()).Scan(&confirmedPath); err != nil {
		t.Fatal(err)
	}
	if outcome, err = fixture.store.PlanMissingAltMountFile(fixture.ctx, fixture.altMount.ID, confirmedPath, time.Now().UTC()); err != nil || outcome != RepairOpened {
		t.Fatalf("confirmed publication outcome=%q err=%v, want %q", outcome, err, RepairOpened)
	}
	assertPublicationState(t, fixture, confirmed.ID, "current", "plex_confirmed")
}

func mustDestination(t *testing.T, fixture *plexPublicationFixture, versionID uuid.UUID) string {
	t.Helper()
	var destination string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT link.destination_relative FROM library_link_versions version JOIN library_links link ON link.id=version.library_link_id WHERE version.id=$1`, versionID.String()).Scan(&destination); err != nil {
		t.Fatal(err)
	}
	return destination
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
