package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

type SeerrStatusSyncContext struct {
	MediaID          uuid.UUID
	Kind             catalog.MediaKind
	Identities       []catalog.ExternalIdentity
	RequestIDs       []string
	AvailableSeasons []int16
	AllAvailable     bool
}

func (store *Store) LoadSeerrStatusSyncContext(ctx context.Context, instanceID string, workflowID uuid.UUID) (SeerrStatusSyncContext, error) {
	var result SeerrStatusSyncContext
	var mediaText string
	var state workflows.State
	err := store.Pool.QueryRow(ctx, `SELECT workflow.media_id::text,media.kind,workflow.state
		FROM workflows workflow JOIN media_items media ON media.id=workflow.media_id
		WHERE workflow.id=$1`, workflowID.String()).Scan(&mediaText, &result.Kind, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return SeerrStatusSyncContext{}, ErrNotFound
	}
	if err != nil {
		return SeerrStatusSyncContext{}, err
	}
	if state != workflows.StatePlexConfirmed {
		return SeerrStatusSyncContext{}, ErrConcurrentModification
	}
	result.MediaID, err = uuid.Parse(mediaText)
	if err != nil {
		return SeerrStatusSyncContext{}, err
	}
	result.Identities, err = store.identities(ctx, result.MediaID)
	if err != nil {
		return SeerrStatusSyncContext{}, err
	}

	rows, err := store.Pool.Query(ctx, `SELECT DISTINCT snapshot.request_id
		FROM seerr_request_snapshots snapshot
		WHERE snapshot.seerr_instance_id=$1 AND snapshot.state='2'
		AND EXISTS(
			SELECT 1 FROM seerr_request_workflows provenance
			JOIN workflows mapped ON mapped.id=provenance.workflow_id
			WHERE provenance.seerr_instance_id=snapshot.seerr_instance_id
			AND provenance.request_id=snapshot.request_id AND mapped.media_id=$2
		)
		ORDER BY snapshot.request_id`, instanceID, result.MediaID.String())
	if err != nil {
		return SeerrStatusSyncContext{}, err
	}
	for rows.Next() {
		var requestID string
		if err := rows.Scan(&requestID); err != nil {
			rows.Close()
			return SeerrStatusSyncContext{}, err
		}
		result.RequestIDs = append(result.RequestIDs, requestID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SeerrStatusSyncContext{}, err
	}
	rows.Close()
	if len(result.RequestIDs) == 0 || result.Kind == catalog.KindMovie {
		result.AllAvailable = len(result.RequestIDs) > 0
		return result, nil
	}

	rows, err = store.Pool.Query(ctx, `SELECT season.season_number,
		bool_or(season.availability_status=5 OR EXISTS(
			SELECT 1 FROM seerr_request_workflows provenance
			JOIN workflows workflow ON workflow.id=provenance.workflow_id
			WHERE provenance.seerr_instance_id=season.seerr_instance_id
			AND provenance.request_id=season.request_id AND workflow.media_id=$2
			AND workflow.variant=('season:' || lpad(season.season_number::text,2,'0'))
			AND workflow.state='plex_confirmed'
		)) AS available
		FROM seerr_request_seasons season
		JOIN seerr_request_snapshots snapshot
		  ON snapshot.seerr_instance_id=season.seerr_instance_id AND snapshot.request_id=season.request_id
		WHERE season.seerr_instance_id=$1 AND snapshot.state='2' AND season.requested
		AND EXISTS(
			SELECT 1 FROM seerr_request_workflows provenance
			JOIN workflows mapped ON mapped.id=provenance.workflow_id
			WHERE provenance.seerr_instance_id=season.seerr_instance_id
			AND provenance.request_id=season.request_id AND mapped.media_id=$2
		)
		GROUP BY season.season_number ORDER BY season.season_number`, instanceID, result.MediaID.String())
	if err != nil {
		return SeerrStatusSyncContext{}, err
	}
	result.AllAvailable = true
	seasonCount := 0
	for rows.Next() {
		var season int16
		var available bool
		if err := rows.Scan(&season, &available); err != nil {
			rows.Close()
			return SeerrStatusSyncContext{}, err
		}
		seasonCount++
		if available {
			result.AvailableSeasons = append(result.AvailableSeasons, season)
		} else {
			result.AllAvailable = false
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SeerrStatusSyncContext{}, err
	}
	rows.Close()
	if seasonCount == 0 {
		return SeerrStatusSyncContext{}, errors.New("approved Seerr series request has no season evidence")
	}
	return result, nil
}

func (store *Store) QueueConfirmedSeerrStatusSyncs(ctx context.Context, instanceID string, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,workflow.revision
		FROM workflows workflow
		WHERE workflow.state='plex_confirmed'
		AND EXISTS(
			SELECT 1 FROM seerr_request_workflows provenance
			JOIN seerr_request_snapshots snapshot
			  ON snapshot.seerr_instance_id=provenance.seerr_instance_id AND snapshot.request_id=provenance.request_id
			WHERE provenance.workflow_id=workflow.id AND provenance.seerr_instance_id=$1 AND snapshot.state='2'
		)
		AND NOT EXISTS(
			SELECT 1 FROM outbox_events event
			WHERE event.aggregate_type='workflow' AND event.aggregate_id=workflow.id
			AND event.aggregate_revision=workflow.revision AND event.event_type='seerr.status_sync_requested'
		)
		ORDER BY workflow.terminal_at,workflow.id LIMIT $2`, instanceID, limit)
	if err != nil {
		return 0, err
	}
	type confirmedWorkflow struct {
		id       string
		revision int64
	}
	var workflowsToQueue []confirmedWorkflow
	for rows.Next() {
		var item confirmedWorkflow
		if err := rows.Scan(&item.id, &item.revision); err != nil {
			rows.Close()
			return 0, err
		}
		workflowsToQueue = append(workflowsToQueue, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	queued := 0
	for _, item := range workflowsToQueue {
		payload, _ := json.Marshal(map[string]string{"workflow_id": item.id})
		command, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
			VALUES($1,'workflow',$2,$3,'seerr.status_sync_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), item.id, item.revision, payload, now.UTC())
		if err != nil {
			return queued, err
		}
		if command.RowsAffected() == 1 {
			queued++
		}
	}
	return queued, nil
}

func seerrStatusEventPayload(workflowID uuid.UUID) string {
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String()})
	return string(payload)
}
