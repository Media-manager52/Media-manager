package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listCalendar(writer http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	from, to := now.AddDate(0, 0, -30), now.AddDate(1, 0, 0)
	var err error
	if value := request.URL.Query().Get("from"); value != "" {
		from, err = time.Parse("2006-01-02", value)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_range", "from date is invalid")
			return
		}
	}
	if value := request.URL.Query().Get("to"); value != "" {
		to, err = time.Parse("2006-01-02", value)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_range", "to date is invalid")
			return
		}
	}
	if to.Before(from) || to.Sub(from) > 2*366*24*time.Hour {
		writeError(writer, http.StatusBadRequest, "invalid_range", "calendar range must be ordered and no longer than two years")
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListCalendar(request.Context(), from, to, limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load calendar")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]any{"count": len(items), "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")}})
}

func collectionMediaIDs(values []string) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, nil
}

func (server *Server) listCollections(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListCollections(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load collections")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) previewCollection(writer http.ResponseWriter, request *http.Request) {
	var input previewCollectionInput
	if err := decodeJSON(request, &input, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	ids, err := collectionMediaIDs(input.MediaIDs)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_media", "collection media IDs are invalid")
		return
	}
	items, err := server.store.PreviewCollection(request.Context(), ids)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_media", "collection media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_collection", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) createCollection(writer http.ResponseWriter, request *http.Request) {
	var input createCollectionInput
	if err := decodeJSON(request, &input, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	ids, err := collectionMediaIDs(input.MediaIDs)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_media", "collection media IDs are invalid")
		return
	}
	item, err := server.store.CreateCollection(request.Context(), input.Name, input.Description, ids, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "collection_create_failed", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/collections/"+item.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: item})
}

func (server *Server) deleteCollection(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "collection ID is invalid")
		return
	}
	if err := server.store.DeleteCollection(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "collection was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not delete collection")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
