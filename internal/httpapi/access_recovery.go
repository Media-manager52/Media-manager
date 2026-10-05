package httpapi

import (
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) retryAccessRejectedRelease(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	var input retryAccessRejectedReleaseInput
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	candidate, err := uuid.Parse(input.CandidateID)
	if err != nil || input.Revision == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "candidate ID and current workflow revision are required")
		return
	}
	if err := server.store.RetryAccessRejectedRelease(request.Context(), id, candidate, input.Revision, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeConflict(writer, err, "access_retry_failed", "The workflow changed or this release is no longer eligible for access recovery. Refresh and review its current state.")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "retrieval_queued", "id": id.String()}})
}
