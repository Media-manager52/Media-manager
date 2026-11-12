package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestFailedAltMountImportIsRetriedAndThenGivenUpWithoutOwner proves that a
// failed import waiting at owner attention is picked up again automatically
// (once), that retries are counted and paced across jobs, and that giving up
// excludes the release and returns the workflow to the round instead of
// asking the owner.
func TestFailedAltMountImportIsRetriedAndThenGivenUpWithoutOwner(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()

	stuck := func(title, tmdb, key string, queueID int64) AltMountJobContext {
		t.Helper()
		_, workflowID := fixture.movieWorkflow(t, title, tmdb)
		job, _, _ := seedSubtitleAttempt(t, ctx, store, workflowID, fixture.altMount.ID, fixture.indexer.ID, 1, key)
		if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',external_queue_id=$2,error_code='altmount_job_attention',error_detail='fast-fail validation inconclusive: context deadline exceeded' WHERE id=$1`, job.JobID.String(), queueID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='needs_attention',attention_code='altmount_job_attention',attention_detail='fast-fail validation inconclusive: context deadline exceeded',revision=revision+1 WHERE id=$1`, workflowID.String()); err != nil {
			t.Fatal(err)
		}
		return job
	}
	first := stuck("Auto Retry First", "591001", "auto-retry-first", 9101)
	second := stuck("Auto Retry Second", "591002", "auto-retry-second", 9102)

	// A job without an AltMount queue ID is not a failed import: left alone.
	_, other := fixture.movieWorkflow(t, "Auto Retry No Queue", "591003")
	otherJob, _, _ := seedSubtitleAttempt(t, ctx, store, other, fixture.altMount.ID, fixture.indexer.ID, 1, "auto-retry-noqueue")
	if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention' WHERE id=$1`, otherJob.JobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='needs_attention',attention_code='altmount_job_attention' WHERE id=$1`, other.String()); err != nil {
		t.Fatal(err)
	}

	resumed, err := store.ResumeAltMountAttention(ctx, now, 10)
	if err != nil || resumed != 2 {
		t.Fatalf("resumed=%d err=%v, want 2", resumed, err)
	}
	if again, err := store.ResumeAltMountAttention(ctx, now, 10); err != nil || again != 0 {
		t.Fatalf("second sweep resumed=%d err=%v, want 0 (each job once)", again, err)
	}
	var otherState string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1`, other.String()).Scan(&otherState); err != nil || otherState != "needs_attention" {
		t.Fatalf("job without queue ID state=%q err=%v, want untouched", otherState, err)
	}
	for _, job := range []AltMountJobContext{first, second} {
		var workflowState, attention, actor string
		var events int
		if err := store.Pool.QueryRow(ctx, `SELECT state,COALESCE(attention_code,'') FROM workflows WHERE id=$1`, job.Workflow.ID.String()).Scan(&workflowState, &attention); err != nil || workflowState != "queued" || attention != "" {
			t.Fatalf("resumed workflow state=%q attention=%q err=%v", workflowState, attention, err)
		}
		if err := store.Pool.QueryRow(ctx, `SELECT actor FROM workflow_transitions WHERE workflow_id=$1 AND from_state='needs_attention' AND to_state='queued'`, job.Workflow.ID.String()).Scan(&actor); err != nil || actor != "worker" {
			t.Fatalf("resume transition actor=%q err=%v", actor, err)
		}
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='altmount.reconcile_requested' AND aggregate_revision=$2`, job.JobID.String(), int64(job.JobRevision)+1).Scan(&events); err != nil || events != 1 {
			t.Fatalf("reconcile events=%d err=%v, want 1 at the new revision", events, err)
		}
	}

	loaded, err := store.LoadAltMountJob(ctx, first.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.JobState != "queued" || loaded.AutoRetryCount != 0 || loaded.AutoRetryDueAt == nil || loaded.ErrorDetail == "" {
		t.Fatalf("resumed job=%+v, want queued with a due retry and the kept failure", loaded)
	}

	// The first claim wins; a second job within the spacing waits.
	claimed, _, err := store.ClaimAltMountAutoRetry(ctx, loaded, now, 90*time.Second)
	if err != nil || !claimed {
		t.Fatalf("first claim=%v err=%v", claimed, err)
	}
	secondLoaded, err := store.LoadAltMountJob(ctx, second.JobID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, free, err := store.ClaimAltMountAutoRetry(ctx, secondLoaded, now.Add(10*time.Second), 90*time.Second)
	if err != nil || claimed || !free.Equal(now.Add(90*time.Second).Truncate(time.Microsecond)) {
		t.Fatalf("paced claim=%v free=%v err=%v, want refused until %v", claimed, free, err, now.Add(90*time.Second))
	}
	if claimed, _, err := store.ClaimAltMountAutoRetry(ctx, secondLoaded, now.Add(91*time.Second), 90*time.Second); err != nil || !claimed {
		t.Fatalf("claim after spacing=%v err=%v", claimed, err)
	}
	afterClaim, err := store.LoadAltMountJob(ctx, first.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if afterClaim.AutoRetryCount != 1 || afterClaim.AutoRetryDueAt != nil || afterClaim.JobRevision != loaded.JobRevision+1 {
		t.Fatalf("after claim count=%d due=%v revision=%d", afterClaim.AutoRetryCount, afterClaim.AutoRetryDueAt, afterClaim.JobRevision)
	}
	// A stale context cannot plan or claim.
	if err := store.ScheduleAltMountAutoRetry(ctx, loaded, "again", now.Add(time.Hour)); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("stale schedule err=%v, want ErrConcurrentModification", err)
	}
	if err := store.ScheduleAltMountAutoRetry(ctx, afterClaim, "fast-fail validation inconclusive: context deadline exceeded", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	planned, err := store.LoadAltMountJob(ctx, first.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if planned.AutoRetryDueAt == nil || !planned.AutoRetryDueAt.Equal(now.Add(time.Hour).Truncate(time.Microsecond)) {
		t.Fatalf("planned due=%v", planned.AutoRetryDueAt)
	}

	// Giving up excludes the release and returns the workflow to its round.
	if err := store.GiveUpAltMountImport(ctx, planned, "AltMount could not import this release after 4 automatic retries: timeout", now); err != nil {
		t.Fatal(err)
	}
	var workflowState, attention, outcome, kind string
	if err := store.Pool.QueryRow(ctx, `SELECT state,COALESCE(attention_code,'') FROM workflows WHERE id=$1`, first.Workflow.ID.String()).Scan(&workflowState, &attention); err != nil {
		t.Fatal(err)
	}
	if workflowState != "waiting" || attention == "altmount_job_attention" {
		t.Fatalf("after give-up state=%q attention=%q, want waiting without owner attention", workflowState, attention)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT acquisition_outcome FROM altmount_jobs WHERE id=$1`, first.JobID.String()).Scan(&outcome); err != nil || outcome != ImportFailedCode {
		t.Fatalf("job outcome=%q err=%v", outcome, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT failure_kind FROM workflow_release_exclusions WHERE workflow_id=$1 AND candidate_id=$2`, first.Workflow.ID.String(), first.CandidateID.String()).Scan(&kind); err != nil || kind != ImportFailedCode {
		t.Fatalf("exclusion kind=%q err=%v", kind, err)
	}
}

// TestOwnerCanStillCancelOrDeleteDuringAutomaticRetryPause proves a title
// whose failed import waits hours for its next automatic retry is not stuck
// as "downloading": cancelling the workflow or deleting the title still works,
// and the stopped retry is never picked up again.
func TestOwnerCanStillCancelOrDeleteDuringAutomaticRetryPause(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()

	parked := func(title, tmdb, key string, queueID int64) (catalog.MediaItem, AltMountJobContext) {
		t.Helper()
		media, workflowID := fixture.movieWorkflow(t, title, tmdb)
		job, _, _ := seedSubtitleAttempt(t, ctx, store, workflowID, fixture.altMount.ID, fixture.indexer.ID, 1, key)
		if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='queued',external_queue_id=$2 WHERE id=$1`, job.JobID.String(), queueID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='queued',revision=revision+1 WHERE id=$1`, workflowID.String()); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.LoadAltMountJob(ctx, job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ScheduleAltMountAutoRetry(ctx, loaded, "fast-fail validation inconclusive: context deadline exceeded", now.Add(4*time.Hour)); err != nil {
			t.Fatal(err)
		}
		return media, loaded
	}
	assertStopped := func(job AltMountJobContext) {
		t.Helper()
		var jobState string
		var due *time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT state,auto_retry_due_at FROM altmount_jobs WHERE id=$1`, job.JobID.String()).Scan(&jobState, &due); err != nil {
			t.Fatal(err)
		}
		if jobState != "needs_attention" || due != nil {
			t.Fatalf("stopped job state=%q due=%v, want needs_attention without a planned retry", jobState, due)
		}
	}

	_, cancelJob := parked("Auto Retry Cancel", "592001", "auto-retry-cancel", 9201)
	cancelled, err := store.CancelWorkflowForOwner(ctx, cancelJob.Workflow.ID, now)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatalf("cancel during retry pause state=%q err=%v", cancelled.State, err)
	}
	assertStopped(cancelJob)

	media, deleteJob := parked("Auto Retry Delete", "592002", "auto-retry-delete", 9202)
	if count, err := store.CancelOpenWorkOfMedia(ctx, media.ID, now); err != nil || count != 1 {
		t.Fatalf("cancel open work during retry pause count=%d err=%v", count, err)
	}
	assertStopped(deleteJob)
	if resumed, err := store.ResumeAltMountAttention(ctx, now, 10); err != nil || resumed != 0 {
		t.Fatalf("resumed=%d err=%v, want stopped retries left alone", resumed, err)
	}
}
