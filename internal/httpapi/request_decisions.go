package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/postgres"
)

// decideRequest sends the owner's approve or decline decision to Seerr for a
// request that is still pending there, then queues the normal intake
// reconcile. Nothing is admitted locally: intake re-reads the request from
// Seerr exactly as it does after a webhook.
func (server *Server) decideRequest(decision seerr.RequestDecision) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if server.config.MetadataProvider == nil || server.config.SeerrInstanceID == "" {
			writeError(writer, http.StatusServiceUnavailable, "seerr_unavailable", "Seerr is not configured")
			return
		}
		id := strings.TrimSpace(request.PathValue("id"))
		instanceID, requestID := server.config.SeerrInstanceID, id
		if qualifiedInstance, qualifiedRequest, ok := splitSeerrRequestID(id); ok {
			instanceID, requestID = qualifiedInstance, qualifiedRequest
		}
		// Only the configured Seerr client can act, so only its requests qualify.
		if instanceID != server.config.SeerrInstanceID {
			writeError(writer, http.StatusNotFound, "not_found", "request was not found")
			return
		}
		numeric, err := strconv.ParseInt(requestID, 10, 64)
		if err != nil || numeric <= 0 {
			writeError(writer, http.StatusBadRequest, "invalid_request", "request id must be a positive Seerr request number")
			return
		}
		pending, err := server.store.SeerrRequestPending(request.Context(), instanceID, requestID)
		if errors.Is(err, postgres.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "not_found", "request was not found")
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "database_error", "could not load request")
			return
		}
		if !pending {
			writeError(writer, http.StatusConflict, "not_pending", "this request is not waiting for approval in Seerr")
			return
		}
		result, err := server.config.MetadataProvider.DecideRequest(request.Context(), numeric, decision)
		if errors.Is(err, seerr.ErrRequestNotFound) {
			writeError(writer, http.StatusNotFound, "not_found", "Seerr no longer has this request")
			return
		}
		if err != nil {
			writeError(writer, http.StatusBadGateway, "seerr_error", "Seerr did not confirm the decision")
			return
		}
		if err := server.store.RecordSeerrOwnerDecision(request.Context(), instanceID, requestID, string(decision), time.Now().UTC()); err != nil {
			writeError(writer, http.StatusInternalServerError, "database_error", "Seerr accepted the decision, but it could not be recorded; intake will still see it on the next Seerr poll")
			return
		}
		status := "approved"
		if decision == seerr.DecisionDecline {
			status = "declined"
		}
		writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]any{"status": status, "request_id": requestID, "seerr_status": result.Status}})
	}
}

// reconfirmTimedOutPlex re-queues exact-path Plex confirmation for every
// publication whose previous confirmation timed out. It never asks Plex for a
// library-wide scan; each workflow keeps its own verified-links check and
// retry limit.
func (server *Server) reconfirmTimedOutPlex(writer http.ResponseWriter, request *http.Request) {
	result, err := server.store.ReconfirmTimedOutPlex(request.Context(), "owner", time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not re-check Plex")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: result})
}
