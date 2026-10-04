package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

const retryDiscoveryBaseInterval = 24 * time.Hour

type AcquisitionRound struct {
	ID                    uuid.UUID
	Ordinal               int
	State                 string
	AttemptLimit          int
	NoveltyCutoffSequence int64
	PolicyVersion         uint64
	Policy                json.RawMessage
	UpgradeBaseline       string
}

type acquisitionFailureResult struct {
	RetryQueued    bool
	RoundExhausted bool
}

func (store *Store) insertInitialAcquisitionRound(ctx context.Context, transaction pgx.Tx, workflowID, mediaID, profileID uuid.UUID, variant string, now time.Time) error {
	roundID := uuid.NewV7()
	command, err := transaction.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
		SELECT $1,$2,1,'open',$7,0,profile.policy_version,profile.policy,
		COALESCE((SELECT string_agg(version.id::text,',' ORDER BY version.id) FROM library_link_versions version
		 JOIN workflows published ON published.id=version.workflow_id
		 WHERE published.media_id=$3 AND published.variant=$5 AND version.state='current'),''),$6
		FROM quality_profiles profile WHERE profile.id=$4`, roundID.String(), workflowID.String(), mediaID.String(), profileID.String(), variant, now.UTC(), store.AcquisitionLimits().AttemptsPerRound)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

func retryDiscoveryAt(workflowID uuid.UUID, generation int64, completeNoNewScans int, now time.Time) time.Time {
	interval := retryDiscoveryBaseInterval
	for completed := 14; completed <= completeNoNewScans && interval < 14*24*time.Hour; completed += 14 {
		interval *= 2
		if interval > 14*24*time.Hour {
			interval = 14 * 24 * time.Hour
		}
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", workflowID.String(), generation)))
	// Stable range [-0.20,+0.20] in one-basis-point increments.
	jitterBasisPoints := int64(binary.BigEndian.Uint64(digest[:8])%4001) - 2000
	jitter := time.Duration(int64(interval) * jitterBasisPoints / 10_000)
	return now.UTC().Add(interval + jitter)
}

func (store *Store) ValidateRecoveryDiscovery(ctx context.Context, workflowID uuid.UUID, generation int64) error {
	var valid bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM workflow_retry_discovery discovery
		JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
		JOIN workflows workflow ON workflow.id=discovery.workflow_id
		WHERE discovery.workflow_id=$1 AND discovery.state='waiting' AND discovery.rescan_generation=$2
		AND round.state='exhausted' AND workflow.state='waiting'
		AND NOT EXISTS(SELECT 1 FROM acquisition_rounds open_round WHERE open_round.workflow_id=workflow.id AND open_round.state='open')
		AND NOT EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=workflow.id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
	)`, workflowID.String(), generation).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrConcurrentModification
	}
	return nil
}

// RecoveryIndexerFilter returns the unfinished provider set for a partial
// discovery scan. A nil filter means this generation must query every enabled,
// commissioned indexer. The generation guard is repeated inside the Newznab
// client immediately before network I/O.
func (store *Store) RecoveryIndexerFilter(ctx context.Context, workflowID uuid.UUID, generation int64) (map[string]bool, error) {
	var constrained bool
	var values []string
	err := store.Pool.QueryRow(ctx, `SELECT pending_indexer_ids IS NOT NULL,COALESCE(pending_indexer_ids::text[],ARRAY[]::text[])
		FROM workflow_retry_discovery
		WHERE workflow_id=$1 AND state='waiting' AND rescan_generation=$2`, workflowID.String(), generation).Scan(&constrained, &values)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConcurrentModification
	}
	if err != nil {
		return nil, err
	}
	if !constrained {
		return nil, nil
	}
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result, nil
}

func (store *Store) ensureOpenAcquisitionRound(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID, now time.Time) (AcquisitionRound, error) {
	var result AcquisitionRound
	var idText string
	var policyVersion int64
	err := transaction.QueryRow(ctx, `SELECT round.id::text,round.round_ordinal,round.state,round.attempt_limit_snapshot,round.novelty_cutoff_sequence,round.policy_version,round.policy_snapshot,round.upgrade_baseline_link_versions
		FROM acquisition_rounds round WHERE round.workflow_id=$1 AND round.state='open' FOR UPDATE`, workflowID.String()).Scan(&idText, &result.Ordinal, &result.State, &result.AttemptLimit, &result.NoveltyCutoffSequence, &policyVersion, &result.Policy, &result.UpgradeBaseline)
	if err == nil {
		result.ID, _ = uuid.Parse(idText)
		result.PolicyVersion = uint64(policyVersion)
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AcquisitionRound{}, err
	}
	var existingRounds int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, workflowID.String()).Scan(&existingRounds); err != nil {
		return AcquisitionRound{}, err
	}
	if existingRounds != 0 {
		return AcquisitionRound{}, errors.New("the next acquisition round requires owner approval")
	}
	var currentPolicy int64
	if err := transaction.QueryRow(ctx, `SELECT profile.policy_version,profile.policy FROM workflows workflow JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id WHERE workflow.id=$1`, workflowID.String()).Scan(&currentPolicy, &result.Policy); err != nil {
		return AcquisitionRound{}, err
	}
	err = transaction.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'') FROM workflows requested
		LEFT JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
		LEFT JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
		WHERE requested.id=$1`, workflowID.String()).Scan(&result.UpgradeBaseline)
	if err != nil {
		return AcquisitionRound{}, err
	}
	result.ID, result.Ordinal, result.State, result.AttemptLimit, result.PolicyVersion = uuid.NewV7(), 1, "open", store.AcquisitionLimits().AttemptsPerRound, uint64(currentPolicy)
	if _, err := transaction.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
		VALUES($1,$2,1,'open',$3,0,$4,$5,$6,$7)`, result.ID.String(), workflowID.String(), result.AttemptLimit, currentPolicy, result.Policy, result.UpgradeBaseline, now.UTC()); err != nil {
		return AcquisitionRound{}, err
	}
	return result, nil
}

func (store *Store) recordAttributableAcquisitionFailure(ctx context.Context, transaction pgx.Tx, job AltMountJobContext, verificationID *uuid.UUID, failureKind, failureCode, detail string, now time.Time) (acquisitionFailureResult, error) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "completed AltMount output did not satisfy release requirements"
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	var workflowState workflows.State
	var workflowRevision, jobRevision int64
	var currentJob *string
	var candidateText, indexerText, nativeKey, roundText string
	var roundSlot, attemptLimit int
	var packageIdentity []byte
	err := transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,workflow.grab_attempt_id::text,
		job.revision,job.candidate_id::text,candidate.indexer_instance_id::text,candidate.native_key,
		job.round_id::text,job.round_slot,round.attempt_limit_snapshot,job.nzb_package_sha256
		FROM workflows workflow
		JOIN altmount_jobs job ON job.workflow_id=workflow.id
		JOIN release_candidates candidate ON candidate.workflow_id=workflow.id AND candidate.id=job.candidate_id
		JOIN acquisition_rounds round ON round.workflow_id=workflow.id AND round.id=job.round_id
		WHERE workflow.id=$1 AND job.id=$2 FOR UPDATE OF workflow,job,round`, job.Workflow.ID.String(), job.JobID.String()).Scan(
		&workflowState, &workflowRevision, &currentJob, &jobRevision, &candidateText, &indexerText, &nativeKey,
		&roundText, &roundSlot, &attemptLimit, &packageIdentity,
	)
	if err != nil {
		return acquisitionFailureResult{}, err
	}
	if currentJob == nil || *currentJob != job.JobID.String() || uint64(jobRevision) != job.JobRevision || candidateText != job.CandidateID.String() {
		return acquisitionFailureResult{}, ErrConcurrentModification
	}
	var verification any
	if verificationID != nil {
		verification = verificationID.String()
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO NOTHING`, job.Workflow.ID.String(), candidateText, indexerText, nativeKey, verification, failureKind, detail, now.UTC()); err != nil {
		return acquisitionFailureResult{}, err
	}
	if len(packageIdentity) == sha256.Size {
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_package_exclusions(workflow_id,package_sha256,job_id,candidate_id,failure_kind,reason,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(workflow_id,package_sha256) DO NOTHING`, job.Workflow.ID.String(), packageIdentity, job.JobID.String(), candidateText, failureCode, detail, now.UTC()); err != nil {
			return acquisitionFailureResult{}, err
		}
	}
	command, err := transaction.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',revision=revision+1,last_reconciled_at=$2,error_code=$3,error_detail=$4,acquisition_outcome=$3,acquisition_closed_at=$2 WHERE id=$1 AND revision=$5`, job.JobID.String(), now.UTC(), failureCode, detail, jobRevision)
	if err != nil {
		return acquisitionFailureResult{}, err
	}
	if command.RowsAffected() != 1 {
		return acquisitionFailureResult{}, ErrConcurrentModification
	}
	exhausted := roundSlot >= attemptLimit
	attentionCode, attentionDetail := "", ""
	if exhausted {
		attentionCode = workflows.AttentionRoundExhaustedWaitingNewIdentity
		attentionDetail = fmt.Sprintf("All %d acquisition slots in this round were used. Scheduled discovery will wait for a newly observed eligible release; opening another round requires the owner.", attemptLimit)
		var cutoff int64
		if err := transaction.QueryRow(ctx, `SELECT COALESCE(max(first_seen_sequence),0) FROM release_candidates WHERE workflow_id=$1`, job.Workflow.ID.String()).Scan(&cutoff); err != nil {
			return acquisitionFailureResult{}, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE acquisition_rounds SET state='exhausted',novelty_cutoff_sequence=$2,exhausted_at=$3,closed_at=$3,close_reason=$4 WHERE id=$1 AND state='open'`, roundText, cutoff, now.UTC(), failureCode); err != nil {
			return acquisitionFailureResult{}, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET retry_state='exhausted' WHERE workflow_id=$1 AND retry_state='queued'`, job.Workflow.ID.String()); err != nil {
			return acquisitionFailureResult{}, err
		}
		var discoveryGeneration int64
		if err := transaction.QueryRow(ctx, `SELECT COALESCE((SELECT rescan_generation FROM workflow_retry_discovery WHERE workflow_id=$1),0)+1`, job.Workflow.ID.String()).Scan(&discoveryGeneration); err != nil {
			return acquisitionFailureResult{}, err
		}
		next := retryDiscoveryAt(job.Workflow.ID, discoveryGeneration, 0, now)
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,next_rescan_at,complete_no_new_scans,pending_indexer_ids,created_at,updated_at)
			VALUES($1,$2,'waiting',$3,$4,0,NULL,$5,$5)
			ON CONFLICT(workflow_id) DO UPDATE SET exhausted_round_id=EXCLUDED.exhausted_round_id,state='waiting',rescan_generation=EXCLUDED.rescan_generation,next_rescan_at=EXCLUDED.next_rescan_at,last_scan_at=NULL,last_scan_complete=NULL,complete_no_new_scans=0,pending_indexer_ids=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=EXCLUDED.updated_at`, job.Workflow.ID.String(), roundText, discoveryGeneration, next.UTC(), now.UTC()); err != nil {
			return acquisitionFailureResult{}, err
		}
		payload, _ := json.Marshal(map[string]any{"workflow_id": job.Workflow.ID.String(), "rescan_generation": discoveryGeneration})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
			VALUES($1,'workflow_retry_discovery',$2,$3,'workflow.discovery_rescan_requested',$4,$5,$6)`, uuid.NewV7().String(), job.Workflow.ID.String(), discoveryGeneration, payload, now.UTC(), next.UTC()); err != nil {
			return acquisitionFailureResult{}, err
		}
	}
	newRevision := workflowRevision + 2
	command, err = transaction.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,grab_attempt_id=NULL,revision=$2,updated_at=$3,attention_code=NULLIF($4,''),attention_detail=NULLIF($5,'') WHERE id=$1 AND revision=$6`, job.Workflow.ID.String(), newRevision, now.UTC(), attentionCode, attentionDetail, workflowRevision)
	if err != nil {
		return acquisitionFailureResult{}, err
	}
	if command.RowsAffected() != 1 {
		return acquisitionFailureResult{}, ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES
		($1,$2,$4,'needs_attention',$5,'worker',$6),
		($1,$3,'needs_attention','waiting',$7,'worker',$6)`, job.Workflow.ID.String(), workflowRevision+1, newRevision, workflowState, detail, now.UTC(), func() string {
		if exhausted {
			return attentionDetail
		}
		return fmt.Sprintf("%d acquisition slots remain; exact failed release and package excluded", attemptLimit-roundSlot)
	}()); err != nil {
		return acquisitionFailureResult{}, err
	}
	if !exhausted {
		payload, _ := json.Marshal(map[string]string{"workflow_id": job.Workflow.ID.String(), "reason": "release-attributable verification failure; exact release and package excluded"})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
			VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), job.Workflow.ID.String(), newRevision, payload, now.UTC()); err != nil {
			return acquisitionFailureResult{}, err
		}
	}
	return acquisitionFailureResult{RetryQueued: !exhausted, RoundExhausted: exhausted}, nil
}

func (store *Store) CompleteReleaseFailure(ctx context.Context, job AltMountJobContext, failureCode, detail string, now time.Time) error {
	failureKind := failureCode
	switch failureCode {
	case "season_pack_incomplete", "movie_primary_ambiguous", "output_empty", "media_identity_mismatch", "media_corrupt", "password_protected",
		"archive_unsupported", "no_playable_media", "release_incomplete":
	default:
		return errors.New("failure is not safely classified as release-attributable")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	if _, err := store.recordAttributableAcquisitionFailure(ctx, transaction, job, nil, failureKind, failureCode, detail, now); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) OpenNextAcquisitionRound(ctx context.Context, workflowID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var workflowState workflows.State
	var workflowRevision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&workflowState, &workflowRevision); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if workflowState != workflows.StateWaiting {
		return errors.New("workflow is not waiting for a new acquisition round")
	}
	var retainedOutputs int
	if err := transaction.QueryRow(ctx, `SELECT count(DISTINCT job.id) FROM altmount_jobs job
		JOIN altmount_output_inventories inventory ON inventory.job_id=job.id
		WHERE job.workflow_id=$1 AND job.acquisition_outcome IS NOT NULL AND job.acquisition_outcome <> 'output_verified'`, workflowID.String()).Scan(&retainedOutputs); err != nil {
		return err
	}
	if retainedOutputs >= store.AcquisitionLimits().RetainedFailedOutputs {
		return errors.New("retained failed-output limit reached; owner storage review is required before another round")
	}
	var exhaustedRoundText, readyCandidateText string
	var generation, readySequence int64
	err = transaction.QueryRow(ctx, `SELECT discovery.exhausted_round_id::text,discovery.ready_candidate_id::text,discovery.rescan_generation,discovery.ready_first_seen_sequence
		FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=$1 AND discovery.state='ready' FOR UPDATE`, workflowID.String()).Scan(&exhaustedRoundText, &readyCandidateText, &generation, &readySequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("no newly observed eligible release is ready for owner approval")
	}
	if err != nil {
		return err
	}
	var ordinal int
	var cutoff, policyVersion int64
	var policySnapshot json.RawMessage
	if err := transaction.QueryRow(ctx, `SELECT round_ordinal,novelty_cutoff_sequence,policy_version,policy_snapshot FROM acquisition_rounds WHERE id=$1 AND workflow_id=$2 AND state='exhausted' FOR UPDATE`, exhaustedRoundText, workflowID.String()).Scan(&ordinal, &cutoff, &policyVersion, &policySnapshot); err != nil {
		return ErrConcurrentModification
	}
	if readySequence <= cutoff {
		return ErrConcurrentModification
	}
	var profileVersion int64
	if err := transaction.QueryRow(ctx, `SELECT profile.policy_version FROM workflows workflow JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id WHERE workflow.id=$1`, workflowID.String()).Scan(&profileVersion); err != nil {
		return err
	}
	if profileVersion != policyVersion {
		return errors.New("the quality policy changed after this round was exhausted; apply the current policy to the profile instead of reopening the old snapshot")
	}
	var candidateValid bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM release_candidates candidate
		JOIN candidate_decisions decision ON decision.workflow_id=candidate.workflow_id AND decision.candidate_id=candidate.id
		WHERE candidate.workflow_id=$1 AND candidate.id=$2 AND candidate.first_seen_sequence=$3 AND decision.policy_version=$4 AND decision.verdict='eligible'
		AND NOT EXISTS(SELECT 1 FROM workflow_attempted_releases attempted WHERE attempted.workflow_id=candidate.workflow_id AND attempted.candidate_id=candidate.id)
		AND NOT EXISTS(SELECT 1 FROM workflow_release_exclusions exclusion WHERE exclusion.workflow_id=candidate.workflow_id AND exclusion.indexer_instance_id=candidate.indexer_instance_id AND exclusion.native_key=candidate.native_key)`+" AND NOT "+knownSubtitleFailureSQL("candidate")+`
	)`, workflowID.String(), readyCandidateText, readySequence, policyVersion).Scan(&candidateValid); err != nil {
		return err
	}
	if !candidateValid {
		return errors.New("the discovered release is no longer eligible; wait for a new complete discovery scan")
	}
	var baseline string
	err = transaction.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'') FROM workflows requested
		LEFT JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
		LEFT JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
		WHERE requested.id=$1`, workflowID.String()).Scan(&baseline)
	if err != nil {
		return err
	}
	roundID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
		VALUES($1,$2,$3,'open',$9,$4,$5,$6,$7,$8)`, roundID.String(), workflowID.String(), ordinal+1, cutoff, policyVersion, policySnapshot, baseline, now.UTC(), store.AcquisitionLimits().AttemptsPerRound); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='paused',next_rescan_at=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$2 WHERE workflow_id=$1 AND rescan_generation=$3`, workflowID.String(), now.UTC(), generation); err != nil {
		return err
	}
	newRevision := workflowRevision + 1
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='searching',revision=$2,updated_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$4`, workflowID.String(), newRevision, now.UTC(), workflowRevision); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at)
		VALUES($1,$2,$3,'searching','owner opened a new bounded acquisition round after new eligible discovery','owner',$4)`, workflowID.String(), newRevision, workflowState, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "round_id": roundID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), newRevision, payload, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
