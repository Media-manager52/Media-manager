package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/integrationsecrets"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

// The setup surface lets the owner connect Media Manager from the website:
// store a connection's secret encrypted, edit Plex/AltMount/Seerr connections,
// check that a connection works, pick a Plex library for a new library root,
// and see what is still missing.

type saveSecretInput struct {
	Value string `json:"value"`
}

type connectionCheck struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// SetupStep is one item of the website's "Kom i gang" checklist.
type SetupStep struct {
	ID       string `json:"id"`
	Done     bool   `json:"done"`
	Optional bool   `json:"optional"`
	Detail   string `json:"detail"`
}

type setupStatus struct {
	Complete             bool        `json:"complete"`
	SecretStorageEnabled bool        `json:"secret_storage_enabled"`
	ServarrEnabled       bool        `json:"servarr_enabled"`
	SeerrIntakeEnabled   bool        `json:"seerr_intake_enabled"`
	Steps                []SetupStep `json:"steps"`
}

func (server *Server) saveIntegrationSecret(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	if server.config.IntegrationSecrets == nil {
		writeError(writer, http.StatusServiceUnavailable, "secret_storage_unavailable", integrationsecrets.ErrUnavailable.Error())
		return
	}
	var input saveSecretInput
	if err := decodeJSON(request, &input, 16<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "send {\"value\": \"...\"}")
		return
	}
	err = server.config.IntegrationSecrets.Save(request.Context(), id, input.Value, time.Now().UTC())
	clear([]byte(input.Value))
	var refusal postgres.ActionRefusal
	switch {
	case errors.Is(err, integrationsecrets.ErrInvalidSecret):
		writeError(writer, http.StatusBadRequest, "invalid_secret", err.Error())
		return
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "integration was not found")
		return
	case errors.As(err, &refusal):
		writeErrorHint(writer, http.StatusBadRequest, refusal.Code, refusal.Message, refusal.Hint)
		return
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "secret_save_failed", "the secret could not be saved")
		return
	}
	integration, err := server.store.Integration(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the integration")
		return
	}
	if integration.Kind == "tmdb" {
		server.tmdbChanged()
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration})
}

func (server *Server) updateIntegrationConnection(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	var input postgres.IntegrationConnectionPatch
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	integration, err := server.store.UpdateIntegrationConnection(request.Context(), id, input, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "integration was not found")
		return
	}
	var refusal postgres.ActionRefusal
	if errors.As(err, &refusal) {
		writeErrorHint(writer, http.StatusBadRequest, refusal.Code, refusal.Message, refusal.Hint)
		return
	}
	if err != nil {
		writeConflict(writer, err, "settings_update_failed", "the connection could not be changed")
		return
	}
	if integration.Kind == "tmdb" {
		server.tmdbChanged()
	}
	requeued, _ := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

// resolveSecret resolves a secret_ref the same way the workers do.
func (server *Server) resolveSecret(reference string) (string, error) {
	if server.config.ResolveSecret == nil {
		return "", errors.New("secret resolution is unavailable")
	}
	return server.config.ResolveSecret(reference)
}

func (server *Server) checkConnection(ctx context.Context, integration postgres.Integration) connectionCheck {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch integration.Kind {
	case "plex":
		libraries, err := acquisition.PlexLibraries(ctx, integration, server.resolveSecret)
		if err != nil {
			return connectionCheck{Detail: safeConnectionError(err)}
		}
		return connectionCheck{OK: true, Detail: "Plex answered and shows " + strconv.Itoa(len(libraries)) + " libraries"}
	case "altmount":
		if err := acquisition.CheckAltMount(ctx, integration, server.resolveSecret, nil); err != nil {
			return connectionCheck{Detail: safeConnectionError(err)}
		}
		return connectionCheck{OK: true, Detail: "AltMount accepted the login and its files are visible"}
	case "seerr":
		if err := acquisition.CheckSeerr(ctx, integration, server.resolveSecret); err != nil {
			return connectionCheck{Detail: safeConnectionError(err)}
		}
		return connectionCheck{OK: true, Detail: "Seerr accepted the API key"}
	case "tmdb":
		token, err := server.resolveSecret(integration.SecretRef)
		if err != nil || strings.TrimSpace(token) == "" {
			return connectionCheck{Detail: "save the TMDB read access token first"}
		}
		if err := metadataservice.CheckTMDBToken(ctx, token); err != nil {
			return connectionCheck{Detail: "TMDB did not accept the token; use the long \"API Read Access Token\", not the short API key"}
		}
		server.tmdbChanged()
		return connectionCheck{OK: true, Detail: "TMDB accepted the token; title search uses it"}
	default:
		return connectionCheck{Detail: "an indexer is checked by commissioning it"}
	}
}

func (server *Server) tmdbChanged() {
	if server.config.TMDBChanged != nil {
		server.config.TMDBChanged()
	}
}

// metadataProviderStates reports each metadata provider; TMDB is live because
// its token can be saved on the website after start.
func (server *Server) metadataProviderStates(ctx context.Context) map[string]string {
	states := make(map[string]string, len(server.config.MetadataProviderStates)+1)
	for name, state := range server.config.MetadataProviderStates {
		states[name] = state
	}
	if server.config.TMDBDiscovery != nil && server.config.TMDBDiscovery.Configured(ctx) {
		states["tmdb"] = "direct_configured"
	} else if server.config.MetadataProvider != nil {
		states["tmdb"] = "seerr_fallback"
	} else {
		states["tmdb"] = "not_configured"
	}
	return states
}

// safeConnectionError keeps a connection error useful without echoing a URL
// that could carry a credential.
func safeConnectionError(err error) string {
	message := err.Error()
	if index := strings.Index(message, "://"); index >= 0 {
		return "the service could not be reached"
	}
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}

func (server *Server) checkIntegration(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	integration, err := server.store.Integration(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "integration was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the integration")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: server.checkConnection(request.Context(), integration)})
}

func (server *Server) listPlexLibraries(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	integration, err := server.store.Integration(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) || (err == nil && integration.Kind != "plex") {
		writeError(writer, http.StatusNotFound, "not_found", "Plex integration was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the integration")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	libraries, err := acquisition.PlexLibraries(ctx, integration, server.resolveSecret)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "plex_unavailable", safeConnectionError(err))
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: libraries})
}

func (server *Server) setupStatus(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	integrations, err := server.store.ListIntegrations(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load integrations")
		return
	}
	roots, err := server.store.ListLibraryRoots(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load library roots")
		return
	}
	profiles, err := server.store.ListQualityProfiles(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load quality profiles")
		return
	}
	status := setupStatus{
		SecretStorageEnabled: server.config.IntegrationSecrets != nil,
		ServarrEnabled:       strings.TrimSpace(server.config.ServarrAPIKey) != "",
		SeerrIntakeEnabled:   server.config.SeerrEnabled,
	}
	defaultConnection := func(kind string) (postgres.Integration, bool) {
		for _, integration := range integrations {
			if integration.Kind == kind && integration.Enabled && integration.Default {
				return integration, true
			}
		}
		return postgres.Integration{}, false
	}
	plexStep := SetupStep{ID: "plex", Detail: "Add Plex and its token"}
	if integration, ok := defaultConnection("plex"); ok {
		plexStep.Done = strings.TrimSpace(integration.SecretRef) != ""
		plexStep.Detail = integration.Name
		if !plexStep.Done {
			plexStep.Detail = "Save the Plex token"
		}
	}
	altMountStep := SetupStep{ID: "altmount", Detail: "Add AltMount"}
	if integration, ok := defaultConnection("altmount"); ok {
		altMountStep.Done, altMountStep.Detail = true, integration.Name
	}
	indexerStep := SetupStep{ID: "indexer", Detail: "Add an indexer and commission it"}
	commissioned := 0
	for _, integration := range integrations {
		var config struct {
			ModeVerified bool `json:"mode_verified"`
		}
		_ = json.Unmarshal(integration.Config, &config)
		if (integration.Kind == "newznab" || integration.Kind == "oldboys") && integration.Enabled && config.ModeVerified {
			commissioned++
		}
	}
	if commissioned > 0 {
		indexerStep.Done, indexerStep.Detail = true, strconv.Itoa(commissioned)+" commissioned"
	}
	rootStep := func(kind catalog.MediaKind, id string) SetupStep {
		step := SetupStep{ID: id, Detail: "Add a library folder and make it the default"}
		for _, root := range roots {
			if root.MediaKind == kind && root.Enabled && root.Default && root.PlexLibraryID != "" {
				step.Done, step.Detail = true, root.Name
			}
		}
		return step
	}
	profileStep := func(kind catalog.MediaKind, id string) SetupStep {
		step := SetupStep{ID: id, Detail: "Choose a default quality profile"}
		for _, profile := range profiles {
			if profile.MediaKind == kind && profile.Enabled && profile.Default {
				step.Done, step.Detail = true, profile.Name
			}
		}
		return step
	}
	// TMDB powers title search, posters and descriptions. Seerr's TMDB proxy
	// covers search, so the step is optional only while Seerr is connected.
	metadataStep := SetupStep{ID: "metadata", Optional: server.config.MetadataProvider != nil, Detail: "Add TMDB under Connections for title search, posters and descriptions"}
	switch server.metadataProviderStates(ctx)["tmdb"] {
	case "direct_configured":
		metadataStep.Done, metadataStep.Detail = true, "TMDB"
	case "seerr_fallback":
		metadataStep.Done, metadataStep.Detail = true, "TMDB via Seerr"
	}
	seerrStep := SetupStep{ID: "seerr", Optional: true, Detail: "Connect Seerr to take requests from it"}
	if integration, ok := defaultConnection("seerr"); ok {
		seerrStep.Detail = integration.Name
		seerrStep.Done = server.config.SeerrEnabled
		if !seerrStep.Done {
			seerrStep.Detail = integration.Name + " is saved; Seerr intake starts after the API restarts with it configured"
		}
	}
	status.Steps = []SetupStep{plexStep, altMountStep, indexerStep,
		rootStep(catalog.KindMovie, "movie_root"), rootStep(catalog.KindSeries, "series_root"),
		profileStep(catalog.KindMovie, "movie_profile"), profileStep(catalog.KindSeries, "series_profile"),
		metadataStep, seerrStep}
	status.Complete = true
	for _, step := range status.Steps {
		if !step.Optional && !step.Done {
			status.Complete = false
		}
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: status})
}
