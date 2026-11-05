package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/plexlogin"
	"github.com/media-manager/media-manager/internal/postgres"
)

// "Log ind med Plex": the owner approves a Plex PIN login on app.plex.tv,
// Media Manager lists the account's servers with the addresses it can
// reach, and saves the chosen one as a Plex connection with the token
// sealed. The Plex token never leaves the API.

type plexLoginConnectInput struct {
	ServerID string `json:"server_id"`
	// URI is optional; it must be one of the server's reachable addresses.
	URI string `json:"uri,omitempty"`
	// IntegrationID, when set, updates that Plex connection instead of
	// creating one.
	IntegrationID string `json:"integration_id,omitempty"`
}

type plexLoginConnected struct {
	Integration postgres.Integration `json:"integration"`
	Created     bool                 `json:"created"`
	Check       connectionCheck      `json:"check"`
}

func (server *Server) plexLoginAvailable(writer http.ResponseWriter) bool {
	if server.config.PlexLogin == nil {
		writeError(writer, http.StatusServiceUnavailable, "plex_login_unavailable", "Plex login is unavailable")
		return false
	}
	return true
}

func (server *Server) startPlexLogin(writer http.ResponseWriter, request *http.Request) {
	if !server.plexLoginAvailable(writer) {
		return
	}
	if server.config.IntegrationSecrets == nil {
		writeError(writer, http.StatusServiceUnavailable, "secret_storage_unavailable", integrationsecrets.ErrUnavailable.Error())
		return
	}
	started, err := server.config.PlexLogin.Start(request.Context())
	if err != nil {
		writeError(writer, http.StatusBadGateway, "plex_login_failed", "Plex could not start a login: "+err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: started})
}

func (server *Server) pollPlexLogin(writer http.ResponseWriter, request *http.Request) {
	if !server.plexLoginAvailable(writer) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	status, err := server.config.PlexLogin.Poll(ctx, request.PathValue("id"))
	if errors.Is(err, plexlogin.ErrUnknownLogin) {
		writeError(writer, http.StatusNotFound, "plex_login_expired", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadGateway, "plex_login_failed", "Plex did not answer: "+err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: status})
}

func (server *Server) connectPlexLogin(writer http.ResponseWriter, request *http.Request) {
	if !server.plexLoginAvailable(writer) {
		return
	}
	if server.config.IntegrationSecrets == nil {
		writeError(writer, http.StatusServiceUnavailable, "secret_storage_unavailable", integrationsecrets.ErrUnavailable.Error())
		return
	}
	var input plexLoginConnectInput
	if err := decodeJSON(request, &input, 8<<10); err != nil || strings.TrimSpace(input.ServerID) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request", "send {\"server_id\": \"...\"}")
		return
	}
	ctx := request.Context()
	var existing *postgres.Integration
	if strings.TrimSpace(input.IntegrationID) != "" {
		id, err := uuid.Parse(input.IntegrationID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
			return
		}
		integration, err := server.store.Integration(ctx, id)
		if errors.Is(err, postgres.ErrNotFound) || (err == nil && integration.Kind != "plex") {
			writeError(writer, http.StatusNotFound, "not_found", "Plex connection was not found")
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "database_error", "could not load the connection")
			return
		}
		existing = &integration
	}
	choice, err := server.config.PlexLogin.Choose(request.PathValue("id"), input.ServerID, input.URI)
	switch {
	case errors.Is(err, plexlogin.ErrUnknownLogin):
		writeError(writer, http.StatusNotFound, "plex_login_expired", err.Error())
		return
	case errors.Is(err, plexlogin.ErrNotApproved):
		writeError(writer, http.StatusConflict, "plex_login_waiting", err.Error())
		return
	case err != nil:
		writeError(writer, http.StatusBadRequest, "plex_server_unknown", err.Error())
		return
	}
	defer clear([]byte(choice.Token))

	now := time.Now().UTC()
	integrations, err := server.store.ListIntegrations(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load connections")
		return
	}
	if existing == nil {
		// The same address already saved: refresh that connection's token
		// instead of adding a duplicate.
		for index := range integrations {
			if integrations[index].Kind == "plex" && plexBaseURL(integrations[index]) == choice.URI {
				existing = &integrations[index]
				break
			}
		}
	}
	created := false
	var integration postgres.Integration
	if existing != nil {
		baseURL, allowHTTP, enabled := choice.URI, choice.AllowHTTP, true
		integration, err = server.store.UpdateIntegrationConnection(ctx, existing.ID, postgres.IntegrationConnectionPatch{BaseURL: &baseURL, AllowHTTP: &allowHTTP, Enabled: &enabled}, now)
		if err != nil {
			writeConflict(writer, err, "settings_update_failed", "the Plex connection could not be changed")
			return
		}
	} else {
		hasDefault := false
		for _, item := range integrations {
			if item.Kind == "plex" && item.Default && item.Enabled {
				hasDefault = true
			}
		}
		config, _ := json.Marshal(map[string]any{"base_url": choice.URI, "allow_http": choice.AllowHTTP})
		name := strings.TrimSpace(choice.ServerName)
		if name == "" {
			name = "Plex"
		}
		integration, err = server.store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: name, Enabled: true, Default: !hasDefault, Config: config})
		if err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_integration", err.Error())
			return
		}
		created = true
	}
	if err := server.config.IntegrationSecrets.Save(ctx, integration.ID, choice.Token, now); err != nil {
		writeError(writer, http.StatusInternalServerError, "secret_save_failed", "the Plex token could not be saved")
		return
	}
	integration, err = server.store.Integration(ctx, integration.ID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the connection")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: plexLoginConnected{Integration: integration, Created: created, Check: server.checkConnection(ctx, integration)}})
}

func plexBaseURL(integration postgres.Integration) string {
	var config struct {
		BaseURL string `json:"base_url"`
	}
	_ = json.Unmarshal(integration.Config, &config)
	return strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
}
