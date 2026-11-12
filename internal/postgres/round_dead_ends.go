package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/media-manager/media-manager/internal/workflows"
)

// roundDeadEndCodes are attention codes that only say the open round has no
// untried release left: every remaining release was a duplicate package or
// its NZB could not be fetched. They need no owner decision; the workflow
// waits for a newly observed release like a round whose slots are used up.
var roundDeadEndCodes = []string{
	"duplicate_package_no_alternate", "duplicate_package_skip_limit",
	"release_retrieval_failed", "release_retrieval_limit", "release_retrieval_round_full",
}

func roundDeadEndCode(code string) bool {
	for _, deadEnd := range roundDeadEndCodes {
		if code == deadEnd {
			return true
		}
	}
	return false
}

// roundDeadEndToWaiting closes the workflow's open round as exhausted and
// lets the workflow wait for a newly observed release instead of asking the
// owner, inside the caller's transaction. Paced discovery then opens the next
// round automatically once a new eligible release is seen. It reports false,
// having written nothing, when the workflow has no open round, an indexer
// refused NZB access (HTTP 401/403) in the round, which is an account problem
// the owner must fix, or an AltMount job of the workflow is still active.
func roundDeadEndToWaiting(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID, closeReason, detail string, now time.Time) (bool, error) {
	var state workflows.State
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 AND terminal_at IS NULL FOR UPDATE`, workflowID.String()).Scan(&state, &revision); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var roundText string
	var accessRejected, activeJob bool
	err := transaction.QueryRow(ctx, `SELECT round.id::text,
		EXISTS(SELECT 1 FROM workflow_release_exclusions denied WHERE denied.retrieval_round_id=round.id
			AND denied.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403')),
		EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=round.workflow_id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		FROM acquisition_rounds round WHERE round.workflow_id=$1 AND round.state='open' FOR UPDATE OF round`, workflowID.String()).Scan(&roundText, &accessRejected, &activeJob)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if accessRejected || activeJob {
		return false, nil
	}
	if err := exhaustOpenRound(ctx, transaction, workflowID, roundText, closeReason, now); err != nil {
		return false, err
	}
	waitingDetail := "No untried release is left in this round (" + detail + "). Scheduled discovery waits for a newly observed eligible release and then opens the next round automatically."
	steps := int64(2)
	if state == workflows.StateNeedsAttention {
		steps = 1
	}
	command, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,grab_attempt_id=NULL,revision=$2,updated_at=$3,attention_code=$4,attention_detail=$5 WHERE id=$1 AND revision=$6`,
		workflowID.String(), revision+steps, now.UTC(), workflows.AttentionRoundExhaustedWaitingNewIdentity, waitingDetail, revision)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() != 1 {
		return false, ErrConcurrentModification
	}
	if steps == 2 {
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention',$4,'worker',$5)`, workflowID.String(), revision+1, state, detail, now.UTC()); err != nil {
			return false, err
		}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'needs_attention','waiting',$3,'worker',$4)`, workflowID.String(), revision+steps, waitingDetail, now.UTC()); err != nil {
		return false, err
	}
	return true, nil
}

// ResumeRoundDeadEnds hands workflows that stopped at owner attention only
// because their round had no untried release left to the same wait for a new
// release. Rounds with an indexer access rejection stay with the owner.
func (store *Store) ResumeRoundDeadEnds(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,workflow.attention_code,COALESCE(workflow.attention_detail,'') FROM workflows workflow
		WHERE workflow.state='needs_attention' AND workflow.terminal_at IS NULL AND workflow.attention_code = ANY($1)
		  AND EXISTS(SELECT 1 FROM acquisition_rounds round WHERE round.workflow_id=workflow.id AND round.state='open'
		    AND NOT EXISTS(SELECT 1 FROM workflow_release_exclusions denied WHERE denied.retrieval_round_id=round.id
		      AND denied.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403')))
		  AND NOT EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=workflow.id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		ORDER BY workflow.updated_at,workflow.id LIMIT $2`, roundDeadEndCodes, limit)
	if err != nil {
		return 0, err
	}
	type deadEnd struct{ id, code, detail string }
	items := []deadEnd{}
	for rows.Next() {
		var item deadEnd
		if err := rows.Scan(&item.id, &item.code, &item.detail); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	resumed := 0
	for _, item := range items {
		id, err := uuid.Parse(item.id)
		if err != nil {
			return resumed, err
		}
		ok, err := store.resumeRoundDeadEnd(ctx, id, item.code, item.detail, now)
		if err != nil {
			return resumed, err
		}
		if ok {
			resumed++
		}
	}
	return resumed, nil
}

func (store *Store) resumeRoundDeadEnd(ctx context.Context, workflowID uuid.UUID, code, detail string, now time.Time) (bool, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	var current string
	if err := transaction.QueryRow(ctx, `SELECT COALESCE(attention_code,'') FROM workflows WHERE id=$1 AND state='needs_attention' FOR UPDATE`, workflowID.String()).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if current != code {
		return false, nil
	}
	if detail == "" {
		detail = code
	}
	ok, err := roundDeadEndToWaiting(ctx, transaction, workflowID, code, detail, now)
	if err != nil || !ok {
		return false, err
	}
	return true, transaction.Commit(ctx)
}
