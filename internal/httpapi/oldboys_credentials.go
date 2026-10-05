package httpapi

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) oldBoysCredentialsStatus(writer http.ResponseWriter, request *http.Request) {
	if server.config.OldBoysCredentials == nil {
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: oldboyscredentials.Status{}})
		return
	}
	status, err := server.config.OldBoysCredentials.Status(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "oldboys_credentials_unavailable", "Could not read OldBoys credential status")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: status})
}

func (server *Server) saveOldBoysCredentials(writer http.ResponseWriter, request *http.Request) {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "json_required", "Credentials must be submitted as JSON")
		return
	}
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(writer, http.StatusForbidden, "origin_rejected", "Save credentials from the Media Manager settings page")
		return
	}
	if server.config.OldBoysCredentials == nil {
		writeError(writer, http.StatusServiceUnavailable, "oldboys_credentials_unavailable", "OldBoys credential storage is not configured on this server")
		return
	}
	var credentials oldboyscredentials.Credentials
	if err := decodeJSON(request, &credentials, 16<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_oldboys_credentials", "Enter the API Token, Pass Key (PID), and RSS Key (RID)")
		return
	}
	status, err := server.config.OldBoysCredentials.Save(request.Context(), credentials)
	if errors.Is(err, oldboyscredentials.ErrInvalidCredentials) {
		writeError(writer, http.StatusBadRequest, "invalid_oldboys_credentials", "Enter all three credentials as tokens without spaces or URLs")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "oldboys_credentials_unavailable", "Could not save OldBoys credentials")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: status})
}

func (server *Server) commissionOldBoys(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(writer, http.StatusForbidden, "origin_rejected", "Validate OldBoys from Media Manager settings")
		return
	}
	if server.config.CommissionOldBoys == nil {
		writeError(writer, http.StatusServiceUnavailable, "commissioning_unavailable", "OldBoys commissioning is unavailable")
		return
	}
	integration, err := server.config.CommissionOldBoys(request.Context())
	if err != nil {
		writeError(writer, http.StatusBadGateway, "oldboys_commission_failed", "OldBoys validation failed; the provider remains disabled. Check the saved credentials and provider pacing before trying again.")
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration, Meta: map[string]string{"warning": "OldBoys enabled; requests awaiting configuration could not be requeued"}})
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) setOldBoysBudget(writer http.ResponseWriter, request *http.Request) {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "json_required", "The budget must be submitted as JSON")
		return
	}
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(writer, http.StatusForbidden, "origin_rejected", "Change the OldBoys budget from Media Manager")
		return
	}
	var input setOldBoysBudgetInput
	if err := decodeJSON(request, &input, 4<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_oldboys_budget", "Enter the daily search limit and optional daily download limit as whole numbers")
		return
	}
	integration, err := server.store.SetOldBoysBudget(request.Context(), input.QueryLimit, input.GrabLimit, time.Now().UTC())
	if errors.Is(err, postgres.ErrInvalidOldBoysBudget) {
		writeError(writer, http.StatusBadRequest, "invalid_oldboys_budget", fmt.Sprintf("Daily searches must be 1-%d and downloads 1-%d", postgres.MaxOldBoysQueryLimit, postgres.MaxOldBoysGrabLimit))
		return
	}
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusConflict, "oldboys_not_commissioned", "Validate and enable OldBoys before setting its budget")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "oldboys_budget_unavailable", "Could not save the OldBoys budget")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration})
}
