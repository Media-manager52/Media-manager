package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

type PublicationAsset struct {
	ID           uuid.UUID
	RelativePath string
	Size         int64
	Episodes     []packs.ExpectedEpisode
	Companions   []PublicationCompanion
}

type PublicationCompanion struct {
	AssetID         uuid.UUID
	RelativePath    string
	Language        string
	Forced          *bool
	HearingImpaired *bool
}

type PublicationContext struct {
	Workflow              workflows.Workflow
	Media                 catalog.MediaItem
	Root                  LibraryRoot
	AltMount              Integration
	InventoryRootRelative string
	Assets                []PublicationAsset
	Technical             []string
	ReleaseToken          string
}

type PublicationVersion struct {
	ID                  uuid.UUID
	AssetID             uuid.UUID
	DestinationRelative string
	SourceRelative      string
	PlexVisibleTarget   string
	State               string
	Generation          int
}

type PublicationPlan struct {
	AssetID             uuid.UUID
	DestinationRelative string
	SourceRelative      string
	PlexVisibleTarget   string
	Companions          []PublicationCompanionPlan
}

type PublicationCompanionPlan struct {
	AssetID             uuid.UUID
	Language            string
	Forced              *bool
	HearingImpaired     *bool
	DestinationRelative string
	SourceRelative      string
	PlexVisibleTarget   string
}

func (store *Store) LoadPublicationContext(ctx context.Context, workflowID, jobID uuid.UUID) (PublicationContext, error) {
	workflow, err := store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return PublicationContext{}, err
	}
	if workflow.State == workflows.StatePlexScanning || workflow.State == workflows.StatePlexConfirmed {
		return PublicationContext{Workflow: workflow}, nil
	}
	if workflow.State != workflows.StateImportComplete && workflow.State != workflows.StateVerifying && workflow.State != workflows.StatePublishing {
		return PublicationContext{}, ErrConcurrentModification
	}
	if workflow.GrabAttemptID == nil || *workflow.GrabAttemptID != jobID {
		return PublicationContext{}, ErrConcurrentModification
	}
	if workflow.SelectedRelease == nil {
		return PublicationContext{}, errors.New("publication has no selected release identity")
	}
	var currentJob bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM altmount_jobs job
		JOIN workflows workflow ON workflow.id=job.workflow_id
		WHERE job.id=$1 AND job.workflow_id=$2 AND job.candidate_id=workflow.selected_release_id AND job.state='import_complete'
	)`, jobID.String(), workflowID.String()).Scan(&currentJob); err != nil {
		return PublicationContext{}, err
	}
	if !currentJob {
		return PublicationContext{}, ErrConcurrentModification
	}
	var result PublicationContext
	result.Workflow = workflow
	var mediaID string
	var year *int16
	var revision int64
	err = store.Pool.QueryRow(ctx, `SELECT id::text,kind,title,year,revision,created_at,updated_at FROM media_items WHERE id=$1`, workflow.MediaID.String()).Scan(&mediaID, &result.Media.Kind, &result.Media.Title, &year, &revision, &result.Media.CreatedAt, &result.Media.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicationContext{}, ErrNotFound
	}
	if err != nil {
		return PublicationContext{}, err
	}
	result.Media.ID, _ = uuid.Parse(mediaID)
	result.Media.Revision = uint64(revision)
	if year != nil {
		converted := uint16(*year)
		result.Media.Year = &converted
	}
	result.Media.Identities, err = store.identities(ctx, result.Media.ID)
	if err != nil {
		return PublicationContext{}, err
	}
	result.ReleaseToken = releaseToken(*workflow.SelectedRelease)
	var factsBody []byte
	if factErr := store.Pool.QueryRow(ctx, `SELECT observation.normalized_facts FROM release_candidates candidate JOIN release_observations observation ON observation.id=candidate.observation_id WHERE candidate.id=$1`, workflow.SelectedRelease.String()).Scan(&factsBody); factErr == nil {
		var facts []releases.Fact
		if json.Unmarshal(factsBody, &facts) == nil {
			for _, fact := range facts {
				if fact.State == releases.FactConflicting || fact.State == releases.FactUnknown {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(fact.Name)) {
				case "resolution", "source", "video_codec", "dynamic_range", "audio_codec":
					result.Technical = append(result.Technical, fact.Values...)
				}
			}
		}
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return PublicationContext{}, err
	}
	for _, root := range roots {
		if root.ID == workflow.RootID && root.Enabled {
			result.Root = root
			break
		}
	}
	if result.Root.ID == (uuid.UUID{}) || result.Root.PlexInstanceID == nil || result.Root.PlexLibraryID == "" {
		return PublicationContext{}, errors.New("library root has no enabled Plex mapping")
	}
	result.AltMount, err = store.Integration(ctx, workflow.DownloadClientID)
	if err != nil {
		return PublicationContext{}, err
	}
	if result.AltMount.Kind != "altmount" || !result.AltMount.Enabled {
		return PublicationContext{}, errors.New("workflow does not use an enabled AltMount client")
	}
	var inventoryID string
	err = store.Pool.QueryRow(ctx, `SELECT inventory.id::text,inventory.root_relative
		FROM altmount_output_inventories inventory
		WHERE inventory.job_id=$1
		ORDER BY inventory.generation DESC,inventory.frozen_at DESC,inventory.id DESC LIMIT 1`, jobID.String()).Scan(&inventoryID, &result.InventoryRootRelative)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicationContext{}, ErrNotFound
	}
	if err != nil {
		return PublicationContext{}, err
	}
	var verificationID string
	var verificationStatus string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text,status FROM subtitle_verifications WHERE altmount_job_id=$1 AND inventory_id=$2`, jobID.String(), inventoryID).Scan(&verificationID, &verificationStatus); err != nil {
		return PublicationContext{}, err
	}
	if verificationStatus != "verified" {
		return PublicationContext{}, errors.New("AltMount output subtitle requirements are not verified")
	}
	if result.Media.Kind == catalog.KindMovie {
		rows, queryErr := store.Pool.Query(ctx, `SELECT id::text,relative_path,size_bytes FROM altmount_output_assets WHERE inventory_id=$1 ORDER BY size_bytes DESC,relative_path`, inventoryID)
		if queryErr != nil {
			return PublicationContext{}, queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var asset PublicationAsset
			var idText string
			if err := rows.Scan(&idText, &asset.RelativePath, &asset.Size); err != nil {
				return PublicationContext{}, err
			}
			asset.ID, _ = uuid.Parse(idText)
			if playablePublicationAsset(asset.RelativePath) && !samplePublicationAsset(asset.RelativePath) {
				result.Assets = append(result.Assets, asset)
			}
		}
		if err := rows.Err(); err != nil {
			return PublicationContext{}, err
		}
		if len(result.Assets) > 1 && result.Assets[1].Size*5 >= result.Assets[0].Size {
			return PublicationContext{}, errors.New("movie inventory has no dominant media asset")
		}
		if len(result.Assets) == 0 {
			return PublicationContext{}, errors.New("movie inventory has no playable media asset")
		}
		result.Assets = result.Assets[:1]
		if err := store.loadPublicationCompanions(ctx, verificationID, &result); err != nil {
			return PublicationContext{}, err
		}
		return result, nil
	}
	rows, err := store.Pool.Query(ctx, `SELECT group_row.id,asset.id::text,asset.relative_path,asset.size_bytes,episode.season,episode.episode,COALESCE(expected.provider_episode_id,''),COALESCE(expected.title,'')
		FROM pack_evaluations evaluation
		JOIN pack_content_groups group_row ON group_row.evaluation_id=evaluation.id
		JOIN pack_group_assets group_asset ON group_asset.group_id=group_row.id
		JOIN altmount_output_assets asset ON asset.id=group_asset.asset_id
		JOIN pack_group_episodes episode ON episode.group_id=group_row.id
		LEFT JOIN pack_expected_episodes expected ON expected.expectation_id=evaluation.expectation_id AND expected.season=episode.season AND expected.episode=episode.episode
		WHERE evaluation.inventory_id=$1 AND evaluation.verdict='verified'
		ORDER BY group_row.ordinal,asset.relative_path,episode.season,episode.episode`, inventoryID)
	if err != nil {
		return PublicationContext{}, err
	}
	defer rows.Close()
	groupIndex := make(map[string]int)
	for rows.Next() {
		var groupID, assetID, relativePath, providerEpisodeID, title string
		var size int64
		var season, episode int16
		if err := rows.Scan(&groupID, &assetID, &relativePath, &size, &season, &episode, &providerEpisodeID, &title); err != nil {
			return PublicationContext{}, err
		}
		key := groupID + "\x00" + assetID
		index, exists := groupIndex[key]
		if !exists {
			parsed, parseErr := uuid.Parse(assetID)
			if parseErr != nil {
				return PublicationContext{}, parseErr
			}
			index = len(result.Assets)
			groupIndex[key] = index
			result.Assets = append(result.Assets, PublicationAsset{ID: parsed, RelativePath: relativePath, Size: size})
		}
		result.Assets[index].Episodes = append(result.Assets[index].Episodes, packs.ExpectedEpisode{Key: packs.EpisodeKey{Season: uint16(season), Episode: uint16(episode)}, ProviderEpisodeID: providerEpisodeID, Title: title})
	}
	if err := rows.Err(); err != nil {
		return PublicationContext{}, err
	}
	if len(result.Assets) == 0 {
		return PublicationContext{}, errors.New("verified season pack has no content groups")
	}
	if err := store.loadPublicationCompanions(ctx, verificationID, &result); err != nil {
		return PublicationContext{}, err
	}
	return result, nil
}

func (store *Store) loadPublicationCompanions(ctx context.Context, verificationID string, result *PublicationContext) error {
	byVideo := make(map[uuid.UUID]int, len(result.Assets))
	for index, asset := range result.Assets {
		byVideo[asset.ID] = index
	}
	rows, err := store.Pool.Query(ctx, `SELECT companion.video_asset_id::text,companion.subtitle_asset_id::text,companion.relative_path,companion.language,companion.forced,companion.hearing_impaired FROM subtitle_verification_companions companion WHERE companion.verification_id=$1 ORDER BY companion.relative_path`, verificationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var videoText, assetText string
		var item PublicationCompanion
		if err := rows.Scan(&videoText, &assetText, &item.RelativePath, &item.Language, &item.Forced, &item.HearingImpaired); err != nil {
			return err
		}
		videoID, _ := uuid.Parse(videoText)
		index, exists := byVideo[videoID]
		if !exists {
			return errors.New("verified subtitle companion has no selected video")
		}
		item.AssetID, _ = uuid.Parse(assetText)
		result.Assets[index].Companions = append(result.Assets[index].Companions, item)
	}
	return rows.Err()
}

func (store *Store) PreparePublication(ctx context.Context, workflowID, jobID uuid.UUID, plans []PublicationPlan, now time.Time) ([]PublicationVersion, error) {
	if len(plans) == 0 {
		return nil, errors.New("publication plan is empty")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var rootID string
	var attemptText *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,root_id::text,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &revision, &rootID, &attemptText); err != nil {
		return nil, err
	}
	if attemptText == nil || *attemptText != jobID.String() {
		return nil, ErrConcurrentModification
	}
	if state == workflows.StateImportComplete {
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='verifying',revision=revision+1,updated_at=$2 WHERE id=$1`, workflowID.String(), now.UTC()); err != nil {
			return nil, err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'import_complete','verifying','building a contained library publication plan','worker',$3)`, workflowID.String(), revision+1, now.UTC()); err != nil {
			return nil, err
		}
		revision++
		state = workflows.StateVerifying
	}
	if state != workflows.StateVerifying && state != workflows.StatePublishing {
		return nil, ErrConcurrentModification
	}
	versions := make([]PublicationVersion, 0, len(plans))
	for _, plan := range plans {
		linkID := uuid.NewV7()
		var linkText string
		if err := transaction.QueryRow(ctx, `INSERT INTO library_links(id,root_id,destination_relative,created_at) VALUES($1,$2,$3,$4)
			ON CONFLICT(root_id,destination_relative) DO UPDATE SET destination_relative=EXCLUDED.destination_relative RETURNING id::text`, linkID.String(), rootID, plan.DestinationRelative, now.UTC()).Scan(&linkText); err != nil {
			return nil, err
		}
		linkID, _ = uuid.Parse(linkText)
		var existing PublicationVersion
		var existingText, existingSource, existingTarget, existingDestination string
		err := transaction.QueryRow(ctx, `SELECT version.id::text,version.state,version.generation,version.source_relative,version.plex_visible_target,link.destination_relative
			FROM library_link_versions version JOIN library_links link ON link.id=version.library_link_id
			WHERE version.workflow_id=$1 AND version.altmount_job_id=$2 AND version.source_asset_id=$3`, workflowID.String(), jobID.String(), plan.AssetID.String()).Scan(&existingText, &existing.State, &existing.Generation, &existingSource, &existingTarget, &existingDestination)
		if err == nil {
			if existingSource != plan.SourceRelative || existingTarget != plan.PlexVisibleTarget || existingDestination != plan.DestinationRelative {
				return nil, errors.New("persisted publication plan differs from the retry plan")
			}
			existing.ID, _ = uuid.Parse(existingText)
			existing.AssetID = plan.AssetID
			existing.DestinationRelative = plan.DestinationRelative
			existing.SourceRelative = plan.SourceRelative
			existing.PlexVisibleTarget = plan.PlexVisibleTarget
			if err := preparePublicationCompanions(ctx, transaction, existing.ID, plan.Companions); err != nil {
				return nil, err
			}
			versions = append(versions, existing)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var generation int
		if err := transaction.QueryRow(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM library_link_versions WHERE library_link_id=$1`, linkID.String()).Scan(&generation); err != nil {
			return nil, err
		}
		versionID := uuid.NewV7()
		if _, err := transaction.Exec(ctx, `INSERT INTO library_link_versions(id,library_link_id,workflow_id,source_asset_id,altmount_job_id,generation,source_relative,plex_visible_target,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'planned')`, versionID.String(), linkID.String(), workflowID.String(), plan.AssetID.String(), jobID.String(), generation, plan.SourceRelative, plan.PlexVisibleTarget); err != nil {
			return nil, err
		}
		if err := preparePublicationCompanions(ctx, transaction, versionID, plan.Companions); err != nil {
			return nil, err
		}
		versions = append(versions, PublicationVersion{ID: versionID, AssetID: plan.AssetID, DestinationRelative: plan.DestinationRelative, SourceRelative: plan.SourceRelative, PlexVisibleTarget: plan.PlexVisibleTarget, State: "planned", Generation: generation})
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, err
	}
	return versions, nil
}

func preparePublicationCompanions(ctx context.Context, transaction pgx.Tx, versionID uuid.UUID, plans []PublicationCompanionPlan) error {
	for _, plan := range plans {
		var existingSource, existingDestination, existingTarget, existingLanguage string
		var existingForced, existingHearing *bool
		err := transaction.QueryRow(ctx, `SELECT source_relative,destination_relative,plex_visible_target,language,forced,hearing_impaired FROM library_link_companions WHERE link_version_id=$1 AND source_asset_id=$2`, versionID.String(), plan.AssetID.String()).Scan(&existingSource, &existingDestination, &existingTarget, &existingLanguage, &existingForced, &existingHearing)
		if err == nil {
			if existingSource != plan.SourceRelative || existingDestination != plan.DestinationRelative || existingTarget != plan.PlexVisibleTarget || existingLanguage != plan.Language || !sameOptionalBool(existingForced, plan.Forced) || !sameOptionalBool(existingHearing, plan.HearingImpaired) {
				return errors.New("persisted subtitle companion plan differs from the retry plan")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO library_link_companions(id,link_version_id,source_asset_id,language,forced,hearing_impaired,source_relative,destination_relative,plex_visible_target,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'planned')`, uuid.NewV7().String(), versionID.String(), plan.AssetID.String(), plan.Language, plan.Forced, plan.HearingImpaired, plan.SourceRelative, plan.DestinationRelative, plan.PlexVisibleTarget); err != nil {
			return err
		}
	}
	var count int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM library_link_companions WHERE link_version_id=$1`, versionID.String()).Scan(&count); err != nil {
		return err
	}
	if count != len(plans) {
		return errors.New("persisted subtitle companion set differs from the retry plan")
	}
	return nil
}

func sameOptionalBool(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (store *Store) MarkPublicationPublishing(ctx context.Context, workflowID, jobID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var attemptText *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &revision, &attemptText); err != nil {
		return err
	}
	if attemptText == nil || *attemptText != jobID.String() {
		return ErrConcurrentModification
	}
	if state == workflows.StatePublishing {
		return transaction.Commit(ctx)
	}
	if state != workflows.StateVerifying {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='publishing' WHERE workflow_id=$1 AND altmount_job_id=$2 AND state='planned'`, workflowID.String(), jobID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE library_link_companions companion SET state='publishing' FROM library_link_versions version WHERE companion.link_version_id=version.id AND version.workflow_id=$1 AND version.altmount_job_id=$2 AND companion.state='planned'`, workflowID.String(), jobID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='publishing',revision=revision+1,updated_at=$2 WHERE id=$1`, workflowID.String(), now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'verifying','publishing','publication preflight passed','worker',$3)`, workflowID.String(), revision+1, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// plexConfirmationWindow is how long one scan request may poll Plex for exact
// identity and path confirmation before it times out.
const plexConfirmationWindow = 20 * time.Minute

func (store *Store) CompletePublication(ctx context.Context, workflowID, jobID uuid.UUID, now time.Time) (uuid.UUID, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return uuid.UUID{}, err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var plexInstanceID *string
	var plexLibraryID string
	var attemptText *string
	if err := transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,workflow.grab_attempt_id::text,root.plex_instance_id::text,COALESCE(root.plex_library_id,'') FROM workflows workflow JOIN library_roots root ON root.id=workflow.root_id WHERE workflow.id=$1 FOR UPDATE OF workflow`, workflowID.String()).Scan(&state, &revision, &attemptText, &plexInstanceID, &plexLibraryID); err != nil {
		return uuid.UUID{}, err
	}
	if attemptText == nil || *attemptText != jobID.String() {
		return uuid.UUID{}, ErrConcurrentModification
	}
	if state == workflows.StatePlexScanning {
		var existing string
		if err := transaction.QueryRow(ctx, `SELECT id::text FROM plex_scan_requests WHERE workflow_id=$1 AND altmount_job_id=$2 ORDER BY requested_at DESC LIMIT 1`, workflowID.String(), jobID.String()).Scan(&existing); err != nil {
			return uuid.UUID{}, err
		}
		parsed, _ := uuid.Parse(existing)
		return parsed, transaction.Commit(ctx)
	}
	if state != workflows.StatePublishing || plexInstanceID == nil || plexLibraryID == "" {
		return uuid.UUID{}, ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='verified',verified_at=$3 WHERE workflow_id=$1 AND altmount_job_id=$2 AND state IN ('planned','publishing')`, workflowID.String(), jobID.String(), now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE library_link_companions companion SET state='verified',verified_at=$3 FROM library_link_versions version WHERE companion.link_version_id=version.id AND version.workflow_id=$1 AND version.altmount_job_id=$2 AND companion.state IN ('planned','publishing')`, workflowID.String(), jobID.String(), now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	scanID := uuid.NewV7()
	deadline := now.UTC().Add(plexConfirmationWindow)
	if _, err := transaction.Exec(ctx, `INSERT INTO plex_scan_requests(id,workflow_id,altmount_job_id,plex_instance_id,plex_library_id,import_generation,state,revision,poll_attempts,requested_at,deadline_at) VALUES($1,$2,$3,$4,$5,$6,'planned',1,0,$7,$8)`, scanID.String(), workflowID.String(), jobID.String(), *plexInstanceID, plexLibraryID, revision+1, now.UTC(), deadline); err != nil {
		return uuid.UUID{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='plex_scanning',revision=revision+1,updated_at=$2 WHERE id=$1`, workflowID.String(), now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'publishing','plex_scanning','library links verified; requesting Plex scan','worker',$3)`, workflowID.String(), revision+1, now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	payload, _ := json.Marshal(map[string]string{"scan_request_id": scanID.String(), "workflow_id": workflowID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'plex_scan',$2,1,'plex.scan_requested',$3,$4,$4)`, uuid.NewV7().String(), scanID.String(), string(payload), now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	return scanID, transaction.Commit(ctx)
}

func (store *Store) FailPublication(ctx context.Context, workflowID, jobID uuid.UUID, detail string, now time.Time) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var attemptText *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &revision, &attemptText); err != nil {
		return err
	}
	if attemptText == nil || *attemptText != jobID.String() {
		return transaction.Commit(ctx)
	}
	if state == workflows.StateNeedsAttention {
		return transaction.Commit(ctx)
	}
	if state != workflows.StateImportComplete && state != workflows.StateVerifying && state != workflows.StatePublishing {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='quarantined' WHERE workflow_id=$1 AND altmount_job_id=$2 AND state IN ('planned','publishing')`, workflowID.String(), jobID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE library_link_companions companion SET state='quarantined' FROM library_link_versions version WHERE companion.link_version_id=version.id AND version.workflow_id=$1 AND version.altmount_job_id=$2 AND companion.state IN ('planned','publishing')`, workflowID.String(), jobID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',revision=revision+1,updated_at=$2,attention_code='publication_failed',attention_detail=$3 WHERE id=$1`, workflowID.String(), now.UTC(), detail); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention',$4,'worker',$5)`, workflowID.String(), revision+1, state, detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

type PlexVersion struct {
	VersionID           uuid.UUID
	DestinationRelative string
	PlexVisiblePath     string
	Episodes            []packs.EpisodeKey
}

type PlexScanContext struct {
	ID           uuid.UUID
	State        string
	Revision     uint64
	PollAttempts int
	RequestedAt  time.Time
	DeadlineAt   time.Time
	Workflow     workflows.Workflow
	Media        catalog.MediaItem
	Root         LibraryRoot
	Integration  Integration
	Versions     []PlexVersion
}

func (store *Store) LoadPlexScan(ctx context.Context, scanID uuid.UUID) (PlexScanContext, error) {
	var result PlexScanContext
	var scanIDText, workflowID, mediaID, rootID, profileID, clientID, integrationID string
	var workflowRevision, scanRevision, mediaRevision int64
	var year *int16
	var config []byte
	err := store.Pool.QueryRow(ctx, `SELECT scan.id::text,scan.state,scan.revision,scan.poll_attempts,scan.requested_at,scan.deadline_at,
		workflow.id::text,workflow.state,workflow.revision,workflow.media_id::text,workflow.root_id::text,workflow.quality_profile_id::text,workflow.download_client_id::text,
		media.kind,media.title,media.year,media.revision,media.created_at,media.updated_at,
		root.name,root.destination_root,root.plex_visible_root,root.source_visible_root,root.plex_library_id,root.is_default,root.enabled,
		integration.id::text,integration.kind,integration.name,integration.enabled,integration.is_default,integration.config,COALESCE(integration.secret_ref,''),integration.created_at,integration.updated_at
		FROM plex_scan_requests scan
		JOIN workflows workflow ON workflow.id=scan.workflow_id
		JOIN media_items media ON media.id=workflow.media_id
		JOIN library_roots root ON root.id=workflow.root_id
		JOIN integration_instances integration ON integration.id=scan.plex_instance_id
		WHERE scan.id=$1`, scanID.String()).Scan(&scanIDText, &result.State, &scanRevision, &result.PollAttempts, &result.RequestedAt, &result.DeadlineAt,
		&workflowID, &result.Workflow.State, &workflowRevision, &mediaID, &rootID, &profileID, &clientID,
		&result.Media.Kind, &result.Media.Title, &year, &mediaRevision, &result.Media.CreatedAt, &result.Media.UpdatedAt,
		&result.Root.Name, &result.Root.DestinationRoot, &result.Root.PlexVisibleRoot, &result.Root.SourceVisibleRoot, &result.Root.PlexLibraryID, &result.Root.Default, &result.Root.Enabled,
		&integrationID, &result.Integration.Kind, &result.Integration.Name, &result.Integration.Enabled, &result.Integration.Default, &config, &result.Integration.SecretRef, &result.Integration.CreatedAt, &result.Integration.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlexScanContext{}, ErrNotFound
	}
	if err != nil {
		return PlexScanContext{}, err
	}
	result.ID = scanID
	result.Revision = uint64(scanRevision)
	result.Workflow.ID, _ = uuid.Parse(workflowID)
	result.Workflow.MediaID, _ = uuid.Parse(mediaID)
	result.Workflow.RootID, _ = uuid.Parse(rootID)
	result.Workflow.QualityProfileID, _ = uuid.Parse(profileID)
	result.Workflow.DownloadClientID, _ = uuid.Parse(clientID)
	result.Workflow.Revision = uint64(workflowRevision)
	result.Media.ID = result.Workflow.MediaID
	result.Media.Revision = uint64(mediaRevision)
	if year != nil {
		converted := uint16(*year)
		result.Media.Year = &converted
	}
	result.Media.Identities, err = store.identities(ctx, result.Media.ID)
	if err != nil {
		return PlexScanContext{}, err
	}
	result.Root.ID = result.Workflow.RootID
	parsedIntegration, _ := uuid.Parse(integrationID)
	result.Integration.ID = parsedIntegration
	result.Integration.Config = append(json.RawMessage(nil), config...)
	rows, err := store.Pool.Query(ctx, `SELECT version.id::text,link.destination_relative,episode.season,episode.episode
		FROM library_link_versions version
		JOIN library_links link ON link.id=version.library_link_id
		JOIN plex_scan_requests scan ON scan.id=$2
		LEFT JOIN LATERAL (
			SELECT DISTINCT group_episode.season,group_episode.episode
			FROM pack_group_assets group_asset
			JOIN pack_content_groups content_group ON content_group.id=group_asset.group_id
			JOIN pack_evaluations evaluation ON evaluation.id=content_group.evaluation_id AND evaluation.verdict='verified'
			JOIN pack_group_episodes group_episode ON group_episode.group_id=content_group.id
			WHERE group_asset.asset_id=version.source_asset_id
		) episode ON true
		WHERE version.workflow_id=$1 AND version.altmount_job_id=scan.altmount_job_id
		ORDER BY link.destination_relative,episode.season,episode.episode`, result.Workflow.ID.String(), scanID.String())
	if err != nil {
		return PlexScanContext{}, err
	}
	defer rows.Close()
	versionIndex := make(map[string]int)
	for rows.Next() {
		var version PlexVersion
		var versionID string
		var season, episode *int16
		if err := rows.Scan(&versionID, &version.DestinationRelative, &season, &episode); err != nil {
			return PlexScanContext{}, err
		}
		index, exists := versionIndex[versionID]
		if !exists {
			version.VersionID, _ = uuid.Parse(versionID)
			version.PlexVisiblePath = filepath.Join(result.Root.PlexVisibleRoot, version.DestinationRelative)
			index = len(result.Versions)
			versionIndex[versionID] = index
			result.Versions = append(result.Versions, version)
		}
		if season != nil && episode != nil {
			result.Versions[index].Episodes = append(result.Versions[index].Episodes, packs.EpisodeKey{Season: uint16(*season), Episode: uint16(*episode)})
		}
	}
	if err := rows.Err(); err != nil {
		return PlexScanContext{}, err
	}
	if len(result.Versions) == 0 {
		return PlexScanContext{}, errors.New("Plex scan has no library versions")
	}
	if result.Media.Kind == catalog.KindSeries {
		for _, version := range result.Versions {
			if len(version.Episodes) == 0 {
				return PlexScanContext{}, errors.New("series publication has no exact episode expectations")
			}
		}
	}
	return result, nil
}

func (store *Store) MarkPlexRefreshRequested(ctx context.Context, scan PlexScanContext, next time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	// The confirmation window starts when Plex is actually asked to refresh,
	// not when the scan was planned, so time spent waiting in the outbox never
	// counts against Plex.
	command, err := transaction.Exec(ctx, `UPDATE plex_scan_requests SET state='polling',revision=revision+1,deadline_at=GREATEST(deadline_at,$3) WHERE id=$1 AND state='planned' AND revision=$2`, scan.ID.String(), int64(scan.Revision), time.Now().UTC().Add(plexConfirmationWindow))
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var state string
		if err := transaction.QueryRow(ctx, `SELECT state FROM plex_scan_requests WHERE id=$1`, scan.ID.String()).Scan(&state); err != nil {
			return err
		}
		if state == "polling" || state == "confirmed" {
			return transaction.Commit(ctx)
		}
		return ErrConcurrentModification
	}
	payload, _ := json.Marshal(map[string]string{"scan_request_id": scan.ID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'plex_scan',$2,$3,'plex.confirm_requested',$4,now(),$5)`, uuid.NewV7().String(), scan.ID.String(), int64(scan.Revision+1), string(payload), next.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) ReschedulePlexConfirmation(ctx context.Context, scan PlexScanContext, next time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var revision int64
	if err := transaction.QueryRow(ctx, `UPDATE plex_scan_requests SET revision=revision+1,poll_attempts=poll_attempts+1,last_polled_at=now() WHERE id=$1 AND state='polling' AND revision=$2 RETURNING revision`, scan.ID.String(), int64(scan.Revision)).Scan(&revision); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"scan_request_id": scan.ID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'plex_scan',$2,$3,'plex.confirm_requested',$4,now(),$5)`, uuid.NewV7().String(), scan.ID.String(), revision, string(payload), next.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) TimeoutPlexScan(ctx context.Context, scan PlexScanContext, detail string, now time.Time) error {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "Plex did not confirm every expected path before the deadline"
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var scanState string
	var scanRevision, workflowRevision int64
	var deadline time.Time
	var workflowState workflows.State
	if err := transaction.QueryRow(ctx, `SELECT request.state,request.revision,request.deadline_at,workflow.state,workflow.revision FROM plex_scan_requests request JOIN workflows workflow ON workflow.id=request.workflow_id WHERE request.id=$1 FOR UPDATE OF request,workflow`, scan.ID.String()).Scan(&scanState, &scanRevision, &deadline, &workflowState, &workflowRevision); err != nil {
		return err
	}
	if scanState == "timed_out" || scanState == "confirmed" {
		return transaction.Commit(ctx)
	}
	if scanState != "planned" && scanState != "polling" || workflowState != workflows.StatePlexScanning || uint64(scanRevision) != scan.Revision || now.Before(deadline) {
		return ErrConcurrentModification
	}
	command, err := transaction.Exec(ctx, `UPDATE plex_scan_requests SET state='timed_out',revision=revision+1,completed_at=$2 WHERE id=$1 AND state IN ('planned','polling') AND revision=$3`, scan.ID.String(), now.UTC(), scanRevision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	command, err = transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',revision=revision+1,updated_at=$2,attention_code='plex_timeout',attention_detail=$4 WHERE id=$1 AND state='plex_scanning' AND revision=$3`, scan.Workflow.ID.String(), now.UTC(), workflowRevision, detail)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'plex_scanning','needs_attention','Plex confirmation timed out','worker',$3) ON CONFLICT DO NOTHING`, scan.Workflow.ID.String(), workflowRevision+1, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// maxPlexReconfirmations bounds owner retries of a timed-out Plex confirmation
// for one publication. Each retry already waits a full confirmation window, so
// repeated failure points at a Plex identity, path, or mount problem that the
// owner must investigate rather than another retry.
const maxPlexReconfirmations = 3

var (
	ErrPlexReconfirmationNotTimedOut = errors.New("workflow is not waiting on a timed-out Plex confirmation")
	ErrPlexReconfirmationUnverified  = errors.New("publication links are not all verified; Plex confirmation cannot be retried")
	ErrPlexReconfirmationLimit       = errors.New("Plex confirmation retry limit reached; investigate the Plex identity, path, or mount before retrying")
)

// RequestPlexReconfirmation lets the owner retry exact Plex confirmation for a
// workflow whose previous scan timed out. It reuses the existing verified link
// versions of the current attempt, never creates or changes links, and is
// idempotent: a workflow already back in plex_scanning with an open scan
// returns that scan unchanged.
func (store *Store) RequestPlexReconfirmation(ctx context.Context, workflowID uuid.UUID, actor string, now time.Time) (uuid.UUID, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return uuid.UUID{}, err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var attentionCode string
	var attemptText, plexInstanceID *string
	var plexLibraryID string
	err = transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,COALESCE(workflow.attention_code,''),workflow.grab_attempt_id::text,root.plex_instance_id::text,COALESCE(root.plex_library_id,'')
		FROM workflows workflow JOIN library_roots root ON root.id=workflow.root_id WHERE workflow.id=$1 FOR UPDATE OF workflow`, workflowID.String()).Scan(&state, &revision, &attentionCode, &attemptText, &plexInstanceID, &plexLibraryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, ErrNotFound
	}
	if err != nil {
		return uuid.UUID{}, err
	}
	if attemptText == nil {
		return uuid.UUID{}, ErrPlexReconfirmationNotTimedOut
	}
	var latestID, latestState string
	err = transaction.QueryRow(ctx, `SELECT id::text,state FROM plex_scan_requests WHERE workflow_id=$1 AND altmount_job_id=$2 ORDER BY requested_at DESC,id DESC LIMIT 1`, workflowID.String(), *attemptText).Scan(&latestID, &latestState)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, ErrPlexReconfirmationNotTimedOut
	}
	if err != nil {
		return uuid.UUID{}, err
	}
	if state == workflows.StatePlexScanning && (latestState == "planned" || latestState == "polling") {
		existing, _ := uuid.Parse(latestID)
		return existing, transaction.Commit(ctx)
	}
	if state != workflows.StateNeedsAttention || attentionCode != "plex_timeout" || latestState != "timed_out" {
		return uuid.UUID{}, ErrPlexReconfirmationNotTimedOut
	}
	if plexInstanceID == nil || plexLibraryID == "" {
		return uuid.UUID{}, errors.New("library root has no Plex library configured")
	}
	var versions, unverified int
	if err := transaction.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE version.state<>'verified' OR EXISTS(
			SELECT 1 FROM library_link_companions companion WHERE companion.link_version_id=version.id AND companion.state<>'verified'))
		FROM library_link_versions version WHERE version.workflow_id=$1 AND version.altmount_job_id=$2`, workflowID.String(), *attemptText).Scan(&versions, &unverified); err != nil {
		return uuid.UUID{}, err
	}
	if versions == 0 || unverified != 0 {
		return uuid.UUID{}, ErrPlexReconfirmationUnverified
	}
	var scans int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM plex_scan_requests WHERE workflow_id=$1 AND altmount_job_id=$2`, workflowID.String(), *attemptText).Scan(&scans); err != nil {
		return uuid.UUID{}, err
	}
	if scans-1 >= maxPlexReconfirmations {
		return uuid.UUID{}, ErrPlexReconfirmationLimit
	}
	newRevision := revision + 1
	scanID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO plex_scan_requests(id,workflow_id,altmount_job_id,plex_instance_id,plex_library_id,import_generation,state,revision,poll_attempts,requested_at,deadline_at) VALUES($1,$2,$3,$4,$5,$6,'planned',1,0,$7,$8)`, scanID.String(), workflowID.String(), *attemptText, *plexInstanceID, plexLibraryID, newRevision, now.UTC(), now.UTC().Add(plexConfirmationWindow)); err != nil {
		return uuid.UUID{}, err
	}
	command, err := transaction.Exec(ctx, `UPDATE workflows SET state='plex_scanning',revision=$2,updated_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$4`, workflowID.String(), newRevision, now.UTC(), revision)
	if err != nil {
		return uuid.UUID{}, err
	}
	if command.RowsAffected() != 1 {
		return uuid.UUID{}, ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'needs_attention','plex_scanning','owner retried Plex confirmation for verified links',$3,$4)`, workflowID.String(), newRevision, actor, now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	detail, _ := json.Marshal(map[string]any{"scan_request_id": scanID, "timed_out_scan_request_id": latestID, "altmount_job_id": *attemptText, "retry": scans, "retry_limit": maxPlexReconfirmations})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'workflow.plex_reconfirm_requested','workflow',$3,$4,$5)`, uuid.NewV7().String(), actor, workflowID.String(), string(detail), now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	payload, _ := json.Marshal(map[string]string{"scan_request_id": scanID.String(), "workflow_id": workflowID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'plex_scan',$2,1,'plex.scan_requested',$3,$4,$4)`, uuid.NewV7().String(), scanID.String(), string(payload), now.UTC()); err != nil {
		return uuid.UUID{}, err
	}
	return scanID, transaction.Commit(ctx)
}

func (store *Store) ConfirmPlexScan(ctx context.Context, scan PlexScanContext, confirmations []plex.Confirmation, now time.Time) error {
	if len(confirmations) != len(scan.Versions) {
		return errors.New("Plex confirmation count does not match publication")
	}
	byPath := make(map[string]plex.Confirmation, len(confirmations))
	for _, confirmation := range confirmations {
		byPath[filepath.Clean(confirmation.Path)] = confirmation
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var scanState string
	var scanRevision, workflowRevision int64
	var workflowState workflows.State
	if err := transaction.QueryRow(ctx, `SELECT request.state,request.revision,workflow.state,workflow.revision
		FROM plex_scan_requests request JOIN workflows workflow ON workflow.id=request.workflow_id
		WHERE request.id=$1 FOR UPDATE OF request,workflow`, scan.ID.String()).Scan(&scanState, &scanRevision, &workflowState, &workflowRevision); err != nil {
		return err
	}
	if scanState != "polling" || workflowState != workflows.StatePlexScanning || uint64(scanRevision) != scan.Revision {
		return ErrConcurrentModification
	}
	var companionsReady bool
	if err := transaction.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM library_link_companions companion
		JOIN library_link_versions version ON version.id=companion.link_version_id
		JOIN plex_scan_requests request ON request.id=$1
		WHERE version.workflow_id=request.workflow_id AND version.altmount_job_id=request.altmount_job_id AND companion.state<>'verified'
	)`, scan.ID.String()).Scan(&companionsReady); err != nil {
		return err
	}
	if !companionsReady {
		return errors.New("subtitle companions have not completed the Plex verification lifecycle")
	}
	type displacedVersion struct {
		ID         uuid.UUID
		Generation int
	}
	var displaced []displacedVersion
	rows, err := transaction.Query(ctx, `SELECT old.id::text,old.generation
		FROM library_link_versions old
		JOIN workflows old_workflow ON old_workflow.id=old.workflow_id
		JOIN workflows current_workflow ON current_workflow.id=$1
		WHERE old.state='current' AND old_workflow.media_id=current_workflow.media_id AND old_workflow.variant=current_workflow.variant
		  AND old.altmount_job_id<>(SELECT altmount_job_id FROM plex_scan_requests WHERE id=$2)
		FOR UPDATE OF old`, scan.Workflow.ID.String(), scan.ID.String())
	if err != nil {
		return err
	}
	for rows.Next() {
		var idText string
		var generation int
		if err := rows.Scan(&idText, &generation); err != nil {
			rows.Close()
			return err
		}
		parsed, _ := uuid.Parse(idText)
		displaced = append(displaced, displacedVersion{ID: parsed, Generation: generation})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, old := range displaced {
		if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='displaced',displaced_at=$2 WHERE id=$1`, old.ID.String(), now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_companions SET state='displaced',displaced_at=$2 WHERE link_version_id=$1 AND state='current'`, old.ID.String(), now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_links SET current_version_id=NULL WHERE current_version_id=$1`, old.ID.String()); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"version_id": old.ID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'link_version',$2,$3,'library.displaced_remove_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), old.ID.String(), old.Generation, string(payload), now.UTC()); err != nil {
			return err
		}
	}
	for _, version := range scan.Versions {
		confirmation, exists := byPath[filepath.Clean(version.PlexVisiblePath)]
		if !exists {
			return errors.New("Plex omitted a planned path")
		}
		var linkID string
		if err := transaction.QueryRow(ctx, `SELECT library_link_id::text FROM library_link_versions WHERE id=$1 FOR UPDATE`, version.VersionID.String()).Scan(&linkID); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_companions companion SET state='displaced',displaced_at=$3 FROM library_link_versions old WHERE companion.link_version_id=old.id AND old.library_link_id=$1 AND old.state='current' AND old.id<>$2 AND companion.state='current'`, linkID, version.VersionID.String(), now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='displaced',displaced_at=$2 WHERE library_link_id=$1 AND state='current' AND id<>$3`, linkID, now.UTC(), version.VersionID.String()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='current',verified_at=COALESCE(verified_at,$2) WHERE id=$1`, version.VersionID.String(), now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_companions SET state='current',verified_at=COALESCE(verified_at,$2) WHERE link_version_id=$1 AND state='verified'`, version.VersionID.String(), now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_links SET current_version_id=$2 WHERE id=$1`, linkID, version.VersionID.String()); err != nil {
			return err
		}
		namespace, value := splitPlexGUID(confirmation.ProviderGUID)
		digest := sha256.Sum256([]byte(confirmation.RatingKey + "\x00" + confirmation.Path + "\x00" + confirmation.ProviderGUID))
		if _, err := transaction.Exec(ctx, `INSERT INTO plex_confirmations(id,scan_request_id,link_version_id,plex_instance_id,plex_item_key,provider_namespace,provider_value,observed_path,evidence_sha256,confirmed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(link_version_id,plex_instance_id) DO NOTHING`, uuid.NewV7().String(), scan.ID.String(), version.VersionID.String(), scan.Integration.ID.String(), confirmation.RatingKey, namespace, value, confirmation.Path, digest[:], now.UTC()); err != nil {
			return err
		}
	}
	if _, err := store.planAdoptedUpgradeDisplacements(ctx, transaction, scan.Workflow.ID, now); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `UPDATE plex_scan_requests SET state='confirmed',revision=revision+1,completed_at=$2,last_polled_at=$2 WHERE id=$1 AND state='polling' AND revision=$3`, scan.ID.String(), now.UTC(), scanRevision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	command, err = transaction.Exec(ctx, `UPDATE workflows SET state='plex_confirmed',revision=revision+1,updated_at=$2,terminal_at=$2,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND state='plex_scanning' AND revision=$3`, scan.Workflow.ID.String(), now.UTC(), workflowRevision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'plex_scanning','plex_confirmed','Plex confirmed every expected identity and path','worker',$3) ON CONFLICT DO NOTHING`, scan.Workflow.ID.String(), workflowRevision+1, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE acquisition_rounds round SET state='plex_confirmed',closed_at=$2,close_reason='exact Plex identity and path confirmed'
		FROM plex_scan_requests scan WHERE scan.id=$3 AND round.workflow_id=$1 AND round.id=(SELECT job.round_id FROM altmount_jobs job WHERE job.id=scan.altmount_job_id)
		AND round.state IN ('open','exhausted')`, scan.Workflow.ID.String(), now.UTC(), scan.ID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='paused',next_rescan_at=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$2 WHERE workflow_id=$1`, scan.Workflow.ID.String(), now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		SELECT $1,'workflow',$2,$3,'seerr.status_sync_requested',$4,$5,$5
		WHERE EXISTS(SELECT 1 FROM seerr_request_workflows WHERE workflow_id=$2)
		-- Single-episode workflows are dispatched from a season workflow without
		-- their own request provenance; the request tracking that season still
		-- needs availability feedback for each confirmed episode.
		OR EXISTS(
			SELECT 1 FROM workflows episode
			JOIN workflows season ON season.media_id=episode.media_id
			  AND season.variant LIKE 'season:%' AND season.variant NOT LIKE '%:episode:%'
			  AND episode.variant LIKE season.variant || ':episode:%'
			JOIN seerr_request_workflows provenance ON provenance.workflow_id=season.id
			WHERE episode.id=$2
		)
		ON CONFLICT DO NOTHING`, uuid.NewV7().String(), scan.Workflow.ID.String(), workflowRevision+1, seerrStatusEventPayload(scan.Workflow.ID), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

type DisplacedLink struct {
	VersionID           uuid.UUID
	RootID              uuid.UUID
	DestinationRoot     string
	DestinationRelative string
	ExpectedTarget      string
	Removed             bool
	Companions          []DisplacedCompanion
}

type DisplacedCompanion struct {
	DestinationRelative string
	ExpectedTarget      string
}

func (store *Store) LoadDisplacedLink(ctx context.Context, versionID uuid.UUID) (DisplacedLink, error) {
	var result DisplacedLink
	var rootText string
	var state string
	err := store.Pool.QueryRow(ctx, `SELECT root.id::text,root.destination_root,link.destination_relative,version.plex_visible_target,version.state,EXISTS(SELECT 1 FROM library_link_removals removal WHERE removal.version_id=version.id)
		FROM library_link_versions version JOIN library_links link ON link.id=version.library_link_id JOIN library_roots root ON root.id=link.root_id WHERE version.id=$1`, versionID.String()).Scan(&rootText, &result.DestinationRoot, &result.DestinationRelative, &result.ExpectedTarget, &state, &result.Removed)
	if errors.Is(err, pgx.ErrNoRows) {
		return DisplacedLink{}, ErrNotFound
	}
	if err != nil {
		return DisplacedLink{}, err
	}
	if state != "displaced" {
		return DisplacedLink{}, ErrConcurrentModification
	}
	result.VersionID = versionID
	result.RootID, _ = uuid.Parse(rootText)
	rows, err := store.Pool.Query(ctx, `SELECT destination_relative,plex_visible_target FROM library_link_companions WHERE link_version_id=$1 AND state='displaced' ORDER BY destination_relative`, versionID.String())
	if err != nil {
		return DisplacedLink{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var companion DisplacedCompanion
		if err := rows.Scan(&companion.DestinationRelative, &companion.ExpectedTarget); err != nil {
			return DisplacedLink{}, err
		}
		result.Companions = append(result.Companions, companion)
	}
	if err := rows.Err(); err != nil {
		return DisplacedLink{}, err
	}
	return result, nil
}

func (store *Store) RecordDisplacedLinkRemoved(ctx context.Context, link DisplacedLink, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	command, err := transaction.Exec(ctx, `INSERT INTO library_link_removals(version_id,expected_target,removed_at) VALUES($1,$2,$3) ON CONFLICT(version_id) DO NOTHING`, link.VersionID.String(), link.ExpectedTarget, now.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() == 1 {
		payload, _ := json.Marshal(map[string]string{"version_id": link.VersionID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'link_version',$2,1,'plex.displaced_refresh_requested',$3,$4,$4)`, uuid.NewV7().String(), link.VersionID.String(), payload, now.UTC()); err != nil {
			return err
		}
	}
	detail, _ := json.Marshal(map[string]string{"destination_relative": link.DestinationRelative, "source_retained": "true"})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) SELECT $1,'worker','upgrade.displaced_link_removed','library_link_version',$2,$3,$4 WHERE NOT EXISTS(SELECT 1 FROM audit_events WHERE action='upgrade.displaced_link_removed' AND aggregate_id=$2)`, uuid.NewV7().String(), link.VersionID.String(), string(detail), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) PlexForDisplacedLink(ctx context.Context, versionID uuid.UUID) (DisplacedLink, LibraryRoot, Integration, error) {
	link, err := store.LoadDisplacedLink(ctx, versionID)
	if err != nil {
		return DisplacedLink{}, LibraryRoot{}, Integration{}, err
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return DisplacedLink{}, LibraryRoot{}, Integration{}, err
	}
	var root LibraryRoot
	for _, candidate := range roots {
		if candidate.ID == link.RootID {
			root = candidate
			break
		}
	}
	if root.ID == (uuid.UUID{}) || !root.Enabled || root.PlexInstanceID == nil || root.PlexLibraryID == "" {
		return DisplacedLink{}, LibraryRoot{}, Integration{}, errors.New("displaced link has no enabled Plex library")
	}
	integration, err := store.Integration(ctx, *root.PlexInstanceID)
	if err != nil {
		return DisplacedLink{}, LibraryRoot{}, Integration{}, err
	}
	return link, root, integration, nil
}

func playablePublicationAsset(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mp4", ".m4v", ".avi", ".ts":
		return true
	}
	return false
}

func samplePublicationAsset(path string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(filepath.Base(path)), func(value rune) bool { return value < 'a' || value > 'z' }) {
		if token == "sample" || token == "samples" {
			return true
		}
	}
	return false
}

// releaseToken derives the durable, deterministic filename token for a
// selected release. It embeds the full release UUID because the leading bytes
// of a UUIDv7 are a timestamp: truncating them would make two releases selected
// around the same time share a token and collide during an upgrade.
func releaseToken(releaseID uuid.UUID) string {
	return "MM-" + strings.ReplaceAll(releaseID.String(), "-", "")
}

func splitPlexGUID(guid string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(guid), "://", 2)
	if len(parts) != 2 {
		return "unknown", guid
	}
	return strings.ToLower(parts[0]), parts[1]
}

func sortPublicationEpisodes(episodes []packs.ExpectedEpisode) {
	sort.Slice(episodes, func(i, j int) bool { return episodes[i].Key.Episode < episodes[j].Key.Episode })
}
