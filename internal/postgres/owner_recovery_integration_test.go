package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
)

// TestRecordPreSubmissionRejectionDoesNotSpendRoundSlot proves the D5
// follow-up: a package rejected before the AltMount submission intent excludes
// the exact release and queues the next search without creating a job, so the
// round's two download attempts stay available.
func TestRecordPreSubmissionRejectionDoesNotSpendRoundSlot(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Pre Submission Rejection", "900901")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "pre-submission-rejection")

	if err := fixture.store.RecordPreSubmissionRejection(fixture.ctx, retrieval, "archive_unsupported", "release package rejected before AltMount submission: release contains an archive set instead of a playable media file", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateWaiting) {
		t.Fatalf("state=%q, want waiting", state)
	}
	if jobs := fixture.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()); jobs != 0 {
		t.Fatalf("pre-submission rejection created %d altmount jobs, want 0", jobs)
	}
	exclusions := fixture.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind='archive_unsupported'`, workflow.ID.String())
	if exclusions != 1 {
		t.Fatalf("exclusions=%d, want 1", exclusions)
	}
	pending := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflow.ID.String())
	if pending == 0 {
		t.Fatal("pre-submission rejection queued no next search")
	}
}

// TestOpenKnownAcquisitionRoundPrefersDanishEvidence proves the H6 owner
// action: a new round can be opened from releases already observed, prefers
// explicit Danish-subtitle evidence over a higher raw score, selects the
// candidate directly, and records the round and retrieve event.
func TestOpenKnownAcquisitionRoundPrefersDanishEvidence(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Known Round", "900902")
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test' WHERE workflow_id=$1 AND state='open'`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	transitioned, err := fixture.store.TransitionWorkflow(fixture.ctx, workflow.ID, workflow.Revision, workflows.StateWaiting, "test known round", "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	noEvidence := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "known-no-evidence", 900, time.Now().UTC())
	danish := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexerTwo.ID, "known-danish-evidence", 100, time.Now().UTC())
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE release_observations SET normalized_facts=$2::jsonb WHERE id=(SELECT observation_id FROM release_candidates WHERE id=$1)`, danish.String(), `[{"name":"subtitle_languages","values":["da"],"state":"inferred"}]`); err != nil {
		t.Fatal(err)
	}

	if err := fixture.store.OpenKnownAcquisitionRound(fixture.ctx, workflow.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var state, selected string
	var revision int64
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,COALESCE(selected_release_id::text,''),revision FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state, &selected, &revision); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateCandidateSelected) || selected != danish.String() {
		t.Fatalf("state=%q selected=%s, want candidate_selected with the Danish-evidence release %s (no-evidence %s)", state, selected, danish, noEvidence)
	}
	if uint64(revision) <= transitioned.Revision {
		t.Fatalf("revision=%d, want greater than %d", revision, transitioned.Revision)
	}
	var ordinal int
	var cutoff int64
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT round_ordinal,novelty_cutoff_sequence FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, workflow.ID.String()).Scan(&ordinal, &cutoff); err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 || cutoff != 0 {
		t.Fatalf("open round ordinal=%d cutoff=%d, want 2 and 0", ordinal, cutoff)
	}
	retrieve := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested' AND completed_at IS NULL`, workflow.ID.String())
	if retrieve != 1 {
		t.Fatalf("retrieve events=%d, want 1", retrieve)
	}
	// The action is not repeatable while the round is open.
	if err := fixture.store.OpenKnownAcquisitionRound(fixture.ctx, workflow.ID, time.Now().UTC()); err == nil {
		t.Fatal("second known round opened while one is already open")
	}
}

// TestRevivePreSubmissionRejectionsFreesSlotAndRelease proves the supported
// revival path for the archive rejections that predate RAR submission: the
// pre-upload job, its attempt marker, its package guard, and the exclusion all
// disappear so the release can be selected again, and an audit event records
// it.
func TestRevivePreSubmissionRejectionsFreesSlotAndRelease(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Revive Archive Rejections", "900903")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "revive-archive-rejection")
	intent, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(55), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	job, err := fixture.store.LoadAltMountJob(fixture.ctx, intent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteReleaseFailure(fixture.ctx, job, "archive_unsupported", "release package rejected before AltMount submission: release contains an archive set instead of a playable media file", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if fixture.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind='archive_unsupported'`, workflow.ID.String()) != 1 {
		t.Fatal("fixture did not record the pre-upload archive exclusion")
	}

	revived, err := fixture.store.RevivePreSubmissionRejections(fixture.ctx, workflow.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if revived != 1 {
		t.Fatalf("revived=%d, want 1", revived)
	}
	for query, name := range map[string]string{
		`SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`:               "jobs",
		`SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1`: "attempted releases",
		`SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`: "release exclusions",
		`SELECT count(*) FROM workflow_submitted_packages WHERE workflow_id=$1`: "submitted packages",
		`SELECT count(*) FROM workflow_package_exclusions WHERE workflow_id=$1`: "package exclusions",
	} {
		if count := fixture.count(t, query, workflow.ID.String()); count != 0 {
			t.Fatalf("%s after revival=%d, want 0", name, count)
		}
	}
	if audit := fixture.count(t, `SELECT count(*) FROM audit_events WHERE action='workflow.archive_rejections_revived' AND aggregate_id=$1`, workflow.ID.String()); audit != 1 {
		t.Fatalf("revival audit events=%d, want 1", audit)
	}
}
