package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listIndexerUsage(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.IndexerUsages(request.Context(), time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load indexer usage")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]any{"count": len(items), "note": "Background and discovery searches may use at most background_query_limit of the shared query budget."}})
}

func (server *Server) systemQueue(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := server.store.QueueSnapshot(request.Context(), time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the background queue")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: snapshot, Meta: map[string]string{"order": "One event at a time: other events first, then searches, then discovery rescans; oldest first within each."}})
}

func (server *Server) listWorkflowSearches(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	runs, err := server.store.WorkflowSearchRuns(request.Context(), id, limit)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load search runs")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: runs, Meta: map[string]any{"count": len(runs)}})
}

func (server *Server) listWorkflowCandidates(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	verdict := request.URL.Query().Get("verdict")
	if verdict != "" && verdict != "eligible" && verdict != "held" && verdict != "rejected" {
		writeError(writer, http.StatusBadRequest, "invalid_verdict", "verdict must be eligible, held or rejected")
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.WorkflowCandidates(request.Context(), id, verdict, limit)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load candidates")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]any{"count": len(items)}})
}
