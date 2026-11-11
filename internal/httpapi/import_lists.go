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
	if strings.EqualFold(strings.TrimSpace(input.Provider), "mdblist") {
		server.createMDBList(writer, request, input)
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

func listTargets(movieRoot, movieProfile, seriesRoot, seriesProfile string) (postgres.MDBListSettings, bool) {
	var settings postgres.MDBListSettings
	var errs [4]error
	settings.MovieRootID, errs[0] = optionalUUID(movieRoot)
	settings.MovieQualityProfileID, errs[1] = optionalUUID(movieProfile)
	settings.SeriesRootID, errs[2] = optionalUUID(seriesRoot)
	settings.SeriesQualityProfileID, errs[3] = optionalUUID(seriesProfile)
	for _, err := range errs {
		if err != nil {
			return settings, false
		}
	}
	return settings, true
}

func writeImportListError(writer http.ResponseWriter, err error, fallback string) {
	var refusal postgres.ActionRefusal
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "import list was not found")
	case errors.As(err, &refusal):
		writeErrorHint(writer, http.StatusUnprocessableEntity, refusal.Code, refusal.Message, refusal.Hint)
	default:
		writeError(writer, http.StatusInternalServerError, "database_error", fallback)
	}
}

// createMDBList follows a public MDBList list. Its first read is queued at
// once; every new title on it is then added and searched.
func (server *Server) createMDBList(writer http.ResponseWriter, request *http.Request, input createImportListInput) {
	ref, err := importlists.ParseMDBListURL(input.URL)
	if err != nil {
		writeErrorHint(writer, http.StatusUnprocessableEntity, "invalid_list_url", "the address is not a public MDBList list", "Copy the address of the list page, for example https://mdblist.com/lists/owner/list-name.")
		return
	}
	settings, ok := listTargets(input.MovieRootID, input.MovieQualityProfileID, input.SeriesRootID, input.SeriesQualityProfileID)
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_configuration", "a library folder or profile ID is invalid")
		return
	}
	settings.Name, settings.Owner, settings.Slug, settings.Enabled = input.Name, ref.Owner, ref.Slug, input.Enabled
	now := time.Now().UTC()
	item, err := server.store.CreateMDBList(request.Context(), settings, now)
	if err != nil {
		var refusal postgres.ActionRefusal
		if errors.As(err, &refusal) && refusal.Code == "import_list_exists" {
			writeErrorHint(writer, http.StatusConflict, refusal.Code, refusal.Message, refusal.Hint)
			return
		}
		writeImportListError(writer, err, "could not save the list")
		return
	}
	if item.Enabled {
		if _, err := server.store.QueueImportListSync(request.Context(), item.ID, now); err != nil {
			writeError(writer, http.StatusInternalServerError, "database_error", "the list was saved but its first read could not be queued")
			return
		}
	}
	writer.Header().Set("Location", "/api/v1/import-lists/"+item.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: item})
}

func (server *Server) updateImportList(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "import-list ID is invalid")
		return
	}
	var input updateImportListInput
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	settings, ok := listTargets(input.MovieRootID, input.MovieQualityProfileID, input.SeriesRootID, input.SeriesQualityProfileID)
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_configuration", "a library folder or profile ID is invalid")
		return
	}
	settings.Name, settings.Enabled = input.Name, input.Enabled
	now := time.Now().UTC()
	item, err := server.store.UpdateMDBList(request.Context(), id, settings, now)
	if err != nil {
		writeImportListError(writer, err, "could not change the list")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) deleteImportList(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "import-list ID is invalid")
		return
	}
	if err := server.store.DeleteImportList(request.Context(), id, time.Now().UTC()); err != nil {
		writeImportListError(writer, err, "could not delete the list")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) syncImportList(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "import-list ID is invalid")
		return
	}
	queued, err := server.store.QueueImportListSync(request.Context(), id, time.Now().UTC())
	if err != nil {
		writeImportListError(writer, err, "could not queue a read of the list")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]bool{"queued": queued, "already_waiting": !queued}})
}

func (server *Server) listImportListEntries(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "import-list ID is invalid")
		return
	}
	if _, err := server.store.ImportList(request.Context(), id); err != nil {
		writeImportListError(writer, err, "could not load the list")
		return
	}
	entries, err := server.store.ImportListEntries(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the list's titles")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: entries})
}
