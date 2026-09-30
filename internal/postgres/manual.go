package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

type ManualEpisode struct {
	Number     uint16 `json:"number"`
	ProviderID string `json:"provider_episode_id,omitempty"`
	Title      string `json:"title,omitempty"`
	AirDate    string `json:"air_date,omitempty"`
}

type ManualSeason struct {
	Number            uint16          `json:"number"`
	Episodes          []ManualEpisode `json:"episodes"`
	Monitored         bool            `json:"monitored"`
	SearchImmediately bool            `json:"search_immediately"`
	PreferSeasonPack  bool            `json:"prefer_season_pack"`
}

func (store *Store) ResolveMediaConfiguration(ctx context.Context, kind catalog.MediaKind, rootID, profileID, clientID *uuid.UUID) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	resolvedRoot, err := store.resolveSetting(ctx, `SELECT root.id::text FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.id=$1 AND root.media_kind=$2 AND root.enabled AND root.plex_library_id IS NOT NULL`, `SELECT root.id::text FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.media_kind=$1 AND root.is_default AND root.enabled AND root.plex_library_id IS NOT NULL ORDER BY root.id LIMIT 1`, kind, rootID)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, uuid.UUID{}, err
	}
	resolvedProfile, err := store.resolveSetting(ctx, `SELECT id::text FROM quality_profiles WHERE id=$1 AND media_kind=$2 AND enabled`, `SELECT id::text FROM quality_profiles WHERE media_kind=$1 AND is_default AND enabled ORDER BY id LIMIT 1`, kind, profileID)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, uuid.UUID{}, err
	}
	var clientText string
	if clientID != nil {
		err = store.Pool.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE id=$1 AND kind='altmount' AND enabled`, clientID.String()).Scan(&clientText)
	} else {
		err = store.Pool.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE kind='altmount' AND is_default AND enabled ORDER BY id LIMIT 1`).Scan(&clientText)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, uuid.UUID{}, uuid.UUID{}, ErrDefaultsMissing
	}
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, uuid.UUID{}, err
	}
	resolvedClient, err := uuid.Parse(clientText)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, uuid.UUID{}, err
	}
	return resolvedRoot, resolvedProfile, resolvedClient, nil
}

func (store *Store) resolveSetting(ctx context.Context, explicitQuery, defaultQuery string, kind catalog.MediaKind, selected *uuid.UUID) (uuid.UUID, error) {
	var value string
	var err error
	if selected != nil {
		err = store.Pool.QueryRow(ctx, explicitQuery, selected.String(), kind).Scan(&value)
	} else {
		err = store.Pool.QueryRow(ctx, defaultQuery, kind).Scan(&value)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, ErrDefaultsMissing
	}
	if err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(value)
}

func (store *Store) CreateManualMedia(ctx context.Context, item catalog.MediaItem, rootID, profileID, clientID uuid.UUID, seasons []ManualSeason, searchMovie bool, actor string) ([]workflows.Workflow, error) {
	if item.Kind == catalog.KindSeries && len(seasons) == 0 {
		return nil, errors.New("a series add requires at least one season snapshot")
	}
	if item.Kind == catalog.KindMovie && len(seasons) != 0 {
		return nil, errors.New("a movie cannot contain season snapshots")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback(ctx)
	needsIndexer := searchMovie
	for _, season := range seasons {
		needsIndexer = needsIndexer || season.Monitored && season.SearchImmediately
	}
	if needsIndexer {
		var available bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND enabled AND config->'mode_verified' = 'true'::jsonb)`).Scan(&available); err != nil {
			return nil, err
		}
		if !available {
			return nil, errors.New("search requires at least one commissioned Newznab indexer")
		}
	}
	var year any
	if item.Year != nil {
		year = int16(*item.Year)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, item.ID.String(), item.Kind, item.Title, year, int64(item.Revision), item.CreatedAt, item.UpdatedAt); err != nil {
		return nil, err
	}
	for _, identity := range item.Identities {
		if _, err := transaction.Exec(ctx, `INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewV7().String(), item.ID.String(), item.Kind, identity.Namespace, identity.Value, identity.State, identity.Source, item.CreatedAt); err != nil {
			return nil, err
		}
	}
	if err := persistMetadata(ctx, transaction, item.ID, item.Metadata, item.CreatedAt); err != nil {
		return nil, err
	}
	created := make([]workflows.Workflow, 0, max(1, len(seasons)))
	if item.Kind == catalog.KindMovie {
		workflow := workflows.NewWithClient(item.ID, rootID, profileID, clientID, item.CreatedAt)
		if err := store.insertManualWorkflow(ctx, transaction, workflow, "default", searchMovie); err != nil {
			return nil, err
		}
		created = append(created, workflow)
	} else {
		sort.Slice(seasons, func(i, j int) bool { return seasons[i].Number < seasons[j].Number })
		seenSeasons := make(map[uint16]struct{}, len(seasons))
		for _, season := range seasons {
			if season.Number > 999 {
				return nil, errors.New("season number is outside the supported range")
			}
			if _, duplicate := seenSeasons[season.Number]; duplicate {
				return nil, errors.New("season snapshot is duplicated")
			}
			seenSeasons[season.Number] = struct{}{}
			if len(season.Episodes) == 0 {
				return nil, errors.New("season snapshot has no episodes")
			}
			workflow := workflows.NewWithClient(item.ID, rootID, profileID, clientID, item.CreatedAt)
			variant := fmt.Sprintf("season:%02d", season.Number)
			if err := store.insertManualWorkflow(ctx, transaction, workflow, variant, season.Monitored && season.SearchImmediately); err != nil {
				return nil, err
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO season_monitoring(media_id,season_number,monitored,prefer_season_pack,search_immediately,state,updated_at) VALUES($1,$2,$3,$4,$5,'wanted',$6)`, item.ID.String(), int16(season.Number), season.Monitored, season.PreferSeasonPack, season.SearchImmediately, item.CreatedAt); err != nil {
				return nil, err
			}
			expectationID := uuid.NewV7()
			digest, sortedEpisodes, err := manualExpectationDigest(item.ID, season)
			if err != nil {
				return nil, err
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,1,$5,$6,$6)`, expectationID.String(), workflow.ID.String(), seriesIdentity(item), int16(season.Number), digest, item.CreatedAt); err != nil {
				return nil, err
			}
			for _, episode := range sortedEpisodes {
				if _, err := transaction.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title,air_date) VALUES($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,'')::date)`, expectationID.String(), int16(season.Number), int16(episode.Number), strings.TrimSpace(episode.ProviderID), strings.TrimSpace(episode.Title), strings.TrimSpace(episode.AirDate)); err != nil {
					return nil, err
				}
			}
			created = append(created, workflow)
		}
	}
	detail, _ := json.Marshal(map[string]any{"workflow_ids": created})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'media.created','media',$3,$4,$5)`, uuid.NewV7().String(), actor, item.ID.String(), string(detail), item.CreatedAt); err != nil {
		return nil, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, err
	}
	return created, nil
}

const AdoptedReplacementConfirmation = "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"

var ErrAdoptedReplacementConfirmationRequired = errors.New("exact adopted-file replacement confirmation is required")

func (store *Store) CreateManualWorkflowForExisting(ctx context.Context, item catalog.MediaItem, rootID, profileID, clientID uuid.UUID, season *ManualSeason, search bool, adoptedReplacementConfirmation, actor string, now time.Time) (workflows.Workflow, error) {
	if item.Kind == catalog.KindMovie && season != nil || item.Kind == catalog.KindSeries && season == nil {
		return workflows.Workflow{}, errors.New("workflow scope does not match the media kind")
	}
	variant := "default"
	if season != nil {
		if season.Number > 999 || len(season.Episodes) == 0 {
			return workflows.Workflow{}, errors.New("series workflow requires a valid exact season snapshot")
		}
		variant = fmt.Sprintf("season:%02d", season.Number)
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workflows.Workflow{}, err
	}
	defer transaction.Rollback(ctx)
	if search {
		var available bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND enabled AND config->'mode_verified' = 'true'::jsonb)`).Scan(&available); err != nil {
			return workflows.Workflow{}, err
		}
		if !available {
			return workflows.Workflow{}, errors.New("search requires at least one commissioned Newznab indexer")
		}
	}
	var kind catalog.MediaKind
	var deletedAt *time.Time
	if err := transaction.QueryRow(ctx, `SELECT kind,deleted_at FROM media_items WHERE id=$1 FOR UPDATE`, item.ID.String()).Scan(&kind, &deletedAt); errors.Is(err, pgx.ErrNoRows) {
		return workflows.Workflow{}, ErrNotFound
	} else if err != nil {
		return workflows.Workflow{}, err
	}
	if deletedAt != nil || kind != item.Kind {
		return workflows.Workflow{}, errors.New("media is not active")
	}
	var adoptionMonitoringReserved bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adoption_items adoption_item JOIN adoption_batches batch ON batch.id=adoption_item.batch_id WHERE adoption_item.media_id=$1 AND adoption_item.variant=$2 AND adoption_item.review_state='approved' AND COALESCE(adoption_item.monitoring_enabled,false) AND batch.state='executing')`, item.ID.String(), variant).Scan(&adoptionMonitoringReserved); err != nil {
		return workflows.Workflow{}, err
	}
	if adoptionMonitoringReserved {
		return workflows.Workflow{}, errors.New("reviewed adoption monitoring owns this scope until Plex verification finishes")
	}
	var adoptedLifecycleActive bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adopted_files WHERE media_id=$1 AND variant=$2 AND state IN ('recycle_planned','retained','restore_confirming','restore_attention'))`, item.ID.String(), variant).Scan(&adoptedLifecycleActive); err != nil {
		return workflows.Workflow{}, err
	}
	if adoptedLifecycleActive {
		return workflows.Workflow{}, errors.New("an adopted-file replacement or restore lifecycle is already active")
	}
	var upgradesEnabled, hasCurrentLink bool
	if err := transaction.QueryRow(ctx, `SELECT upgrades_enabled FROM quality_profiles WHERE id=$1 AND media_kind=$2 AND enabled`, profileID.String(), item.Kind).Scan(&upgradesEnabled); errors.Is(err, pgx.ErrNoRows) {
		return workflows.Workflow{}, ErrDefaultsMissing
	} else if err != nil {
		return workflows.Workflow{}, err
	}
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM library_link_versions version JOIN workflows workflow ON workflow.id=version.workflow_id
		WHERE workflow.media_id=$1 AND workflow.variant=$2 AND version.state='current'
	)`, item.ID.String(), variant).Scan(&hasCurrentLink); err != nil {
		return workflows.Workflow{}, err
	}
	type adoptedCurrent struct {
		id                  uuid.UUID
		destinationRelative string
		size, modtimeNS     int64
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,destination_relative,source_size,source_modtime_ns FROM adopted_files WHERE media_id=$1 AND variant=$2 AND state='current' ORDER BY id FOR UPDATE`, item.ID.String(), variant)
	if err != nil {
		return workflows.Workflow{}, err
	}
	var adoptedFiles []adoptedCurrent
	for rows.Next() {
		var current adoptedCurrent
		var idText string
		if err := rows.Scan(&idText, &current.destinationRelative, &current.size, &current.modtimeNS); err != nil {
			rows.Close()
			return workflows.Workflow{}, err
		}
		current.id, _ = uuid.Parse(idText)
		adoptedFiles = append(adoptedFiles, current)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return workflows.Workflow{}, err
	}
	rows.Close()
	hasCurrent := hasCurrentLink || len(adoptedFiles) > 0
	if hasCurrent && !upgradesEnabled {
		return workflows.Workflow{}, errors.New("upgrades are disabled for the selected quality profile")
	}
	if len(adoptedFiles) > 0 && adoptedReplacementConfirmation != AdoptedReplacementConfirmation {
		return workflows.Workflow{}, ErrAdoptedReplacementConfirmationRequired
	}
	workflow := workflows.NewWithClient(item.ID, rootID, profileID, clientID, now)
	if err := store.insertManualWorkflow(ctx, transaction, workflow, variant, search); err != nil {
		return workflows.Workflow{}, err
	}
	for _, adopted := range adoptedFiles {
		if _, err := transaction.Exec(ctx, `INSERT INTO adopted_replacement_authorizations(workflow_id,adopted_file_id,destination_relative,source_size,source_modtime_ns,authorized_by,authorized_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, workflow.ID.String(), adopted.id.String(), adopted.destinationRelative, adopted.size, adopted.modtimeNS, actor, now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
	}
	if season != nil {
		if _, err := transaction.Exec(ctx, `INSERT INTO season_monitoring(media_id,season_number,monitored,prefer_season_pack,search_immediately,state,updated_at) VALUES($1,$2,$3,$4,$5,'wanted',$6) ON CONFLICT(media_id,season_number) DO UPDATE SET monitored=EXCLUDED.monitored,prefer_season_pack=EXCLUDED.prefer_season_pack,search_immediately=EXCLUDED.search_immediately,state='wanted',updated_at=EXCLUDED.updated_at`, item.ID.String(), int16(season.Number), season.Monitored, season.PreferSeasonPack, season.SearchImmediately, now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
		digest, sortedEpisodes, err := manualExpectationDigest(item.ID, *season)
		if err != nil {
			return workflows.Workflow{}, err
		}
		expectationID := uuid.NewV7()
		if _, err := transaction.Exec(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,1,$5,$6,$6)`, expectationID.String(), workflow.ID.String(), seriesIdentity(item), int16(season.Number), digest, now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
		for _, episode := range sortedEpisodes {
			if _, err := transaction.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title,air_date) VALUES($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,'')::date)`, expectationID.String(), int16(season.Number), int16(episode.Number), strings.TrimSpace(episode.ProviderID), strings.TrimSpace(episode.Title), strings.TrimSpace(episode.AirDate)); err != nil {
				return workflows.Workflow{}, err
			}
		}
	}
	detail, _ := json.Marshal(map[string]any{"workflow_id": workflow.ID, "variant": variant, "search_immediately": search, "adopted_replacement_authorizations": len(adoptedFiles)})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'workflow.created','media',$3,$4,$5)`, uuid.NewV7().String(), actor, item.ID.String(), string(detail), now.UTC()); err != nil {
		return workflows.Workflow{}, err
	}
	return workflow, transaction.Commit(ctx)
}

func (store *Store) RequestWorkflowSearch(ctx context.Context, workflowID uuid.UUID, adoptedReplacementConfirmation string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var attentionCode, mediaText, variant string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,COALESCE(attention_code,''),media_id::text,variant FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &revision, &attentionCode, &mediaText, &variant); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state == workflows.StateSearching {
		return transaction.Commit(ctx)
	}
	if state != workflows.StateMonitored && state != workflows.StateWaiting && state != workflows.StateNeedsAttention && state != workflows.StateHeld {
		return errors.New("workflow cannot be searched from its current state")
	}
	var exhaustedRound, retryDiscovery bool
	if err := transaction.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM acquisition_rounds exhausted WHERE exhausted.workflow_id=$1 AND exhausted.state='exhausted'
		  AND NOT EXISTS(SELECT 1 FROM acquisition_rounds open_round WHERE open_round.workflow_id=$1 AND open_round.state='open')),
		EXISTS(SELECT 1 FROM workflow_retry_discovery WHERE workflow_id=$1 AND state IN ('waiting','ready'))`, workflowID.String()).Scan(&exhaustedRound, &retryDiscovery); err != nil {
		return err
	}
	if exhaustedRound || retryDiscovery {
		return errors.New("the acquisition round is exhausted; wait for a new eligible release and use the owner start-next-round action")
	}
	var indexerAvailable bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND enabled AND config->'mode_verified' = 'true'::jsonb)`).Scan(&indexerAvailable); err != nil {
		return err
	}
	if !indexerAvailable {
		return errors.New("search requires at least one commissioned Newznab indexer")
	}
	type failedOutput struct {
		candidateID, indexerID uuid.UUID
		nativeKey, reason      string
	}
	var outputFailure *failedOutput
	if state == workflows.StateNeedsAttention {
		if infrastructureAttentionCode(attentionCode) || attentionCode == "altmount_submission_rejected" {
			return errors.New("workflow needs owner recovery of the current AltMount job before another search can start")
		}
		switch attentionCode {
		case "plex_timeout", "publication_failed", "seerr_snapshot_changed", "seerr_request_revoked":
			return errors.New("workflow needs owner recovery before another search can start")
		}
		var publicationStarted bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_link_versions WHERE workflow_id=$1 AND state IN ('publishing','verified','current','quarantined'))`, workflowID.String()).Scan(&publicationStarted); err != nil {
			return err
		}
		if publicationStarted {
			return errors.New("workflow publication already started; recover or verify the existing links instead of searching again")
		}
		var jobState, jobText, candidateText, indexerText, nativeKey, detail string
		var roundSlot, roundLimit int
		var packageIdentity []byte
		err := transaction.QueryRow(ctx, `SELECT job.state,job.id::text,job.round_slot,round.attempt_limit_snapshot,candidate.id::text,candidate.indexer_instance_id::text,candidate.native_key,COALESCE(job.error_detail,''),job.nzb_package_sha256
			FROM workflows workflow
			JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
			JOIN acquisition_rounds round ON round.id=job.round_id AND round.workflow_id=workflow.id
			JOIN release_candidates candidate ON candidate.id=job.candidate_id AND candidate.workflow_id=workflow.id AND candidate.id=workflow.selected_release_id
			WHERE workflow.id=$1
			FOR UPDATE OF job,round`, workflowID.String()).Scan(&jobState, &jobText, &roundSlot, &roundLimit, &candidateText, &indexerText, &nativeKey, &detail, &packageIdentity)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && jobState == "needs_attention" {
			if roundSlot >= roundLimit {
				return errors.New("acquisition round attempt limit reached; failed output remains available for owner review")
			}
			candidateID, candidateErr := uuid.Parse(candidateText)
			if candidateErr != nil {
				return candidateErr
			}
			indexerID, indexerErr := uuid.Parse(indexerText)
			if indexerErr != nil {
				return indexerErr
			}
			detail = strings.TrimSpace(detail)
			if detail == "" {
				detail = "completed AltMount output did not satisfy release requirements"
			}
			if len(detail) > 1000 {
				detail = detail[:1000]
			}
			outputFailure = &failedOutput{candidateID: candidateID, indexerID: indexerID, nativeKey: nativeKey, reason: detail}
			if len(packageIdentity) == 32 {
				if _, err := transaction.Exec(ctx, `INSERT INTO workflow_package_exclusions(workflow_id,package_sha256,job_id,candidate_id,failure_kind,reason,created_at)
					VALUES($1,$2,$3,$4,'output_verification',$5,$6) ON CONFLICT(workflow_id,package_sha256) DO NOTHING`, workflowID.String(), packageIdentity, jobText, candidateText, detail, now.UTC()); err != nil {
					return err
				}
			}
		}
	}
	type adoptedCurrent struct {
		id                  string
		destinationRelative string
		size, modtimeNS     int64
		authorized          bool
	}
	rows, err := transaction.Query(ctx, `SELECT adopted.id::text,adopted.destination_relative,adopted.source_size,adopted.source_modtime_ns,
		EXISTS(SELECT 1 FROM adopted_replacement_authorizations replacement_auth WHERE replacement_auth.workflow_id=$1 AND replacement_auth.adopted_file_id=adopted.id AND replacement_auth.destination_relative=adopted.destination_relative AND replacement_auth.source_size=adopted.source_size AND replacement_auth.source_modtime_ns=adopted.source_modtime_ns)
		FROM adopted_files adopted WHERE adopted.media_id=$2 AND adopted.variant=$3 AND adopted.state='current' ORDER BY adopted.id FOR UPDATE`, workflowID.String(), mediaText, variant)
	if err != nil {
		return err
	}
	var adoptedFiles []adoptedCurrent
	missingAuthorization := false
	for rows.Next() {
		var item adoptedCurrent
		if err := rows.Scan(&item.id, &item.destinationRelative, &item.size, &item.modtimeNS, &item.authorized); err != nil {
			rows.Close()
			return err
		}
		missingAuthorization = missingAuthorization || !item.authorized
		adoptedFiles = append(adoptedFiles, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if missingAuthorization {
		if adoptedReplacementConfirmation != AdoptedReplacementConfirmation {
			return ErrAdoptedReplacementConfirmationRequired
		}
		for _, adopted := range adoptedFiles {
			if adopted.authorized {
				continue
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO adopted_replacement_authorizations(workflow_id,adopted_file_id,destination_relative,source_size,source_modtime_ns,authorized_by,authorized_at) VALUES($1,$2,$3,$4,$5,'owner',$6)`, workflowID.String(), adopted.id, adopted.destinationRelative, adopted.size, adopted.modtimeNS, now.UTC()); err != nil {
				return err
			}
		}
		detail, _ := json.Marshal(map[string]any{"adopted_files": len(adoptedFiles), "variant": variant, "search_queued": true})
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','workflow.adopted_replacement_authorized','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), detail, now.UTC()); err != nil {
			return err
		}
	}
	if outputFailure != nil {
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at)
			VALUES($1,$2,$3,$4,NULL,'output_verification',$5,$6)
			ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO NOTHING`, workflowID.String(), outputFailure.candidateID.String(), outputFailure.indexerID.String(), outputFailure.nativeKey, outputFailure.reason, now.UTC()); err != nil {
			return err
		}
	}
	newRevision := revision + 1
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='searching',revision=$2,updated_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1`, workflowID.String(), newRevision, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'searching','owner requested search','owner',$4)`, workflowID.String(), newRevision, state, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), newRevision, string(payload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) insertManualWorkflow(ctx context.Context, transaction pgx.Tx, workflow workflows.Workflow, variant string, search bool) error {
	if _, err := transaction.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, workflow.ID.String(), workflow.MediaID.String(), workflow.State, workflow.RootID.String(), workflow.QualityProfileID.String(), workflow.DownloadClientID.String(), variant, int64(workflow.Revision), workflow.CreatedAt, workflow.UpdatedAt); err != nil {
		return err
	}
	if err := store.insertInitialAcquisitionRound(ctx, transaction, workflow.ID, workflow.MediaID, workflow.QualityProfileID, variant, workflow.CreatedAt); err != nil {
		return err
	}
	if search {
		payload, _ := json.Marshal(map[string]string{"workflow_id": workflow.ID.String(), "variant": variant})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,1,'workflow.search_requested',$3,$4,$4)`, uuid.NewV7().String(), workflow.ID.String(), string(payload), workflow.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func manualExpectationDigest(mediaID uuid.UUID, season ManualSeason) ([]byte, []ManualEpisode, error) {
	episodes := append([]ManualEpisode(nil), season.Episodes...)
	sort.Slice(episodes, func(i, j int) bool { return episodes[i].Number < episodes[j].Number })
	hash := sha256.New()
	fmt.Fprintf(hash, "manual-expectation-v2\x00%s\x00%d\x00", mediaID.String(), season.Number)
	var previous uint16
	for _, episode := range episodes {
		if episode.Number == 0 || episode.Number > 999 || episode.Number == previous {
			return nil, nil, errors.New("episode snapshot contains an invalid or duplicate episode")
		}
		previous = episode.Number
		airDate := strings.TrimSpace(episode.AirDate)
		if airDate != "" {
			if _, err := time.Parse("2006-01-02", airDate); err != nil {
				return nil, nil, errors.New("episode snapshot contains an invalid air date")
			}
		}
		fmt.Fprintf(hash, "%d\x00%s\x00%s\x00%s\x00", episode.Number, strings.TrimSpace(episode.ProviderID), strings.TrimSpace(episode.Title), airDate)
	}
	return hash.Sum(nil), episodes, nil
}

func seriesIdentity(item catalog.MediaItem) string {
	for _, namespace := range []string{"tvdb", "tmdb"} {
		for _, identity := range item.Identities {
			if identity.State == catalog.IdentityVerified && identity.Namespace == namespace {
				return namespace + ":" + identity.Value
			}
		}
	}
	return item.ID.String()
}
