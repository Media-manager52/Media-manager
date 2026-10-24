package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

type AltMountJobContext struct {
	JobID               uuid.UUID
	Workflow            workflows.Workflow
	Variant             string
	CandidateID         uuid.UUID
	UploadName          string
	JobState            string
	ErrorCode           string
	ErrorDetail         string
	SubmissionStartedAt time.Time
	ExternalQueueID     *int64
	JobRevision         uint64
	AttemptGeneration   int
	CurrentAttemptID    *uuid.UUID
	Client              Integration
	Root                LibraryRoot
	Media               catalog.MediaItem
	ReleaseObservation  releases.Observation
}

func (store *Store) LoadAltMountJob(ctx context.Context, jobID uuid.UUID) (AltMountJobContext, error) {
	var result AltMountJobContext
	var workflowText, candidateText, clientText, rootText, mediaText string
	var currentAttemptText *string
	var jobRevision, workflowRevision, mediaRevision int64
	var year *int16
	err := store.Pool.QueryRow(ctx, `SELECT j.workflow_id::text,j.candidate_id::text,j.upload_name,j.state,j.submission_started_at,j.external_queue_id,j.revision,j.attempt_generation,w.state,w.revision,w.variant,w.download_client_id::text,w.root_id::text,w.media_id::text,w.grab_attempt_id::text,m.kind,m.title,m.year,m.revision,m.created_at,m.updated_at,COALESCE(j.error_code,''),COALESCE(j.error_detail,'') FROM altmount_jobs j JOIN workflows w ON w.id=j.workflow_id JOIN media_items m ON m.id=w.media_id WHERE j.id=$1`, jobID.String()).Scan(&workflowText, &candidateText, &result.UploadName, &result.JobState, &result.SubmissionStartedAt, &result.ExternalQueueID, &jobRevision, &result.AttemptGeneration, &result.Workflow.State, &workflowRevision, &result.Variant, &clientText, &rootText, &mediaText, &currentAttemptText, &result.Media.Kind, &result.Media.Title, &year, &mediaRevision, &result.Media.CreatedAt, &result.Media.UpdatedAt, &result.ErrorCode, &result.ErrorDetail)
	if errors.Is(err, pgx.ErrNoRows) {
		return AltMountJobContext{}, ErrNotFound
	}
	if err != nil {
		return AltMountJobContext{}, err
	}
	result.JobID = jobID
	result.CandidateID, _ = uuid.Parse(candidateText)
	var releaseFacts []byte
	err = store.Pool.QueryRow(ctx, `SELECT o.title,o.parse_version,o.normalized_facts FROM release_candidates c JOIN release_observations o ON o.id=c.observation_id WHERE c.id=$1 AND c.workflow_id=$2`, candidateText, workflowText).Scan(&result.ReleaseObservation.Title, &result.ReleaseObservation.ParseVersion, &releaseFacts)
	if err != nil {
		return AltMountJobContext{}, err
	}
	if err = json.Unmarshal(releaseFacts, &result.ReleaseObservation.Facts); err != nil {
		return AltMountJobContext{}, err
	}
	result.Workflow.ID, _ = uuid.Parse(workflowText)
	result.Workflow.DownloadClientID, _ = uuid.Parse(clientText)
	result.Workflow.RootID, _ = uuid.Parse(rootText)
	result.Workflow.MediaID, _ = uuid.Parse(mediaText)
	result.Workflow.Revision = uint64(workflowRevision)
	result.JobRevision = uint64(jobRevision)
	if currentAttemptText != nil {
		parsed, parseErr := uuid.Parse(*currentAttemptText)
		if parseErr != nil {
			return AltMountJobContext{}, parseErr
		}
		result.CurrentAttemptID = &parsed
		result.Workflow.GrabAttemptID = &parsed
	}
	result.Media.ID = result.Workflow.MediaID
	result.Media.Revision = uint64(mediaRevision)
	if year != nil {
		converted := uint16(*year)
		result.Media.Year = &converted
	}
	result.Media.Identities, err = store.identities(ctx, result.Media.ID)
	if err != nil {
		return AltMountJobContext{}, err
	}
	// Runtime and original-language metadata drive output checks for every
	// release, not only OldBoys. A missing row is not an error.
	result.Media.Metadata, err = store.metadata(ctx, result.Media.ID)
	if err != nil {
		return AltMountJobContext{}, err
	}
	result.Client, err = store.Integration(ctx, result.Workflow.DownloadClientID)
	if err != nil {
		return AltMountJobContext{}, err
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return AltMountJobContext{}, err
	}
	for _, root := range roots {
		if root.ID == result.Workflow.RootID {
			result.Root = root
			break
		}
	}
	if result.Root.ID == (uuid.UUID{}) {
		return AltMountJobContext{}, ErrNotFound
	}
	return result, nil
}

func (store *Store) AttachRecoveredQueue(ctx context.Context, job AltMountJobContext, queueID int64, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var workflowText string
	var existingQueueID *int64
	var jobRevision int64
	if err := transaction.QueryRow(ctx, `SELECT workflow_id::text,external_queue_id,revision FROM altmount_jobs WHERE id=$1 FOR UPDATE`, job.JobID.String()).Scan(&workflowText, &existingQueueID, &jobRevision); err != nil {
		return err
	}
	if uint64(jobRevision) != job.JobRevision {
		return transaction.Commit(ctx)
	}
	if existingQueueID != nil && *existingQueueID != queueID {
		return ErrConcurrentModification
	}
	nextJobRevision := jobRevision
	if existingQueueID == nil {
		if err := transaction.QueryRow(ctx, `UPDATE altmount_jobs SET external_queue_id=$2,state='queued',last_reconciled_at=$3,revision=revision+1 WHERE id=$1 AND revision=$4 RETURNING revision`, job.JobID.String(), queueID, now.UTC(), jobRevision).Scan(&nextJobRevision); err != nil {
			return err
		}
	}
	var current workflows.State
	var revision int64
	var currentAttempt *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, workflowText).Scan(&current, &revision, &currentAttempt); err != nil {
		return err
	}
	if currentAttempt == nil || *currentAttempt != job.JobID.String() {
		return transaction.Commit(ctx)
	}
	if current == workflows.StateTransportUnknown {
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='queued',revision=revision+1,updated_at=$2 WHERE id=$1`, workflowText, now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'queued','recovered AltMount queue identity','worker',$4)`, workflowText, revision+1, current, now.UTC()); err != nil {
			return err
		}
	} else if current != workflows.StateQueued {
		return ErrConcurrentModification
	}
	if existingQueueID == nil {
		payload, _ := json.Marshal(map[string]string{"job_id": job.JobID.String(), "workflow_id": job.Workflow.ID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'altmount_job',$2,$3,'altmount.reconcile_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), job.JobID.String(), nextJobRevision, string(payload), now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func (store *Store) RequestAltMountReconciliation(ctx context.Context, workflowID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var jobText, workflowState, jobState, errorCode string
	var queueID *int64
	var revision int64
	var workflowRevision int64
	err = transaction.QueryRow(ctx, `SELECT job.id::text,workflow.state,workflow.revision,job.state,COALESCE(job.error_code,''),job.external_queue_id,job.revision
		FROM workflows workflow JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
		WHERE workflow.id=$1 FOR UPDATE OF workflow,job`, workflowID.String()).Scan(&jobText, &workflowState, &workflowRevision, &jobState, &errorCode, &queueID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if workflowState == string(workflows.StateNeedsAttention) && jobState == "needs_attention" && infrastructureAttentionCode(errorCode) {
		nextWorkflowState, nextJobState := workflows.StateQueued, "queued"
		if queueID == nil {
			nextWorkflowState, nextJobState = workflows.StateTransportUnknown, "transport_unknown"
		}
		command, updateErr := transaction.Exec(ctx, `UPDATE altmount_jobs SET state=$2,revision=revision+1,last_reconciled_at=$3 WHERE id=$1 AND revision=$4 AND acquisition_closed_at IS NULL`, jobText, nextJobState, now.UTC(), revision)
		if updateErr != nil {
			return updateErr
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
		command, updateErr = transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=revision+1,updated_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$4`, workflowID.String(), nextWorkflowState, now.UTC(), workflowRevision)
		if updateErr != nil {
			return updateErr
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
		if _, updateErr := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,'owner requested recovery of the same retained AltMount job','owner',$5)`, workflowID.String(), workflowRevision+1, workflowState, nextWorkflowState, now.UTC()); updateErr != nil {
			return updateErr
		}
		revision++
	} else if queueID == nil || workflowState != string(workflows.StateQueued) || terminalAltMountState(jobState) {
		return ErrConcurrentModification
	}
	jobID, parseErr := uuid.Parse(jobText)
	if parseErr != nil {
		return parseErr
	}
	payload, _ := json.Marshal(map[string]string{"job_id": jobID.String(), "workflow_id": workflowID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'altmount_job',$2,$3,'altmount.reconcile_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), jobID.String(), revision, string(payload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func infrastructureAttentionCode(code string) bool {
	switch code {
	case "altmount_identity_unresolved", "altmount_identity_ambiguous", "altmount_output_unavailable", "altmount_job_attention", "season_expectation_missing", "output_inspection_failed", "infrastructure_attention":
		return true
	default:
		return false
	}
}

func terminalAltMountState(state string) bool {
	switch state {
	case "import_complete", "failed", "cancelled", "removed", "needs_attention":
		return true
	default:
		return false
	}
}

func (store *Store) ScheduleAltMountReconcile(ctx context.Context, job AltMountJobContext, next time.Time) error {
	payload, _ := json.Marshal(map[string]string{"job_id": job.JobID.String(), "workflow_id": job.Workflow.ID.String()})
	_, err := store.Pool.Exec(ctx, `WITH bumped AS (
		UPDATE altmount_jobs job SET last_reconciled_at=now(),revision=job.revision+1
		WHERE job.id=$1 AND job.revision=$5 AND EXISTS(SELECT 1 FROM workflows workflow WHERE workflow.id=job.workflow_id AND workflow.grab_attempt_id=job.id)
		RETURNING job.revision
	) INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) SELECT $2,'altmount_job',$1,revision,'altmount.reconcile_requested',$3,now(),$4 FROM bumped`, job.JobID.String(), uuid.NewV7().String(), string(payload), next.UTC(), int64(job.JobRevision))
	return err
}

func (store *Store) ObserveAltMountAndReschedule(ctx context.Context, job AltMountJobContext, item altmount.QueueItem, phase altmount.Phase, next time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var currentAttempt *string
	var current workflows.State
	var workflowRevision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, job.Workflow.ID.String()).Scan(&current, &workflowRevision, &currentAttempt); err != nil {
		return err
	}
	if currentAttempt == nil || *currentAttempt != job.JobID.String() {
		return transaction.Commit(ctx)
	}
	var persistedJobRevision int64
	if err := transaction.QueryRow(ctx, `SELECT revision FROM altmount_jobs WHERE id=$1 FOR UPDATE`, job.JobID.String()).Scan(&persistedJobRevision); err != nil {
		return err
	}
	if uint64(persistedJobRevision) != job.JobRevision {
		return transaction.Commit(ctx)
	}
	payload, _ := json.Marshal(item)
	digest := sha256Bytes(payload)
	observationKey := sha256Bytes([]byte(item.UpdatedAt.UTC().String() + "\x00" + string(item.Status) + "\x00" + item.Stage))
	if _, err := transaction.Exec(ctx, `INSERT INTO altmount_job_observations(id,job_id,observation_key,external_state,payload_sha256,source_timestamp,observed_at) VALUES($1,$2,$3,$4,$5,$6,now()) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), job.JobID.String(), observationKey, string(item.Status), digest, item.UpdatedAt); err != nil {
		return err
	}
	jobState := string(phase)
	if phase == altmount.PhasePaused {
		jobState = "queued"
	}
	var nextJobRevision int64
	percent, stage := altMountProgress(item)
	if err := transaction.QueryRow(ctx, `UPDATE altmount_jobs SET state=$2,last_reconciled_at=now(),revision=revision+1,progress_percent=$4,progress_stage=$5,progress_observed_at=now() WHERE id=$1 AND revision=$3 RETURNING revision`, job.JobID.String(), jobState, persistedJobRevision, percent, stage).Scan(&nextJobRevision); err != nil {
		return err
	}
	workflowState := current
	if phase == altmount.PhaseQueued || phase == altmount.PhasePaused || phase == altmount.PhaseProcessing {
		if current != workflows.StateStreamingReady {
			workflowState = workflows.StateQueued
		}
	} else if phase == altmount.PhaseStreamingReady {
		workflowState = workflows.StateStreamingReady
	} else if phase == altmount.PhaseFailed || phase == altmount.PhaseNeedsAttention {
		workflowState = workflows.StateNeedsAttention
	}
	if workflowState != current {
		probe := workflows.Workflow{State: current, Revision: uint64(workflowRevision)}
		if _, err := probe.Transition(workflowState, "AltMount phase observed", "worker", time.Now().UTC()); err != nil {
			return ErrConcurrentModification
		}
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=revision+1,updated_at=now() WHERE id=$1 AND grab_attempt_id=$3`, job.Workflow.ID.String(), workflowState, job.JobID.String()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,'AltMount phase observed','worker',now())`, job.Workflow.ID.String(), workflowRevision+1, current, workflowState); err != nil {
			return err
		}
	}
	if !next.IsZero() {
		eventPayload, _ := json.Marshal(map[string]string{"job_id": job.JobID.String(), "workflow_id": job.Workflow.ID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'altmount_job',$2,$3,'altmount.reconcile_requested',$4,now(),$5)`, uuid.NewV7().String(), job.JobID.String(), nextJobRevision, string(eventPayload), next.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func (store *Store) PersistAltMountInventory(ctx context.Context, job AltMountJobContext, inventory altmount.Inventory, now time.Time) (uuid.UUID, []packs.ObservedAsset, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return uuid.UUID{}, nil, err
	}
	defer transaction.Rollback(ctx)
	var current bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_jobs job JOIN workflows workflow ON workflow.id=job.workflow_id WHERE job.id=$1 AND workflow.grab_attempt_id=job.id)`, job.JobID.String()).Scan(&current); err != nil {
		return uuid.UUID{}, nil, err
	}
	if !current {
		return uuid.UUID{}, nil, ErrConcurrentModification
	}
	inventoryID := uuid.NewV7()
	digest, err := hex.DecodeString(inventory.Digest)
	if err != nil {
		return uuid.UUID{}, nil, err
	}
	var existingText string
	if existingErr := transaction.QueryRow(ctx, `SELECT id::text FROM altmount_output_inventories WHERE job_id=$1 AND inventory_digest=$2`, job.JobID.String(), digest).Scan(&existingText); existingErr == nil {
		inventoryID, _ = uuid.Parse(existingText)
		rows, queryErr := transaction.Query(ctx, `SELECT id::text,relative_path,size_bytes FROM altmount_output_assets WHERE inventory_id=$1 ORDER BY relative_path`, inventoryID.String())
		if queryErr != nil {
			return uuid.UUID{}, nil, queryErr
		}
		var existingAssets []packs.ObservedAsset
		for rows.Next() {
			var assetText, path string
			var size int64
			if scanErr := rows.Scan(&assetText, &path, &size); scanErr != nil {
				rows.Close()
				return uuid.UUID{}, nil, scanErr
			}
			assetID, _ := uuid.Parse(assetText)
			existingAssets = append(existingAssets, packs.ObservedAsset{ID: assetID, RelativePath: path, Size: size})
		}
		rows.Close()
		if commitErr := transaction.Commit(ctx); commitErr != nil {
			return uuid.UUID{}, nil, commitErr
		}
		return inventoryID, existingAssets, nil
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return uuid.UUID{}, nil, existingErr
	}
	var generation int
	if err := transaction.QueryRow(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM altmount_output_inventories WHERE job_id=$1`, job.JobID.String()).Scan(&generation); err != nil {
		return uuid.UUID{}, nil, err
	}
	rootRelative := inventory.LocalRelative
	if rootRelative == "" {
		rootRelative = "."
	}
	var returnedID string
	if err := transaction.QueryRow(ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,$3,$4,$5,$6,$6) ON CONFLICT(job_id,inventory_digest) DO UPDATE SET observed_at=altmount_output_inventories.observed_at RETURNING id::text`, inventoryID.String(), job.JobID.String(), generation, rootRelative, digest, now.UTC()).Scan(&returnedID); err != nil {
		return uuid.UUID{}, nil, err
	}
	returned, _ := uuid.Parse(returnedID)
	if returned != inventoryID {
		rows, queryErr := transaction.Query(ctx, `SELECT id::text,relative_path,size_bytes FROM altmount_output_assets WHERE inventory_id=$1 ORDER BY relative_path`, returned.String())
		if queryErr != nil {
			return uuid.UUID{}, nil, queryErr
		}
		var existing []packs.ObservedAsset
		for rows.Next() {
			var idText, path string
			var size int64
			if scanErr := rows.Scan(&idText, &path, &size); scanErr != nil {
				rows.Close()
				return uuid.UUID{}, nil, scanErr
			}
			assetID, _ := uuid.Parse(idText)
			existing = append(existing, packs.ObservedAsset{ID: assetID, RelativePath: path, Size: size})
		}
		rows.Close()
		if err := transaction.Commit(ctx); err != nil {
			return uuid.UUID{}, nil, err
		}
		return returned, existing, nil
	}
	var assets []packs.ObservedAsset
	for _, file := range inventory.Files {
		if file.Size <= 0 {
			continue
		}
		assetID := uuid.NewV7()
		relative := filepath.ToSlash(file.RelativePath)
		if _, err := transaction.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind) VALUES($1,$2,$3,$4,$5)`, assetID.String(), inventoryID.String(), relative, file.Size, job.Media.Kind); err != nil {
			return uuid.UUID{}, nil, err
		}
		assets = append(assets, packs.ObservedAsset{ID: assetID, RelativePath: relative, Size: file.Size})
	}
	if err := transaction.Commit(ctx); err != nil {
		return uuid.UUID{}, nil, err
	}
	return inventoryID, assets, nil
}

func (store *Store) PackExpectation(ctx context.Context, workflowID uuid.UUID) (uuid.UUID, string, uint16, []packs.ExpectedEpisode, error) {
	var expectationText, seriesID string
	var season int16
	err := store.Pool.QueryRow(ctx, `SELECT id::text,series_identity,season FROM pack_expectation_sets WHERE workflow_id=$1 ORDER BY frozen_at DESC LIMIT 1`, workflowID.String()).Scan(&expectationText, &seriesID, &season)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, "", 0, nil, ErrNotFound
	}
	if err != nil {
		return uuid.UUID{}, "", 0, nil, err
	}
	expectationID, _ := uuid.Parse(expectationText)
	rows, err := store.Pool.Query(ctx, `SELECT season,episode,COALESCE(provider_episode_id,''),title,COALESCE(air_date::text,'') FROM pack_expected_episodes WHERE expectation_id=$1 ORDER BY season,episode`, expectationID.String())
	if err != nil {
		return uuid.UUID{}, "", 0, nil, err
	}
	defer rows.Close()
	var expected []packs.ExpectedEpisode
	for rows.Next() {
		var item packs.ExpectedEpisode
		var itemSeason, itemEpisode int16
		if err := rows.Scan(&itemSeason, &itemEpisode, &item.ProviderEpisodeID, &item.Title, &item.AirDate); err != nil {
			return uuid.UUID{}, "", 0, nil, err
		}
		item.Key = packs.EpisodeKey{SeriesID: seriesID, Season: uint16(itemSeason), Episode: uint16(itemEpisode)}
		expected = append(expected, item)
	}
	return expectationID, seriesID, uint16(season), expected, rows.Err()
}

func (store *Store) PersistPackEvaluation(ctx context.Context, expectationID, inventoryID uuid.UUID, report packs.Report, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var exists bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pack_evaluations WHERE expectation_id=$1 AND inventory_id=$2 AND evaluator_version=2)`, expectationID.String(), inventoryID.String()).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return transaction.Commit(ctx)
	}
	evaluationID := uuid.NewV7()
	digest, err := hex.DecodeString(report.InventoryDigest)
	if err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO pack_evaluations(id,expectation_id,inventory_id,evaluator_version,inventory_digest,verdict,evaluated_at) VALUES($1,$2,$3,2,$4,$5,$6)`, evaluationID.String(), expectationID.String(), inventoryID.String(), digest, report.Verdict, now.UTC()); err != nil {
		return err
	}
	for ordinal, group := range report.Groups {
		databaseGroupID := evaluationID.String() + ":" + group.ID
		if _, err := transaction.Exec(ctx, `INSERT INTO pack_content_groups(id,evaluation_id,ordinal) VALUES($1,$2,$3)`, databaseGroupID, evaluationID.String(), ordinal); err != nil {
			return err
		}
		for _, assetID := range group.AssetIDs {
			if _, err := transaction.Exec(ctx, `INSERT INTO pack_group_assets(group_id,asset_id) VALUES($1,$2)`, databaseGroupID, assetID.String()); err != nil {
				return err
			}
		}
		for _, episode := range group.Episodes {
			if _, err := transaction.Exec(ctx, `INSERT INTO pack_group_episodes(group_id,season,episode) VALUES($1,$2,$3)`, databaseGroupID, episode.Season, episode.Episode); err != nil {
				return err
			}
		}
	}
	for _, issue := range report.Issues {
		episodes, _ := json.Marshal(issue.Episodes)
		if _, err := transaction.Exec(ctx, `INSERT INTO pack_issues(id,evaluation_id,code,path,detail,episodes) VALUES($1,$2,$3,NULLIF($4,''),$5,$6)`, uuid.NewV7().String(), evaluationID.String(), issue.Code, issue.Path, issue.Detail, string(episodes)); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func (store *Store) CompleteImport(ctx context.Context, job AltMountJobContext, publishable bool, detail string, now time.Time) error {
	return store.CompleteImportWithCode(ctx, job, publishable, "", detail, now)
}

func (store *Store) CompleteImportWithCode(ctx context.Context, job AltMountJobContext, publishable bool, attentionCode, detail string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	workflowState := workflows.StateNeedsAttention
	jobState := "needs_attention"
	if publishable {
		workflowState = workflows.StateImportComplete
		jobState = "import_complete"
	}
	var revision int64
	var old workflows.State
	var currentAttempt *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, job.Workflow.ID.String()).Scan(&old, &revision, &currentAttempt); err != nil {
		return err
	}
	if currentAttempt == nil || *currentAttempt != job.JobID.String() {
		return transaction.Commit(ctx)
	}
	var persistedJobRevision int64
	if err := transaction.QueryRow(ctx, `SELECT revision FROM altmount_jobs WHERE id=$1 FOR UPDATE`, job.JobID.String()).Scan(&persistedJobRevision); err != nil {
		return err
	}
	if uint64(persistedJobRevision) != job.JobRevision {
		return transaction.Commit(ctx)
	}
	if publishable && (old == workflows.StateImportComplete || old == workflows.StateVerifying || old == workflows.StatePublishing || old == workflows.StatePlexScanning || old == workflows.StatePlexConfirmed) {
		return transaction.Commit(ctx)
	}
	if !publishable && old == workflows.StateNeedsAttention {
		return transaction.Commit(ctx)
	}
	probe := workflows.Workflow{State: old, Revision: uint64(revision)}
	if _, err := probe.Transition(workflowState, detail, "worker", now); err != nil {
		return ErrConcurrentModification
	}
	outcome := attentionCode
	var closedAt any
	if publishable {
		outcome = "output_verified"
		closedAt = now.UTC()
	} else if outcome == "" {
		outcome = "infrastructure_attention"
	}
	if _, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state=$2,last_reconciled_at=$3,revision=revision+1,error_code=NULLIF($4,''),error_detail=NULLIF($5,''),acquisition_outcome=$7,acquisition_closed_at=$8 WHERE id=$1 AND revision=$6`, job.JobID.String(), jobState, now.UTC(), attentionCode, detail, persistedJobRevision, outcome, closedAt); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=revision+1,updated_at=$3,attention_code=NULLIF($4,''),attention_detail=NULLIF($5,'') WHERE id=$1 AND revision=$6 AND grab_attempt_id=$7`, job.Workflow.ID.String(), workflowState, now.UTC(), attentionCode, detail, revision, job.JobID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,$5,'worker',$6)`, job.Workflow.ID.String(), revision+1, old, workflowState, detail, now.UTC()); err != nil {
		return err
	}
	if publishable {
		payload, _ := json.Marshal(map[string]string{"workflow_id": job.Workflow.ID.String(), "job_id": job.JobID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'library.publish_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), job.Workflow.ID.String(), revision+1, string(payload), now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func playableAsset(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mp4", ".m4v", ".avi", ".ts":
		return true
	}
	return false
}

// altMountProgress returns the progress AltMount itself reported, bounded to
// what the schema accepts. A missing or out-of-range percentage is stored as
// NULL rather than guessed, and the stage is only a short display label.
func altMountProgress(item altmount.QueueItem) (*int16, *string) {
	var percent *int16
	if item.Percentage != nil && *item.Percentage >= 0 && *item.Percentage <= 100 {
		value := int16(*item.Percentage)
		percent = &value
	}
	var stage *string
	// The stage is display-only text from AltMount: keep it valid UTF-8 and
	// cut it by characters (the column allows 64), never mid-character, so a
	// long or non-ASCII label can never make PostgreSQL reject the job update.
	if trimmed := strings.TrimSpace(strings.ToValidUTF8(item.Stage, "")); trimmed != "" {
		if runes := []rune(trimmed); len(runes) > 64 {
			trimmed = strings.TrimSpace(string(runes[:64]))
		}
		stage = &trimmed
	}
	return percent, stage
}

// InfrastructureAttentionCode reports attention codes that are recovered by
// reconciling the same AltMount job rather than searching for another release.
func InfrastructureAttentionCode(code string) bool { return infrastructureAttentionCode(code) }
