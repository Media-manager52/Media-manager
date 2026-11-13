package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/altmountswitch"
	"github.com/media-manager/media-manager/internal/plexaccess"
	"github.com/media-manager/media-manager/internal/postgres"
)

type altMountSwitchInput struct {
	// CosmosHost is the name Media Manager is opened at; Cosmos answers its
	// API on it.
	CosmosHost string `json:"cosmos_host"`
	// CosmosAPIKey is used for this request only and never stored.
	CosmosAPIKey string `json:"cosmos_api_key"`
	// Container chooses AltMount's container when several look like AltMount.
	Container string `json:"altmount_container,omitempty"`
	Apply     bool   `json:"apply"`
}

type altMountSwitchResponse struct {
	altmountswitch.Plan
	// Problem is "choose_altmount" when Candidates must be chosen from.
	Problem string `json:"problem,omitempty"`
}

// switchAltMount is "Skift til Media Managers AltMount": through Cosmos, an
// existing normal AltMount gets Media Manager's build as its image and keeps
// its settings, database, files and drive.
func (server *Server) switchAltMount(writer http.ResponseWriter, request *http.Request) {
	if server.config.PlexAccess == nil {
		writeError(writer, http.StatusServiceUnavailable, "altmount_switch_unavailable", "switching AltMount through Cosmos is unavailable")
		return
	}
	var input altMountSwitchInput
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
	integration := defaultEnabled(integrations, "altmount")
	if integration == nil {
		writeErrorHint(writer, http.StatusConflict, "altmount_not_configured", "Media Manager has no AltMount connection", "Add AltMount under Indstillinger → Forbindelser first.")
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
	switchCosmos, ok := cosmosClient.(altmountswitch.Cosmos)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "altmount_switch_unavailable", "switching AltMount through Cosmos is unavailable")
		return
	}
	self, err := server.config.PlexAccess.Self()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "self_unknown", "Media Manager does not know its own container")
		return
	}
	switcher := altmountswitch.Switcher{Cosmos: switchCosmos, AltMount: func(ctx context.Context) (altmountswitch.AltMount, error) {
		client, err := acquisition.AltMountProviderClient(ctx, *integration, server.resolveSecret, nil)
		if err != nil {
			return nil, err
		}
		return client, nil
	}}
	plan, err := switcher.Run(ctx, altmountswitch.Request{Self: self, AltMountHost: connectionHost(integration.Config), Container: strings.TrimSpace(input.Container), Apply: input.Apply})
	var versionErr altmountswitch.VersionError
	var rolledBack altmountswitch.RolledBackError
	switch {
	case errors.Is(err, altmountswitch.ErrChooseAltMount):
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: altMountSwitchResponse{Plan: plan, Problem: "choose_altmount"}})
		return
	case errors.Is(err, altmountswitch.ErrAltMountNotFound):
		writeErrorHint(writer, http.StatusConflict, "altmount_container_not_found", "no AltMount container was found in Cosmos", "AltMount must run in the same Cosmos as Media Manager.")
		return
	case errors.Is(err, altmountswitch.ErrUnsupportedFolder):
		writeError(writer, http.StatusConflict, "altmount_folder_refused", err.Error())
		return
	case errors.As(err, &versionErr) && versionErr.Newer:
		writeError(writer, http.StatusConflict, "altmount_version_newer", versionErr.Error())
		return
	case errors.As(err, &versionErr):
		writeError(writer, http.StatusConflict, "altmount_version_unknown", versionErr.Error())
		return
	case errors.As(err, &rolledBack) && rolledBack.RollbackErr != nil:
		writeError(writer, http.StatusBadGateway, "altmount_switch_failed", safeConnectionError(err))
		return
	case errors.As(err, &rolledBack):
		writeError(writer, http.StatusBadGateway, "altmount_switch_rolled_back", safeConnectionError(err))
		return
	case errors.Is(err, plexaccess.ErrCosmosUnauthorized):
		writeErrorHint(writer, http.StatusBadRequest, "cosmos_unauthorized", "Cosmos did not accept the API token", "The token must not be read-only.")
		return
	case err != nil:
		writeError(writer, http.StatusBadGateway, "altmount_switch_failed", safeConnectionError(err))
		return
	}
	if plan.Applied {
		_ = server.store.RecordAltMountSwitch(request.Context(), integration.ID, postgres.AltMountSwitchAudit{Container: plan.Container, FromImage: plan.CurrentImage, FromVersion: plan.CurrentVersion, ToImage: plan.NewImage, ToVersion: plan.NewVersion}, time.Now().UTC())
		server.forgetPlexAltMount()
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: altMountSwitchResponse{Plan: plan}})
}
