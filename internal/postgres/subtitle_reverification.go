package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

type RetainedSubtitleOutput struct {
	InventoryID  uuid.UUID
	RootRelative string
	MediaKind    catalog.MediaKind
	Policy       subtitles.Policy
	Assets       []packs.ObservedAsset
	Selected     []subtitles.Asset
}

func (store *Store) RequestRetainedSubtitleReverification(ctx context.Context, workflowID, jobID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)

	var state workflows.State
	var revision int64
	var attentionCode, jobState, jobErrorCode, candidateText, verificationText string
	var alreadyRequested, hasPublication bool
	err = transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,COALESCE(workflow.attention_code,''),job.state,COALESCE(job.error_code,''),job.candidate_id::text,verification.id::text,
		EXISTS(SELECT 1 FROM audit_events WHERE action='subtitle.output_reverification_requested' AND aggregate_type='subtitle_verification' AND aggregate_id=verification.id),
		EXISTS(SELECT 1 FROM library_link_versions WHERE workflow_id=workflow.id AND altmount_job_id=job.id)
		FROM workflows workflow
		JOIN altmount_jobs job ON job.workflow_id=workflow.id AND job.id=$2
		JOIN subtitle_verifications verification ON verification.altmount_job_id=job.id AND verification.workflow_id=workflow.id
		JOIN altmount_output_inventories inventory ON inventory.id=verification.inventory_id AND inventory.job_id=job.id
		JOIN media_items media ON media.id=workflow.media_id
		WHERE workflow.id=$1 AND verification.status='unverified'
		AND EXISTS(SELECT 1 FROM altmount_output_assets WHERE inventory_id=inventory.id)
		AND EXISTS(SELECT 1 FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id AND discovery.state IN ('waiting','ready'))
		AND NOT EXISTS(SELECT 1 FROM altmount_jobs active_job WHERE active_job.workflow_id=workflow.id AND active_job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		AND (media.kind<>'series' OR EXISTS(
			SELECT 1 FROM pack_expectation_sets expectation
			JOIN pack_evaluations evaluation ON evaluation.expectation_id=expectation.id AND evaluation.inventory_id=inventory.id AND evaluation.evaluator_version=2 AND evaluation.verdict='verified'
			JOIN pack_content_groups content_group ON content_group.evaluation_id=evaluation.id
			JOIN pack_group_assets group_asset ON group_asset.group_id=content_group.id
			WHERE expectation.workflow_id=workflow.id
		))
		FOR UPDATE OF workflow,job,verification`, workflowID.String(), jobID.String()).Scan(&state, &revision, &attentionCode, &jobState, &jobErrorCode, &candidateText, &verificationText, &alreadyRequested, &hasPublication)
	if errors.Is(err, pgx.ErrNoRows) {
		var knownRequest bool
		if lookupErr := transaction.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM workflows workflow
			JOIN altmount_jobs job ON job.workflow_id=workflow.id AND job.id=$2
			JOIN subtitle_verifications verification ON verification.workflow_id=workflow.id AND verification.altmount_job_id=job.id
			WHERE workflow.id=$1
		)`, workflowID.String(), jobID.String()).Scan(&knownRequest); lookupErr != nil {
			return lookupErr
		}
		if knownRequest {
			return ErrConcurrentModification
		}
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != workflows.StateWaiting || attentionCode != "round_exhausted_waiting_new_identity" || jobState != "needs_attention" || jobErrorCode != "subtitle_requirements_unverified" || alreadyRequested || hasPublication {
		return ErrConcurrentModification
	}
	if _, err := uuid.Parse(candidateText); err != nil {
		return ErrConcurrentModification
	}

	nextRevision := revision + 1
	command, err := transaction.Exec(ctx, `UPDATE workflows SET state='verifying',selected_release_id=$2,grab_attempt_id=$3,revision=$4,updated_at=$5,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$6`, workflowID.String(), candidateText, jobID.String(), nextRevision, now.UTC(), revision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='paused',next_rescan_at=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$2 WHERE workflow_id=$1`, workflowID.String(), now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'verifying','owner requested one bounded retained-output subtitle reverification','owner',$4)`, workflowID.String(), nextRevision, state, now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"job_id": jobID.String(), "retained_output": true, "external_requests": false})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','subtitle.output_reverification_requested','subtitle_verification',$2,$3,$4)`, uuid.NewV7().String(), verificationText, detail, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "job_id": jobID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'altmount.output_reverify_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), nextRevision, payload, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) LoadRetainedSubtitleOutput(ctx context.Context, workflowID, jobID uuid.UUID) (RetainedSubtitleOutput, error) {
	var result RetainedSubtitleOutput
	var inventoryText string
	var required, preferred []string
	err := store.Pool.QueryRow(ctx, `SELECT inventory.id::text,inventory.root_relative,media.kind,verification.required_languages,verification.preferred_languages
		FROM workflows workflow
		JOIN media_items media ON media.id=workflow.media_id
		JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id AND job.workflow_id=workflow.id AND job.candidate_id=workflow.selected_release_id
		JOIN subtitle_verifications verification ON verification.altmount_job_id=job.id AND verification.workflow_id=workflow.id AND verification.status='unverified'
		JOIN altmount_output_inventories inventory ON inventory.id=verification.inventory_id AND inventory.job_id=job.id
		WHERE workflow.id=$1 AND job.id=$2 AND workflow.state='verifying' AND job.state='needs_attention' AND job.error_code='subtitle_requirements_unverified'`, workflowID.String(), jobID.String()).Scan(&inventoryText, &result.RootRelative, &result.MediaKind, &required, &preferred)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetainedSubtitleOutput{}, ErrConcurrentModification
	}
	if err != nil {
		return RetainedSubtitleOutput{}, err
	}
	result.InventoryID, err = uuid.Parse(inventoryText)
	if err != nil {
		return RetainedSubtitleOutput{}, err
	}
	result.Policy = subtitles.Policy{Required: append([]string(nil), required...), Preferred: append([]string(nil), preferred...)}

	rows, err := store.Pool.Query(ctx, `SELECT id::text,relative_path,size_bytes FROM altmount_output_assets WHERE inventory_id=$1 ORDER BY relative_path`, result.InventoryID.String())
	if err != nil {
		return RetainedSubtitleOutput{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var idText, relative string
		var size int64
		if err := rows.Scan(&idText, &relative, &size); err != nil {
			return RetainedSubtitleOutput{}, err
		}
		assetID, parseErr := uuid.Parse(idText)
		if parseErr != nil {
			return RetainedSubtitleOutput{}, parseErr
		}
		result.Assets = append(result.Assets, packs.ObservedAsset{ID: assetID, RelativePath: relative, Size: size})
	}
	if err := rows.Err(); err != nil {
		return RetainedSubtitleOutput{}, err
	}
	if len(result.Assets) == 0 {
		return RetainedSubtitleOutput{}, ErrConcurrentModification
	}
	if result.MediaKind != catalog.KindSeries {
		return result, nil
	}

	selectedRows, err := store.Pool.Query(ctx, `SELECT DISTINCT asset.id::text,asset.relative_path,asset.size_bytes
		FROM pack_expectation_sets expectation
		JOIN pack_evaluations evaluation ON evaluation.expectation_id=expectation.id AND evaluation.inventory_id=$2 AND evaluation.evaluator_version=2 AND evaluation.verdict='verified'
		JOIN pack_content_groups content_group ON content_group.evaluation_id=evaluation.id
		JOIN pack_group_assets group_asset ON group_asset.group_id=content_group.id
		JOIN altmount_output_assets asset ON asset.id=group_asset.asset_id AND asset.inventory_id=evaluation.inventory_id
		WHERE expectation.workflow_id=$1
		ORDER BY asset.relative_path`, workflowID.String(), result.InventoryID.String())
	if err != nil {
		return RetainedSubtitleOutput{}, err
	}
	defer selectedRows.Close()
	for selectedRows.Next() {
		var idText, relative string
		var size int64
		if err := selectedRows.Scan(&idText, &relative, &size); err != nil {
			return RetainedSubtitleOutput{}, err
		}
		assetID, parseErr := uuid.Parse(idText)
		if parseErr != nil {
			return RetainedSubtitleOutput{}, parseErr
		}
		result.Selected = append(result.Selected, subtitles.Asset{ID: assetID, RelativePath: relative, Size: size})
	}
	if err := selectedRows.Err(); err != nil {
		return RetainedSubtitleOutput{}, err
	}
	if len(result.Selected) == 0 {
		return RetainedSubtitleOutput{}, ErrConcurrentModification
	}
	return result, nil
}

func (store *Store) CompleteRetainedSubtitleReverification(ctx context.Context, job AltMountJobContext, eventRevision uint64, inventoryID uuid.UUID, verification subtitles.Verification, now time.Time) error {
	if verification.Status != "verified" && verification.Status != "unverified" {
		return errors.New("invalid retained-output subtitle verification")
	}
	verification.RequiredLanguages = append([]string{}, verification.RequiredLanguages...)
	verification.PreferredLanguages = append([]string{}, verification.PreferredLanguages...)
	verification.ObservedLanguages = append([]string{}, verification.ObservedLanguages...)
	if verification.Status == "verified" && len(verification.Videos) == 0 {
		return errors.New("verified retained output has no video evidence")
	}
	evidence, err := json.Marshal(verification)
	if err != nil {
		return err
	}
	failureReasons, _ := json.Marshal(verification.FailureReasons)

	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision, jobRevision int64
	var currentJob, selectedCandidate, jobCandidate, jobState, jobErrorCode, verificationText, verificationStatus, persistedInventory string
	var required, preferred []string
	err = transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,workflow.grab_attempt_id::text,workflow.selected_release_id::text,job.candidate_id::text,job.state,COALESCE(job.error_code,''),job.revision,verification.id::text,verification.status,verification.inventory_id::text,verification.required_languages,verification.preferred_languages
		FROM workflows workflow
		JOIN altmount_jobs job ON job.id=$2 AND job.workflow_id=workflow.id
		JOIN subtitle_verifications verification ON verification.altmount_job_id=job.id AND verification.workflow_id=workflow.id
		WHERE workflow.id=$1 FOR UPDATE OF workflow,job,verification`, job.Workflow.ID.String(), job.JobID.String()).Scan(&state, &revision, &currentJob, &selectedCandidate, &jobCandidate, &jobState, &jobErrorCode, &jobRevision, &verificationText, &verificationStatus, &persistedInventory, &required, &preferred)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != workflows.StateVerifying || uint64(revision) != eventRevision || currentJob != job.JobID.String() || selectedCandidate != job.CandidateID.String() || jobCandidate != job.CandidateID.String() || jobState != "needs_attention" || jobErrorCode != "subtitle_requirements_unverified" || uint64(jobRevision) != job.JobRevision || verificationStatus != "unverified" || persistedInventory != inventoryID.String() || !sameLanguageSet(required, verification.RequiredLanguages) || !sameLanguageSet(preferred, verification.PreferredLanguages) {
		return ErrConcurrentModification
	}

	for _, video := range verification.Videos {
		mediaInfo, marshalErr := json.Marshal(video.MediaInfo)
		if marshalErr != nil {
			return marshalErr
		}
		command, updateErr := transaction.Exec(ctx, `UPDATE altmount_output_assets SET media_info=$3 WHERE id=$1 AND inventory_id=$2`, video.AssetID.String(), inventoryID.String(), mediaInfo)
		if updateErr != nil {
			return updateErr
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM subtitle_verification_companions WHERE verification_id=$1`, verificationText); err != nil {
		return err
	}
	for _, companion := range verification.Companions {
		command, insertErr := transaction.Exec(ctx, `INSERT INTO subtitle_verification_companions(verification_id,video_asset_id,subtitle_asset_id,language,raw_language,forced,hearing_impaired,relative_path)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8
			WHERE EXISTS(SELECT 1 FROM altmount_output_assets WHERE id=$2 AND inventory_id=$9)
			  AND EXISTS(SELECT 1 FROM altmount_output_assets WHERE id=$3 AND inventory_id=$9)`, verificationText, companion.VideoAssetID.String(), companion.SubtitleAssetID.String(), companion.Language, companion.RawLanguage, companion.Forced, companion.HearingImpaired, companion.RelativePath, inventoryID.String())
		if insertErr != nil {
			return insertErr
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
	}

	retryState := "exhausted"
	var verifiedAt any
	if verification.Status == "verified" {
		retryState = "succeeded"
		verifiedAt = now.UTC()
	}
	if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET status=$2,required_languages=$3,preferred_languages=$4,observed_languages=$5,evidence=$6,failure_reasons=$7,retry_state=$8,verified_at=$9 WHERE id=$1`, verificationText, verification.Status, verification.RequiredLanguages, verification.PreferredLanguages, verification.ObservedLanguages, evidence, failureReasons, retryState, verifiedAt); err != nil {
		return err
	}

	if verification.Status == "verified" {
		if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET retry_state='succeeded' WHERE workflow_id=$1 AND id<>$2 AND retry_state='queued'`, job.Workflow.ID.String(), verificationText); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state='import_complete',revision=revision+1,last_reconciled_at=$2,error_code=NULL,error_detail=NULL,acquisition_outcome='output_verified',acquisition_closed_at=$2 WHERE id=$1`, job.JobID.String(), now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET updated_at=$2,attention_code=NULL,attention_detail=NULL WHERE id=$1`, job.Workflow.ID.String(), now.UTC()); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"job_id": job.JobID.String(), "status": "verified", "retained_output": true, "previous_failure_retained_in_workflow_history": true})
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','subtitle.output_reverified','subtitle_verification',$2,$3,$4)`, uuid.NewV7().String(), verificationText, detail, now.UTC()); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"workflow_id": job.Workflow.ID.String(), "job_id": job.JobID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'library.publish_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), job.Workflow.ID.String(), revision, payload, now.UTC()); err != nil {
			return err
		}
	} else {
		detail := strings.Join(verification.FailureReasons, "; ")
		if detail == "" {
			detail = "required subtitle evidence could not be verified"
		}
		if len(detail) > 1000 {
			detail = detail[:1000]
		}
		if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET retry_state='exhausted' WHERE workflow_id=$1 AND retry_state='queued'`, job.Workflow.ID.String()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',revision=revision+1,last_reconciled_at=$2,error_code='subtitle_requirements_unverified',error_detail=$3,acquisition_outcome='subtitle_requirements_unverified',acquisition_closed_at=$2 WHERE id=$1`, job.JobID.String(), now.UTC(), detail); err != nil {
			return err
		}
		nextRevision := revision + 1
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,grab_attempt_id=NULL,revision=$2,updated_at=$3,attention_code='round_exhausted_waiting_new_identity',attention_detail='Retained output still did not verify. Scheduled discovery remains pending and current media is unchanged.' WHERE id=$1`, job.Workflow.ID.String(), nextRevision, now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'verifying','waiting',$3,'worker',$4)`, job.Workflow.ID.String(), nextRevision, detail, now.UTC()); err != nil {
			return err
		}
		var generation, noNew int64
		if err := transaction.QueryRow(ctx, `SELECT rescan_generation+1,complete_no_new_scans FROM workflow_retry_discovery WHERE workflow_id=$1 FOR UPDATE`, job.Workflow.ID.String()).Scan(&generation, &noNew); err != nil {
			return err
		}
		next := retryDiscoveryAt(job.Workflow.ID, generation, int(noNew), now)
		if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='waiting',rescan_generation=$2,next_rescan_at=$3,last_scan_at=NULL,last_scan_complete=NULL,pending_indexer_ids=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$4 WHERE workflow_id=$1`, job.Workflow.ID.String(), generation, next.UTC(), now.UTC()); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"workflow_id": job.Workflow.ID.String(), "rescan_generation": generation})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow_retry_discovery',$2,$3,'workflow.discovery_rescan_requested',$4,$5,$6)`, uuid.NewV7().String(), job.Workflow.ID.String(), generation, payload, now.UTC(), next.UTC()); err != nil {
			return err
		}
		audit, _ := json.Marshal(map[string]any{"job_id": job.JobID.String(), "status": "unverified", "retained_output": true})
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','subtitle.output_reverification_failed','subtitle_verification',$2,$3,$4)`, uuid.NewV7().String(), verificationText, audit, now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func sameLanguageSet(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
