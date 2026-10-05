package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

// OpenKnownAcquisitionRound opens one bounded acquisition round from releases
// Media Manager has already observed and judged eligible under the profile's
// current policy. An exhausted round otherwise waits for a genuinely new post
// even when never-attempted, never-excluded eligible releases are known.
//
// The round opens from the best known candidate directly instead of waiting
// for a search to re-observe it. Candidate order prefers explicit Danish
// subtitle evidence, then a Danish/Nordic title marker, then decision score,
// and exhausted workflows still honor the retained failed-output limit.
func (store *Store) OpenKnownAcquisitionRound(ctx context.Context, workflowID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var workflowState workflows.State
	var workflowRevision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&workflowState, &workflowRevision); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if workflowState != workflows.StateWaiting && workflowState != workflows.StateHeld {
		return refuse("round_not_waiting", "workflow is not waiting for a new acquisition round", "Only waiting workflows with an exhausted round can open a new round.")
	}
	var openRounds int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, workflowID.String()).Scan(&openRounds); err != nil {
		return err
	}
	if openRounds != 0 {
		return refuse("round_already_open", "workflow already has an open acquisition round", "Use search; the open round still has slots.")
	}
	var retainedOutputs int
	if err := transaction.QueryRow(ctx, `SELECT count(DISTINCT job.id) FROM altmount_jobs job
		JOIN altmount_output_inventories inventory ON inventory.job_id=job.id
		WHERE job.workflow_id=$1 AND job.acquisition_outcome IS NOT NULL AND job.acquisition_outcome <> 'output_verified'`, workflowID.String()).Scan(&retainedOutputs); err != nil {
		return err
	}
	if retainedOutputs >= store.AcquisitionLimits().RetainedFailedOutputs {
		return refuse("retained_output_limit_reached", "retained failed-output limit reached; owner storage review is required before another round", "The owner must review retained failed outputs (or raise ACQUISITION_RETAINED_OUTPUT_LIMIT) before another round.")
	}
	var policyVersion int64
	var policy json.RawMessage
	if err := transaction.QueryRow(ctx, `SELECT profile.policy_version,profile.policy
		FROM workflows workflow JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
		WHERE workflow.id=$1`, workflowID.String()).Scan(&policyVersion, &policy); err != nil {
		return err
	}
	var candidateText string
	err = transaction.QueryRow(ctx, `SELECT candidate.id::text
		FROM release_candidates candidate
		JOIN candidate_decisions decision ON decision.workflow_id=candidate.workflow_id AND decision.candidate_id=candidate.id AND decision.policy_version=$2
		JOIN release_observations observation ON observation.id=candidate.observation_id
		WHERE candidate.workflow_id=$1 AND decision.verdict='eligible'
		  AND NOT EXISTS (SELECT 1 FROM workflow_attempted_releases attempted WHERE attempted.workflow_id=candidate.workflow_id AND attempted.candidate_id=candidate.id)
		  AND NOT EXISTS (SELECT 1 FROM workflow_release_exclusions exclusion WHERE exclusion.workflow_id=candidate.workflow_id AND exclusion.indexer_instance_id=candidate.indexer_instance_id AND exclusion.native_key=candidate.native_key)`+" AND NOT "+knownSubtitleFailureSQL("candidate")+`
		ORDER BY
		  (EXISTS (SELECT 1 FROM jsonb_array_elements(observation.normalized_facts) fact WHERE fact->>'name'='subtitle_languages' AND fact->'values' @> '["da"]'::jsonb)) DESC,
		  (observation.title ~* '(nordic|danish|dansub|dksubs|dansk)') DESC,
		  decision.score DESC,
		  candidate.first_seen_sequence
		LIMIT 1`, workflowID.String(), policyVersion).Scan(&candidateText)
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse("no_known_eligible_release", "no known eligible release is available for a new acquisition round", "Wait for paced discovery or queue one with discover.")
	}
	if err != nil {
		return err
	}
	candidateID, err := uuid.Parse(candidateText)
	if err != nil {
		return err
	}
	var maxOrdinal int
	if err := transaction.QueryRow(ctx, `SELECT COALESCE(max(round_ordinal),0) FROM acquisition_rounds WHERE workflow_id=$1`, workflowID.String()).Scan(&maxOrdinal); err != nil {
		return err
	}
	var baseline string
	if err := transaction.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'') FROM workflows requested
		LEFT JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
		LEFT JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
		WHERE requested.id=$1`, workflowID.String()).Scan(&baseline); err != nil {
		return err
	}
	roundID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
		VALUES($1,$2,$3,'open',$4,0,$5,$6,$7,$8)`, roundID.String(), workflowID.String(), maxOrdinal+1, store.AcquisitionLimits().AttemptsPerRound, policyVersion, policy, baseline, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='paused',next_rescan_at=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$2 WHERE workflow_id=$1 AND state IN ('waiting','ready')`, workflowID.String(), now.UTC()); err != nil {
		return err
	}
	newRevision := workflowRevision + 1
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,grab_attempt_id=NULL,revision=$3,updated_at=$4,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$5`, workflowID.String(), candidateID.String(), newRevision, now.UTC(), workflowRevision); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO candidate_selections(workflow_id,candidate_id,selected_at,actor,reason) VALUES($1,$2,$3,'owner','owner opened a round from a known eligible release')
		ON CONFLICT(workflow_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id,selected_at=EXCLUDED.selected_at,actor=EXCLUDED.actor,reason=EXCLUDED.reason`, workflowID.String(), candidateID.String(), now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'candidate_selected','owner opened a new bounded round from a known eligible release','owner',$4)`, workflowID.String(), newRevision, workflowState, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "candidate_id": candidateID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow',$2,$3,'release.retrieve_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), newRevision, string(payload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// RevivePreSubmissionRejections removes the archive rejections that predate
// RAR/7-Zip submission support: the package screen refused those releases
// before AltMount ever saw them, yet each consumed an attempt slot and kept
// the release excluded. Only jobs that never reached AltMount
// (external_queue_id IS NULL) with the archive_unsupported outcome are
// revived; queue-time archive failures stay excluded because AltMount actually
// tried them. The freed jobs are deleted so the round slot accounting and the
// per-package submission guard treat the releases as never attempted.
func (store *Store) RevivePreSubmissionRejections(ctx context.Context, workflowID uuid.UUID, now time.Time) (int, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	var exists bool
	if err := transaction.QueryRow(ctx, `SELECT true FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	rows, err := transaction.Query(ctx, `SELECT id::text FROM altmount_jobs
		WHERE workflow_id=$1 AND acquisition_outcome='archive_unsupported' AND external_queue_id IS NULL`, workflowID.String())
	if err != nil {
		return 0, err
	}
	jobIDs := []string{}
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			rows.Close()
			return 0, err
		}
		jobIDs = append(jobIDs, jobID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(jobIDs) == 0 {
		return 0, transaction.Commit(ctx)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM workflow_package_exclusions WHERE job_id::text = ANY($1)`, jobIDs); err != nil {
		return 0, err
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM workflow_submitted_packages WHERE first_job_id::text = ANY($1)`, jobIDs); err != nil {
		return 0, err
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM workflow_attempted_releases WHERE workflow_id=$1 AND candidate_id IN (SELECT candidate_id FROM altmount_jobs WHERE id::text = ANY($2))`, workflowID.String(), jobIDs); err != nil {
		return 0, err
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM workflow_release_exclusions WHERE workflow_id=$1 AND candidate_id IN (SELECT candidate_id FROM altmount_jobs WHERE id::text = ANY($2))`, workflowID.String(), jobIDs); err != nil {
		return 0, err
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM altmount_jobs WHERE id::text = ANY($1)`, jobIDs); err != nil {
		return 0, err
	}
	detail, _ := json.Marshal(map[string]any{"workflow_id": workflowID.String(), "revived_jobs": len(jobIDs)})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','workflow.archive_rejections_revived','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), string(detail), now.UTC()); err != nil {
		return 0, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return 0, err
	}
	return len(jobIDs), nil
}
