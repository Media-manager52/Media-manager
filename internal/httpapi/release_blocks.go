package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listReleaseBlocks(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListReleaseBlocks(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load release blocks")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) createReleaseBlock(writer http.ResponseWriter, request *http.Request) {
	var input createReleaseBlockInput
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	candidateID, err := uuid.Parse(input.CandidateID)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_candidate", "candidate ID is invalid")
		return
	}
	var expiresAt *time.Time
	if strings.TrimSpace(input.ExpiresAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, input.ExpiresAt)
		if parseErr != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_expiry", "expiry must be an RFC 3339 timestamp")
			return
		}
		expiresAt = &parsed
	} else if input.RetentionDays != 0 {
		if input.RetentionDays != 30 && input.RetentionDays != 90 {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_expiry", "retention must be 30 days, 90 days, or manual")
			return
		}
		value := time.Now().UTC().Add(time.Duration(input.RetentionDays) * 24 * time.Hour)
		expiresAt = &value
	}
	item, err := server.store.CreateReleaseBlockFromCandidate(request.Context(), candidateID, input.Scope, input.Reason, expiresAt, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "release candidate was not found")
		return
	}
	if errors.Is(err, postgres.ErrReleaseBlockExists) {
		writeError(writer, http.StatusConflict, "already_blocked", "this release scope is already blocked")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_block", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/blocklist/"+item.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: item})
}

func (server *Server) deleteReleaseBlock(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "release block ID is invalid")
		return
	}
	if err := server.store.DeleteReleaseBlock(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "release block was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not remove release block")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
