package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/postgres"
)

// defaultSeerrWebhookURL is the address Seerr reaches the API at when both run
// on the shared Docker network of the standard install.
const defaultSeerrWebhookURL = "http://media-manager-api:8080/api/v1/integrations/seerr/webhook"

type seerrWebhookStatus struct {
	// IntakeRunning is true when this API takes Seerr requests from this
	// connection; only then can Seerr's webhook be set up from here.
	IntakeRunning bool                           `json:"intake_running"`
	Webhook       *acquisition.SeerrWebhookState `json:"webhook,omitempty"`
	Detail        string                         `json:"detail,omitempty"`
	SuggestedURL  string                         `json:"suggested_url"`
}

type configureSeerrWebhookInput struct {
	WebhookURL string `json:"webhook_url"`
}

func (server *Server) seerrConnection(writer http.ResponseWriter, request *http.Request) (postgres.Integration, bool) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return postgres.Integration{}, false
	}
	integration, err := server.store.Integration(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) || (err == nil && integration.Kind != "seerr") {
		writeError(writer, http.StatusNotFound, "not_found", "Seerr integration was not found")
		return postgres.Integration{}, false
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the integration")
		return postgres.Integration{}, false
	}
	return integration, true
}

func (server *Server) intakeRunsFor(integration postgres.Integration) bool {
	return server.config.SeerrEnabled && len(server.config.SeerrWebhookSecret) >= 32 && server.config.SeerrInstanceID == integration.ID.String()
}

func (server *Server) getSeerrWebhook(writer http.ResponseWriter, request *http.Request) {
	integration, ok := server.seerrConnection(writer, request)
	if !ok {
		return
	}
	status := seerrWebhookStatus{IntakeRunning: server.intakeRunsFor(integration), SuggestedURL: defaultSeerrWebhookURL}
	secret := ""
	if status.IntakeRunning {
		secret = server.config.SeerrWebhookSecret
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	state, err := acquisition.SeerrWebhook(ctx, integration, server.resolveSecret, secret)
	if err != nil {
		status.Detail = safeConnectionError(err)
	} else {
		status.Webhook = &state
		if state.URL != "" {
			status.SuggestedURL = state.URL
		}
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: status})
}

func (server *Server) configureSeerrWebhook(writer http.ResponseWriter, request *http.Request) {
	integration, ok := server.seerrConnection(writer, request)
	if !ok {
		return
	}
	var input configureSeerrWebhookInput
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !server.intakeRunsFor(integration) {
		writeErrorHint(writer, http.StatusConflict, "intake_not_running", "this API does not take requests from this Seerr connection yet", "Restart the Media Manager API once after saving the Seerr connection, then set up the webhook.")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 45*time.Second)
	defer cancel()
	state, err := acquisition.ConfigureSeerrWebhook(ctx, integration, server.resolveSecret, server.config.SeerrWebhookSecret, input.WebhookURL)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "seerr_webhook_failed", safeConnectionError(err))
		return
	}
	_ = server.store.RecordSeerrWebhookConfigured(request.Context(), integration.ID, state.URL, time.Now().UTC())
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: seerrWebhookStatus{IntakeRunning: true, Webhook: &state, SuggestedURL: state.URL}})
}
