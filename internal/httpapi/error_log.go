package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/media-manager/media-manager/internal/postgres"
)

const (
	errorLogDefaultLimit = 200
	errorLogMaxLimit     = 1000
	errorLogMaxQuery     = 200
)

var errorLogAreas = map[string]bool{"download": true, "language": true, "search": true, "repair": true, "backup": true, "connection": true, "title": true}

func (server *Server) listErrorLog(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	filter := postgres.ErrorLogFilter{Area: strings.TrimSpace(query.Get("area")), Query: strings.TrimSpace(query.Get("q")), Limit: errorLogDefaultLimit}
	if filter.Area != "" && !errorLogAreas[filter.Area] {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_area", "area is not known")
		return
	}
	if utf8.RuneCountInString(filter.Query) > errorLogMaxQuery {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_query", "search text is too long")
		return
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > errorLogMaxLimit {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_limit", "limit must be between 1 and 1000")
			return
		}
		filter.Limit = limit
	}
	log, err := server.store.ErrorLog(request.Context(), filter, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the error log")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: redactErrorLog(log)})
}

// redactErrorLog removes credentials from provider text, because the page
// exists so the owner can copy these lines into a request for help.
func redactErrorLog(log postgres.ErrorLog) postgres.ErrorLog {
	for index := range log.Entries {
		log.Entries[index].Detail = redactSupportText(log.Entries[index].Detail)
		log.Entries[index].Subject = redactSupportText(log.Entries[index].Subject)
	}
	return log
}
