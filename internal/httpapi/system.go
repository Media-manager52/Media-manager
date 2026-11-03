package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) systemInfo(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"version":                  server.config.Version,
		"commit":                   server.config.Commit,
		"schema_version":           postgres.CurrentSchemaVersion,
		"owner_only":               true,
		"browser_auth":             "gateway_basic_auth",
		"api_auth":                 "server_injected_owner_bearer",
		"seerr_webhook_enabled":    server.config.SeerrEnabled,
		"metadata_providers":       server.config.MetadataProviderStates,
		"altmount_source_policy":   "read_only",
		"publication_type":         "plex_safe_symlink",
		"upgrade_replacement_gate": "plex_path_and_identity_confirmation",
		"recycle_retention_days":   30,
		"acquisition_limits":       server.store.AcquisitionLimits(),
		"public_edge_owner":        "Cosmos Cloud",
		"machine_api_keys":         false,
		"api_prefix":               "/api/v1",
		"started_at":               server.readySince,
		"checked_at":               time.Now().UTC(),
	}})
}

func (server *Server) dashboard(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := server.store.Dashboard(request.Context(), time.Now().UTC())
	if err != nil {
		writeDatabaseError(writer, err, "could not load dashboard")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: snapshot})
}

func (server *Server) listSources(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListSourceStatuses(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load sources")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) systemHealth(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := server.store.SystemHealth(ctx, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "health_unavailable", "component health could not be loaded")
		return
	}
	snapshot.Mounts = altMountMountHealth(snapshot.Components, altmount.CheckMount)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: snapshot})
}

// altMountMountHealth probes the read-only output root of every enabled
// AltMount client. AltMount's API can be healthy while its FUSE mount is gone
// from this container, so the mount is reported separately.
func altMountMountHealth(components []postgres.SourceStatus, check func(string) error) []postgres.MountHealth {
	mounts := []postgres.MountHealth{}
	for _, component := range components {
		if component.Kind != "altmount" || !component.Enabled {
			continue
		}
		var config struct {
			LocalReadOnlyRoot string `json:"local_read_only_root"`
		}
		_ = json.Unmarshal(component.Config, &config)
		mount := postgres.MountHealth{IntegrationID: component.ID, Name: component.Name, Path: config.LocalReadOnlyRoot, State: "available"}
		if err := check(config.LocalReadOnlyRoot); err != nil {
			mount.State, mount.Detail = "unavailable", err.Error()
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

func (server *Server) live(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"status": "live", "version": server.config.Version, "time": time.Now().UTC()}})
}

func (server *Server) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := server.store.Ready(ctx); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "not_ready", "database or schema is not ready")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"status": "ready", "since": server.readySince}})
}
