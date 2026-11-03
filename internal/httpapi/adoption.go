package httpapi

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adoption"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listAdoptionBatches(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListAdoptionBatches(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load adoption batches")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) createAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	var input createAdoptionBatchInput
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, rootErr := uuid.Parse(input.RootID)
	profileID, profileErr := uuid.Parse(input.QualityProfileID)
	if rootErr != nil || profileErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "root and quality profile IDs are required")
		return
	}
	configuration, err := server.store.AdoptionConfiguration(request.Context(), rootID, profileID)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "enabled compatible root and profile were not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not validate adoption configuration")
		return
	}
	sourceRoot := filepath.Clean(strings.TrimSpace(input.SourceRoot))
	relative, relErr := filepath.Rel(configuration.Root.DestinationRoot, sourceRoot)
	if relErr != nil || !filepath.IsAbs(sourceRoot) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		writeError(writer, http.StatusUnprocessableEntity, "unsafe_source_root", "source root must be the selected library root or one of its subdirectories")
		return
	}
	entries, err := adoption.Scan(request.Context(), sourceRoot, 50000)
	if errors.Is(err, adoption.ErrScanLimit) {
		writeError(writer, http.StatusUnprocessableEntity, "scan_limit", "read-only discovery exceeded 50,000 files")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "scan_failed", "source root could not be scanned safely")
		return
	}
	batch, err := server.store.CreateAdoptionBatch(request.Context(), sourceRoot, configuration, input.MonitoringEnabled, input.SearchImmediately, entries, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "adoption_plan_failed", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/adoption/"+batch.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: batch})
}

func (server *Server) getAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch ID is invalid")
		return
	}
	item, err := server.store.AdoptionBatch(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load adoption batch")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) reviewAdoptionItem(writer http.ResponseWriter, request *http.Request) {
	batchID, batchErr := uuid.Parse(request.PathValue("id"))
	itemID, itemErr := uuid.Parse(request.PathValue("itemID"))
	if batchErr != nil || itemErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch or item ID is invalid")
		return
	}
	var input reviewAdoptionItemInput
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	mediaID, mediaErr := optionalUUID(input.MediaID)
	rootID, rootErr := optionalUUID(input.RootID)
	profileID, profileErr := optionalUUID(input.QualityProfileID)
	if mediaErr != nil || rootErr != nil || profileErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "review IDs are invalid")
		return
	}
	err := server.store.ReviewAdoptionItem(request.Context(), batchID, itemID, postgres.AdoptionReview{State: input.ReviewState, MediaID: mediaID, RootID: rootID, QualityProfileID: profileID, ProposedRelative: input.ProposedRelative, Variant: input.Variant, MonitoringEnabled: input.MonitoringEnabled, SearchImmediately: input.SearchImmediately}, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch or item was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "review_failed", err.Error())
		return
	}
	item, _ := server.store.AdoptionBatch(request.Context(), batchID)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) approveAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch ID is invalid")
		return
	}
	err = server.store.ApproveAdoptionBatch(request.Context(), id, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "approval_failed", err.Error())
		return
	}
	item, _ := server.store.AdoptionBatch(request.Context(), id)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) executeAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch ID is invalid")
		return
	}
	var input executeAdoptionBatchInput
	if err := decodeJSON(request, &input, 8<<10); err != nil || input.Confirmation != "EXECUTE REVIEWED ADOPTION" {
		writeError(writer, http.StatusBadRequest, "confirmation_required", "exact adoption execution confirmation is required")
		return
	}
	err = server.store.QueueAdoptionExecution(request.Context(), id, input.AdoptedReplacementConfirmation, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch was not found")
		return
	}
	if errors.Is(err, postgres.ErrAdoptedReplacementConfirmationRequired) {
		writeError(writer, http.StatusConflict, "replacement_confirmation_required", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "execution_failed", err.Error())
		return
	}
	item, _ := server.store.AdoptionBatch(request.Context(), id)
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: item})
}

func (server *Server) restoreAdoptedFiles(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "recycle entry ID is invalid")
		return
	}
	var input restoreAdoptedFilesInput
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if err := server.store.RequestAdoptedRestore(request.Context(), id, input.Confirmation, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "recycle entry was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "restore_not_confirmed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "adopted_restore_queued", "id": id.String()}})
}
