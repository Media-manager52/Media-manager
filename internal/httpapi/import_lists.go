package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/importlists"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listImportLists(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListImportLists(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load import lists")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) createImportList(writer http.ResponseWriter, request *http.Request) {
	var input createImportListInput
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, rootErr := optionalUUID(input.MovieRootID)
	profileID, profileErr := optionalUUID(input.MovieQualityProfileID)
	if rootErr != nil || profileErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_configuration", "movie root or profile ID is invalid")
		return
	}
	item, err := server.store.CreateImportList(request.Context(), postgres.ImportList{Name: input.Name, Provider: input.Provider, Enabled: input.Enabled, MovieRootID: rootID, MovieQualityProfileID: profileID}, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_import_list", "import list is invalid or conflicts with an existing name")
		return
	}
	writer.Header().Set("Location", "/api/v1/import-lists/"+item.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: item})
}

func (server *Server) getImportList(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "import-list ID is invalid")
		return
	}
	item, err := server.store.ImportList(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "import list was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load import list")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) createImportListSnapshot(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "import-list ID is invalid")
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "text/csv" && mediaType != "application/csv" && mediaType != "application/vnd.ms-excel" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_content_type", "an IMDb CSV export is required")
		return
	}
	const maxSnapshotBytes = 2 << 20
	body, err := io.ReadAll(io.LimitReader(request.Body, maxSnapshotBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxSnapshotBytes {
		writeError(writer, http.StatusBadRequest, "invalid_snapshot", "IMDb export must be non-empty and no larger than 2 MiB")
		return
	}
	parsed, err := importlists.ParseIMDbExport(strings.NewReader(string(body)))
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_snapshot", err.Error())
		return
	}
	digestBytes := sha256.Sum256(body)
	snapshot, err := server.store.CreateImportListSnapshot(request.Context(), id, hex.EncodeToString(digestBytes[:]), parsed, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "import list was not found")
		return
	}
	if errors.Is(err, postgres.ErrDuplicateSnapshot) {
		writeError(writer, http.StatusConflict, "duplicate_snapshot", "this exact IMDb export was already reviewed")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "snapshot_failed", "could not persist the import-list review snapshot")
		return
	}
	writer.Header().Set("Location", "/api/v1/import-lists/"+id.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: snapshot})
}
