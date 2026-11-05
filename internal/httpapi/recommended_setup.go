package httpapi

import (
	"context"
	"net/http"
	"time"
)

type recommendedSetupInput struct {
	// Apply creates what is missing; false only reports what would be done.
	Apply bool `json:"apply"`
}

func (server *Server) recommendedSetup(writer http.ResponseWriter, request *http.Request) {
	if server.config.RecommendedSetup == nil {
		writeError(writer, http.StatusServiceUnavailable, "recommended_setup_unavailable", "the recommended setup is unavailable")
		return
	}
	var input recommendedSetupInput
	if request.ContentLength != 0 {
		if err := decodeJSON(request, &input, 1<<10); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "send {\"apply\": true} or {\"apply\": false}")
			return
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	report, err := server.config.RecommendedSetup.Run(ctx, input.Apply)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "the current setup could not be read")
		return
	}
	if input.Apply {
		_, _ = server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: report})
}
