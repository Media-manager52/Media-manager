package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
)

type SeerrEpisode struct {
	Number     int16  `json:"number"`
	ProviderID string `json:"provider_id"`
	Title      string `json:"title"`
	AirDate    string `json:"air_date"`
}
type SeerrSeason struct {
	Number       int16          `json:"number"`
	Availability int            `json:"availability"`
	Episodes     []SeerrEpisode `json:"episodes"`
}
type SeerrIntake struct {
	InstanceID   string
	RequestID    string
	UpdatedAt    time.Time
	RequestState string
	Kind         catalog.MediaKind
	Title        string
	Year         *uint16
	Identities   []catalog.ExternalIdentity
	Metadata     *catalog.MediaMetadata
	Seasons      []SeerrSeason
	PayloadHash  []byte
	DeliveryID   uuid.UUID
}

type IntakeResult struct {
	MediaID     uuid.UUID   `json:"media_id"`
	WorkflowIDs []uuid.UUID `json:"workflow_ids"`
	Created     bool        `json:"created"`
}

var (
	ErrDefaultsMissing            = errors.New("default root or quality profile is missing")
	ErrIdentityNeedsAttention     = errors.New("external identities resolve to different media")
	ErrMediaRecycled              = errors.New("matching media is in the recycle bin")
	ErrSeerrSnapshotDrift         = errors.New("Seerr episode snapshot changed while its workflow was active")
	ErrSeerrSnapshotConflict      = errors.New("Seerr snapshot changed without a newer update cursor")
	ErrAdoptionMonitoringReserved = errors.New("reviewed adoption monitoring owns this scope until Plex verification finishes")
)

func (store *Store) UpsertSeerrSnapshot(ctx context.Context, intake SeerrIntake) (bool, error) {
	if intake.UpdatedAt.IsZero() {
		return false, ErrSeerrSnapshotConflict
	}
	identities, _ := json.Marshal(intake.Identities)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	var revision int64
	err = transaction.QueryRow(ctx, `INSERT INTO seerr_request_snapshots(seerr_instance_id,request_id,external_updated_at,state,identities,payload_sha256,last_delivery_id,revision,reconciled_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,now())
		ON CONFLICT(seerr_instance_id,request_id) DO UPDATE SET external_updated_at=EXCLUDED.external_updated_at,state=EXCLUDED.state,identities=EXCLUDED.identities,payload_sha256=EXCLUDED.payload_sha256,last_delivery_id=EXCLUDED.last_delivery_id,revision=seerr_request_snapshots.revision+1,reconciled_at=now()
		WHERE seerr_request_snapshots.external_updated_at IS NULL OR (EXCLUDED.external_updated_at IS NOT NULL AND EXCLUDED.external_updated_at > seerr_request_snapshots.external_updated_at)
		RETURNING revision`, intake.InstanceID, intake.RequestID, nullableTime(intake.UpdatedAt), intake.RequestState, string(identities), intake.PayloadHash, intake.DeliveryID.String()).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingUpdated *time.Time
		var existingHash []byte
		if loadErr := transaction.QueryRow(ctx, `SELECT external_updated_at,payload_sha256 FROM seerr_request_snapshots WHERE seerr_instance_id=$1 AND request_id=$2`, intake.InstanceID, intake.RequestID).Scan(&existingUpdated, &existingHash); loadErr != nil {
			return false, loadErr
		}
		if existingUpdated != nil && existingUpdated.Equal(intake.UpdatedAt.UTC()) && !bytes.Equal(existingHash, intake.PayloadHash) {
			return false, ErrSeerrSnapshotConflict
		}
		if commitErr := transaction.Commit(ctx); commitErr != nil {
			return false, commitErr
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM seerr_request_seasons WHERE seerr_instance_id=$1 AND request_id=$2`, intake.InstanceID, intake.RequestID); err != nil {
		return false, err
	}
	for _, season := range intake.Seasons {
		if _, err := transaction.Exec(ctx, `INSERT INTO seerr_request_seasons(seerr_instance_id,request_id,season_number,availability_status,requested) VALUES($1,$2,$3,$4,true)`, intake.InstanceID, intake.RequestID, season.Number, season.Availability); err != nil {
			return false, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (store *Store) EnsureMediaFromSeerr(ctx context.Context, intake SeerrIntake, now time.Time) (IntakeResult, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return IntakeResult{}, err
	}
	defer transaction.Rollback(ctx)
	var rootText, profileText, clientText string
	if err := transaction.QueryRow(ctx, `SELECT root.id::text FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.media_kind=$1 AND root.is_default AND root.enabled AND root.plex_library_id IS NOT NULL ORDER BY root.id LIMIT 1`, intake.Kind).Scan(&rootText); errors.Is(err, pgx.ErrNoRows) {
		return IntakeResult{}, ErrDefaultsMissing
	} else if err != nil {
		return IntakeResult{}, err
	}
	if err := transaction.QueryRow(ctx, `SELECT id::text FROM quality_profiles WHERE media_kind=$1 AND is_default AND enabled ORDER BY id LIMIT 1`, intake.Kind).Scan(&profileText); errors.Is(err, pgx.ErrNoRows) {
		return IntakeResult{}, ErrDefaultsMissing
	} else if err != nil {
		return IntakeResult{}, err
	}
	if err := transaction.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE kind='altmount' AND is_default AND enabled ORDER BY id LIMIT 1`).Scan(&clientText); errors.Is(err, pgx.ErrNoRows) {
		return IntakeResult{}, ErrDefaultsMissing
	} else if err != nil {
		return IntakeResult{}, err
	}
	var indexerAvailable bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND enabled AND config->'mode_verified' = 'true'::jsonb)`).Scan(&indexerAvailable); err != nil {
		return IntakeResult{}, err
	}
	if !indexerAvailable {
		return IntakeResult{}, ErrDefaultsMissing
	}
	rootID, _ := uuid.Parse(rootText)
	profileID, _ := uuid.Parse(profileText)
	clientID, _ := uuid.Parse(clientText)
	var snapshotRevision int64
	if err := transaction.QueryRow(ctx, `SELECT revision FROM seerr_request_snapshots WHERE seerr_instance_id=$1 AND request_id=$2`, intake.InstanceID, intake.RequestID).Scan(&snapshotRevision); err != nil {
		return IntakeResult{}, err
	}

	var resolved *uuid.UUID
	for _, identity := range intake.Identities {
		var mediaText string
		var deletedAt *time.Time
		err := transaction.QueryRow(ctx, `SELECT identity.media_id::text,media.deleted_at FROM external_identities identity JOIN media_items media ON media.id=identity.media_id WHERE identity.media_kind=$1 AND identity.namespace=$2 AND identity.value=$3 AND identity.state='verified'`, intake.Kind, identity.Namespace, identity.Value).Scan(&mediaText, &deletedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return IntakeResult{}, err
		}
		if deletedAt != nil {
			return IntakeResult{}, ErrMediaRecycled
		}
		parsed, err := uuid.Parse(mediaText)
		if err != nil {
			return IntakeResult{}, err
		}
		if resolved != nil && *resolved != parsed {
			return IntakeResult{}, ErrIdentityNeedsAttention
		}
		resolved = &parsed
	}
	result := IntakeResult{}
	if resolved == nil {
		item, err := catalog.New(intake.Kind, intake.Title, intake.Year, intake.Identities, now)
		if err != nil {
			return IntakeResult{}, err
		}
		result.MediaID, result.Created = item.ID, true
		var year any
		if item.Year != nil {
			year = int16(*item.Year)
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$5)`, item.ID.String(), item.Kind, item.Title, year, now.UTC()); err != nil {
			return IntakeResult{}, err
		}
		for _, identity := range item.Identities {
			if _, err := transaction.Exec(ctx, `INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewV7().String(), item.ID.String(), item.Kind, identity.Namespace, identity.Value, identity.State, identity.Source, now.UTC()); err != nil {
				return IntakeResult{}, err
			}
		}
	} else {
		result.MediaID = *resolved
	}
	var mediaLock int
	if err := transaction.QueryRow(ctx, `SELECT 1 FROM media_items WHERE id=$1 FOR UPDATE`, result.MediaID.String()).Scan(&mediaLock); err != nil {
		return IntakeResult{}, err
	}
	if err := persistMetadata(ctx, transaction, result.MediaID, intake.Metadata, now); err != nil {
		return IntakeResult{}, err
	}
	if intake.Metadata != nil && !result.Created {
		if _, err := transaction.Exec(ctx, `UPDATE media_items SET revision=revision+1,updated_at=$2 WHERE id=$1`, result.MediaID.String(), now.UTC()); err != nil {
			return IntakeResult{}, err
		}
	}

	variants := []struct {
		Name   string
		Season *int16
	}{{Name: "default"}}
	if intake.Kind == catalog.KindSeries && len(intake.Seasons) > 0 {
		variants = variants[:0]
		for index := range intake.Seasons {
			if intake.Seasons[index].Availability == 5 {
				continue
			}
			season := intake.Seasons[index].Number
			variants = append(variants, struct {
				Name   string
				Season *int16
			}{Name: fmt.Sprintf("season:%02d", season), Season: &season})
		}
	}
	for _, variant := range variants {
		var adoptionMonitoringReserved bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adoption_items adoption_item JOIN adoption_batches batch ON batch.id=adoption_item.batch_id WHERE adoption_item.media_id=$1 AND adoption_item.variant=$2 AND adoption_item.review_state='approved' AND COALESCE(adoption_item.monitoring_enabled,false) AND batch.state='executing')`, result.MediaID.String(), variant.Name).Scan(&adoptionMonitoringReserved); err != nil {
			return IntakeResult{}, err
		}
		if adoptionMonitoringReserved {
			return IntakeResult{}, ErrAdoptionMonitoringReserved
		}
		var adoptedProtected bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adopted_files WHERE media_id=$1 AND variant=$2 AND state IN ('current','recycle_planned','retained','restore_confirming','restore_attention'))`, result.MediaID.String(), variant.Name).Scan(&adoptedProtected); err != nil {
			return IntakeResult{}, err
		}
		if adoptedProtected {
			return IntakeResult{}, ErrAdoptedReplacementConfirmationRequired
		}
		var existingText string
		var existingState string
		var existingRevision int64
		err := transaction.QueryRow(ctx, `SELECT id::text,state,revision FROM workflows WHERE media_id=$1 AND variant=$2 AND terminal_at IS NULL`, result.MediaID.String(), variant.Name).Scan(&existingText, &existingState, &existingRevision)
		if err == nil {
			replaceExisting := false
			existing, _ := uuid.Parse(existingText)
			if variant.Season != nil {
				seasonInput := seerrSeasonByNumber(intake.Seasons, *variant.Season)
				if seasonInput == nil || len(seasonInput.Episodes) == 0 {
					return IntakeResult{}, ErrSeerrSnapshotDrift
				}
				digest := packExpectationDigest(result.MediaID, *seasonInput)
				var frozenDigest []byte
				frozenErr := transaction.QueryRow(ctx, `SELECT expectation_digest FROM pack_expectation_sets WHERE workflow_id=$1 ORDER BY frozen_at DESC,id DESC LIMIT 1`, existingText).Scan(&frozenDigest)
				if frozenErr != nil || !bytes.Equal(frozenDigest, digest) {
					if seerrPreRetrievalState(existingState) {
						if _, updateErr := transaction.Exec(ctx, `UPDATE workflows SET state='cancelled',revision=revision+1,updated_at=$2,terminal_at=$2,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$3 AND terminal_at IS NULL`, existingText, now.UTC(), existingRevision); updateErr != nil {
							return IntakeResult{}, updateErr
						}
						if _, transitionErr := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'cancelled','Seerr replaced the exact episode snapshot before retrieval','seerr',$4) ON CONFLICT DO NOTHING`, existingText, existingRevision+1, existingState, now.UTC()); transitionErr != nil {
							return IntakeResult{}, transitionErr
						}
						replaceExisting = true
					} else {
						if _, updateErr := transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',revision=revision+1,updated_at=$2,attention_code='seerr_snapshot_changed',attention_detail='Seerr changed the exact episode snapshot; review and create a fresh season workflow' WHERE id=$1 AND terminal_at IS NULL`, existingText, now.UTC()); updateErr != nil {
							return IntakeResult{}, updateErr
						}
						if _, transitionErr := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention','Seerr changed the exact episode snapshot','seerr',$4) ON CONFLICT DO NOTHING`, existingText, existingRevision+1, existingState, now.UTC()); transitionErr != nil {
							return IntakeResult{}, transitionErr
						}
						if commitErr := transaction.Commit(ctx); commitErr != nil {
							return IntakeResult{}, commitErr
						}
						return IntakeResult{}, ErrSeerrSnapshotDrift
					}
				}
			}
			if !replaceExisting {
				if _, err := transaction.Exec(ctx, `INSERT INTO seerr_request_workflows(seerr_instance_id,request_id,workflow_id,attached_snapshot_revision,owns_workflow) VALUES($1,$2,$3,$4,false) ON CONFLICT(seerr_instance_id,request_id,workflow_id) DO UPDATE SET attached_snapshot_revision=EXCLUDED.attached_snapshot_revision`, intake.InstanceID, intake.RequestID, existingText, snapshotRevision); err != nil {
					return IntakeResult{}, err
				}
				result.WorkflowIDs = append(result.WorkflowIDs, existing)
				continue
			}
			err = pgx.ErrNoRows
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return IntakeResult{}, err
		}
		workflowID := uuid.NewV7()
		if _, err := transaction.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at) VALUES($1,$2,'monitored',$3,$4,$5,$6,1,$7,$7)`, workflowID.String(), result.MediaID.String(), rootID.String(), profileID.String(), clientID.String(), variant.Name, now.UTC()); err != nil {
			return IntakeResult{}, err
		}
		if variant.Season != nil {
			if _, err := transaction.Exec(ctx, `INSERT INTO season_monitoring(media_id,season_number,monitored,prefer_season_pack,search_immediately,state,updated_at) VALUES($1,$2,true,true,true,'wanted',$3) ON CONFLICT(media_id,season_number) DO UPDATE SET monitored=true,prefer_season_pack=true,updated_at=EXCLUDED.updated_at`, result.MediaID.String(), *variant.Season, now.UTC()); err != nil {
				return IntakeResult{}, err
			}
			seasonInput := seerrSeasonByNumber(intake.Seasons, *variant.Season)
			if seasonInput != nil && len(seasonInput.Episodes) > 0 {
				digest := packExpectationDigest(result.MediaID, *seasonInput)
				candidateExpectationID := uuid.NewV7()
				var expectationText string
				if err := transaction.QueryRow(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7) ON CONFLICT(workflow_id,expectation_digest) DO UPDATE SET frozen_at=pack_expectation_sets.frozen_at RETURNING id::text`, candidateExpectationID.String(), workflowID.String(), result.MediaID.String(), seasonInput.Number, snapshotRevision, digest, now.UTC()).Scan(&expectationText); err != nil {
					return IntakeResult{}, err
				}
				expectationID, _ := uuid.Parse(expectationText)
				for _, episode := range seasonInput.Episodes {
					if episode.Number <= 0 {
						continue
					}
					if _, err := transaction.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title,air_date) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::date) ON CONFLICT DO NOTHING`, expectationID.String(), seasonInput.Number, episode.Number, episode.ProviderID, episode.Title, episode.AirDate); err != nil {
						return IntakeResult{}, err
					}
				}
			}
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO seerr_request_workflows(seerr_instance_id,request_id,workflow_id,attached_snapshot_revision,owns_workflow) VALUES($1,$2,$3,$4,true) ON CONFLICT(seerr_instance_id,request_id,workflow_id) DO UPDATE SET attached_snapshot_revision=EXCLUDED.attached_snapshot_revision,owns_workflow=seerr_request_workflows.owns_workflow OR EXCLUDED.owns_workflow`, intake.InstanceID, intake.RequestID, workflowID.String(), snapshotRevision); err != nil {
			return IntakeResult{}, err
		}
		payload, _ := json.Marshal(map[string]any{"workflow_id": workflowID.String(), "variant": variant.Name})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,1,'workflow.search_requested',$3,$4,$4)`, uuid.NewV7().String(), workflowID.String(), string(payload), now.UTC()); err != nil {
			return IntakeResult{}, err
		}
		result.WorkflowIDs = append(result.WorkflowIDs, workflowID)
	}
	detail, _ := json.Marshal(map[string]any{"seerr_request_id": intake.RequestID, "workflow_ids": result.WorkflowIDs})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'seerr','request.intake','media',$2,$3,$4)`, uuid.NewV7().String(), result.MediaID.String(), string(detail), now.UTC()); err != nil {
		return IntakeResult{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IntakeResult{}, err
	}
	return result, nil
}

func seerrPreRetrievalState(state string) bool {
	switch state {
	case "monitored", "waiting", "searching", "candidate_selected", "held", "blocked":
		return true
	default:
		return false
	}
}

func seerrSeasonByNumber(seasons []SeerrSeason, number int16) *SeerrSeason {
	for index := range seasons {
		if seasons[index].Number == number {
			return &seasons[index]
		}
	}
	return nil
}

func (store *Store) MarkSeerrRequestMissing(ctx context.Context, instanceID, requestID string, deliveryID uuid.UUID, now time.Time) error {
	_, err := store.Pool.Exec(ctx, `UPDATE seerr_request_snapshots SET state='deleted',last_delivery_id=$3,revision=revision+1,reconciled_at=$4 WHERE seerr_instance_id=$1 AND request_id=$2 AND state<>'deleted'`, instanceID, requestID, deliveryID.String(), now.UTC())
	return err
}

func (store *Store) ReconcileSeerrRevocation(ctx context.Context, instanceID, requestID string, now time.Time) (bool, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, `SELECT workflow.id::text,workflow.state,workflow.revision
		FROM seerr_request_workflows provenance
		JOIN workflows workflow ON workflow.id=provenance.workflow_id
		WHERE provenance.seerr_instance_id=$1 AND provenance.request_id=$2 AND provenance.owns_workflow AND workflow.terminal_at IS NULL
		AND NOT EXISTS(
			SELECT 1 FROM seerr_request_workflows other
			JOIN seerr_request_snapshots snapshot ON snapshot.seerr_instance_id=other.seerr_instance_id AND snapshot.request_id=other.request_id
			WHERE other.workflow_id=workflow.id AND NOT (other.seerr_instance_id=$1 AND other.request_id=$2) AND snapshot.state='2'
		)
		ORDER BY workflow.id FOR UPDATE OF workflow`, instanceID, requestID)
	if err != nil {
		return false, err
	}
	type revocation struct {
		id       string
		state    string
		revision int64
	}
	var pending []revocation
	for rows.Next() {
		var item revocation
		if err := rows.Scan(&item.id, &item.state, &item.revision); err != nil {
			rows.Close()
			return false, err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	needsAttention := false
	for _, item := range pending {
		preRetrieval := item.state == "monitored" || item.state == "waiting" || item.state == "searching" || item.state == "candidate_selected" || item.state == "held" || item.state == "blocked"
		toState := "cancelled"
		reason := "Seerr request is no longer approved before retrieval"
		var terminalAt any = now.UTC()
		if !preRetrieval {
			toState = "needs_attention"
			reason = "Seerr request changed after retrieval started; owner action is required"
			terminalAt = nil
			needsAttention = true
		}
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=revision+1,updated_at=$3,terminal_at=$4,attention_code=CASE WHEN $2='needs_attention' THEN 'seerr_request_revoked' ELSE NULL END,attention_detail=CASE WHEN $2='needs_attention' THEN $5 ELSE NULL END WHERE id=$1 AND revision=$6`, item.id, toState, now.UTC(), terminalAt, reason, item.revision); err != nil {
			return false, err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,$5,'seerr',$6)`, item.id, item.revision+1, item.state, toState, reason, now.UTC()); err != nil {
			return false, err
		}
	}
	return needsAttention, transaction.Commit(ctx)
}

func packExpectationDigest(mediaID uuid.UUID, season SeerrSeason) []byte {
	hash := sha256.New()
	fmt.Fprintf(hash, "expectation-v1\x00%s\x00%d\x00", mediaID.String(), season.Number)
	sort.Slice(season.Episodes, func(i, j int) bool { return season.Episodes[i].Number < season.Episodes[j].Number })
	for _, episode := range season.Episodes {
		fmt.Fprintf(hash, "%d\x00%s\x00%s\x00", episode.Number, episode.ProviderID, episode.Title)
	}
	return hash.Sum(nil)
}
