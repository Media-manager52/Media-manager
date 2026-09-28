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
)

// publishTimedOutMovie publishes a verified movie through the real store and
// symlink lifecycle, then lets its first Plex scan time out.
func publishTimedOutMovie(t *testing.T, fixture *plexPublicationFixture, title, tmdb, nativeKey string) (uuid.UUID, uuid.UUID, PublicationVersion, string) {
	t.Helper()
	_, workflowID := fixture.movieWorkflow(t, title, tmdb)
	job, _, _ := fixture.seedVerifiedMovieOutput(t, workflowID, 1, nativeKey)
	sourceFile := filepath.Join(fixture.localRoot, "completed", nativeKey, "Movie.mkv")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("read-only altmount output"), 0o444); err != nil {
		t.Fatal(err)
	}
	publicationContext, err := fixture.store.LoadPublicationContext(fixture.ctx, workflowID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	scanID, version, _, destination := fixture.publishMoviePlans(t, workflowID, job.JobID, publicationContext)
	timeOutScan(t, fixture, scanID)
	return workflowID, scanID, version, filepath.Join(fixture.plexVisibleRoot, destination)
}

func timeOutScan(t *testing.T, fixture *plexPublicationFixture, scanID uuid.UUID) {
	t.Helper()
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.TimeoutPlexScan(fixture.ctx, scan, "Plex matched the path to another identity", scan.DeadlineAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var state, code, detail string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT workflow.state,COALESCE(workflow.attention_code,''),COALESCE(workflow.attention_detail,'') FROM workflows workflow JOIN plex_scan_requests scan ON scan.workflow_id=workflow.id WHERE scan.id=$1`, scanID.String()).Scan(&state, &code, &detail); err != nil || state != "needs_attention" || code != "plex_timeout" || detail != "Plex matched the path to another identity" {
		t.Fatalf("timed-out workflow state=%q code=%q detail=%q err=%v", state, code, detail, err)
	}
}

// TestPlexReconfirmationRetriesTimedOutVerifiedLinks proves the owner can retry
// a timed-out Plex confirmation from the existing verified links without SQL,
// that the retry is idempotent and audited, that it reaches plex_confirmed
// through the normal confirmation path, and that retries are bounded.
func TestPlexReconfirmationRetriesTimedOutVerifiedLinks(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	workflowID, firstScan, version, plexPath := publishTimedOutMovie(t, fixture, "Reconfirm Movie", "560001", "reconfirm-movie")
	var linksBefore int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM library_link_versions WHERE workflow_id=$1`, workflowID.String()).Scan(&linksBefore); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	retryScan, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, workflowID, "owner", now)
	if err != nil || retryScan == firstScan {
		t.Fatalf("reconfirmation scan=%s first=%s err=%v", retryScan, firstScan, err)
	}
	var state string
	var attention *string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,attention_code FROM workflows WHERE id=$1`, workflowID.String()).Scan(&state, &attention); err != nil || state != "plex_scanning" || attention != nil {
		t.Fatalf("workflow state=%q attention=%v err=%v", state, attention, err)
	}
	var scanState string
	var deadline time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,deadline_at FROM plex_scan_requests WHERE id=$1`, retryScan.String()).Scan(&scanState, &deadline); err != nil || scanState != "planned" || (deadline.Sub(now)-plexConfirmationWindow).Abs() > time.Millisecond {
		t.Fatalf("retry scan state=%q deadline=%s err=%v", scanState, deadline, err)
	}
	var events, audits, transitions, linksAfter int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=$2 AND event_type='plex.scan_requested' AND completed_at IS NULL),
		(SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.plex_reconfirm_requested' AND actor='owner'),
		(SELECT count(*) FROM workflow_transitions WHERE workflow_id=$1 AND from_state='needs_attention' AND to_state='plex_scanning' AND actor='owner'),
		(SELECT count(*) FROM library_link_versions WHERE workflow_id=$1)`, workflowID.String(), retryScan.String()).Scan(&events, &audits, &transitions, &linksAfter); err != nil {
		t.Fatal(err)
	}
	if events != 1 || audits != 1 || transitions != 1 || linksAfter != linksBefore {
		t.Fatalf("events=%d audits=%d transitions=%d links=%d->%d", events, audits, transitions, linksBefore, linksAfter)
	}

	// A repeated owner request while the retry is open is a no-op.
	again, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, workflowID, "owner", now.Add(time.Minute))
	if err != nil || again != retryScan {
		t.Fatalf("idempotent retry scan=%s want %s err=%v", again, retryScan, err)
	}
	var scans int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM plex_scan_requests WHERE workflow_id=$1`, workflowID.String()).Scan(&scans); err != nil || scans != 2 {
		t.Fatalf("scan requests=%d err=%v, want 2", scans, err)
	}

	// Bounded: two more retries are allowed, the fourth is rejected.
	for retry := 2; retry <= maxPlexReconfirmations; retry++ {
		timeOutScan(t, fixture, retryScan)
		if retryScan, err = fixture.store.RequestPlexReconfirmation(fixture.ctx, workflowID, "owner", time.Now().UTC()); err != nil {
			t.Fatalf("retry %d: %v", retry, err)
		}
	}
	// The last allowed retry still confirms through the normal path.
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, retryScan)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if scan, err = fixture.store.LoadPlexScan(fixture.ctx, retryScan); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, []plexadapter.Confirmation{{RatingKey: "movie-1", Path: plexPath, ProviderGUID: "tmdb://560001"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertPublicationState(t, fixture, version.ID, "current", "plex_confirmed")
	if _, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, workflowID, "owner", time.Now().UTC()); !errors.Is(err, ErrPlexReconfirmationNotTimedOut) {
		t.Fatalf("confirmed workflow retry error=%v, want ErrPlexReconfirmationNotTimedOut", err)
	}

	limited, limitedScan, _, _ := publishTimedOutMovie(t, fixture, "Reconfirm Limit Movie", "560002", "reconfirm-limit")
	for retry := 1; retry <= maxPlexReconfirmations; retry++ {
		if limitedScan, err = fixture.store.RequestPlexReconfirmation(fixture.ctx, limited, "owner", time.Now().UTC()); err != nil {
			t.Fatalf("limit retry %d: %v", retry, err)
		}
		timeOutScan(t, fixture, limitedScan)
	}
	if _, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, limited, "owner", time.Now().UTC()); !errors.Is(err, ErrPlexReconfirmationLimit) {
		t.Fatalf("retry beyond limit error=%v, want ErrPlexReconfirmationLimit", err)
	}
}

// TestPlexReconfirmationRejectsUnverifiedLinksAndOtherStates proves a retry
// never proceeds when any link or companion of the attempt is not verified, and
// never applies to a workflow that is not parked on a Plex timeout.
func TestPlexReconfirmationRejectsUnverifiedLinksAndOtherStates(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	workflowID, _, version, _ := publishTimedOutMovie(t, fixture, "Unverified Movie", "560003", "reconfirm-unverified")
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE library_link_versions SET state='quarantined' WHERE id=$1`, version.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, workflowID, "owner", time.Now().UTC()); !errors.Is(err, ErrPlexReconfirmationUnverified) {
		t.Fatalf("unverified links error=%v, want ErrPlexReconfirmationUnverified", err)
	}
	var state string
	var scans int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,(SELECT count(*) FROM plex_scan_requests WHERE workflow_id=$1) FROM workflows WHERE id=$1`, workflowID.String()).Scan(&state, &scans); err != nil || state != "needs_attention" || scans != 1 {
		t.Fatalf("rejected retry changed state=%q scans=%d err=%v", state, scans, err)
	}

	_, unpublished := fixture.movieWorkflow(t, "Unpublished Movie", "560004")
	if _, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, unpublished, "owner", time.Now().UTC()); !errors.Is(err, ErrPlexReconfirmationNotTimedOut) {
		t.Fatalf("unpublished workflow error=%v, want ErrPlexReconfirmationNotTimedOut", err)
	}
	if _, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, uuid.NewV7(), "owner", time.Now().UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing workflow error=%v, want ErrNotFound", err)
	}
}
