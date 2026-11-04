package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
)

func (server *Server) seerrWebhook(writer http.ResponseWriter, request *http.Request) {
	if !server.config.SeerrEnabled || len(server.config.SeerrWebhookSecret) < 32 || server.config.SeerrInstanceID == "" {
		writeError(writer, http.StatusServiceUnavailable, "not_configured", "Seerr webhook intake is not configured")
		return
	}
	if err := seerr.AuthenticateWebhook(request.Header.Get("Authorization"), server.config.SeerrWebhookSecret); err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "webhook authorization failed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, (256<<10)+1))
	if err != nil || len(body) > 256<<10 {
		writeError(writer, http.StatusRequestEntityTooLarge, "payload_too_large", "webhook body exceeds 256 KiB")
		return
	}
	// Seerr's "test" button (and the website's webhook setup) sends a test
	// notification. It proves the address and secret work and starts nothing.
	var probe struct {
		NotificationType string `json:"notification_type"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.NotificationType == seerr.TestNotificationType {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	event, err := seerr.ParseWebhook(body)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_event", "webhook payload is invalid")
		return
	}
	created, err := server.store.RecordSeerrDelivery(request.Context(), server.config.SeerrInstanceID, event.RequestID.String(), request.Header.Get("X-Seerr-Event-ID"), body, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "intake_failed", "webhook could not be stored")
		return
	}
	if !created {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "accepted", "request_id": event.RequestID.String()}})
}
