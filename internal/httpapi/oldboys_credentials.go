package httpapi

import (
	"errors"
	"mime"
	"net/http"

	"github.com/media-manager/media-manager/internal/oldboyscredentials"
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
