package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

type WorkflowSummary struct {
	ID                    uuid.UUID         `json:"id"`
	MediaID               uuid.UUID         `json:"media_id"`
	MediaKind             catalog.MediaKind `json:"media_kind"`
	Title                 string            `json:"title"`
	Year                  *uint16           `json:"year,omitempty"`
	Variant               string            `json:"variant"`
	State                 workflows.State   `json:"state"`
	Revision              uint64            `json:"revision"`
	AttentionCode         string            `json:"attention_code,omitempty"`
	AttentionDetail       string            `json:"attention_detail,omitempty"`
	AcquisitionAttempts   int               `json:"acquisition_attempts"`
	RoundOrdinal          int               `json:"round_ordinal"`
	RoundState            string            `json:"round_state,omitempty"`
	RoundSlotsUsed        int               `json:"round_slots_used"`
	RoundAttemptLimit     int               `json:"round_attempt_limit"`
	RetryDiscoveryState   string            `json:"retry_discovery_state,omitempty"`
	NextRescanAt          *time.Time        `json:"next_rescan_at,omitempty"`
	LastDiscoveryAt       *time.Time        `json:"last_discovery_at,omitempty"`
	LastDiscoveryComplete *bool             `json:"last_discovery_complete,omitempty"`
	CompleteNoNewScans    int               `json:"complete_no_new_scans"`
	NewReleaseReady       bool              `json:"new_release_ready"`
	ReadyCandidateID      *uuid.UUID        `json:"ready_candidate_id,omitempty"`
	ReadyReleaseTitle     string            `json:"ready_release_title,omitempty"`
	ReadyIndexerName      string            `json:"ready_indexer_name,omitempty"`
	ReadyFirstObservedAt  *time.Time        `json:"ready_first_observed_at,omitempty"`
	OpenNextRoundAllowed  bool              `json:"open_next_round_allowed"`
	RetainedOutputLimit   int               `json:"retained_output_limit"`
	RetainedOutputCount   int               `json:"retained_output_count"`
	RetainedOutputBytes   int64             `json:"retained_output_bytes"`
	ExcludedReleaseCount  int               `json:"excluded_release_count"`
	SearchAllowed         bool              `json:"search_allowed"`
	SearchBlockedReason   string            `json:"search_blocked_reason,omitempty"`

	// Exact eligible access rejection the owner can retry after repairing provider access.
	AccessRejectedCandidateID  *uuid.UUID `json:"access_rejected_candidate_id,omitempty"`
	AccessRejectedReleaseTitle string     `json:"access_rejected_release_title,omitempty"`
	AccessRejectedIndexerName  string     `json:"access_rejected_indexer_name,omitempty"`

	// Download progress exactly as AltMount last reported it for the current
	// grab attempt, only while that attempt is in flight. Absent means AltMount
	// reported nothing; the UI never fills the gap with an estimate.
	DownloadPercent    *int       `json:"download_percent,omitempty"`
	DownloadStage      string     `json:"download_stage,omitempty"`
	DownloadObservedAt *time.Time `json:"download_observed_at,omitempty"`
	// QualityProfile is the name of the profile this workflow acquires under,
	// so an owner sees it before any file exists.
	QualityProfile string `json:"quality_profile,omitempty"`
	// OwnerActionRequired is the single owner-facing "needs attention"
	// predicate. It is true only when the workflow cannot advance without the
	// owner: an unresolved failure state, or a waiting workflow whose paced
	// discovery found a new eligible release that the owner must approve.
	// Self-recovering states (held, searching, normal waiting) are false.
	OwnerActionRequired                    bool       `json:"owner_action_required"`
	AdoptedReplacementConfirmationRequired bool       `json:"adopted_replacement_confirmation_required"`
	TerminalAt                             *time.Time `json:"terminal_at,omitempty"`
	CreatedAt                              time.Time  `json:"created_at"`
	UpdatedAt                              time.Time  `json:"updated_at"`
}

func (store *Store) GetMedia(ctx context.Context, id uuid.UUID) (catalog.MediaItem, error) {
	var item catalog.MediaItem
	var idText string
	var year *int16
	var revision int64
	err := store.Pool.QueryRow(ctx, `SELECT id::text,kind,title,year,revision,created_at,updated_at FROM media_items WHERE id=$1 AND deleted_at IS NULL`, id.String()).Scan(&idText, &item.Kind, &item.Title, &year, &revision, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.MediaItem{}, ErrNotFound
	}
	if err != nil {
		return catalog.MediaItem{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	item.Revision = uint64(revision)
	if year != nil {
		converted := uint16(*year)
		item.Year = &converted
	}
	item.Identities, err = store.identities(ctx, item.ID)
	if err != nil {
		return catalog.MediaItem{}, err
	}
	item.Metadata, err = store.metadata(ctx, item.ID)
	return item, err
}

func (store *Store) ListWorkflowSummaries(ctx context.Context, limit int, activeOnly bool) ([]WorkflowSummary, error) {
	return store.workflowSummaries(ctx, limit, activeOnly, nil)
}

func (store *Store) workflowSummaries(ctx context.Context, limit int, activeOnly bool, mediaID *uuid.UUID) ([]WorkflowSummary, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var mediaIDValue any
	if mediaID != nil {
		mediaIDValue = mediaID.String()
	}
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,workflow.media_id::text,media.kind,media.title,media.year,workflow.variant,workflow.state,workflow.revision,COALESCE(workflow.attention_code,''),COALESCE(NULLIF(workflow.attention_detail,''),(SELECT transition.reason FROM workflow_transitions transition WHERE transition.workflow_id=workflow.id AND transition.to_state='needs_attention' ORDER BY transition.to_revision DESC LIMIT 1),''),
		COALESCE((SELECT max(job.attempt_generation) FROM altmount_jobs job WHERE job.workflow_id=workflow.id),0),
		COALESCE((SELECT round.round_ordinal FROM acquisition_rounds round WHERE round.workflow_id=workflow.id ORDER BY round.round_ordinal DESC LIMIT 1),0),
		COALESCE((SELECT round.state FROM acquisition_rounds round WHERE round.workflow_id=workflow.id ORDER BY round.round_ordinal DESC LIMIT 1),''),
		COALESCE((SELECT count(*) FROM altmount_jobs job JOIN acquisition_rounds round ON round.id=job.round_id WHERE round.workflow_id=workflow.id AND round.round_ordinal=(SELECT max(latest.round_ordinal) FROM acquisition_rounds latest WHERE latest.workflow_id=workflow.id)),0),
		COALESCE((SELECT round.attempt_limit_snapshot FROM acquisition_rounds round WHERE round.workflow_id=workflow.id ORDER BY round.round_ordinal DESC LIMIT 1),0),
		COALESCE((SELECT discovery.state FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),''),
		(SELECT discovery.next_rescan_at FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),
		(SELECT discovery.last_scan_at FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),
		(SELECT discovery.last_scan_complete FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),
		COALESCE((SELECT discovery.complete_no_new_scans FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),0),
		COALESCE((SELECT discovery.state='ready' FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),false),
		(SELECT discovery.ready_candidate_id::text FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id),
		COALESCE((SELECT candidate.title FROM workflow_retry_discovery discovery JOIN release_candidates candidate ON candidate.workflow_id=discovery.workflow_id AND candidate.id=discovery.ready_candidate_id WHERE discovery.workflow_id=workflow.id),''),
		COALESCE((SELECT integration.name FROM workflow_retry_discovery discovery JOIN release_candidates candidate ON candidate.workflow_id=discovery.workflow_id AND candidate.id=discovery.ready_candidate_id JOIN integration_instances integration ON integration.id=candidate.indexer_instance_id WHERE discovery.workflow_id=workflow.id),''),
		(SELECT candidate.first_observed_at FROM workflow_retry_discovery discovery JOIN release_candidates candidate ON candidate.workflow_id=discovery.workflow_id AND candidate.id=discovery.ready_candidate_id WHERE discovery.workflow_id=workflow.id),
		(SELECT count(DISTINCT job.id) FROM altmount_output_inventories inventory JOIN altmount_jobs job ON job.id=inventory.job_id WHERE job.workflow_id=workflow.id AND job.acquisition_outcome IS NOT NULL AND job.acquisition_outcome <> 'output_verified'),
		COALESCE((SELECT sum(asset.size_bytes) FROM altmount_output_assets asset JOIN altmount_output_inventories inventory ON inventory.id=asset.inventory_id JOIN altmount_jobs job ON job.id=inventory.job_id WHERE job.workflow_id=workflow.id AND job.acquisition_outcome IS NOT NULL AND job.acquisition_outcome <> 'output_verified' AND inventory.generation=(SELECT max(latest.generation) FROM altmount_output_inventories latest WHERE latest.job_id=job.id)),0),
		(SELECT count(*) FROM workflow_release_exclusions exclusion WHERE exclusion.workflow_id=workflow.id),
		EXISTS(SELECT 1 FROM library_link_versions version WHERE version.workflow_id=workflow.id AND version.state IN ('publishing','verified','current','quarantined')),
		COALESCE((SELECT job.state='needs_attention' FROM altmount_jobs job WHERE job.id=workflow.grab_attempt_id),false),
		EXISTS(SELECT 1 FROM adopted_files adopted WHERE adopted.media_id=workflow.media_id AND adopted.variant=workflow.variant AND adopted.state='current' AND NOT EXISTS(SELECT 1 FROM adopted_replacement_authorizations replacement_auth WHERE replacement_auth.workflow_id=workflow.id AND replacement_auth.adopted_file_id=adopted.id AND replacement_auth.destination_relative=adopted.destination_relative AND replacement_auth.source_size=adopted.source_size AND replacement_auth.source_modtime_ns=adopted.source_modtime_ns)),
		workflow.terminal_at,workflow.created_at,workflow.updated_at,
		progress.progress_percent,COALESCE(progress.progress_stage,''),progress.progress_observed_at,
		COALESCE(profile.name,''),access_retry.id,COALESCE(access_retry.title,''),COALESCE(access_retry.indexer_name,'')
		FROM workflows workflow JOIN media_items media ON media.id=workflow.media_id
		LEFT JOIN altmount_jobs progress ON progress.id=workflow.grab_attempt_id AND workflow.state IN ('submitting','queued','downloading','streaming_ready')
		LEFT JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
		LEFT JOIN LATERAL (SELECT c.id::text,c.title,i.name AS indexer_name `+store.accessRejectedRetryQuery()+` AND w.id=workflow.id) access_retry ON true
		WHERE media.deleted_at IS NULL AND (NOT $2 OR workflow.terminal_at IS NULL)
			AND ($3::uuid IS NULL OR workflow.media_id=$3::uuid)
		ORDER BY workflow.updated_at DESC,workflow.id LIMIT $1`, limit, activeOnly, mediaIDValue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WorkflowSummary{}
	for rows.Next() {
		var item WorkflowSummary
		var idText, mediaText string
		var readyCandidateText, accessCandidateText *string
		var year *int16
		var revision int64
		var publicationStarted, currentJobNeedsAttention bool
		var downloadPercent *int16
		if err := rows.Scan(&idText, &mediaText, &item.MediaKind, &item.Title, &year, &item.Variant, &item.State, &revision, &item.AttentionCode, &item.AttentionDetail, &item.AcquisitionAttempts, &item.RoundOrdinal, &item.RoundState, &item.RoundSlotsUsed, &item.RoundAttemptLimit, &item.RetryDiscoveryState, &item.NextRescanAt, &item.LastDiscoveryAt, &item.LastDiscoveryComplete, &item.CompleteNoNewScans, &item.NewReleaseReady, &readyCandidateText, &item.ReadyReleaseTitle, &item.ReadyIndexerName, &item.ReadyFirstObservedAt, &item.RetainedOutputCount, &item.RetainedOutputBytes, &item.ExcludedReleaseCount, &publicationStarted, &currentJobNeedsAttention, &item.AdoptedReplacementConfirmationRequired, &item.TerminalAt, &item.CreatedAt, &item.UpdatedAt, &downloadPercent, &item.DownloadStage, &item.DownloadObservedAt, &item.QualityProfile, &accessCandidateText, &item.AccessRejectedReleaseTitle, &item.AccessRejectedIndexerName); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.MediaID, _ = uuid.Parse(mediaText)
		if readyCandidateText != nil {
			readyCandidateID, parseErr := uuid.Parse(*readyCandidateText)
			if parseErr != nil {
				return nil, parseErr
			}
			item.ReadyCandidateID = &readyCandidateID
		}
		if accessCandidateText != nil {
			id, err := uuid.Parse(*accessCandidateText)
			if err != nil {
				return nil, err
			}
			item.AccessRejectedCandidateID = &id
		}
		item.Revision = uint64(revision)
		if downloadPercent != nil {
			converted := int(*downloadPercent)
			item.DownloadPercent = &converted
		}
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		item.SearchAllowed, item.SearchBlockedReason = workflowSearchReadiness(item, publicationStarted, currentJobNeedsAttention)
		item.OwnerActionRequired = workflowOwnerActionRequired(item)
		item.RetainedOutputLimit = store.AcquisitionLimits().RetainedFailedOutputs
		item.OpenNextRoundAllowed = item.State == workflows.StateWaiting && item.RetryDiscoveryState == "ready" && item.NewReleaseReady && item.RetainedOutputCount < item.RetainedOutputLimit
		result = append(result, item)
	}
	return result, rows.Err()
}

// workflowOwnerActionRequired is the read-model definition of "needs
// attention": the owner must act before the workflow can advance. A waiting
// workflow only counts once paced discovery has produced a ready candidate the
// owner must approve; while discovery is still waiting, the system is still
// working. Held is a recoverable, paced no-selection state and never requires
// owner action on its own.
func workflowOwnerActionRequired(item WorkflowSummary) bool {
	switch item.State {
	case workflows.StateNeedsAttention, workflows.StateBlocked, workflows.StateQuarantined, workflows.StateTransportUnknown:
		return true
	case workflows.StateWaiting:
		return item.RetryDiscoveryState == "ready"
	default:
		return false
	}
}

func workflowSearchReadiness(item WorkflowSummary, publicationStarted, currentJobNeedsAttention bool) (bool, string) {
	if item.RetryDiscoveryState == "waiting" {
		return false, "The round is exhausted; scheduled discovery is waiting for a newly observed eligible release."
	}
	if item.RetryDiscoveryState == "ready" {
		return false, "A new eligible release is ready; the owner must start the next acquisition round."
	}
	if item.RoundState == "exhausted" {
		return false, "The acquisition round is exhausted."
	}
	if item.State == workflows.StateMonitored || item.State == workflows.StateWaiting || item.State == workflows.StateHeld {
		return true, ""
	}
	if item.State != workflows.StateNeedsAttention {
		return false, "Workflow is already in progress or terminal."
	}
	if item.AttentionCode == "altmount_submission_rejected" {
		return false, "Resolve the rejected AltMount submission; a different release is not permitted."
	}
	if infrastructureAttentionCode(item.AttentionCode) {
		return false, "Recover the current AltMount job instead of searching for another release."
	}
	switch item.AttentionCode {
	case "plex_timeout", "publication_failed", "seerr_snapshot_changed", "seerr_request_revoked":
		return false, "Owner recovery is required before another search can start."
	}
	if publicationStarted {
		return false, "Publication has started; recover or verify the existing links instead of searching again."
	}
	if currentJobNeedsAttention && item.RoundAttemptLimit > 0 && item.RoundSlotsUsed >= item.RoundAttemptLimit {
		return false, "This acquisition round used all available slots; failed output remains retained for owner review."
	}
	return true, ""
}
