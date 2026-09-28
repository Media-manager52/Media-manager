package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

// Policy application outcomes reported per workflow.
const (
	PolicyOutcomeApplied               = "applied"
	PolicyOutcomeAppliedSearchQueued   = "applied_search_queued"
	PolicyOutcomeAlreadyCurrent        = "already_current"
	PolicyOutcomeInFlight              = "in_flight"
	PolicyOutcomeOwnerRecoveryRequired = "owner_recovery_required"
	PolicyOutcomeRetainedOutputLimit   = "retained_output_limit"
	PolicyOutcomeNoAcquisitionRound    = "no_acquisition_round"
)

type PolicyWorkflowOutcome struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
	State      string    `json:"state"`
	Outcome    string    `json:"outcome"`
	RoundID    uuid.UUID `json:"round_id,omitempty"`
}

type PolicyApplication struct {
	ProfileID     uuid.UUID               `json:"profile_id"`
	PolicyVersion uint64                  `json:"policy_version"`
	Applied       int                     `json:"applied"`
	Skipped       int                     `json:"skipped"`
	Workflows     []PolicyWorkflowOutcome `json:"workflows"`
}

type policyWorkflow struct {
	id                  uuid.UUID
	state               workflows.State
	revision            int64
	attentionCode       string
	hasAttempt          bool
	openRoundID         *string
	openRoundVersion    *int64
	exhaustedRoundVer   *int64
	maxOrdinal          int
	activeJob           bool
	resumableImport     bool
	publicationStarted  bool
	retainedFailedCount int
}

// ApplyQualityProfilePolicy moves every non-terminal workflow on a quality
// profile onto the profile's current policy version in one serializable
// transaction. A workflow whose round snapshot is older gets its open round
// superseded (state cancelled, close reason naming the new version) and a new
// open round with the current policy snapshot, current upgrade baseline, and a
// zero novelty cutoff: a policy round changes the evaluation itself, so every
// known candidate is re-evaluated, while attempted and excluded releases stay
// disqualified. Exclusions are never revived.
//
// State handling is deliberately minimal:
//   - candidate_selected: the selection was made under the old policy, so it
//     is cleared (needs_attention -> waiting) and a search is queued now;
//   - waiting after round exhaustion, or parked by quality_policy_changed: the
//     attention is cleared and a search is queued now (the owner action grants
//     the new round); exhausted workflows still honor the retained failed-output
//     storage limit;
//   - monitored, waiting, held, searching, and attempt-free needs_attention:
//     only the round is replaced, so any existing search schedule continues
//     under the new policy;
//   - workflows with an active or attention AltMount attempt, a started
//     publication, or a recovery-only attention code are skipped and reported;
//     they finish under the policy they started with.
func (store *Store) ApplyQualityProfilePolicy(ctx context.Context, profileID uuid.UUID, actor string, now time.Time) (PolicyApplication, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PolicyApplication{}, err
	}
	defer transaction.Rollback(ctx)
	var profileVersion int64
	var policy []byte
	if err := transaction.QueryRow(ctx, `SELECT policy_version,policy FROM quality_profiles WHERE id=$1 FOR SHARE`, profileID.String()).Scan(&profileVersion, &policy); errors.Is(err, pgx.ErrNoRows) {
		return PolicyApplication{}, ErrNotFound
	} else if err != nil {
		return PolicyApplication{}, err
	}
	rows, err := transaction.Query(ctx, `SELECT workflow.id::text,workflow.state,workflow.revision,COALESCE(workflow.attention_code,''),workflow.grab_attempt_id IS NOT NULL,
		open_round.id::text,open_round.policy_version,exhausted.policy_version,
		(SELECT COALESCE(max(round.round_ordinal),0) FROM acquisition_rounds round WHERE round.workflow_id=workflow.id),
		EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=workflow.id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested')),
		EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=workflow.id AND job.candidate_id=workflow.selected_release_id AND job.state='import_complete'),
		EXISTS(SELECT 1 FROM library_link_versions version WHERE version.workflow_id=workflow.id AND version.state IN ('planned','publishing','verified','current','quarantined')),
		(SELECT count(DISTINCT job.id) FROM altmount_jobs job JOIN altmount_output_inventories inventory ON inventory.job_id=job.id
		  WHERE job.workflow_id=workflow.id AND job.acquisition_outcome IS NOT NULL AND job.acquisition_outcome <> 'output_verified')
		FROM workflows workflow
		LEFT JOIN acquisition_rounds open_round ON open_round.workflow_id=workflow.id AND open_round.state='open'
		LEFT JOIN workflow_retry_discovery discovery ON discovery.workflow_id=workflow.id AND discovery.state IN ('waiting','ready')
		LEFT JOIN acquisition_rounds exhausted ON exhausted.id=discovery.exhausted_round_id AND exhausted.workflow_id=workflow.id AND exhausted.state='exhausted'
		WHERE workflow.quality_profile_id=$1 AND workflow.terminal_at IS NULL
		ORDER BY workflow.created_at,workflow.id
		FOR UPDATE OF workflow`, profileID.String())
	if err != nil {
		return PolicyApplication{}, err
	}
	var candidates []policyWorkflow
	for rows.Next() {
		var item policyWorkflow
		var idText string
		if err := rows.Scan(&idText, &item.state, &item.revision, &item.attentionCode, &item.hasAttempt, &item.openRoundID, &item.openRoundVersion, &item.exhaustedRoundVer, &item.maxOrdinal, &item.activeJob, &item.resumableImport, &item.publicationStarted, &item.retainedFailedCount); err != nil {
			rows.Close()
			return PolicyApplication{}, err
		}
		item.id, _ = uuid.Parse(idText)
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PolicyApplication{}, err
	}
	rows.Close()

	result := PolicyApplication{ProfileID: profileID, PolicyVersion: uint64(profileVersion), Workflows: []PolicyWorkflowOutcome{}}
	closeReason := fmt.Sprintf("quality policy superseded by profile version %d", profileVersion)
	for _, item := range candidates {
		outcome := PolicyWorkflowOutcome{WorkflowID: item.id, State: string(item.state)}
		skip := policySkipReason(item, profileVersion)
		if skip != "" {
			outcome.Outcome = skip
			result.Skipped++
			result.Workflows = append(result.Workflows, outcome)
			continue
		}
		if item.openRoundID != nil {
			command, err := transaction.Exec(ctx, `UPDATE acquisition_rounds SET state='cancelled',closed_at=$2,close_reason=$3 WHERE id=$1 AND state='open'`, *item.openRoundID, now.UTC(), closeReason)
			if err != nil {
				return PolicyApplication{}, err
			}
			if command.RowsAffected() != 1 {
				return PolicyApplication{}, ErrConcurrentModification
			}
		}
		baseline, err := currentUpgradeBaseline(ctx, transaction, item.id)
		if err != nil {
			return PolicyApplication{}, err
		}
		roundID := uuid.NewV7()
		if _, err := transaction.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
			VALUES($1,$2,$3,'open',$4,0,$5,$6,$7,$8)`, roundID.String(), item.id.String(), item.maxOrdinal+1, maxAcquisitionAttemptsPerRound, profileVersion, policy, baseline, now.UTC()); err != nil {
			return PolicyApplication{}, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='paused',next_rescan_at=NULL,ready_candidate_id=NULL,ready_first_seen_sequence=NULL,updated_at=$2 WHERE workflow_id=$1 AND state IN ('waiting','ready')`, item.id.String(), now.UTC()); err != nil {
			return PolicyApplication{}, err
		}
		newRevision := item.revision
		queueSearch := false
		switch {
		case item.state == workflows.StateCandidateSelected:
			newRevision = item.revision + 2
			command, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,revision=$2,updated_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$4`, item.id.String(), newRevision, now.UTC(), item.revision)
			if err != nil {
				return PolicyApplication{}, err
			}
			if command.RowsAffected() != 1 {
				return PolicyApplication{}, ErrConcurrentModification
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES
				($1,$2,'candidate_selected','needs_attention',$4,$5,$6),
				($1,$3,'needs_attention','waiting',$7,$5,$6)`, item.id.String(), item.revision+1, newRevision, "quality policy changed after candidate selection", actor, now.UTC(), closeReason+"; re-searching under the new policy"); err != nil {
				return PolicyApplication{}, err
			}
			queueSearch = true
		case item.openRoundID == nil || item.attentionCode == workflows.AttentionQualityPolicyChanged:
			newRevision = item.revision + 1
			command, err := transaction.Exec(ctx, `UPDATE workflows SET revision=$2,updated_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$4 AND state='waiting'`, item.id.String(), newRevision, now.UTC(), item.revision)
			if err != nil {
				return PolicyApplication{}, err
			}
			if command.RowsAffected() != 1 {
				return PolicyApplication{}, ErrConcurrentModification
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'waiting','waiting',$3,$4,$5)`, item.id.String(), newRevision, "owner applied quality policy version "+fmt.Sprint(profileVersion)+" in a new acquisition round", actor, now.UTC()); err != nil {
				return PolicyApplication{}, err
			}
			queueSearch = true
		}
		if queueSearch {
			payload, _ := json.Marshal(map[string]string{"workflow_id": item.id.String(), "round_id": roundID.String(), "reason": "owner applied the current quality policy"})
			if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), item.id.String(), newRevision, string(payload), now.UTC()); err != nil {
				return PolicyApplication{}, err
			}
			outcome.Outcome = PolicyOutcomeAppliedSearchQueued
		} else {
			outcome.Outcome = PolicyOutcomeApplied
		}
		var previousVersion int64
		if item.openRoundVersion != nil {
			previousVersion = *item.openRoundVersion
		} else if item.exhaustedRoundVer != nil {
			previousVersion = *item.exhaustedRoundVer
		}
		detail, _ := json.Marshal(map[string]any{"quality_profile_id": profileID, "previous_policy_version": previousVersion, "policy_version": profileVersion, "round_id": roundID, "outcome": outcome.Outcome})
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'workflow.quality_policy_applied','workflow',$3,$4,$5)`, uuid.NewV7().String(), actor, item.id.String(), string(detail), now.UTC()); err != nil {
			return PolicyApplication{}, err
		}
		outcome.RoundID = roundID
		result.Applied++
		result.Workflows = append(result.Workflows, outcome)
	}
	summary, _ := json.Marshal(map[string]any{"policy_version": profileVersion, "applied": result.Applied, "skipped": result.Skipped})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'quality_profile.policy_applied','quality_profile',$3,$4,$5)`, uuid.NewV7().String(), actor, profileID.String(), string(summary), now.UTC()); err != nil {
		return PolicyApplication{}, err
	}
	return result, transaction.Commit(ctx)
}

func policySkipReason(item policyWorkflow, profileVersion int64) string {
	var roundVersion int64
	switch {
	case item.openRoundVersion != nil:
		roundVersion = *item.openRoundVersion
	case item.exhaustedRoundVer != nil:
		roundVersion = *item.exhaustedRoundVer
	default:
		return PolicyOutcomeNoAcquisitionRound
	}
	if roundVersion == profileVersion {
		return PolicyOutcomeAlreadyCurrent
	}
	if item.activeJob || item.resumableImport || item.publicationStarted {
		return PolicyOutcomeInFlight
	}
	switch item.state {
	case workflows.StateMonitored, workflows.StateWaiting, workflows.StateHeld, workflows.StateSearching, workflows.StateCandidateSelected:
	case workflows.StateNeedsAttention:
		if item.hasAttempt || infrastructureAttentionCode(item.attentionCode) {
			return PolicyOutcomeOwnerRecoveryRequired
		}
		switch item.attentionCode {
		case "altmount_submission_rejected", "plex_timeout", "publication_failed", "seerr_snapshot_changed", "seerr_request_revoked":
			return PolicyOutcomeOwnerRecoveryRequired
		}
	default:
		return PolicyOutcomeInFlight
	}
	if item.openRoundID == nil {
		// Only an exhausted round waiting on discovery: the owner action grants
		// a new round, so the storage-review limit applies exactly as it does
		// for OpenNextAcquisitionRound.
		if item.state != workflows.StateWaiting {
			return PolicyOutcomeOwnerRecoveryRequired
		}
		if item.retainedFailedCount >= 4 {
			return PolicyOutcomeRetainedOutputLimit
		}
	}
	if item.attentionCode == workflows.AttentionQualityPolicyChanged && item.state != workflows.StateWaiting {
		return PolicyOutcomeOwnerRecoveryRequired
	}
	return ""
}

func currentUpgradeBaseline(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID) (string, error) {
	var baseline string
	err := transaction.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'') FROM workflows requested
		LEFT JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
		LEFT JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
		WHERE requested.id=$1`, workflowID.String()).Scan(&baseline)
	return baseline, err
}

// MarkQualityPolicyReviewRequired parks a searchable workflow whose open round
// was snapshotted under an older quality policy. The workflow waits with an
// owner-visible attention code instead of silently searching under the old
// policy or retrying a failing search forever; ApplyQualityProfilePolicy
// clears it and queues the search.
func (store *Store) MarkQualityPolicyReviewRequired(ctx context.Context, workflowID uuid.UUID, expectedRevision uint64, roundVersion, profileVersion uint64, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &revision); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if uint64(revision) != expectedRevision {
		return ErrConcurrentModification
	}
	switch state {
	case workflows.StateMonitored, workflows.StateWaiting, workflows.StateHeld, workflows.StateSearching:
	default:
		return ErrConcurrentModification
	}
	detail := fmt.Sprintf("The quality profile is at policy version %d but this acquisition round was opened under version %d. Apply the current policy to the profile's workflows before searching continues.", profileVersion, roundVersion)
	command, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',revision=revision+1,updated_at=$2,attention_code=$3,attention_detail=$4 WHERE id=$1 AND revision=$5`, workflowID.String(), now.UTC(), workflows.AttentionQualityPolicyChanged, detail, revision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'waiting','quality policy changed after the acquisition round opened; owner must apply the current policy','worker',$4)`, workflowID.String(), revision+1, state, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
