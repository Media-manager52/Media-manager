package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listNotificationDestinations(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListNotificationDestinations(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load notification destinations")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) createNotificationDestination(writer http.ResponseWriter, request *http.Request) {
	var input createNotificationDestinationInput
	if err := decodeJSON(request, &input, 64<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	item, err := server.store.CreateNotificationDestination(request.Context(), postgres.NotificationDestination{Name: input.Name, Endpoint: input.Endpoint, AllowHTTP: input.AllowHTTP, Enabled: input.Enabled, Events: input.Events, SecretRef: input.SecretRef}, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_destination", "notification destination is invalid or conflicts with an existing name")
		return
	}
	item.Endpoint = postgres.RedactNotificationEndpoint(item.Endpoint)
	writer.Header().Set("Location", "/api/v1/settings/notifications/"+item.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: item})
}

func (server *Server) setNotificationDestinationState(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "notification destination ID is invalid")
		return
	}
	var input setNotificationDestinationStateInput
	if err := decodeJSON(request, &input, 8<<10); err != nil || input.Enabled == nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "enabled state is required")
		return
	}
	if err := server.store.SetNotificationDestinationEnabled(request.Context(), id, *input.Enabled, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "notification destination was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not update notification destination")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"id": id, "enabled": *input.Enabled}})
}

func (server *Server) listNotificationDeliveries(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListNotificationDeliveries(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load notification deliveries")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}
