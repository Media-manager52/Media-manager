package httpapi

import (
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) reinspectRetainedMovie(writer http.ResponseWriter, request *http.Request) {
	workflowID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	var input struct {
		JobID string `json:"job_id"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	jobID, err := uuid.Parse(input.JobID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_job_id", "AltMount job ID is invalid")
		return
	}
	if err := server.store.RequestRetainedMovieInspection(request.Context(), workflowID, jobID, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "retained movie job was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "reinspection_failed", "retained movie cannot be inspected safely")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "movie_reinspection_queued", "workflow_id": workflowID.String(), "job_id": jobID.String()}})
}
