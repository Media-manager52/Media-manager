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
	// PartialContent reports that a requested season that is not yet
	// available already has at least one Plex-confirmed episode workflow.
	PartialContent bool
}

// seerrSeasonAvailableSQL decides whether one requested Seerr season is
// available. It expects the enclosing query to alias seerr_request_seasons as
// season and to bind the media ID as $2. A season is available when Seerr
// already reports it available, when the season workflow tracked by the request
// is Plex-confirmed, or when every episode frozen in that season workflow's
// latest expectation has its own Plex-confirmed single-episode workflow.
// Airing seasons and seasons whose releases are only posted per episode are
// acquired through those episode workflows, so the season workflow itself never
// reaches plex_confirmed. An empty expectation never counts as coverage.
const seerrSeasonAvailableSQL = `(season.availability_status=5 OR EXISTS(
		SELECT 1 FROM seerr_request_workflows provenance
		JOIN workflows workflow ON workflow.id=provenance.workflow_id
		LEFT JOIN LATERAL (
			SELECT expectation_set.id FROM pack_expectation_sets expectation_set
			WHERE expectation_set.workflow_id=workflow.id
			ORDER BY expectation_set.frozen_at DESC,expectation_set.id DESC LIMIT 1
		) expectation ON true
		WHERE provenance.seerr_instance_id=season.seerr_instance_id
		AND provenance.request_id=season.request_id AND workflow.media_id=$2
		AND workflow.variant=('season:' || lpad(season.season_number::text,2,'0'))
		AND (workflow.state='plex_confirmed' OR (
			expectation.id IS NOT NULL
			AND EXISTS(SELECT 1 FROM pack_expected_episodes expected WHERE expected.expectation_id=expectation.id)
			AND NOT EXISTS(
				SELECT 1 FROM pack_expected_episodes expected
				WHERE expected.expectation_id=expectation.id
				AND NOT EXISTS(
					SELECT 1 FROM workflows episode
					WHERE episode.media_id=$2 AND episode.state='plex_confirmed'
					AND episode.variant=('season:' || lpad(expected.season::text,2,'0') || ':episode:' || lpad(expected.episode::text,2,'0'))
				)
			)
		))
	))`

// seerrSeasonPartialSQL reports whether a requested season has at least one
// Plex-confirmed single-episode workflow. Same aliases and bindings as
// seerrSeasonAvailableSQL.
const seerrSeasonPartialSQL = `EXISTS(
		SELECT 1 FROM workflows episode
		WHERE episode.media_id=$2 AND episode.state='plex_confirmed'
		AND episode.variant LIKE ('season:' || lpad(season.season_number::text,2,'0') || ':episode:%')
	)`

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
		bool_or(`+seerrSeasonAvailableSQL+`) AS available,
		bool_or(`+seerrSeasonPartialSQL+`) AS partial
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
		var available, partial bool
		if err := rows.Scan(&season, &available, &partial); err != nil {
			rows.Close()
			return SeerrStatusSyncContext{}, err
		}
		seasonCount++
		if available {
			result.AvailableSeasons = append(result.AvailableSeasons, season)
		} else {
			result.AllAvailable = false
			if partial {
				result.PartialContent = true
			}
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
	// A confirmed workflow qualifies when an approved request tracks it, or when
	// it is a single-episode workflow whose season workflow an approved request
	// tracks (episodes are dispatched without their own request provenance).
	// One status sync reads the whole media's current state, so an episode is
	// skipped when any status sync for the same media was queued after it was
	// confirmed, and at most one episode per media is queued per call.
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,workflow.revision,workflow.media_id::text,workflow.variant LIKE '%:episode:%'
		FROM workflows workflow
		WHERE workflow.state='plex_confirmed'
		AND EXISTS(
			SELECT 1 FROM seerr_request_workflows provenance
			JOIN seerr_request_snapshots snapshot
			  ON snapshot.seerr_instance_id=provenance.seerr_instance_id AND snapshot.request_id=provenance.request_id
			JOIN workflows tracked ON tracked.id=provenance.workflow_id
			WHERE provenance.seerr_instance_id=$1 AND snapshot.state='2'
			AND (tracked.id=workflow.id OR (
				tracked.media_id=workflow.media_id AND tracked.variant LIKE 'season:%'
				AND tracked.variant NOT LIKE '%:episode:%'
				AND workflow.variant LIKE tracked.variant || ':episode:%'
			))
		)
		AND NOT EXISTS(
			SELECT 1 FROM outbox_events event
			WHERE event.aggregate_type='workflow' AND event.aggregate_id=workflow.id
			AND event.aggregate_revision=workflow.revision AND event.event_type='seerr.status_sync_requested'
		)
		AND (workflow.variant NOT LIKE '%:episode:%' OR NOT EXISTS(
			SELECT 1 FROM workflows sibling
			JOIN outbox_events event ON event.aggregate_type='workflow' AND event.aggregate_id=sibling.id
			WHERE sibling.media_id=workflow.media_id AND event.event_type='seerr.status_sync_requested'
			AND event.occurred_at>=workflow.terminal_at
		))
		ORDER BY workflow.terminal_at,workflow.id LIMIT $2`, instanceID, limit)
	if err != nil {
		return 0, err
	}
	type confirmedWorkflow struct {
		id       string
		revision int64
	}
	var workflowsToQueue []confirmedWorkflow
	episodeMedia := map[string]struct{}{}
	for rows.Next() {
		var item confirmedWorkflow
		var mediaID string
		var episode bool
		if err := rows.Scan(&item.id, &item.revision, &mediaID, &episode); err != nil {
			rows.Close()
			return 0, err
		}
		if episode {
			if _, queued := episodeMedia[mediaID]; queued {
				continue
			}
			episodeMedia[mediaID] = struct{}{}
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

// queueSeerrRequestStatusSync queues one Seerr availability sync for a request
// snapshot when Plex already holds confirmed content for one of its requested
// variants: the movie, a whole season, or any single episode of a requested
// season. The event is keyed by the request and its snapshot revision rather
// than by a workflow revision, so attaching a new request to content that was
// already synced for an earlier request still reaches Seerr, while replaying
// the same snapshot stays idempotent. The payload names one confirmed workflow
// because the status handler loads the media's state through it.
func queueSeerrRequestStatusSync(ctx context.Context, transaction pgx.Tx, instanceID, requestID string, snapshotRevision int64, mediaID uuid.UUID, variants []string, now time.Time) error {
	if len(variants) == 0 {
		return nil
	}
	_, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		SELECT $1,'seerr_request',md5($2::text || ':' || $3::text)::uuid,$4,'seerr.status_sync_requested',jsonb_build_object('workflow_id',workflow.id::text),$5,$5
		FROM workflows workflow
		WHERE workflow.media_id=$6 AND workflow.state='plex_confirmed'
		AND (workflow.variant=ANY($7::text[]) OR EXISTS(
			SELECT 1 FROM unnest($7::text[]) requested(variant)
			WHERE requested.variant LIKE 'season:%' AND workflow.variant LIKE requested.variant || ':episode:%'
		))
		ORDER BY workflow.terminal_at DESC NULLS LAST,workflow.id LIMIT 1
		ON CONFLICT DO NOTHING`, uuid.NewV7().String(), instanceID, requestID, snapshotRevision, now.UTC(), mediaID.String(), variants)
	return err
}

func seerrStatusEventPayload(workflowID uuid.UUID) string {
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String()})
	return string(payload)
}
