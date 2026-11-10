package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listIntegrations(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListIntegrations(request.Context())
	if err != nil {
		writeError(writer, 500, "database_error", "could not load integrations")
		return
	}
	writeJSON(writer, 200, dataEnvelope{Data: items})
}

func (server *Server) createIntegration(writer http.ResponseWriter, request *http.Request) {
	var item postgres.Integration
	if err := decodeJSON(request, &item, 256<<10); err != nil {
		writeError(writer, 400, "invalid_request", "request body is invalid")
		return
	}
	created, err := server.store.CreateIntegration(request.Context(), item)
	if err != nil {
		if errors.Is(err, postgres.ErrDuplicateIndexerURL) {
			writeError(writer, http.StatusConflict, "duplicate_indexer_url", err.Error())
			return
		}
		writeError(writer, 422, "invalid_integration", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]string{"warning": "setting saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) commissionIntegration(writer http.ResponseWriter, request *http.Request) {
	if server.config.CommissionNewznab == nil {
		writeError(writer, http.StatusServiceUnavailable, "commissioning_unavailable", "Newznab commissioning is unavailable")
		return
	}
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	integration, err := server.config.CommissionNewznab(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "integration was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadGateway, "commission_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration})
}

func (server *Server) listAltMountRepairs(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListAltMountRepairs(request.Context(), 500)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load repairs")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) requestAltMountReplacement(writer http.ResponseWriter, request *http.Request) {
	var input requestReplacementInput
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	path := strings.Trim(strings.TrimSpace(input.AltMountFilePath), "/")
	if _, err := library.ValidateRelative(path); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_path", "altmount_file_path must be a relative path inside the AltMount mount")
		return
	}
	var fileID *uuid.UUID
	if input.FileID != "" {
		parsed, err := uuid.Parse(input.FileID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_id", "file ID is invalid")
			return
		}
		fileID = &parsed
	}
	outcome, err := server.store.RequestAltMountReplacementForFile(request.Context(), path, fileID, time.Now().UTC())
	switch {
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "database_error", "could not open the replacement")
	case outcome == postgres.RepairUnmanaged:
		writeError(writer, http.StatusNotFound, "not_published", "no current publication uses this AltMount file")
	case outcome == postgres.RepairBusy:
		writeError(writer, http.StatusConflict, "publication_in_flight", "the workflow that published this file is still running; try again when it finishes")
	default:
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: replacementResult{Outcome: outcome}})
	}
}

func (server *Server) listLibraryRoots(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListLibraryRoots(request.Context())
	if err != nil {
		writeError(writer, 500, "database_error", "could not load library roots")
		return
	}
	writeJSON(writer, 200, dataEnvelope{Data: items})
}

func (server *Server) createLibraryRoot(writer http.ResponseWriter, request *http.Request) {
	var item postgres.LibraryRoot
	if err := decodeJSON(request, &item, 256<<10); err != nil {
		writeError(writer, 400, "invalid_request", "request body is invalid")
		return
	}
	created, err := server.store.CreateLibraryRoot(request.Context(), item)
	if err != nil {
		writeError(writer, 422, "invalid_library_root", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]string{"warning": "setting saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) updateLibraryRoot(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "library root ID is invalid")
		return
	}
	var input postgres.LibraryRootPatch
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "send a JSON object with name, is_default and/or enabled")
		return
	}
	root, err := server.store.UpdateLibraryRoot(request.Context(), id, input, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "library root was not found")
		return
	}
	var refusal postgres.ActionRefusal
	if errors.As(err, &refusal) && (strings.HasPrefix(refusal.Code, "invalid_") || refusal.Code == "empty_patch") {
		writeErrorHint(writer, http.StatusBadRequest, refusal.Code, refusal.Message, refusal.Hint)
		return
	}
	if err != nil {
		writeConflict(writer, err, "library_root_update_failed", "the library root could not be changed")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: root})
}

func (server *Server) listQualityProfiles(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListQualityProfiles(request.Context())
	if err != nil {
		writeError(writer, 500, "database_error", "could not load quality profiles")
		return
	}
	writeJSON(writer, 200, dataEnvelope{Data: items})
}

func (server *Server) createQualityProfile(writer http.ResponseWriter, request *http.Request) {
	var item postgres.QualityProfile
	if err := decodeJSON(request, &item, 512<<10); err != nil {
		writeError(writer, 400, "invalid_request", "request body is invalid")
		return
	}
	created, err := server.store.CreateQualityProfile(request.Context(), item)
	if err != nil {
		writeError(writer, 422, "invalid_quality_profile", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]string{"warning": "setting saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) updateQualityProfile(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "quality profile ID is invalid")
		return
	}
	var item postgres.QualityProfile
	if err := decodeJSON(request, &item, 512<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	item.ID = id
	// policy_version is the version the editor read. It is required: an edit
	// without it cannot be told apart from a stale one, and silently replacing a
	// newer policy (including its required rules) is the failure this prevents.
	if item.PolicyVersion == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "current policy_version is required")
		return
	}
	updated, err := server.store.UpdateQualityProfile(request.Context(), item, item.PolicyVersion)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "quality profile was not found")
		return
	}
	if errors.Is(err, postgres.ErrConcurrentModification) {
		writeError(writer, http.StatusConflict, "quality_profile_changed", "the quality profile changed; reload it and retry")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_quality_profile", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: updated})
}

func (server *Server) deleteQualityProfile(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "quality profile ID is invalid")
		return
	}
	var replaceWith *uuid.UUID
	if raw := request.URL.Query().Get("replace_with"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_id", "replace_with is not a quality profile ID")
			return
		}
		replaceWith = &parsed
	}
	result, err := server.store.DeleteQualityProfile(request.Context(), id, replaceWith, "owner", time.Now().UTC())
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "quality profile was not found")
	case errors.Is(err, postgres.ErrQualityProfileIsDefault):
		writeError(writer, http.StatusConflict, "quality_profile_is_default", "the default quality profile cannot be deleted; make another profile the default first")
	case errors.Is(err, postgres.ErrQualityProfileInUse):
		writeErrorHint(writer, http.StatusConflict, "quality_profile_in_use", "the quality profile is used by titles, files or a Seerr route", "Send replace_with=<id of an enabled profile of the same media kind> to move them to that profile, or disable the profile instead.")
	case errors.Is(err, postgres.ErrQualityProfileReplacementInvalid):
		writeError(writer, http.StatusUnprocessableEntity, "quality_profile_replacement_invalid", "the replacement must be another enabled quality profile of the same media kind")
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not delete the quality profile")
	default:
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"status": "removed", "moved": result}})
	}
}

func (server *Server) deleteLibraryRoot(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "library root ID is invalid")
		return
	}
	server.writeSettingsDelete(writer, server.store.DeleteLibraryRoot(request.Context(), id, "owner", time.Now().UTC()), "library root")
}

func (server *Server) deleteIntegration(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	server.writeSettingsDelete(writer, server.store.DeleteIntegration(request.Context(), id, "owner", time.Now().UTC()), "integration")
}

func (server *Server) writeSettingsDelete(writer http.ResponseWriter, err error, what string) {
	var refusal postgres.ActionRefusal
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", what+" was not found")
	case errors.As(err, &refusal):
		writeErrorHint(writer, http.StatusConflict, refusal.Code, refusal.Message, refusal.Hint)
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not delete the "+what)
	default:
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]string{"status": "removed"}})
	}
}

func (server *Server) applyQualityProfilePolicy(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "quality profile ID is invalid")
		return
	}
	result, err := server.store.ApplyQualityProfilePolicy(request.Context(), id, "owner", time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "quality profile was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "policy_application_failed", "workflows changed while the policy was applied; retry the request")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: result})
}

func (server *Server) listSubtitleVerifications(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	var workflowID *uuid.UUID
	if value := request.URL.Query().Get("workflow_id"); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
			return
		}
		workflowID = &parsed
	}
	items, err := server.store.ListSubtitleVerifications(request.Context(), limit, workflowID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load subtitle verifications")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}
