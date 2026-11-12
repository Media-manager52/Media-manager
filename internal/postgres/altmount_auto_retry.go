package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// altMountAutoRetryLock serialises the global retry pacing check.
const altMountAutoRetryLock = "altmount_auto_retry"

// altMountAutoRetryReconcileDelay is how long after a requested retry the job
// is read again; AltMount needs a moment to move the item to processing.
const altMountAutoRetryReconcileDelay = 30 * time.Second

// ImportFailedCode is the release failure recorded when AltMount could not
// import a release even after the automatic retries.
const ImportFailedCode = "import_failed"

func trimFailureDetail(detail string) string {
	detail = strings.TrimSpace(strings.ToValidUTF8(detail, ""))
	if runes := []rune(detail); len(runes) > 1000 {
		detail = string(runes[:1000])
	}
	return detail
}

// ScheduleAltMountAutoRetry plans the next automatic AltMount retry of a
// failed job at due and reads the job again then. The workflow keeps its
// state, so nothing asks the owner for a decision while the retry waits; the
// failure detail is kept on the job for display and for a later give-up.
func (store *Store) ScheduleAltMountAutoRetry(ctx context.Context, job AltMountJobContext, detail string, due time.Time) error {
	payload, _ := json.Marshal(map[string]string{"job_id": job.JobID.String(), "workflow_id": job.Workflow.ID.String()})
	command, err := store.Pool.Exec(ctx, `WITH bumped AS (
		UPDATE altmount_jobs job SET auto_retry_due_at=$4,error_detail=NULLIF($6,''),last_reconciled_at=now(),revision=job.revision+1
		WHERE job.id=$1 AND job.revision=$5 AND EXISTS(SELECT 1 FROM workflows workflow WHERE workflow.id=job.workflow_id AND workflow.grab_attempt_id=job.id)
		RETURNING job.revision
	) INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) SELECT $2,'altmount_job',$1,revision,'altmount.reconcile_requested',$3,now(),$4 FROM bumped`,
		job.JobID.String(), uuid.NewV7().String(), string(payload), due.UTC(), int64(job.JobRevision), trimFailureDetail(detail))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

// ClaimAltMountAutoRetry records that Media Manager is about to ask AltMount
// to retry this job. Retries are paced across all jobs: when another retry
// was requested less than spacing ago nothing is written and the earliest
// free time is returned instead. A successful claim counts the retry, clears
// the plan and schedules the next read of the job, so the retry is counted
// even if the request to AltMount then fails.
func (store *Store) ClaimAltMountAutoRetry(ctx context.Context, job AltMountJobContext, now time.Time, spacing time.Duration) (bool, time.Time, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return false, time.Time{}, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, altMountAutoRetryLock); err != nil {
		return false, time.Time{}, err
	}
	var last *time.Time
	if err := transaction.QueryRow(ctx, `SELECT max(auto_retry_requested_at) FROM altmount_jobs WHERE auto_retry_requested_at IS NOT NULL`).Scan(&last); err != nil {
		return false, time.Time{}, err
	}
	if last != nil && now.Before(last.Add(spacing)) {
		return false, last.Add(spacing).UTC(), transaction.Commit(ctx)
	}
	var revision int64
	err = transaction.QueryRow(ctx, `UPDATE altmount_jobs job SET auto_retry_count=job.auto_retry_count+1,auto_retry_requested_at=$2,auto_retry_due_at=NULL,last_reconciled_at=$2,revision=job.revision+1
		WHERE job.id=$1 AND job.revision=$3 AND EXISTS(SELECT 1 FROM workflows workflow WHERE workflow.id=job.workflow_id AND workflow.grab_attempt_id=job.id)
		RETURNING job.revision`, job.JobID.String(), now.UTC(), int64(job.JobRevision)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, ErrConcurrentModification
	}
	if err != nil {
		return false, time.Time{}, err
	}
	payload, _ := json.Marshal(map[string]string{"job_id": job.JobID.String(), "workflow_id": job.Workflow.ID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'altmount_job',$2,$3,'altmount.reconcile_requested',$4,$5,$6)`,
		uuid.NewV7().String(), job.JobID.String(), revision, string(payload), now.UTC(), now.Add(altMountAutoRetryReconcileDelay).UTC()); err != nil {
		return false, time.Time{}, err
	}
	return true, time.Time{}, transaction.Commit(ctx)
}

// GiveUpAltMountImport excludes a release AltMount could not import even after
// the automatic retries and lets the round try the next release, exactly like
// any other release failure (an exhausted round waits for a new release). Only
// a job without an acquisition round, which predates rounds, still stops at
// owner attention.
func (store *Store) GiveUpAltMountImport(ctx context.Context, job AltMountJobContext, detail string, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	_, err = store.recordAttributableAcquisitionFailure(ctx, transaction, job, nil, ImportFailedCode, ImportFailedCode, trimFailureDetail(detail), now)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = transaction.Rollback(ctx)
		return store.CompleteImportWithCode(ctx, job, false, "altmount_job_attention", trimFailureDetail(detail), now)
	}
	if err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// ResumeAltMountAttention picks up workflows that stopped at owner attention
// because their AltMount import failed (altmount_job_attention) and lets the
// automatic retry handle them: the job and workflow return to queued and the
// job is read again, which retries it in AltMount or moves on to the next
// release. Each job is resumed at most once, and the reads are spread out so
// AltMount is not asked about many jobs at the same moment.
func (store *Store) ResumeAltMountAttention(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text FROM workflows workflow
		JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
		WHERE workflow.state='needs_attention' AND workflow.attention_code='altmount_job_attention'
		  AND job.state='needs_attention' AND job.acquisition_closed_at IS NULL AND job.auto_resumed_at IS NULL
		  AND job.external_queue_id IS NOT NULL
		ORDER BY workflow.updated_at,workflow.id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	workflowIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		workflowIDs = append(workflowIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	resumed := 0
	for index, workflowID := range workflowIDs {
		ok, err := store.resumeAltMountAttention(ctx, workflowID, now, now.Add(time.Duration(index)*20*time.Second))
		if err != nil {
			return resumed, err
		}
		if ok {
			resumed++
		}
	}
	return resumed, nil
}

func (store *Store) resumeAltMountAttention(ctx context.Context, workflowID string, now, readAt time.Time) (bool, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	var jobID string
	var workflowRevision, jobRevision int64
	err = transaction.QueryRow(ctx, `SELECT job.id::text,workflow.revision,job.revision FROM workflows workflow
		JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
		WHERE workflow.id=$1 AND workflow.state='needs_attention' AND workflow.attention_code='altmount_job_attention'
		  AND job.state='needs_attention' AND job.acquisition_closed_at IS NULL AND job.auto_resumed_at IS NULL
		  AND job.external_queue_id IS NOT NULL
		FOR UPDATE OF workflow,job`, workflowID).Scan(&jobID, &workflowRevision, &jobRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state='queued',auto_resumed_at=$2,auto_retry_due_at=$2,last_reconciled_at=$2,revision=revision+1 WHERE id=$1`, jobID, now.UTC()); err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='queued',revision=revision+1,updated_at=$2,attention_code=NULL,attention_detail=NULL WHERE id=$1`, workflowID, now.UTC()); err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'needs_attention','queued','automatic recovery: retry the failed AltMount import, or move on to the next release','worker',$3)`, workflowID, workflowRevision+1, now.UTC()); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]string{"job_id": jobID, "workflow_id": workflowID})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'altmount_job',$2,$3,'altmount.reconcile_requested',$4,$5,$6)`,
		uuid.NewV7().String(), jobID, jobRevision+1, string(payload), now.UTC(), readAt.UTC()); err != nil {
		return false, err
	}
	return true, transaction.Commit(ctx)
}

// settleParkedAltMountJobs hands workflows whose failed import is only waiting
// for its next automatic retry back to owner attention, so the owner can
// cancel or delete them as before instead of waiting hours for the retry. A
// retry that AltMount is running already is real work in flight and is left
// alone. A settled job is never resumed automatically again.
func (store *Store) settleParkedAltMountJobs(ctx context.Context, where string, arg string, now time.Time) error {
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text FROM workflows workflow
		JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
		WHERE `+where+` AND job.auto_retry_due_at IS NOT NULL`, arg)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := store.settleParkedAltMountJob(ctx, id, now); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) settleParkedAltMountJob(ctx context.Context, workflowID string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var jobID, state, detail string
	var revision int64
	err = transaction.QueryRow(ctx, `SELECT job.id::text,workflow.state,workflow.revision,COALESCE(job.error_detail,'') FROM workflows workflow
		JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
		WHERE workflow.id=$1 AND job.auto_retry_due_at IS NOT NULL
		  AND job.state IN ('queued','processing','streaming_ready','transport_unknown')
		  AND workflow.state IN ('queued','streaming_ready','transport_unknown')
		FOR UPDATE OF workflow,job`, workflowID).Scan(&jobID, &state, &revision, &detail)
	if errors.Is(err, pgx.ErrNoRows) {
		return transaction.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if detail == "" {
		detail = "AltMount job failed"
	}
	if _, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',error_code='altmount_job_attention',auto_retry_due_at=NULL,auto_resumed_at=COALESCE(auto_resumed_at,$2),last_reconciled_at=$2,revision=revision+1 WHERE id=$1`, jobID, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',attention_code='altmount_job_attention',attention_detail=$3,revision=revision+1,updated_at=$2 WHERE id=$1`, workflowID, now.UTC(), detail); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention','owner stopped the automatic AltMount retry','owner',$4)`, workflowID, revision+1, state, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
