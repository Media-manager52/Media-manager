package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func (server *Server) listWorkflows(writer http.ResponseWriter, request *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_limit", "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	offset := 0
	if raw := strings.TrimSpace(request.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_offset", "offset must be zero or greater")
			return
		}
		offset = parsed
	}
	state := strings.TrimSpace(request.URL.Query().Get("state"))
	if state != "" && !validWorkflowState(state) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_state", "state must be a known workflow state")
		return
	}
	attention := false
	if raw := strings.TrimSpace(request.URL.Query().Get("attention")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_attention", "attention must be true or false")
			return
		}
		attention = parsed
	}
	activeOnly := request.URL.Query().Get("active") != "false"
	items, err := server.store.ListWorkflowSummariesPage(request.Context(), limit, offset, activeOnly, state, attention)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflows")
		return
	}
	views := make([]workflowView, 0, len(items))
	for _, item := range items {
		views = append(views, newWorkflowView(item))
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: views, Meta: map[string]any{"count": len(items), "limit": limit, "offset": offset, "state": state, "attention": attention, "active_only": activeOnly}})
}

func validWorkflowState(value string) bool {
	switch workflows.State(value) {
	case workflows.StateMonitored, workflows.StateWaiting, workflows.StateSearching,
		workflows.StateCandidateSelected, workflows.StateSubmitting, workflows.StateTransportUnknown,
		workflows.StateQueued, workflows.StateStreamingReady, workflows.StateImportComplete,
		workflows.StateVerifying, workflows.StatePublishing, workflows.StatePlexScanning,
		workflows.StatePlexConfirmed, workflows.StateNeedsAttention, workflows.StateHeld,
		workflows.StateBlocked, workflows.StateQuarantined, workflows.StateCancelled:
		return true
	default:
		return false
	}
}

func (server *Server) createMediaWorkflow(writer http.ResponseWriter, request *http.Request) {
	mediaID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	item, err := server.store.GetMedia(request.Context(), mediaID)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load media")
		return
	}
	var input createMediaWorkflowInput
	if err := decodeJSON(request, &input, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, rootErr := optionalUUID(input.RootID)
	profileID, profileErr := optionalUUID(input.QualityProfileID)
	clientID, clientErr := optionalUUID(input.DownloadClientID)
	if rootErr != nil || profileErr != nil || clientErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "configuration IDs are invalid")
		return
	}
	resolvedRoot, resolvedProfile, resolvedClient, err := server.store.ResolveMediaConfiguration(request.Context(), item.Kind, rootID, profileID, clientID)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "enabled defaults or overrides are missing")
		return
	}
	search := true
	if input.SearchImmediately != nil {
		search = *input.SearchImmediately
	}
	var season *postgres.ManualSeason
	if input.Season != nil {
		monitored, prefer := true, true
		if input.Season.Monitored != nil {
			monitored = *input.Season.Monitored
		}
		if input.Season.PreferSeasonPack != nil {
			prefer = *input.Season.PreferSeasonPack
		}
		if input.Season.SearchImmediately != nil {
			search = *input.Season.SearchImmediately
		}
		season = &postgres.ManualSeason{Number: input.Season.Number, Episodes: input.Season.Episodes, Monitored: monitored, SearchImmediately: search, PreferSeasonPack: prefer}
	}
	workflow, err := server.store.CreateManualWorkflowForExisting(request.Context(), item, resolvedRoot, resolvedProfile, resolvedClient, season, search, input.AdoptedReplacementConfirmation, "owner", time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusConflict, "workflow_create_failed", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/workflows/"+workflow.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: workflow})
}

func (server *Server) searchWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	var input searchWorkflowInput
	if request.ContentLength != 0 {
		if err := decodeJSON(request, &input, 8<<10); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
			return
		}
	}
	if err := server.store.RequestWorkflowSearch(request.Context(), id, input.AdoptedReplacementConfirmation, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "search_queued", "id": id.String()}})
}

func (server *Server) discoverWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	if err := server.store.RequestWorkflowDiscovery(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "discovery_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "discovery_queued", "id": id.String()}})
}

func (server *Server) openAcquisitionRound(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	if err := server.store.OpenNextAcquisitionRound(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "round_open_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "round_opened", "id": id.String()}})
}

func (server *Server) openKnownAcquisitionRound(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	if err := server.store.OpenKnownAcquisitionRound(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "round_open_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "round_opened_from_known", "id": id.String()}})
}

func (server *Server) reviveRejectedArchives(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	revived, err := server.store.RevivePreSubmissionRejections(request.Context(), id, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "revival_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]any{"status": "rejections_revived", "id": id.String(), "revived": revived}})
}

func (server *Server) relocateWorkflowLinks(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	var input relocateWorkflowLinksInput
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, parseErr := uuid.Parse(input.RootID)
	if input.RootID == "" || parseErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_root", "root_id must be a library root ID")
		return
	}
	moved, err := server.store.RelocateWorkflowLinks(request.Context(), id, rootID, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow or root was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "relocate_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]any{"status": "links_relocated", "id": id.String(), "moved": moved}})
}

func (server *Server) recoverAltMountSubmission(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	workflow, err := server.store.GetWorkflow(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflow")
		return
	}
	if workflow.State != workflows.StateSubmitting || workflow.GrabAttemptID == nil {
		writeError(writer, http.StatusConflict, "recovery_failed", "workflow does not have an unresolved AltMount submission")
		return
	}
	if err := server.store.MarkAltMountUnknown(request.Context(), workflow.ID, *workflow.GrabAttemptID, time.Now().UTC()); err != nil {
		writeError(writer, http.StatusConflict, "recovery_failed", "AltMount submission recovery could not be queued")
		return
	}
	workflow, err = server.store.GetWorkflow(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not reload workflow")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: workflow})
}

func (server *Server) reconcileAltMount(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	if err := server.store.RequestAltMountReconciliation(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow or current AltMount job was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "reconciliation_failed", "current AltMount job cannot be reconciled safely")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "reconciliation_queued", "id": id.String()}})
}

func (server *Server) reconfirmPlex(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	scanID, err := server.store.RequestPlexReconfirmation(request.Context(), id, "owner", time.Now().UTC())
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
	case errors.Is(err, postgres.ErrPlexReconfirmationNotTimedOut), errors.Is(err, postgres.ErrPlexReconfirmationUnverified), errors.Is(err, postgres.ErrPlexReconfirmationLimit):
		writeConflict(writer, err, "reconfirmation_rejected", err.Error())
	case err != nil:
		writeError(writer, http.StatusConflict, "reconfirmation_failed", "Plex confirmation cannot be retried safely")
	default:
		writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "plex_reconfirmation_queued", "id": id.String(), "scan_request_id": scanID.String()}})
	}
}

func (server *Server) reverifyRetainedOutput(writer http.ResponseWriter, request *http.Request) {
	workflowID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	var input reverifyRetainedOutputInput
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	jobID, err := uuid.Parse(input.JobID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_job_id", "AltMount job ID is invalid")
		return
	}
	if err := server.store.RequestRetainedSubtitleReverification(request.Context(), workflowID, jobID, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "retained subtitle verification was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "reverification_failed", "retained output cannot be reverified safely")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "reverification_queued", "workflow_id": workflowID.String(), "job_id": jobID.String()}})
}

func (server *Server) cancelWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	workflow, err := server.store.CancelWorkflowForOwner(request.Context(), id, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeConflict(writer, err, "cancel_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: workflow})
}

func (server *Server) getWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	workflow, err := server.store.GetWorkflow(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflow")
		return
	}
	// A workflow that exhausted its acquisition round stays durably in waiting
	// so the low-priority discovery rescan can continue, but it needs an
	// explicit owner action before another acquisition. Surface that as
	// needs_attention to owner-facing readers without changing the durable
	// waiting state that discovery depends on.
	summary, err := server.store.WorkflowSummary(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflow")
		return
	}
	// Actions are computed from the durable state before the owner-facing
	// state replaces it for display.
	view := workflowDetail{workflowView: newWorkflowView(summary), RootID: workflow.RootID.String(), QualityProfileID: workflow.QualityProfileID.String(), DownloadClientID: workflow.DownloadClientID.String(), SelectedReleaseID: workflow.SelectedRelease, GrabAttemptID: workflow.GrabAttemptID}
	view.State = workflow.OwnerAttentionState()
	view.Revision = workflow.Revision
	writer.Header().Set("ETag", `"`+strconv.FormatUint(workflow.Revision, 10)+`"`)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: view})
}

// workflowDetail is the single-workflow read: the list read model, the
// workflow's configuration references and its available actions.
type workflowDetail struct {
	workflowView
	RootID            string     `json:"root_id"`
	QualityProfileID  string     `json:"quality_profile_id"`
	DownloadClientID  string     `json:"download_client_id"`
	SelectedReleaseID *uuid.UUID `json:"selected_release_id,omitempty"`
	GrabAttemptID     *uuid.UUID `json:"grab_attempt_id,omitempty"`
}
