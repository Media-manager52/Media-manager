package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

// listSeerrRoutes returns the configured Seerr root-folder routes together
// with every root folder observed on persisted Seerr requests, so the owner
// can see and complete the mapping.
func (server *Server) listSeerrRoutes(writer http.ResponseWriter, request *http.Request) {
	instanceID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "integration id is invalid")
		return
	}
	integration, err := server.store.Integration(request.Context(), instanceID)
	if err != nil || integration.Kind != "seerr" {
		writeError(writer, http.StatusNotFound, "not_found", "Seerr integration not found")
		return
	}
	routes, err := server.store.ListSeerrLibraryRoutes(request.Context(), instanceID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load Seerr routes")
		return
	}
	observed, err := server.store.ListSeerrObservedFolders(request.Context(), instanceID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load observed Seerr folders")
		return
	}
	if routes == nil {
		routes = []postgres.SeerrLibraryRoute{}
	}
	if observed == nil {
		observed = []postgres.SeerrObservedFolder{}
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"routes": routes, "observed_folders": observed}})
}

// saveSeerrRoute creates or replaces the mapping for one Seerr root folder and
// then requeues any request that was paused waiting for configuration.
func (server *Server) saveSeerrRoute(writer http.ResponseWriter, request *http.Request) {
	instanceID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "integration id is invalid")
		return
	}
	integration, err := server.store.Integration(request.Context(), instanceID)
	if err != nil || integration.Kind != "seerr" {
		writeError(writer, http.StatusNotFound, "not_found", "Seerr integration not found")
		return
	}
	var body struct {
		MediaKind        string `json:"media_kind"`
		RootFolder       string `json:"root_folder"`
		LibraryRootID    string `json:"library_root_id"`
		QualityProfileID string `json:"quality_profile_id"`
	}
	if err := decodeJSON(request, &body, 64<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	libraryRootID, err := uuid.Parse(strings.TrimSpace(body.LibraryRootID))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "library_root_id is invalid")
		return
	}
	var profileID *uuid.UUID
	if value := strings.TrimSpace(body.QualityProfileID); value != "" {
		parsed, parseErr := uuid.Parse(value)
		if parseErr != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "quality_profile_id is invalid")
			return
		}
		profileID = &parsed
	}
	route, err := server.store.UpsertSeerrLibraryRoute(request.Context(), postgres.SeerrLibraryRoute{
		SeerrInstanceID:  instanceID,
		MediaKind:        catalog.MediaKind(strings.TrimSpace(body.MediaKind)),
		RootFolder:       body.RootFolder,
		LibraryRootID:    libraryRootID,
		QualityProfileID: profileID,
	}, "owner")
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_seerr_route", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, http.StatusCreated, dataEnvelope{Data: route, Meta: map[string]string{"warning": "route saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: route, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) deleteSeerrRoute(writer http.ResponseWriter, request *http.Request) {
	instanceID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "integration id is invalid")
		return
	}
	routeID, err := uuid.Parse(request.PathValue("route"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "route id is invalid")
		return
	}
	err = server.store.DeleteSeerrLibraryRoute(request.Context(), instanceID, routeID, "owner")
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "Seerr route not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not remove Seerr route")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]string{"status": "removed"}})
}
