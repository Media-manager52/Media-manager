package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

type adoptionMonitoringScope struct {
	MediaID           uuid.UUID
	MediaKind         catalog.MediaKind
	Variant           string
	RootID            uuid.UUID
	QualityProfileID  uuid.UUID
	SearchImmediately bool
	ItemCount         int
}

func loadAdoptionMonitoringScopes(ctx context.Context, transaction pgx.Tx, batchID uuid.UUID) ([]adoptionMonitoringScope, error) {
	rows, err := transaction.Query(ctx, `SELECT item.media_id::text,media.kind,item.variant,min(item.root_id::text),min(item.quality_profile_id::text),bool_or(COALESCE(item.monitoring_enabled,false)),bool_or(COALESCE(item.search_immediately,false)),count(*),count(DISTINCT (item.root_id,item.quality_profile_id)),count(DISTINCT (COALESCE(item.monitoring_enabled,false),COALESCE(item.search_immediately,false)))
		FROM adoption_items item JOIN media_items media ON media.id=item.media_id
		WHERE item.batch_id=$1 AND item.review_state='approved'
		GROUP BY item.media_id,media.kind,item.variant ORDER BY item.media_id,item.variant`, batchID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []adoptionMonitoringScope
	for rows.Next() {
		var scope adoptionMonitoringScope
		var mediaText, rootText, profileText string
		var monitored bool
		var configurationCount, decisionCount int
		if err := rows.Scan(&mediaText, &scope.MediaKind, &scope.Variant, &rootText, &profileText, &monitored, &scope.SearchImmediately, &scope.ItemCount, &configurationCount, &decisionCount); err != nil {
			return nil, err
		}
		if configurationCount != 1 {
			return nil, errors.New("one adopted media scope cannot use multiple roots or quality profiles")
		}
		if decisionCount != 1 {
			return nil, errors.New("one adopted media scope cannot mix monitoring or immediate-search decisions")
		}
		if !monitored {
			continue
		}
		scope.MediaID, _ = uuid.Parse(mediaText)
		scope.RootID, _ = uuid.Parse(rootText)
		scope.QualityProfileID, _ = uuid.Parse(profileText)
		result = append(result, scope)
	}
	return result, rows.Err()
}

func (store *Store) validateAdoptionMonitoring(ctx context.Context, transaction pgx.Tx, batchID, defaultRootID uuid.UUID, replacementAuthorized bool) error {
	scopes, err := loadAdoptionMonitoringScopes(ctx, transaction, batchID)
	if err != nil || len(scopes) == 0 {
		return err
	}
	var defaultClientText string
	if err := transaction.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE kind='altmount' AND is_default AND enabled ORDER BY id LIMIT 1`).Scan(&defaultClientText); errors.Is(err, pgx.ErrNoRows) {
		return errors.New("adoption monitoring requires an enabled default AltMount client")
	} else if err != nil {
		return err
	}
	searchRequired := false
	for _, scope := range scopes {
		var mediaLock int
		if err := transaction.QueryRow(ctx, `SELECT 1 FROM media_items WHERE id=$1 FOR UPDATE`, scope.MediaID.String()).Scan(&mediaLock); err != nil {
			return err
		}
		if scope.RootID != defaultRootID {
			return errors.New("cross-root adoption execution is not supported")
		}
		if scope.MediaKind == catalog.KindMovie && scope.ItemCount != 1 {
			return errors.New("a monitored adopted movie must contain exactly one primary file")
		}
		searchRequired = searchRequired || scope.SearchImmediately
		var activeWorkflow bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE media_id=$1 AND variant=$2 AND terminal_at IS NULL)`, scope.MediaID.String(), scope.Variant).Scan(&activeWorkflow); err != nil {
			return err
		}
		if activeWorkflow {
			return errors.New("cancel or finish the active workflow before executing reviewed adoption monitoring")
		}
		if scope.MediaKind == catalog.KindSeries {
			var episodes int
			if err := transaction.QueryRow(ctx, `SELECT count(*) FROM pack_expected_episodes episode JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id JOIN workflows workflow ON workflow.id=expectation.workflow_id WHERE workflow.media_id=$1 AND workflow.variant=$2`, scope.MediaID.String(), scope.Variant).Scan(&episodes); err != nil {
				return err
			}
			if episodes == 0 {
				return errors.New("adopted series monitoring requires an exact existing season episode snapshot")
			}
		}
	}
	if searchRequired && !replacementAuthorized {
		return ErrAdoptedReplacementConfirmationRequired
	}
	if searchRequired {
		var indexerAvailable bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND enabled AND config->'mode_verified' = 'true'::jsonb)`).Scan(&indexerAvailable); err != nil {
			return err
		}
		if !indexerAvailable {
			return errors.New("immediate adoption search requires a commissioned Newznab indexer")
		}
	}
	return nil
}

func (store *Store) applyAdoptionMonitoring(ctx context.Context, transaction pgx.Tx, batchID uuid.UUID, now time.Time) (int, int, error) {
	var defaultRootText, defaultClientText string
	var replacementAuthorized bool
	if err := transaction.QueryRow(ctx, `SELECT default_root_id::text,replacement_search_authorized_at IS NOT NULL FROM adoption_batches WHERE id=$1`, batchID.String()).Scan(&defaultRootText, &replacementAuthorized); err != nil {
		return 0, 0, err
	}
	scopes, err := loadAdoptionMonitoringScopes(ctx, transaction, batchID)
	if err != nil || len(scopes) == 0 {
		return 0, 0, err
	}
	if err := transaction.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE kind='altmount' AND is_default AND enabled ORDER BY id LIMIT 1`).Scan(&defaultClientText); err != nil {
		return 0, 0, err
	}
	defaultRootID, _ := uuid.Parse(defaultRootText)
	if err := store.validateAdoptionMonitoring(ctx, transaction, batchID, defaultRootID, replacementAuthorized); err != nil {
		return 0, 0, err
	}
	defaultClientID, _ := uuid.Parse(defaultClientText)
	searches := 0
	for _, scope := range scopes {
		workflow := workflows.NewWithClient(scope.MediaID, scope.RootID, scope.QualityProfileID, defaultClientID, now)
		if err := store.insertManualWorkflow(ctx, transaction, workflow, scope.Variant, false); err != nil {
			return 0, 0, err
		}
		if scope.MediaKind == catalog.KindSeries {
			if err := cloneLatestAdoptionExpectation(ctx, transaction, scope, workflow.ID, now); err != nil {
				return 0, 0, err
			}
		}
		if scope.MediaKind == catalog.KindSeries {
			var season int
			if _, err := fmt.Sscanf(scope.Variant, "season:%d", &season); err != nil {
				return 0, 0, errors.New("adopted series monitoring has an invalid season scope")
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO season_monitoring(media_id,season_number,monitored,prefer_season_pack,search_immediately,state,updated_at) VALUES($1,$2,true,true,$3,'wanted',$4) ON CONFLICT(media_id,season_number) DO UPDATE SET monitored=true,prefer_season_pack=true,search_immediately=EXCLUDED.search_immediately,state='wanted',updated_at=EXCLUDED.updated_at`, scope.MediaID.String(), season, scope.SearchImmediately, now.UTC()); err != nil {
				return 0, 0, err
			}
		}
		if scope.SearchImmediately {
			command, err := transaction.Exec(ctx, `INSERT INTO adopted_replacement_authorizations(workflow_id,adopted_file_id,destination_relative,source_size,source_modtime_ns,authorized_by,authorized_at)
				SELECT $1,adopted.id,adopted.destination_relative,adopted.source_size,adopted.source_modtime_ns,'owner',$2 FROM adopted_files adopted
				WHERE adopted.adoption_batch_id=$3 AND adopted.media_id=$4 AND adopted.variant=$5 AND adopted.state='current' ON CONFLICT(workflow_id,adopted_file_id) DO NOTHING`, workflow.ID.String(), now.UTC(), batchID.String(), scope.MediaID.String(), scope.Variant)
			if err != nil {
				return 0, 0, err
			}
			if command.RowsAffected() != int64(scope.ItemCount) {
				return 0, 0, errors.New("not every adopted file received a fingerprint-bound replacement authorization")
			}
			payload, _ := json.Marshal(map[string]string{"workflow_id": workflow.ID.String(), "variant": scope.Variant})
			if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), workflow.ID.String(), int64(workflow.Revision), payload, now.UTC()); err != nil {
				return 0, 0, err
			}
			searches++
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO adoption_monitoring_applications(batch_id,media_id,variant,workflow_id,search_immediately,replacement_authorized,applied_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, batchID.String(), scope.MediaID.String(), scope.Variant, workflow.ID.String(), scope.SearchImmediately, scope.SearchImmediately, now.UTC()); err != nil {
			return 0, 0, err
		}
	}
	return len(scopes), searches, nil
}

func cloneLatestAdoptionExpectation(ctx context.Context, transaction pgx.Tx, scope adoptionMonitoringScope, workflowID uuid.UUID, now time.Time) error {
	var sourceText, seriesIdentity string
	var season int16
	var sourceRevision int64
	var digest []byte
	err := transaction.QueryRow(ctx, `SELECT expectation.id::text,expectation.series_identity,expectation.season,expectation.source_revision,expectation.expectation_digest
		FROM pack_expectation_sets expectation JOIN workflows workflow ON workflow.id=expectation.workflow_id
		WHERE workflow.media_id=$1 AND workflow.variant=$2 AND EXISTS(SELECT 1 FROM pack_expected_episodes episode WHERE episode.expectation_id=expectation.id)
		ORDER BY expectation.frozen_at DESC,expectation.id DESC LIMIT 1`, scope.MediaID.String(), scope.Variant).Scan(&sourceText, &seriesIdentity, &season, &sourceRevision, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("adopted series monitoring has no exact season episode snapshot to preserve")
	}
	if err != nil {
		return err
	}
	expectationID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7)`, expectationID.String(), workflowID.String(), seriesIdentity, season, sourceRevision, digest, now.UTC()); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title,air_date)
		SELECT $1,season,episode,provider_episode_id,title,air_date FROM pack_expected_episodes WHERE expectation_id=$2 ORDER BY season,episode`, expectationID.String(), sourceText)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("adopted series monitoring copied no exact episode expectations")
	}
	return nil
}
