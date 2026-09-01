package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

const maxAcquisitionAttempts = 2

type SubtitleVerificationRecord struct {
	ID                 uuid.UUID       `json:"id"`
	WorkflowID         uuid.UUID       `json:"workflow_id"`
	AltMountJobID      uuid.UUID       `json:"altmount_job_id"`
	CandidateID        uuid.UUID       `json:"candidate_id"`
	MediaTitle         string          `json:"media_title"`
	ReleaseTitle       string          `json:"release_title"`
	AttemptGeneration  int             `json:"attempt_generation"`
	Status             string          `json:"status"`
	RequiredLanguages  []string        `json:"required_languages"`
	PreferredLanguages []string        `json:"preferred_languages"`
	ObservedLanguages  []string        `json:"observed_languages"`
	Evidence           json.RawMessage `json:"evidence"`
	FailureReasons     json.RawMessage `json:"failure_reasons"`
	RetryState         string          `json:"retry_state"`
	CanReverify        bool            `json:"can_reverify"`
	CreatedAt          time.Time       `json:"created_at"`
}

func (store *Store) SubtitlePolicyForWorkflow(ctx context.Context, workflowID uuid.UUID) (subtitles.Policy, error) {
	var body []byte
	err := store.Pool.QueryRow(ctx, `SELECT profile.policy FROM workflows workflow JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id WHERE workflow.id=$1`, workflowID.String()).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return subtitles.Policy{}, ErrNotFound
	}
	if err != nil {
		return subtitles.Policy{}, err
	}
	return subtitles.ParsePolicy(body)
}

func (store *Store) RecordSubtitleVerification(ctx context.Context, job AltMountJobContext, inventoryID uuid.UUID, verification subtitles.Verification, now time.Time) (SubtitleVerificationRecord, error) {
	if verification.Status != "verified" && verification.Status != "unverified" || job.AttemptGeneration < 1 {
		return SubtitleVerificationRecord{}, errors.New("invalid subtitle verification")
	}
	// pgx encodes a nil slice as SQL NULL, while verification language arrays
	// are deliberately NOT NULL. Preserve the semantic empty set explicitly.
	verification.RequiredLanguages = append([]string{}, verification.RequiredLanguages...)
	verification.PreferredLanguages = append([]string{}, verification.PreferredLanguages...)
	verification.ObservedLanguages = append([]string{}, verification.ObservedLanguages...)
	evidence, err := json.Marshal(verification)
	if err != nil {
		return SubtitleVerificationRecord{}, err
	}
	failureReasons, _ := json.Marshal(verification.FailureReasons)
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SubtitleVerificationRecord{}, err
	}
	defer transaction.Rollback(ctx)
	if existing, existingErr := subtitleVerificationByJob(ctx, transaction, job.JobID); existingErr == nil {
		return existing, transaction.Commit(ctx)
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return SubtitleVerificationRecord{}, existingErr
	}
	var workflowState workflows.State
	var workflowRevision int64
	var currentAttempt *string
	var jobRevision int64
	var candidateText, indexerText, nativeKey, releaseTitle, mediaTitle string
	err = transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,workflow.grab_attempt_id::text,job.revision,job.candidate_id::text,candidate.indexer_instance_id::text,candidate.native_key,candidate.title,media.title
		FROM altmount_jobs job
		JOIN workflows workflow ON workflow.id=job.workflow_id
		JOIN release_candidates candidate ON candidate.id=job.candidate_id AND candidate.workflow_id=workflow.id
		JOIN media_items media ON media.id=workflow.media_id
		WHERE job.id=$1 AND job.workflow_id=$2 FOR UPDATE OF job,workflow`, job.JobID.String(), job.Workflow.ID.String()).Scan(&workflowState, &workflowRevision, &currentAttempt, &jobRevision, &candidateText, &indexerText, &nativeKey, &releaseTitle, &mediaTitle)
	if err != nil {
		return SubtitleVerificationRecord{}, err
	}
	if currentAttempt == nil || *currentAttempt != job.JobID.String() || uint64(jobRevision) != job.JobRevision || candidateText != job.CandidateID.String() {
		return SubtitleVerificationRecord{}, ErrConcurrentModification
	}
	var inventoryCurrent bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_output_inventories WHERE id=$1 AND job_id=$2)`, inventoryID.String(), job.JobID.String()).Scan(&inventoryCurrent); err != nil {
		return SubtitleVerificationRecord{}, err
	}
	if !inventoryCurrent {
		return SubtitleVerificationRecord{}, ErrConcurrentModification
	}
	verificationID := uuid.NewV7()
	retryState := "not_needed"
	if verification.Status == "unverified" {
		if job.AttemptGeneration < maxAcquisitionAttempts {
			retryState = "queued"
		} else {
			retryState = "exhausted"
		}
	}
	var verifiedAt any
	if verification.Status == "verified" {
		verifiedAt = now.UTC()
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO subtitle_verifications(id,workflow_id,altmount_job_id,candidate_id,inventory_id,attempt_generation,status,required_languages,preferred_languages,observed_languages,evidence,failure_reasons,retry_state,verified_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, verificationID.String(), job.Workflow.ID.String(), job.JobID.String(), job.CandidateID.String(), inventoryID.String(), job.AttemptGeneration, verification.Status, verification.RequiredLanguages, verification.PreferredLanguages, verification.ObservedLanguages, evidence, failureReasons, retryState, verifiedAt, now.UTC()); err != nil {
		return SubtitleVerificationRecord{}, err
	}
	if verification.Status == "verified" {
		if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET retry_state='succeeded' WHERE workflow_id=$1 AND id<>$2 AND retry_state='queued'`, job.Workflow.ID.String(), verificationID.String()); err != nil {
			return SubtitleVerificationRecord{}, err
		}
	}
	for _, video := range verification.Videos {
		mediaInfo, marshalErr := json.Marshal(video.MediaInfo)
		if marshalErr != nil {
			return SubtitleVerificationRecord{}, marshalErr
		}
		command, updateErr := transaction.Exec(ctx, `UPDATE altmount_output_assets SET media_info=$3 WHERE id=$1 AND inventory_id=$2`, video.AssetID.String(), inventoryID.String(), mediaInfo)
		if updateErr != nil || command.RowsAffected() != 1 {
			if updateErr != nil {
				return SubtitleVerificationRecord{}, updateErr
			}
			return SubtitleVerificationRecord{}, ErrConcurrentModification
		}
	}
	for _, companion := range verification.Companions {
		command, err := transaction.Exec(ctx, `INSERT INTO subtitle_verification_companions(verification_id,video_asset_id,subtitle_asset_id,language,raw_language,forced,hearing_impaired,relative_path)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8
			WHERE EXISTS(SELECT 1 FROM altmount_output_assets WHERE id=$2 AND inventory_id=$9)
			  AND EXISTS(SELECT 1 FROM altmount_output_assets WHERE id=$3 AND inventory_id=$9)`, verificationID.String(), companion.VideoAssetID.String(), companion.SubtitleAssetID.String(), companion.Language, companion.RawLanguage, companion.Forced, companion.HearingImpaired, companion.RelativePath, inventoryID.String())
		if err != nil {
			return SubtitleVerificationRecord{}, err
		}
		if command.RowsAffected() != 1 {
			return SubtitleVerificationRecord{}, ErrConcurrentModification
		}
	}
	if verification.Status == "unverified" {
		detail := strings.Join(verification.FailureReasons, "; ")
		if detail == "" {
			detail = "required subtitle evidence could not be verified"
		}
		if len(detail) > 1000 {
			detail = detail[:1000]
		}
		if _, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',revision=revision+1,last_reconciled_at=$2,error_code='subtitle_requirements_unverified',error_detail=$3 WHERE id=$1`, job.JobID.String(), now.UTC(), detail); err != nil {
			return SubtitleVerificationRecord{}, err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at) VALUES($1,$2,$3,$4,$5,'subtitle_verification',$6,$7)`, job.Workflow.ID.String(), candidateText, indexerText, nativeKey, verificationID.String(), detail, now.UTC()); err != nil {
			return SubtitleVerificationRecord{}, err
		}
		if retryState == "queued" {
			if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,grab_attempt_id=NULL,revision=revision+2,updated_at=$2,attention_code=NULL,attention_detail=NULL WHERE id=$1`, job.Workflow.ID.String(), now.UTC()); err != nil {
				return SubtitleVerificationRecord{}, err
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES
				($1,$2,$4,'needs_attention',$5,'worker',$6),
				($1,$3,'needs_attention','waiting','one bounded alternate release attempt queued','worker',$6)`, job.Workflow.ID.String(), workflowRevision+1, workflowRevision+2, workflowState, detail, now.UTC()); err != nil {
				return SubtitleVerificationRecord{}, err
			}
			payload, _ := json.Marshal(map[string]string{"workflow_id": job.Workflow.ID.String(), "reason": "subtitle verification failed; exact release excluded"})
			if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), job.Workflow.ID.String(), workflowRevision+2, payload, now.UTC()); err != nil {
				return SubtitleVerificationRecord{}, err
			}
		} else {
			if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',revision=revision+1,updated_at=$2,attention_code='subtitle_requirements_unverified',attention_detail=$3 WHERE id=$1`, job.Workflow.ID.String(), now.UTC(), detail); err != nil {
				return SubtitleVerificationRecord{}, err
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention',$4,'worker',$5)`, job.Workflow.ID.String(), workflowRevision+1, workflowState, detail, now.UTC()); err != nil {
				return SubtitleVerificationRecord{}, err
			}
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return SubtitleVerificationRecord{}, err
	}
	return SubtitleVerificationRecord{ID: verificationID, WorkflowID: job.Workflow.ID, AltMountJobID: job.JobID, CandidateID: job.CandidateID, MediaTitle: mediaTitle, ReleaseTitle: releaseTitle, AttemptGeneration: job.AttemptGeneration, Status: verification.Status, RequiredLanguages: verification.RequiredLanguages, PreferredLanguages: verification.PreferredLanguages, ObservedLanguages: verification.ObservedLanguages, Evidence: evidence, FailureReasons: failureReasons, RetryState: retryState, CreatedAt: now.UTC()}, nil
}

func subtitleVerificationByJob(ctx context.Context, transaction pgx.Tx, jobID uuid.UUID) (SubtitleVerificationRecord, error) {
	var item SubtitleVerificationRecord
	var idText, workflowText, jobText, candidateText string
	var evidence, reasons []byte
	err := transaction.QueryRow(ctx, `SELECT verification.id::text,verification.workflow_id::text,verification.altmount_job_id::text,verification.candidate_id::text,media.title,candidate.title,verification.attempt_generation,verification.status,verification.required_languages,verification.preferred_languages,verification.observed_languages,verification.evidence,verification.failure_reasons,verification.retry_state,verification.created_at
		FROM subtitle_verifications verification JOIN workflows workflow ON workflow.id=verification.workflow_id JOIN media_items media ON media.id=workflow.media_id JOIN release_candidates candidate ON candidate.id=verification.candidate_id WHERE verification.altmount_job_id=$1`, jobID.String()).Scan(&idText, &workflowText, &jobText, &candidateText, &item.MediaTitle, &item.ReleaseTitle, &item.AttemptGeneration, &item.Status, &item.RequiredLanguages, &item.PreferredLanguages, &item.ObservedLanguages, &evidence, &reasons, &item.RetryState, &item.CreatedAt)
	if err != nil {
		return SubtitleVerificationRecord{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	item.WorkflowID, _ = uuid.Parse(workflowText)
	item.AltMountJobID, _ = uuid.Parse(jobText)
	item.CandidateID, _ = uuid.Parse(candidateText)
	item.Evidence = append(json.RawMessage(nil), evidence...)
	item.FailureReasons = append(json.RawMessage(nil), reasons...)
	return item, nil
}

func (store *Store) ListSubtitleVerifications(ctx context.Context, limit int) ([]SubtitleVerificationRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := store.Pool.Query(ctx, `SELECT verification.id::text,verification.workflow_id::text,verification.altmount_job_id::text,verification.candidate_id::text,media.title,candidate.title,verification.attempt_generation,verification.status,verification.required_languages,verification.preferred_languages,verification.observed_languages,verification.evidence,verification.failure_reasons,verification.retry_state,
		verification.status='unverified' AND workflow.state='needs_attention' AND workflow.attention_code='subtitle_requirements_unverified' AND job.state='needs_attention' AND job.error_code='subtitle_requirements_unverified' AND job.attempt_generation BETWEEN 1 AND 2
		AND (SELECT count(*) FROM altmount_jobs workflow_job WHERE workflow_job.workflow_id=workflow.id) <= 2
		AND EXISTS(SELECT 1 FROM altmount_output_inventories inventory JOIN altmount_output_assets asset ON asset.inventory_id=inventory.id WHERE inventory.id=verification.inventory_id AND inventory.job_id=job.id)
		AND (media.kind<>'series' OR EXISTS(
			SELECT 1 FROM pack_expectation_sets expectation
			JOIN pack_evaluations evaluation ON evaluation.expectation_id=expectation.id AND evaluation.inventory_id=verification.inventory_id AND evaluation.evaluator_version=2 AND evaluation.verdict='verified'
			JOIN pack_content_groups content_group ON content_group.evaluation_id=evaluation.id
			JOIN pack_group_assets group_asset ON group_asset.group_id=content_group.id
			WHERE expectation.workflow_id=workflow.id
		))
		AND NOT EXISTS(SELECT 1 FROM library_link_versions WHERE workflow_id=workflow.id AND altmount_job_id=job.id)
		AND NOT EXISTS(SELECT 1 FROM audit_events WHERE action='subtitle.output_reverification_requested' AND aggregate_type='subtitle_verification' AND aggregate_id=verification.id),verification.created_at
		FROM subtitle_verifications verification JOIN workflows workflow ON workflow.id=verification.workflow_id JOIN altmount_jobs job ON job.id=verification.altmount_job_id JOIN media_items media ON media.id=workflow.media_id JOIN release_candidates candidate ON candidate.id=verification.candidate_id ORDER BY verification.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SubtitleVerificationRecord{}
	for rows.Next() {
		var item SubtitleVerificationRecord
		var idText, workflowText, jobText, candidateText string
		var evidence, reasons []byte
		if err := rows.Scan(&idText, &workflowText, &jobText, &candidateText, &item.MediaTitle, &item.ReleaseTitle, &item.AttemptGeneration, &item.Status, &item.RequiredLanguages, &item.PreferredLanguages, &item.ObservedLanguages, &evidence, &reasons, &item.RetryState, &item.CanReverify, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.WorkflowID, _ = uuid.Parse(workflowText)
		item.AltMountJobID, _ = uuid.Parse(jobText)
		item.CandidateID, _ = uuid.Parse(candidateText)
		item.Evidence = append(json.RawMessage(nil), evidence...)
		item.FailureReasons = append(json.RawMessage(nil), reasons...)
		result = append(result, item)
	}
	return result, rows.Err()
}
