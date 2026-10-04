package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/packs"
)

// RequestRetainedMovieInspection authorizes one inspection of the last failed
// movie inventory after its round exhausted. It creates no job or acquisition
// round and preserves exclusions and the original frozen inventory.
func (store *Store) RequestRetainedMovieInspection(ctx context.Context, workflowID, jobID uuid.UUID, now time.Time) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revision int64
	var candidateText string
	err = tx.QueryRow(ctx, `SELECT workflow.revision,job.candidate_id::text
		FROM workflows workflow JOIN media_items media ON media.id=workflow.media_id AND media.kind='movie'
		JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id AND profile.enabled
		JOIN altmount_jobs job ON job.workflow_id=workflow.id AND job.id=$2
		JOIN acquisition_rounds round ON round.id=job.round_id AND round.workflow_id=workflow.id
		WHERE workflow.id=$1 AND workflow.state='waiting' AND workflow.terminal_at IS NULL
		 AND workflow.grab_attempt_id IS NULL AND round.state='exhausted' AND round.policy_version=profile.policy_version
		 AND job.state='needs_attention' AND job.error_code='movie_primary_ambiguous' AND job.acquisition_closed_at IS NOT NULL
		 AND job.round_slot=round.attempt_limit_snapshot
		 AND NOT EXISTS(SELECT 1 FROM acquisition_rounds newer WHERE newer.workflow_id=workflow.id AND newer.round_ordinal>round.round_ordinal)
		 AND NOT EXISTS(SELECT 1 FROM altmount_jobs newer WHERE newer.workflow_id=workflow.id AND newer.attempt_generation>job.attempt_generation)
		 AND EXISTS(SELECT 1 FROM altmount_output_inventories inventory JOIN altmount_output_assets asset ON asset.inventory_id=inventory.id WHERE inventory.job_id=job.id)
		 AND NOT EXISTS(SELECT 1 FROM subtitle_verifications verification WHERE verification.altmount_job_id=job.id)
		 AND NOT EXISTS(SELECT 1 FROM audit_events audit WHERE audit.action='output.movie_reinspection_requested' AND audit.aggregate_type='altmount_job' AND audit.aggregate_id=job.id)
		 AND NOT EXISTS(SELECT 1 FROM altmount_jobs active WHERE active.workflow_id=workflow.id AND active.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		 AND NOT EXISTS(SELECT 1 FROM library_link_versions version JOIN workflows published ON published.id=version.workflow_id WHERE published.media_id=workflow.media_id AND published.variant=workflow.variant AND version.state='current')
		 AND NOT EXISTS(SELECT 1 FROM library_link_versions version WHERE version.workflow_id=workflow.id)
		FOR UPDATE OF workflow,job,round`, workflowID.String(), jobID.String()).Scan(&revision, &candidateText)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_jobs WHERE id=$2 AND workflow_id=$1)`, workflowID.String(), jobID.String()).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrConcurrentModification
		}
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflows SET state='queued',selected_release_id=$2,grab_attempt_id=$3,revision=revision+1,attention_code=NULL,attention_detail=NULL,updated_at=$4 WHERE id=$1 AND revision=$5`, workflowID.String(), candidateText, jobID.String(), now.UTC(), revision); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_retry_discovery SET state='paused',rescan_generation=rescan_generation+1,next_rescan_at=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$2 WHERE workflow_id=$1`, workflowID.String(), now.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'waiting','queued','owner requested one bounded inspection of the retained movie inventory','owner',$3)`, workflowID.String(), revision+1, now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"workflow_id": workflowID.String(), "workflow_revision": revision + 1, "new_nzb_requests": false, "retained_output": true})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','output.movie_reinspection_requested','altmount_job',$2,$3,$4)`, uuid.NewV7().String(), jobID.String(), detail, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "job_id": jobID.String()})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'altmount.movie_reinspect_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), revision+1, payload, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) LoadRetainedMovieOutput(ctx context.Context, workflowID, jobID uuid.UUID) (RetainedSubtitleOutput, error) {
	var result RetainedSubtitleOutput
	var inventoryText string
	err := store.Pool.QueryRow(ctx, `SELECT inventory.id::text,inventory.root_relative
		FROM workflows workflow JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id AND job.workflow_id=workflow.id AND job.candidate_id=workflow.selected_release_id
		JOIN acquisition_rounds round ON round.id=job.round_id AND round.workflow_id=workflow.id AND round.state='exhausted'
		JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id AND profile.enabled AND profile.policy_version=round.policy_version
		JOIN altmount_output_inventories inventory ON inventory.job_id=job.id
		WHERE workflow.id=$1 AND job.id=$2 AND workflow.state='queued' AND job.state='needs_attention' AND job.error_code='movie_primary_ambiguous'
		 AND EXISTS(SELECT 1 FROM audit_events audit WHERE audit.action='output.movie_reinspection_requested' AND audit.aggregate_id=job.id AND audit.detail->>'workflow_revision'=workflow.revision::text)
		ORDER BY inventory.generation DESC LIMIT 1`, workflowID.String(), jobID.String()).Scan(&inventoryText, &result.RootRelative)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrConcurrentModification
	}
	if err != nil {
		return result, err
	}
	result.InventoryID, err = uuid.Parse(inventoryText)
	if err != nil {
		return result, err
	}
	result.Policy, err = store.SubtitlePolicyForJob(ctx, jobID)
	if err != nil {
		return result, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT id::text,relative_path,size_bytes FROM altmount_output_assets WHERE inventory_id=$1 ORDER BY relative_path`, inventoryText)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var idText string
		var asset packs.ObservedAsset
		if err := rows.Scan(&idText, &asset.RelativePath, &asset.Size); err != nil {
			return result, err
		}
		asset.ID, err = uuid.Parse(idText)
		if err != nil {
			return result, err
		}
		result.Assets = append(result.Assets, asset)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(result.Assets) == 0 {
		return result, ErrConcurrentModification
	}
	return result, nil
}
