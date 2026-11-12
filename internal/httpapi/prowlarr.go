package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/prowlarrbackup"
)

func (server *Server) createProwlarrImport(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 128<<20)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		writeError(writer, http.StatusRequestEntityTooLarge, "backup_too_large", "Prowlarr backup exceeds the 128 MiB limit")
		return
	}
	parsed, err := prowlarrbackup.Parse(request.Context(), body)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_prowlarr_backup", err.Error())
		return
	}
	created, err := server.store.CreateProwlarrImport(request.Context(), parsed.SHA256[:], parsed.Candidates, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "import_failed", "could not persist the Prowlarr review")
		return
	}
	// The extracted API keys never leave the server (S18): the owner gives
	// each approved indexer its own secret reference.
	candidates := created.Candidates
	if candidates == nil {
		candidates = []postgres.ProwlarrIndexerCandidate{}
	}
	response := struct {
		ID            uuid.UUID                           `json:"id"`
		State         string                              `json:"state"`
		ParserVersion string                              `json:"parser_version"`
		CreatedAt     time.Time                           `json:"created_at"`
		Candidates    []postgres.ProwlarrIndexerCandidate `json:"candidates"`
	}{ID: created.ID, State: created.State, ParserVersion: created.ParserVersion, CreatedAt: created.CreatedAt, Candidates: candidates}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Location", "/api/v1/imports/prowlarr/"+created.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: response})
}

func (server *Server) getProwlarrImport(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "Prowlarr import ID is invalid")
		return
	}
	item, err := server.store.ProwlarrImport(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "Prowlarr import was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load Prowlarr import")
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) approveProwlarrImport(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "Prowlarr import ID is invalid")
		return
	}
	var input approveProwlarrImportInput
	if err := decodeJSON(request, &input, 64<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "approval body is invalid")
		return
	}
	approvals := make(map[uuid.UUID]string, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidateID, parseErr := uuid.Parse(candidate.ID)
		if parseErr != nil || strings.TrimSpace(candidate.SecretRef) == "" {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_candidate", "candidate IDs and secret references are required")
			return
		}
		approvals[candidateID] = strings.TrimSpace(candidate.SecretRef)
	}
	item, err := server.store.ApproveProwlarrImport(request.Context(), id, approvals, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "Prowlarr import was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "approval_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}
