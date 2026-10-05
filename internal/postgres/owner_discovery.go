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

// RequestWorkflowDiscovery advances an exhausted workflow's existing discovery
// schedule. It does not open a round, select a release, or grant download slots.
// The normal guarded background search retains pacing and pending-provider scope.
func (store *Store) RequestWorkflowDiscovery(ctx context.Context, workflowID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	if err := transaction.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != workflows.StateWaiting {
		return errors.New("discovery requires a waiting workflow with an exhausted acquisition round")
	}
	var generation int64
	err = transaction.QueryRow(ctx, `SELECT discovery.rescan_generation FROM workflow_retry_discovery discovery
		JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
		WHERE discovery.workflow_id=$1 AND discovery.state='waiting' AND round.state='exhausted'
		AND round.round_ordinal=(SELECT max(latest.round_ordinal) FROM acquisition_rounds latest WHERE latest.workflow_id=$1)
		AND NOT EXISTS(SELECT 1 FROM acquisition_rounds open_round WHERE open_round.workflow_id=$1 AND open_round.state='open')
		AND NOT EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=$1 AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		FOR UPDATE OF discovery`, workflowID.String()).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("no exhausted round is waiting for discovery; use the available search or round recovery action")
	}
	if err != nil {
		return err
	}
	var indexerAvailable, alreadyQueued bool
	if err := transaction.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM integration_instances i WHERE i.enabled AND i.config->'mode_verified'='true'::jsonb AND (i.kind='newznab' OR (i.kind='oldboys' AND EXISTS(SELECT 1 FROM media_metadata metadata JOIN workflows workflow ON workflow.media_id=metadata.media_id WHERE workflow.id=$1 AND metadata.data->>'original_language'='da')))),
		EXISTS(SELECT 1 FROM outbox_events WHERE aggregate_type='workflow_retry_discovery' AND aggregate_id=$1
		  AND aggregate_revision=$2 AND event_type='workflow.discovery_rescan_requested' AND completed_at IS NULL AND available_at<=$3)`, workflowID.String(), generation, now.UTC()).Scan(&indexerAvailable, &alreadyQueued); err != nil {
		return err
	}
	if !indexerAvailable {
		return errors.New("discovery requires at least one commissioned indexer for this media")
	}
	if alreadyQueued {
		return transaction.Commit(ctx)
	}
	nextGeneration := generation + 1
	if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET rescan_generation=$2,next_rescan_at=$3,updated_at=$3
		WHERE workflow_id=$1 AND rescan_generation=$4`, workflowID.String(), nextGeneration, now.UTC(), generation); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"workflow_id": workflowID.String(), "rescan_generation": nextGeneration})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow_retry_discovery',$2,$3,'workflow.discovery_rescan_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), nextGeneration, payload, now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"rescan_generation": nextGeneration})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at)
		VALUES($1,'owner','workflow.discovery_requested','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
