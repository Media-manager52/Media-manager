package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listWanted(writer http.ResponseWriter, request *http.Request) {
	limit := 300
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
	items, err := server.store.ListWantedPage(request.Context(), limit, offset)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load wanted media")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]any{"count": len(items), "limit": limit, "offset": offset}})
}

func (server *Server) listRequests(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
	items, err := server.store.ListSeerrRequests(request.Context(), "", limit, offset)
	if err != nil {
		writeDatabaseError(writer, err, "could not load requests")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]any{
		"limit":            limit,
		"offset":           offset,
		"count":            len(items),
		"seerr_configured": server.config.SeerrInstanceID != "",
	}})
}

func (server *Server) getRequest(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimSpace(request.PathValue("id"))
	if id == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request id is required")
		return
	}
	instanceID, requestID := server.config.SeerrInstanceID, id
	if qualifiedInstance, qualifiedRequest, ok := splitSeerrRequestID(id); ok {
		instanceID, requestID = qualifiedInstance, qualifiedRequest
	}
	if instanceID == "" {
		writeError(writer, http.StatusNotFound, "not_found", "request was not found")
		return
	}
	detail, err := server.store.SeerrRequest(request.Context(), instanceID, requestID)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "request was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load request")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: detail})
}

func (server *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListTaskStatuses(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load worker tasks")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"event_types": items,
		"recurring": []map[string]any{
			{"name": "Recycle eligibility", "schedule": "hourly", "automatic": true},
			{"name": "Seerr reconciliation", "schedule": "every 5 minutes", "automatic": server.config.SeerrEnabled},
		},
	}})
}

func (server *Server) listActivity(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListActivity(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load activity")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) listManagedFiles(writer http.ResponseWriter, request *http.Request) {
	limit := 300
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
	items, err := server.store.ListManagedFilesPage(request.Context(), limit, offset)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load managed files")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items), "limit": limit, "offset": offset}})
}

func (server *Server) getManagedFile(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "managed file ID is invalid")
		return
	}
	item, err := server.store.ManagedFile(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "managed file was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load managed file")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) listReleaseCandidates(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListReleaseCandidates(request.Context(), limit)
	if err != nil {
		writeDatabaseError(writer, err, "could not load release intelligence")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}
