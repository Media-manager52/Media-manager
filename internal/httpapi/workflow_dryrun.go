package httpapi

import (
	"errors"
	"net/http"
	"uuid"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/postgres"
)

// dryRunWorkflowCandidates is intentionally a read effect. It replays the
// stored observations through the acquisition evaluator in memory and never
// creates a search, download, AltMount job, publication or exclusion.
func (server *Server) dryRunWorkflowCandidates(writer http.ResponseWriter, request *http.Request) {
	workflowID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	result, err := acquisition.DryRunWorkflow(request.Context(), server.store, workflowID, 200)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "dry_run_failed", "the read-only dry run could not be completed")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: result})
}
