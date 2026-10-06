package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

// workflowAction tells a client which owner action applies to a workflow now.
// Available means every precondition visible in the read model passes; the
// server re-checks everything when the action is called, so a 409 is still
// possible if the workflow changed in between.
type workflowAction struct {
	Action      string         `json:"action"`
	OperationID string         `json:"operation_id"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Effect      apiEffect      `json:"effect"`
	Available   bool           `json:"available"`
	Reason      string         `json:"reason,omitempty"`
	Note        string         `json:"note,omitempty"`
	Body        map[string]any `json:"body,omitempty"`
	// Confirmation is an owner phrase the body must carry. An agent must ask
	// the owner before sending it.
	Confirmation string `json:"confirmation,omitempty"`
}

type workflowView struct {
	postgres.WorkflowSummary
	AvailableActions []workflowAction `json:"available_actions"`
}

func newWorkflowView(item postgres.WorkflowSummary) workflowView {
	return workflowView{WorkflowSummary: item, AvailableActions: workflowActions(item)}
}

func workflowActions(item postgres.WorkflowSummary) []workflowAction {
	path := func(suffix string) string { return "/api/v1/workflows/" + item.ID.String() + suffix }
	unless := func(ok bool, reason string) string {
		if ok {
			return ""
		}
		return reason
	}
	state, terminal := item.State, item.TerminalAt != nil
	waitingExhausted := state == workflows.StateWaiting && item.RoundState == "exhausted"
	underLimit := item.RetainedOutputLimit == 0 || item.RetainedOutputCount < item.RetainedOutputLimit

	search := workflowAction{Action: "search", OperationID: "searchWorkflow", Method: http.MethodPost, Path: path("/search"), Effect: effectProvider, Available: item.SearchAllowed && !item.ActiveAltMountJob && !terminal}
	switch {
	case terminal:
		search.Reason = "The workflow is finished."
	case item.ActiveAltMountJob:
		search.Reason = "An AltMount job is active; wait for it to settle."
	case !underLimit:
		// A search could only rediscover releases the retained-output cap
		// would refuse to download, so it is not offered.
		search.Available = false
		search.Reason = fmt.Sprintf("The retained failed-output limit (%d) is reached; the owner must review storage (or raise ACQUISITION_RETAINED_OUTPUT_LIMIT) before more releases can be tried.", item.RetainedOutputLimit)
	case !item.SearchAllowed:
		search.Reason = item.SearchBlockedReason
		if search.Reason == "" {
			search.Reason = "Search does not apply in state " + string(state) + "."
		}
	}
	if item.AdoptedReplacementConfirmationRequired {
		search.Confirmation = postgres.AdoptedReplacementConfirmation
		search.Note = "This replaces an adopted file. Ask the owner, then send the confirmation as adopted_replacement_confirmation."
	}

	discoverReason := ""
	switch {
	case state != workflows.StateWaiting:
		discoverReason = "Discovery is only for waiting workflows."
	case item.RetryDiscoveryState == "ready":
		discoverReason = "Discovery already found a new eligible release; open the next round instead."
	case item.RetryDiscoveryState != "waiting" || item.RoundState == "open":
		discoverReason = "No exhausted round is waiting for discovery."
	case item.ActiveAltMountJob:
		discoverReason = "An AltMount job is active."
	}

	nextRoundReason := ""
	switch {
	case item.OpenNextRoundAllowed:
	case state != workflows.StateWaiting:
		nextRoundReason = "Only waiting workflows can open a new round."
	case !underLimit:
		nextRoundReason = fmt.Sprintf("The retained failed-output limit (%d) is reached; the owner must review storage first.", item.RetainedOutputLimit)
	default:
		nextRoundReason = "No newly discovered eligible release is ready yet; paced discovery keeps looking."
	}

	knownReason := ""
	switch {
	case !waitingExhausted:
		knownReason = "Only waiting workflows with an exhausted round can open a round from known releases."
	case !underLimit:
		knownReason = fmt.Sprintf("The retained failed-output limit (%d) is reached.", item.RetainedOutputLimit)
	}

	reconcile := state == workflows.StateNeedsAttention && postgres.InfrastructureAttentionCode(item.AttentionCode)
	plex := state == workflows.StateNeedsAttention && item.AttentionCode == "plex_timeout"
	cancellable := !terminal
	cancelReason := ""
	switch state {
	case workflows.StateMonitored, workflows.StateWaiting, workflows.StateSearching, workflows.StateCandidateSelected, workflows.StateHeld, workflows.StateBlocked:
	case workflows.StateNeedsAttention:
		if item.ActiveAltMountJob {
			cancellable, cancelReason = false, "An AltMount job is still active."
		}
	default:
		cancellable, cancelReason = false, "Workflows that are acquiring, publishing, published or finished cannot be cancelled."
	}

	actions := []workflowAction{
		search,
		{Action: "discover", OperationID: "discoverWorkflow", Method: http.MethodPost, Path: path("/discover"), Effect: effectProvider, Available: discoverReason == "", Reason: discoverReason, Note: "Evidence-only rescan: finds new releases, never grabs."},
		{Action: "open_next_round", OperationID: "openAcquisitionRound", Method: http.MethodPost, Path: path("/acquisition-rounds"), Effect: effectProvider, Available: item.OpenNextRoundAllowed, Reason: nextRoundReason},
		{Action: "open_round_from_known", OperationID: "openKnownAcquisitionRound", Method: http.MethodPost, Path: path("/acquisition-rounds/from-known"), Effect: effectProvider, Available: knownReason == "", Reason: knownReason, Note: "Refused with no_known_eligible_release when no stored release is both eligible and untried."},
		{Action: "reconcile_altmount", OperationID: "reconcileAltMount", Method: http.MethodPost, Path: path("/reconcile-altmount"), Effect: effectWrite, Available: reconcile, Reason: unless(reconcile, "Only for needs_attention with an AltMount or inspection attention code.")},
		{Action: "reconfirm_plex", OperationID: "reconfirmPlex", Method: http.MethodPost, Path: path("/reconfirm-plex"), Effect: effectProvider, Available: plex, Reason: unless(plex, "Only after a Plex confirmation timed out (attention_code plex_timeout).")},
		{Action: "cancel", OperationID: "cancelWorkflow", Method: http.MethodPost, Path: path("/cancel"), Effect: effectWrite, Available: cancellable, Reason: cancelReason},
	}
	if item.AccessRejectedCandidateID != nil {
		actions = append(actions, workflowAction{Action: "retry_access_rejected_release", OperationID: "retryAccessRejectedRelease", Method: http.MethodPost, Path: path("/retry-access-rejected-release"), Effect: effectProvider, Available: true,
			Note: strings.TrimSpace(fmt.Sprintf("Retries %s from %s after the provider access problem was fixed.", item.AccessRejectedReleaseTitle, item.AccessRejectedIndexerName)),
			Body: map[string]any{"candidate_id": item.AccessRejectedCandidateID.String(), "revision": item.Revision}})
	}
	return actions
}
