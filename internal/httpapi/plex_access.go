package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/plexaccess"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/quicksetup"
)

type plexAccessInput struct {
	// CosmosHost is the name Media Manager is opened at; Cosmos answers its
	// API on it.
	CosmosHost string `json:"cosmos_host"`
	// CosmosAPIKey is used for this request only and never stored.
	CosmosAPIKey string `json:"cosmos_api_key"`
	// PlexContainer chooses Plex's container when several look like Plex.
	PlexContainer string `json:"plex_container,omitempty"`
	Apply         bool   `json:"apply"`
}

type plexAccessResponse struct {
	plexaccess.Plan
	// Problem is "choose_plex" when Candidates must be chosen from.
	Problem string `json:"problem,omitempty"`
}

// fixPlexAccess is "Ret Plex": through Cosmos, Plex gets AltMount's drive
// (following AltMount's restarts) and the library folders Media Manager
// writes, which are then added to Plex's libraries.
func (server *Server) fixPlexAccess(writer http.ResponseWriter, request *http.Request) {
	if server.config.PlexAccess == nil {
		writeError(writer, http.StatusServiceUnavailable, "plex_access_unavailable", "fixing Plex through Cosmos is unavailable")
		return
	}
	var input plexAccessInput
	if err := decodeJSON(request, &input, 8<<10); err != nil || strings.TrimSpace(input.CosmosHost) == "" || strings.TrimSpace(input.CosmosAPIKey) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request", "send cosmos_host and cosmos_api_key")
		return
	}
	// Cosmos recreates a container and waits for it, which takes longer than
	// the server's usual write limit; the answer must still reach the page.
	_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(9 * time.Minute))
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Minute)
	defer cancel()
	integrations, err := server.store.ListIntegrations(ctx)
	if err != nil {
		writeDatabaseError(writer, err, "the current setup could not be read")
		return
	}
	plexIntegration := defaultEnabled(integrations, "plex")
	if plexIntegration == nil {
		writeErrorHint(writer, http.StatusConflict, "no_plex", "Media Manager has no Plex connection", "Use \"Log ind med Plex\" first.")
		return
	}
	needs, err := server.plexAccessNeeds(ctx, integrations)
	if err != nil {
		writeDatabaseError(writer, err, "the current setup could not be read")
		return
	}
	if len(needs) == 0 {
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: plexAccessResponse{Plan: plexaccess.Plan{Changes: []plexaccess.Change{}, PlexPaths: map[string]string{}}}})
		return
	}
	cosmosClient, err := server.config.PlexAccess.OpenCosmos(ctx, input.CosmosHost, strings.TrimSpace(input.CosmosAPIKey))
	if err != nil {
		if errors.Is(err, plexaccess.ErrCosmosUnauthorized) {
			writeErrorHint(writer, http.StatusBadRequest, "cosmos_unauthorized", "Cosmos did not accept the API token", "Make a new API token in Cosmos (not read-only) and paste it.")
			return
		}
		writeError(writer, http.StatusBadGateway, "cosmos_unreachable", safeConnectionError(err))
		return
	}
	self, err := server.config.PlexAccess.Self()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "self_unknown", "Media Manager does not know its own container")
		return
	}
	fixer := plexaccess.Fixer{Cosmos: cosmosClient, Plex: func(context.Context) (plexaccess.Plex, error) {
		client, err := acquisition.PlexClient(*plexIntegration, server.resolveSecret)
		if err != nil {
			return nil, err
		}
		return client, nil
	}}
	plan, err := fixer.Run(ctx, plexaccess.Request{Self: self, PlexHost: connectionHost(plexIntegration.Config), PlexContainer: strings.TrimSpace(input.PlexContainer), Needs: needs, Apply: input.Apply})
	var folderErr plexaccess.FolderError
	switch {
	case errors.Is(err, plexaccess.ErrChoosePlex):
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: plexAccessResponse{Plan: plan, Problem: "choose_plex"}})
		return
	case errors.Is(err, plexaccess.ErrPlexNotFound):
		writeErrorHint(writer, http.StatusConflict, "plex_container_not_found", "no Plex container was found in Cosmos", "Plex must run in the same Cosmos as Media Manager.")
		return
	case errors.As(err, &folderErr):
		writeError(writer, http.StatusConflict, "plex_folder_refused", folderErr.Reason)
		return
	case errors.Is(err, plexaccess.ErrCosmosUnauthorized):
		writeErrorHint(writer, http.StatusBadRequest, "cosmos_unauthorized", "Cosmos did not accept the API token", "The token must not be read-only.")
		return
	case err != nil:
		writeError(writer, http.StatusBadGateway, "plex_access_failed", safeConnectionError(err))
		return
	}
	if plan.Applied {
		_ = server.store.RecordPlexAccessFix(request.Context(), plexIntegration.ID, postgres.PlexAccessAudit{Container: plan.PlexContainer, Changes: auditChanges(plan.Changes)}, time.Now().UTC())
		server.forgetPlexAltMount()
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: plexAccessResponse{Plan: plan}})
}

// plexAccessNeeds is what Plex must see: AltMount's drive, and the library
// folders of kinds the recommended setup cannot use because Plex does not
// see them or has no library.
func (server *Server) plexAccessNeeds(ctx context.Context, integrations []postgres.Integration) ([]plexaccess.Need, error) {
	var needs []plexaccess.Need
	if altMount := defaultEnabled(integrations, "altmount"); altMount != nil {
		var config struct {
			LocalReadOnlyRoot string `json:"local_read_only_root"`
		}
		_ = json.Unmarshal(altMount.Config, &config)
		if root := filepath.Clean(strings.TrimSpace(config.LocalReadOnlyRoot)); filepath.IsAbs(root) {
			needs = append(needs, plexaccess.Need{Purpose: plexaccess.PurposeAltMount, Path: root})
		}
	}
	setup := server.config.RecommendedSetup
	if setup == nil {
		return needs, nil
	}
	report, err := setup.Run(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, step := range report.Steps {
		if step.State != quicksetup.StateBlocked || (step.Reason != quicksetup.ReasonFolderNotShared && step.Reason != quicksetup.ReasonNoPlexLibrary) {
			continue
		}
		kind, purpose := catalog.KindMovie, plexaccess.PurposeMovies
		if step.ID == "series_root" {
			kind, purpose = catalog.KindSeries, plexaccess.PurposeTV
		} else if step.ID != "movie_root" {
			continue
		}
		if folder := setup.LibraryFolder(kind); folder != "" {
			needs = append(needs, plexaccess.Need{Purpose: purpose, Path: folder})
		}
	}
	return needs, nil
}

func defaultEnabled(integrations []postgres.Integration, kind string) *postgres.Integration {
	var first *postgres.Integration
	for index := range integrations {
		integration := &integrations[index]
		if integration.Kind != kind || !integration.Enabled {
			continue
		}
		if integration.Default {
			return integration
		}
		if first == nil {
			first = integration
		}
	}
	return first
}

// connectionHost is the host of a connection's base_url.
func connectionHost(config json.RawMessage) string {
	var parsed struct {
		BaseURL string `json:"base_url"`
	}
	_ = json.Unmarshal(config, &parsed)
	address, err := url.Parse(strings.TrimSpace(parsed.BaseURL))
	if err != nil {
		return ""
	}
	return address.Hostname()
}

func auditChanges(changes []plexaccess.Change) []string {
	result := make([]string, 0, len(changes))
	for _, change := range changes {
		entry := change.Kind + " " + change.Purpose + " " + change.Path
		if change.Library != "" {
			entry += " (" + change.Library + ")"
		}
		result = append(result, entry)
	}
	return result
}
