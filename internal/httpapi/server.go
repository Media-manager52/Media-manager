package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/adoption"
	"github.com/media-manager/media-manager/internal/backups"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/importlists"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/prowlarrbackup"
	"github.com/media-manager/media-manager/internal/workflows"
)

type Config struct {
	OwnerToken         string
	SeerrEnabled       bool
	SeerrWebhookSecret string
	SeerrInstanceID    string
	Version            string
	MetadataProvider   *seerr.Client
	MetadataEnricher   *metadataservice.Service
	GMDMetadataEnabled bool
	CommissionNewznab  func(context.Context, uuid.UUID) (postgres.Integration, error)
	BackupConfigured   bool
	BackupDirectory    string
}

type Server struct {
	config     Config
	store      *postgres.Store
	mux        *http.ServeMux
	readySince time.Time
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type dataEnvelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

func New(config Config, store *postgres.Store) (*Server, error) {
	if store == nil || len(config.OwnerToken) < 32 {
		return nil, errors.New("owner token must contain at least 32 bytes")
	}
	server := &Server{config: config, store: store, mux: http.NewServeMux(), readySince: time.Now().UTC()}
	server.routes()
	return server, nil
}

func (server *Server) Handler() http.Handler { return server.security(server.mux) }

func (server *Server) routes() {
	server.mux.HandleFunc("GET /health/live", server.live)
	server.mux.HandleFunc("GET /health/ready", server.ready)
	server.mux.HandleFunc("GET /api/v1/dashboard", server.requireOwner(server.dashboard))
	server.mux.HandleFunc("GET /api/v1/adoption", server.requireOwner(server.listAdoptionBatches))
	server.mux.HandleFunc("POST /api/v1/adoption", server.requireOwner(server.createAdoptionBatch))
	server.mux.HandleFunc("GET /api/v1/adoption/{id}", server.requireOwner(server.getAdoptionBatch))
	server.mux.HandleFunc("PATCH /api/v1/adoption/{id}/items/{itemID}", server.requireOwner(server.reviewAdoptionItem))
	server.mux.HandleFunc("POST /api/v1/adoption/{id}/approve", server.requireOwner(server.approveAdoptionBatch))
	server.mux.HandleFunc("POST /api/v1/adoption/{id}/execute", server.requireOwner(server.executeAdoptionBatch))
	server.mux.HandleFunc("GET /api/v1/collections", server.requireOwner(server.listCollections))
	server.mux.HandleFunc("POST /api/v1/collections", server.requireOwner(server.createCollection))
	server.mux.HandleFunc("POST /api/v1/collections/preview", server.requireOwner(server.previewCollection))
	server.mux.HandleFunc("DELETE /api/v1/collections/{id}", server.requireOwner(server.deleteCollection))
	server.mux.HandleFunc("GET /api/v1/calendar", server.requireOwner(server.listCalendar))
	server.mux.HandleFunc("GET /api/v1/discovery/search", server.requireOwner(server.discoverySearch))
	server.mux.HandleFunc("GET /api/v1/discovery/{kind}/{id}", server.requireOwner(server.discoveryMetadata))
	server.mux.HandleFunc("GET /api/v1/media", server.requireOwner(server.listMedia))
	server.mux.HandleFunc("POST /api/v1/media", server.requireOwner(server.createMedia))
	server.mux.HandleFunc("GET /api/v1/media/{id}", server.requireOwner(server.getMedia))
	server.mux.HandleFunc("GET /api/v1/media/{id}/detail", server.requireOwner(server.getMediaDetail))
	server.mux.HandleFunc("POST /api/v1/media/{id}/metadata/refresh", server.requireOwner(server.refreshMediaMetadata))
	server.mux.HandleFunc("DELETE /api/v1/media/{id}", server.requireOwner(server.recycleMedia))
	server.mux.HandleFunc("POST /api/v1/media/{id}/workflows", server.requireOwner(server.createMediaWorkflow))
	server.mux.HandleFunc("GET /api/v1/workflows", server.requireOwner(server.listWorkflows))
	server.mux.HandleFunc("GET /api/v1/workflows/{id}", server.requireOwner(server.getWorkflow))
	server.mux.HandleFunc("POST /api/v1/workflows/{id}/search", server.requireOwner(server.searchWorkflow))
	server.mux.HandleFunc("POST /api/v1/workflows/{id}/recover-altmount-submission", server.requireOwner(server.recoverAltMountSubmission))
	server.mux.HandleFunc("POST /api/v1/workflows/{id}/reconcile-altmount", server.requireOwner(server.reconcileAltMount))
	server.mux.HandleFunc("POST /api/v1/workflows/{id}/cancel", server.requireOwner(server.cancelWorkflow))
	server.mux.HandleFunc("GET /api/v1/activity", server.requireOwner(server.listActivity))
	server.mux.HandleFunc("GET /api/v1/files", server.requireOwner(server.listManagedFiles))
	server.mux.HandleFunc("GET /api/v1/files/{id}", server.requireOwner(server.getManagedFile))
	server.mux.HandleFunc("GET /api/v1/intelligence/releases", server.requireOwner(server.listReleaseCandidates))
	server.mux.HandleFunc("GET /api/v1/acquisition-verifications", server.requireOwner(server.listSubtitleVerifications))
	server.mux.HandleFunc("GET /api/v1/blocklist", server.requireOwner(server.listReleaseBlocks))
	server.mux.HandleFunc("POST /api/v1/blocklist", server.requireOwner(server.createReleaseBlock))
	server.mux.HandleFunc("DELETE /api/v1/blocklist/{id}", server.requireOwner(server.deleteReleaseBlock))
	server.mux.HandleFunc("GET /api/v1/wanted", server.requireOwner(server.listWanted))
	server.mux.HandleFunc("GET /api/v1/sources", server.requireOwner(server.listSources))
	server.mux.HandleFunc("GET /api/v1/import-lists", server.requireOwner(server.listImportLists))
	server.mux.HandleFunc("POST /api/v1/import-lists", server.requireOwner(server.createImportList))
	server.mux.HandleFunc("GET /api/v1/import-lists/{id}", server.requireOwner(server.getImportList))
	server.mux.HandleFunc("POST /api/v1/import-lists/{id}/snapshots", server.requireOwner(server.createImportListSnapshot))
	server.mux.HandleFunc("GET /api/v1/system/health", server.requireOwner(server.systemHealth))
	server.mux.HandleFunc("GET /api/v1/system/tasks", server.requireOwner(server.listTasks))
	server.mux.HandleFunc("GET /api/v1/system/info", server.requireOwner(server.systemInfo))
	server.mux.HandleFunc("GET /api/v1/backups", server.requireOwner(server.listBackups))
	server.mux.HandleFunc("POST /api/v1/backups", server.requireOwner(server.createBackup))
	server.mux.HandleFunc("GET /api/v1/backups/{id}/download", server.requireOwner(server.downloadBackup))
	server.mux.HandleFunc("GET /api/v1/recycle", server.requireOwner(server.listRecycle))
	server.mux.HandleFunc("POST /api/v1/recycle/{id}/restore", server.requireOwner(server.restoreRecycle))
	server.mux.HandleFunc("POST /api/v1/recycle/{id}/restore-adopted", server.requireOwner(server.restoreAdoptedFiles))
	server.mux.HandleFunc("POST /api/v1/recycle/{id}/confirm-purge", server.requireOwner(server.confirmRealFilePurge))
	server.mux.HandleFunc("POST /api/v1/imports/prowlarr", server.requireOwner(server.createProwlarrImport))
	server.mux.HandleFunc("GET /api/v1/imports/prowlarr/{id}", server.requireOwner(server.getProwlarrImport))
	server.mux.HandleFunc("POST /api/v1/imports/prowlarr/{id}/approve", server.requireOwner(server.approveProwlarrImport))
	server.mux.HandleFunc("GET /api/v1/settings/integrations", server.requireOwner(server.listIntegrations))
	server.mux.HandleFunc("POST /api/v1/settings/integrations", server.requireOwner(server.createIntegration))
	server.mux.HandleFunc("POST /api/v1/settings/integrations/{id}/commission", server.requireOwner(server.commissionIntegration))
	server.mux.HandleFunc("GET /api/v1/settings/library-roots", server.requireOwner(server.listLibraryRoots))
	server.mux.HandleFunc("POST /api/v1/settings/library-roots", server.requireOwner(server.createLibraryRoot))
	server.mux.HandleFunc("GET /api/v1/settings/quality-profiles", server.requireOwner(server.listQualityProfiles))
	server.mux.HandleFunc("POST /api/v1/settings/quality-profiles", server.requireOwner(server.createQualityProfile))
	server.mux.HandleFunc("PATCH /api/v1/settings/quality-profiles/{id}", server.requireOwner(server.updateQualityProfile))
	server.mux.HandleFunc("GET /api/v1/settings/notifications", server.requireOwner(server.listNotificationDestinations))
	server.mux.HandleFunc("POST /api/v1/settings/notifications", server.requireOwner(server.createNotificationDestination))
	server.mux.HandleFunc("PATCH /api/v1/settings/notifications/{id}", server.requireOwner(server.setNotificationDestinationState))
	server.mux.HandleFunc("GET /api/v1/notifications/deliveries", server.requireOwner(server.listNotificationDeliveries))
	server.mux.HandleFunc("POST /api/v1/integrations/seerr/webhook", server.seerrWebhook)
}

func (server *Server) listBackups(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListBackups(request.Context(), 100)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load backups")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"configured":           server.config.BackupConfigured,
		"format":               "PostgreSQL custom format encrypted with chunked AES-256-GCM",
		"restore_verification": "isolated temporary database and exact schema version",
		"automatic_deletion":   false,
		"production_restore":   false,
		"records":              items,
	}})
}

func (server *Server) createBackup(writer http.ResponseWriter, request *http.Request) {
	if !server.config.BackupConfigured {
		writeError(writer, http.StatusConflict, "backup_not_configured", "BACKUP_ENCRYPTION_KEY is not configured")
		return
	}
	item, err := server.store.CreateBackup(request.Context(), time.Now().UTC())
	if errors.Is(err, postgres.ErrBackupInProgress) {
		writeError(writer, http.StatusConflict, "backup_in_progress", "a backup is already queued or running")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "backup_failed", "could not queue backup")
		return
	}
	writer.Header().Set("Location", "/api/v1/backups")
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: item})
}

func (server *Server) downloadBackup(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "backup ID is invalid")
		return
	}
	record, err := server.store.Backup(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) || err == nil && record.State != "verified" {
		writeError(writer, http.StatusNotFound, "not_found", "verified backup was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load backup")
		return
	}
	if filepath.Base(record.Filename) != record.Filename || !strings.HasSuffix(record.Filename, ".mmbackup") {
		writeError(writer, http.StatusInternalServerError, "invalid_backup", "backup storage reference is invalid")
		return
	}
	path := filepath.Join(server.config.BackupDirectory, record.Filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		writeError(writer, http.StatusNotFound, "not_found", "backup file is unavailable")
		return
	}
	checksum, size, err := backups.FileDigest(path)
	if err != nil || checksum != record.SHA256 || record.SizeBytes == nil || size != *record.SizeBytes {
		writeError(writer, http.StatusConflict, "backup_integrity_failed", "backup checksum or size no longer matches its verified record")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "backup file is unavailable")
		return
	}
	defer file.Close()
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Disposition", `attachment; filename="`+record.Filename+`"`)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, record.Filename, info.ModTime(), file)
}

func (server *Server) listImportLists(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListImportLists(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load import lists")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) createImportList(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name                  string `json:"name"`
		Provider              string `json:"provider"`
		Enabled               bool   `json:"enabled"`
		MovieRootID           string `json:"movie_root_id"`
		MovieQualityProfileID string `json:"movie_quality_profile_id"`
	}
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

func (server *Server) listNotificationDestinations(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListNotificationDestinations(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load notification destinations")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) createNotificationDestination(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name      string   `json:"name"`
		Endpoint  string   `json:"endpoint"`
		AllowHTTP bool     `json:"allow_http"`
		Enabled   bool     `json:"enabled"`
		Events    []string `json:"events"`
		SecretRef string   `json:"secret_ref"`
	}
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
	var input struct {
		Enabled *bool `json:"enabled"`
	}
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
	var input struct {
		MediaIDs []string `json:"media_ids"`
	}
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
	var input struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		MediaIDs    []string `json:"media_ids"`
	}
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

func (server *Server) listAdoptionBatches(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListAdoptionBatches(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load adoption batches")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) createAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		SourceRoot        string `json:"source_root"`
		RootID            string `json:"root_id"`
		QualityProfileID  string `json:"quality_profile_id"`
		MonitoringEnabled bool   `json:"monitoring_enabled"`
		SearchImmediately bool   `json:"search_immediately"`
	}
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, rootErr := uuid.Parse(input.RootID)
	profileID, profileErr := uuid.Parse(input.QualityProfileID)
	if rootErr != nil || profileErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "root and quality profile IDs are required")
		return
	}
	configuration, err := server.store.AdoptionConfiguration(request.Context(), rootID, profileID)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "enabled compatible root and profile were not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not validate adoption configuration")
		return
	}
	sourceRoot := filepath.Clean(strings.TrimSpace(input.SourceRoot))
	relative, relErr := filepath.Rel(configuration.Root.DestinationRoot, sourceRoot)
	if relErr != nil || !filepath.IsAbs(sourceRoot) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		writeError(writer, http.StatusUnprocessableEntity, "unsafe_source_root", "source root must be the selected library root or one of its subdirectories")
		return
	}
	entries, err := adoption.Scan(request.Context(), sourceRoot, 50000)
	if errors.Is(err, adoption.ErrScanLimit) {
		writeError(writer, http.StatusUnprocessableEntity, "scan_limit", "read-only discovery exceeded 50,000 files")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "scan_failed", "source root could not be scanned safely")
		return
	}
	batch, err := server.store.CreateAdoptionBatch(request.Context(), sourceRoot, configuration, input.MonitoringEnabled, input.SearchImmediately, entries, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "adoption_plan_failed", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/adoption/"+batch.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: batch})
}

func (server *Server) getAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch ID is invalid")
		return
	}
	item, err := server.store.AdoptionBatch(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load adoption batch")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) reviewAdoptionItem(writer http.ResponseWriter, request *http.Request) {
	batchID, batchErr := uuid.Parse(request.PathValue("id"))
	itemID, itemErr := uuid.Parse(request.PathValue("itemID"))
	if batchErr != nil || itemErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch or item ID is invalid")
		return
	}
	var input struct {
		ReviewState       string `json:"review_state"`
		MediaID           string `json:"media_id"`
		RootID            string `json:"root_id"`
		QualityProfileID  string `json:"quality_profile_id"`
		ProposedRelative  string `json:"proposed_relative"`
		Variant           string `json:"variant"`
		MonitoringEnabled bool   `json:"monitoring_enabled"`
		SearchImmediately bool   `json:"search_immediately"`
	}
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	mediaID, mediaErr := optionalUUID(input.MediaID)
	rootID, rootErr := optionalUUID(input.RootID)
	profileID, profileErr := optionalUUID(input.QualityProfileID)
	if mediaErr != nil || rootErr != nil || profileErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "review IDs are invalid")
		return
	}
	err := server.store.ReviewAdoptionItem(request.Context(), batchID, itemID, postgres.AdoptionReview{State: input.ReviewState, MediaID: mediaID, RootID: rootID, QualityProfileID: profileID, ProposedRelative: input.ProposedRelative, Variant: input.Variant, MonitoringEnabled: input.MonitoringEnabled, SearchImmediately: input.SearchImmediately}, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch or item was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "review_failed", err.Error())
		return
	}
	item, _ := server.store.AdoptionBatch(request.Context(), batchID)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) approveAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch ID is invalid")
		return
	}
	err = server.store.ApproveAdoptionBatch(request.Context(), id, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "approval_failed", err.Error())
		return
	}
	item, _ := server.store.AdoptionBatch(request.Context(), id)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) executeAdoptionBatch(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "adoption batch ID is invalid")
		return
	}
	var input struct {
		Confirmation                   string `json:"confirmation"`
		AdoptedReplacementConfirmation string `json:"adopted_replacement_confirmation"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil || input.Confirmation != "EXECUTE REVIEWED ADOPTION" {
		writeError(writer, http.StatusBadRequest, "confirmation_required", "exact adoption execution confirmation is required")
		return
	}
	err = server.store.QueueAdoptionExecution(request.Context(), id, input.AdoptedReplacementConfirmation, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "adoption batch was not found")
		return
	}
	if errors.Is(err, postgres.ErrAdoptedReplacementConfirmationRequired) {
		writeError(writer, http.StatusConflict, "replacement_confirmation_required", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "execution_failed", err.Error())
		return
	}
	item, _ := server.store.AdoptionBatch(request.Context(), id)
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: item})
}

func (server *Server) listReleaseBlocks(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListReleaseBlocks(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load release blocks")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) createReleaseBlock(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		CandidateID   string `json:"candidate_id"`
		Scope         string `json:"scope"`
		Reason        string `json:"reason"`
		ExpiresAt     string `json:"expires_at"`
		RetentionDays int    `json:"retention_days"`
	}
	if err := decodeJSON(request, &input, 32<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	candidateID, err := uuid.Parse(input.CandidateID)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_candidate", "candidate ID is invalid")
		return
	}
	var expiresAt *time.Time
	if strings.TrimSpace(input.ExpiresAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, input.ExpiresAt)
		if parseErr != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_expiry", "expiry must be an RFC 3339 timestamp")
			return
		}
		expiresAt = &parsed
	} else if input.RetentionDays != 0 {
		if input.RetentionDays != 30 && input.RetentionDays != 90 {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_expiry", "retention must be 30 days, 90 days, or manual")
			return
		}
		value := time.Now().UTC().Add(time.Duration(input.RetentionDays) * 24 * time.Hour)
		expiresAt = &value
	}
	item, err := server.store.CreateReleaseBlockFromCandidate(request.Context(), candidateID, input.Scope, input.Reason, expiresAt, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "release candidate was not found")
		return
	}
	if errors.Is(err, postgres.ErrReleaseBlockExists) {
		writeError(writer, http.StatusConflict, "already_blocked", "this release scope is already blocked")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_block", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/blocklist/"+item.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: item})
}

func (server *Server) deleteReleaseBlock(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "release block ID is invalid")
		return
	}
	if err := server.store.DeleteReleaseBlock(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "release block was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not remove release block")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) discoverySearch(writer http.ResponseWriter, request *http.Request) {
	if server.config.MetadataProvider == nil {
		writeError(writer, http.StatusServiceUnavailable, "metadata_unavailable", "Seerr metadata discovery is not configured")
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	page, _ := strconv.Atoi(request.URL.Query().Get("page"))
	if page == 0 {
		page = 1
	}
	if len(query) < 2 || len(query) > 200 || page < 1 || page > 100 {
		writeError(writer, http.StatusBadRequest, "invalid_query", "query must contain 2 to 200 characters and page must be between 1 and 100")
		return
	}
	result, err := server.config.MetadataProvider.Search(request.Context(), query, page)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "metadata_error", "Seerr metadata search failed")
		return
	}
	filtered := result.Results[:0]
	for _, item := range result.Results {
		if item.ID > 0 && (item.MediaType == "movie" || item.MediaType == "tv") {
			filtered = append(filtered, item)
		}
	}
	result.Results = filtered
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: result})
}

func (server *Server) discoveryMetadata(writer http.ResponseWriter, request *http.Request) {
	if server.config.MetadataProvider == nil {
		writeError(writer, http.StatusServiceUnavailable, "metadata_unavailable", "Seerr metadata discovery is not configured")
		return
	}
	kind := request.PathValue("kind")
	if kind != "movie" && kind != "tv" {
		writeError(writer, http.StatusBadRequest, "invalid_kind", "metadata kind must be movie or tv")
		return
	}
	tmdbID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || tmdbID <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_id", "TMDB ID is invalid")
		return
	}
	metadata, err := server.config.MetadataProvider.Metadata(request.Context(), kind, tmdbID)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "metadata_error", "Seerr metadata lookup failed")
		return
	}
	title, date := metadata.Title, metadata.ReleaseDate
	mediaKind := catalog.KindMovie
	if kind == "tv" {
		title, date, mediaKind = metadata.Name, metadata.FirstAirDate, catalog.KindSeries
	}
	identities := []catalog.ExternalIdentity{{Namespace: "tmdb", Value: strconv.FormatInt(tmdbID, 10), State: catalog.IdentityVerified, Source: "seerr_metadata"}}
	if metadata.ExternalIDs.TVDBID != nil && *metadata.ExternalIDs.TVDBID > 0 {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "tvdb", Value: strconv.FormatInt(*metadata.ExternalIDs.TVDBID, 10), State: catalog.IdentityVerified, Source: "seerr_metadata"})
	}
	if strings.TrimSpace(metadata.ExternalIDs.IMDbID) != "" {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "imdb", Value: metadata.ExternalIDs.IMDbID, State: catalog.IdentityVerified, Source: "seerr_metadata"})
	}
	enricher := server.config.MetadataEnricher
	if enricher == nil {
		enricher = &metadataservice.Service{}
	}
	enriched, err := enricher.Build(request.Context(), mediaKind, tmdbID, metadata, identities, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusBadGateway, "metadata_error", "metadata response could not be normalized safely")
		return
	}
	identities = enriched.Identities
	var year *uint16
	if parsed, parseErr := time.Parse("2006-01-02", date); parseErr == nil && parsed.Year() >= 1800 && parsed.Year() <= 9999 {
		value := uint16(parsed.Year())
		year = &value
	}
	response := map[string]any{"kind": mediaKind, "title": title, "year": year, "identities": identities, "tmdb_id": tmdbID, "metadata": enriched.Metadata, "enrichment_state": enriched.EnrichmentState}
	if rawSeason := request.URL.Query().Get("season"); rawSeason != "" {
		if kind != "tv" {
			writeError(writer, http.StatusBadRequest, "invalid_season", "season metadata is available only for TV")
			return
		}
		season, parseErr := strconv.ParseInt(rawSeason, 10, 16)
		if parseErr != nil || season < 0 {
			writeError(writer, http.StatusBadRequest, "invalid_season", "season number is invalid")
			return
		}
		details, detailsErr := server.config.MetadataProvider.SeasonMetadata(request.Context(), tmdbID, int16(season))
		if detailsErr != nil {
			writeError(writer, http.StatusBadGateway, "metadata_error", "Seerr season metadata lookup failed")
			return
		}
		episodes := make([]map[string]any, 0, len(details.Episodes))
		for _, episode := range details.Episodes {
			if episode.EpisodeNumber <= 0 {
				continue
			}
			episodes = append(episodes, map[string]any{"number": episode.EpisodeNumber, "provider_id": "tmdb:" + strconv.FormatInt(episode.ID, 10), "title": episode.Name, "air_date": episode.AirDate})
		}
		response["season"] = map[string]any{"number": details.SeasonNumber, "episodes": episodes}
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: response})
}

func (server *Server) refreshMediaMetadata(writer http.ResponseWriter, request *http.Request) {
	if server.config.MetadataProvider == nil {
		writeError(writer, http.StatusServiceUnavailable, "metadata_unavailable", "Seerr metadata discovery is not configured")
		return
	}
	mediaID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	item, err := server.store.GetMedia(request.Context(), mediaID)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load media")
		return
	}
	var tmdbID int64
	for _, identity := range item.Identities {
		if identity.Namespace == "tmdb" && identity.State == catalog.IdentityVerified {
			tmdbID, _ = strconv.ParseInt(identity.Value, 10, 64)
			break
		}
	}
	if tmdbID <= 0 {
		writeError(writer, http.StatusUnprocessableEntity, "tmdb_identity_required", "metadata refresh requires a verified TMDB identity")
		return
	}
	providerKind := "movie"
	if item.Kind == catalog.KindSeries {
		providerKind = "tv"
	}
	providerMetadata, err := server.config.MetadataProvider.Metadata(request.Context(), providerKind, tmdbID)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "metadata_error", "Seerr metadata lookup failed")
		return
	}
	identities := append([]catalog.ExternalIdentity(nil), item.Identities...)
	if providerMetadata.ExternalIDs.TVDBID != nil && *providerMetadata.ExternalIDs.TVDBID > 0 && !identityNamespacePresent(identities, "tvdb") {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "tvdb", Value: strconv.FormatInt(*providerMetadata.ExternalIDs.TVDBID, 10), State: catalog.IdentityVerified, Source: "seerr_metadata"})
	}
	if strings.TrimSpace(providerMetadata.ExternalIDs.IMDbID) != "" && !identityNamespacePresent(identities, "imdb") {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "imdb", Value: providerMetadata.ExternalIDs.IMDbID, State: catalog.IdentityVerified, Source: "seerr_metadata"})
	}
	enricher := server.config.MetadataEnricher
	if enricher == nil {
		enricher = &metadataservice.Service{}
	}
	enriched, err := enricher.Build(request.Context(), item.Kind, tmdbID, providerMetadata, identities, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusBadGateway, "metadata_error", "metadata response could not be normalized safely")
		return
	}
	updated, err := server.store.RefreshMediaMetadata(request.Context(), item.ID, enriched.Metadata, enriched.Identities, "owner", time.Now().UTC())
	if errors.Is(err, postgres.ErrIdentityNeedsAttention) {
		writeError(writer, http.StatusConflict, "identity_conflict", "provider identities conflict with the existing catalog")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "metadata_persistence_failed", "metadata could not be persisted")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"media": updated, "enrichment_state": enriched.EnrichmentState}})
}

func identityNamespacePresent(identities []catalog.ExternalIdentity, namespace string) bool {
	for _, identity := range identities {
		if identity.Namespace == namespace {
			return true
		}
	}
	return false
}

func (server *Server) getMediaDetail(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	detail, err := server.store.MediaDetail(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load media detail")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: detail})
}

func (server *Server) listWanted(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListWanted(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load wanted media")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListTaskStatuses(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load worker tasks")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"event_types": items,
		"recurring": []map[string]any{
			{"name": "Recycle eligibility", "schedule": "hourly", "automatic": true},
			{"name": "Seerr reconciliation", "schedule": "every 5 minutes", "automatic": server.config.SeerrEnabled},
		},
	}})
}

func (server *Server) systemInfo(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"version":                  server.config.Version,
		"owner_only":               true,
		"browser_auth":             "gateway_basic_auth",
		"api_auth":                 "server_injected_owner_bearer",
		"seerr_webhook_enabled":    server.config.SeerrEnabled,
		"gmd_metadata_enabled":     server.config.GMDMetadataEnabled,
		"altmount_source_policy":   "read_only",
		"publication_type":         "plex_safe_symlink",
		"upgrade_replacement_gate": "plex_path_and_identity_confirmation",
		"recycle_retention_days":   30,
		"public_edge_owner":        "Cosmos Cloud",
		"machine_api_keys":         false,
		"api_prefix":               "/api/v1",
		"checked_at":               time.Now().UTC(),
	}})
}

func (server *Server) dashboard(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := server.store.Dashboard(request.Context(), time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load dashboard")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: snapshot})
}

func (server *Server) listActivity(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListActivity(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load activity")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) listManagedFiles(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListManagedFiles(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load managed files")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) getManagedFile(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "managed file ID is invalid")
		return
	}
	item, err := server.store.ManagedFile(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "managed file was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load managed file")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) listReleaseCandidates(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListReleaseCandidates(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load release intelligence")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) listSources(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListSourceStatuses(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load sources")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"count": len(items)}})
}

func (server *Server) systemHealth(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	snapshot, err := server.store.SystemHealth(ctx, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "health_unavailable", "component health could not be loaded")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: snapshot})
}

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
	type candidateResponse struct {
		postgres.ProwlarrIndexerCandidate
		APIKey string `json:"api_key,omitempty"`
	}
	keys := make(map[string]string, len(parsed.Candidates))
	for _, candidate := range parsed.Candidates {
		keys[candidate.Name+"\x00"+candidate.BaseURL] = candidate.APIKey
	}
	response := struct {
		ID            uuid.UUID           `json:"id"`
		State         string              `json:"state"`
		ParserVersion string              `json:"parser_version"`
		CreatedAt     time.Time           `json:"created_at"`
		Candidates    []candidateResponse `json:"candidates"`
	}{ID: created.ID, State: created.State, ParserVersion: created.ParserVersion, CreatedAt: created.CreatedAt, Candidates: make([]candidateResponse, 0, len(created.Candidates))}
	for _, candidate := range created.Candidates {
		response.Candidates = append(response.Candidates, candidateResponse{ProwlarrIndexerCandidate: candidate, APIKey: keys[candidate.Name+"\x00"+candidate.BaseURL]})
	}
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
	var input struct {
		Candidates []struct {
			ID        string `json:"id"`
			SecretRef string `json:"secret_ref"`
		} `json:"candidates"`
	}
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

func (server *Server) getMedia(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	item, err := server.store.GetMedia(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load media")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: item})
}

func (server *Server) listWorkflows(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	activeOnly := request.URL.Query().Get("active") != "false"
	items, err := server.store.ListWorkflowSummaries(request.Context(), limit, activeOnly)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflows")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]any{"count": len(items), "active_only": activeOnly}})
}

func (server *Server) createMediaWorkflow(writer http.ResponseWriter, request *http.Request) {
	mediaID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	item, err := server.store.GetMedia(request.Context(), mediaID)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load media")
		return
	}
	var input struct {
		RootID                         string `json:"root_id"`
		QualityProfileID               string `json:"quality_profile_id"`
		DownloadClientID               string `json:"download_client_id"`
		AdoptedReplacementConfirmation string `json:"adopted_replacement_confirmation"`
		SearchImmediately              *bool  `json:"search_immediately"`
		Season                         *struct {
			Number            uint16                   `json:"number"`
			Episodes          []postgres.ManualEpisode `json:"episodes"`
			Monitored         *bool                    `json:"monitored"`
			SearchImmediately *bool                    `json:"search_immediately"`
			PreferSeasonPack  *bool                    `json:"prefer_season_pack"`
		} `json:"season"`
	}
	if err := decodeJSON(request, &input, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, rootErr := optionalUUID(input.RootID)
	profileID, profileErr := optionalUUID(input.QualityProfileID)
	clientID, clientErr := optionalUUID(input.DownloadClientID)
	if rootErr != nil || profileErr != nil || clientErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "configuration IDs are invalid")
		return
	}
	resolvedRoot, resolvedProfile, resolvedClient, err := server.store.ResolveMediaConfiguration(request.Context(), item.Kind, rootID, profileID, clientID)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "enabled defaults or overrides are missing")
		return
	}
	search := true
	if input.SearchImmediately != nil {
		search = *input.SearchImmediately
	}
	var season *postgres.ManualSeason
	if input.Season != nil {
		monitored, prefer := true, true
		if input.Season.Monitored != nil {
			monitored = *input.Season.Monitored
		}
		if input.Season.PreferSeasonPack != nil {
			prefer = *input.Season.PreferSeasonPack
		}
		if input.Season.SearchImmediately != nil {
			search = *input.Season.SearchImmediately
		}
		season = &postgres.ManualSeason{Number: input.Season.Number, Episodes: input.Season.Episodes, Monitored: monitored, SearchImmediately: search, PreferSeasonPack: prefer}
	}
	workflow, err := server.store.CreateManualWorkflowForExisting(request.Context(), item, resolvedRoot, resolvedProfile, resolvedClient, season, search, input.AdoptedReplacementConfirmation, "owner", time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusConflict, "workflow_create_failed", err.Error())
		return
	}
	writer.Header().Set("Location", "/api/v1/workflows/"+workflow.ID.String())
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: workflow})
}

func (server *Server) searchWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	var input struct {
		AdoptedReplacementConfirmation string `json:"adopted_replacement_confirmation"`
	}
	if request.ContentLength != 0 {
		if err := decodeJSON(request, &input, 8<<10); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
			return
		}
	}
	if err := server.store.RequestWorkflowSearch(request.Context(), id, input.AdoptedReplacementConfirmation, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "search_queued", "id": id.String()}})
}

func (server *Server) recoverAltMountSubmission(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	workflow, err := server.store.GetWorkflow(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflow")
		return
	}
	if workflow.State != workflows.StateSubmitting || workflow.GrabAttemptID == nil {
		writeError(writer, http.StatusConflict, "recovery_failed", "workflow does not have an unresolved AltMount submission")
		return
	}
	if err := server.store.MarkAltMountUnknown(request.Context(), workflow.ID, *workflow.GrabAttemptID, time.Now().UTC()); err != nil {
		writeError(writer, http.StatusConflict, "recovery_failed", "AltMount submission recovery could not be queued")
		return
	}
	workflow, err = server.store.GetWorkflow(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not reload workflow")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: workflow})
}

func (server *Server) reconcileAltMount(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	if err := server.store.RequestAltMountReconciliation(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow or current AltMount job was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "reconciliation_failed", "current AltMount job cannot be reconciled safely")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "reconciliation_queued", "id": id.String()}})
}

func (server *Server) cancelWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	workflow, err := server.store.GetWorkflow(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflow")
		return
	}
	if workflow.State != workflows.StateMonitored && workflow.State != workflows.StateWaiting && workflow.State != workflows.StateSearching && workflow.State != workflows.StateCandidateSelected && workflow.State != workflows.StateHeld && workflow.State != workflows.StateBlocked {
		writeError(writer, http.StatusConflict, "cancel_failed", "workflow can no longer be cancelled safely")
		return
	}
	workflow, err = server.store.TransitionWorkflow(request.Context(), workflow.ID, workflow.Revision, workflows.StateCancelled, "owner cancelled before retrieval", "owner", time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusConflict, "cancel_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: workflow})
}

func (server *Server) live(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"status": "live", "version": server.config.Version, "time": time.Now().UTC()}})
}

func (server *Server) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := server.store.Ready(ctx); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "not_ready", "database or schema is not ready")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"status": "ready", "since": server.readySince}})
}

func (server *Server) listMedia(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
	items, err := server.store.ListMedia(request.Context(), limit, offset)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load media")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items, Meta: map[string]int{"limit": limit, "offset": offset, "count": len(items)}})
}

func (server *Server) createMedia(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Kind              catalog.MediaKind          `json:"kind"`
		Title             string                     `json:"title"`
		Year              *uint16                    `json:"year"`
		Identities        []catalog.ExternalIdentity `json:"identities"`
		Metadata          *catalog.MediaMetadata     `json:"metadata"`
		RootID            string                     `json:"root_id"`
		QualityProfileID  string                     `json:"quality_profile_id"`
		DownloadClientID  string                     `json:"download_client_id"`
		SearchImmediately *bool                      `json:"search_immediately"`
		Seasons           []struct {
			Number            uint16                   `json:"number"`
			Episodes          []postgres.ManualEpisode `json:"episodes"`
			Monitored         *bool                    `json:"monitored"`
			SearchImmediately *bool                    `json:"search_immediately"`
			PreferSeasonPack  *bool                    `json:"prefer_season_pack"`
		} `json:"seasons"`
	}
	if err := decodeJSON(request, &input, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	rootID, rootErr := optionalUUID(input.RootID)
	profileID, profileErr := optionalUUID(input.QualityProfileID)
	clientID, clientErr := optionalUUID(input.DownloadClientID)
	if rootErr != nil || profileErr != nil || clientErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "configuration IDs are invalid")
		return
	}
	now := time.Now().UTC()
	for index := range input.Identities {
		if strings.TrimSpace(input.Identities[index].Source) == "" {
			input.Identities[index].Source = "manual"
		}
	}
	item, err := catalog.New(input.Kind, input.Title, input.Year, input.Identities, now)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_media", err.Error())
		return
	}
	item.Metadata, err = catalog.NormalizeMetadata(input.Metadata, now)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata", "metadata is outside the supported bounds")
		return
	}
	if item.Year == nil || !hasPublicationIdentity(item) {
		writeError(writer, http.StatusUnprocessableEntity, "publication_identity_required", "a year and verified Plex-supported identity are required before searching")
		return
	}
	resolvedRoot, resolvedProfile, resolvedClient, err := server.store.ResolveMediaConfiguration(request.Context(), item.Kind, rootID, profileID, clientID)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_configuration", "enabled defaults or overrides are missing")
		return
	}
	seasons := make([]postgres.ManualSeason, 0, len(input.Seasons))
	for _, season := range input.Seasons {
		monitored, search, prefer := true, true, true
		if season.Monitored != nil {
			monitored = *season.Monitored
		}
		if season.SearchImmediately != nil {
			search = *season.SearchImmediately
		}
		if season.PreferSeasonPack != nil {
			prefer = *season.PreferSeasonPack
		}
		seasons = append(seasons, postgres.ManualSeason{Number: season.Number, Episodes: season.Episodes, Monitored: monitored, SearchImmediately: search, PreferSeasonPack: prefer})
	}
	searchMovie := true
	if input.SearchImmediately != nil {
		searchMovie = *input.SearchImmediately
	}
	created, err := server.store.CreateManualMedia(request.Context(), item, resolvedRoot, resolvedProfile, resolvedClient, seasons, searchMovie, "owner")
	if err != nil {
		writeError(writer, http.StatusConflict, "create_failed", err.Error())
		return
	}
	if len(created) > 0 {
		writer.Header().Set("Location", "/api/v1/workflows/"+created[0].ID.String())
	}
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: map[string]any{"media": item, "workflows": created}})
}

func hasPublicationIdentity(item catalog.MediaItem) bool {
	for _, identity := range item.Identities {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		if item.Kind == catalog.KindMovie && (identity.Namespace == "tmdb" || identity.Namespace == "imdb") {
			return true
		}
		if item.Kind == catalog.KindSeries && (identity.Namespace == "tvdb" || identity.Namespace == "tmdb") {
			return true
		}
	}
	return false
}

func (server *Server) getWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "workflow ID is invalid")
		return
	}
	workflow, err := server.store.GetWorkflow(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load workflow")
		return
	}
	writer.Header().Set("ETag", `"`+strconv.FormatUint(workflow.Revision, 10)+`"`)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: workflow})
}

func (server *Server) recycleMedia(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	entries, err := server.store.PlanMediaRecycle(request.Context(), id, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "recycle_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: entries})
}

func (server *Server) listRecycle(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListRecycleEntries(request.Context(), 200)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load recycle entries")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) restoreRecycle(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "recycle entry ID is invalid")
		return
	}
	if err := server.store.RequestRecycleRestore(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "recycle entry was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "restore_failed", "entry is not restorable")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "restore_queued", "id": id.String()}})
}

func (server *Server) confirmRealFilePurge(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "recycle entry ID is invalid")
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if err := server.store.RequestRealFilePurge(request.Context(), id, input.Confirmation, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "recycle entry was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "purge_not_confirmed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "real_file_purge_queued", "id": id.String()}})
}

func (server *Server) restoreAdoptedFiles(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "recycle entry ID is invalid")
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if err := server.store.RequestAdoptedRestore(request.Context(), id, input.Confirmation, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "recycle entry was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "restore_not_confirmed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "adopted_restore_queued", "id": id.String()}})
}

func (server *Server) listIntegrations(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListIntegrations(request.Context())
	if err != nil {
		writeError(writer, 500, "database_error", "could not load integrations")
		return
	}
	writeJSON(writer, 200, dataEnvelope{Data: items})
}
func (server *Server) createIntegration(writer http.ResponseWriter, request *http.Request) {
	var item postgres.Integration
	if err := decodeJSON(request, &item, 256<<10); err != nil {
		writeError(writer, 400, "invalid_request", "request body is invalid")
		return
	}
	created, err := server.store.CreateIntegration(request.Context(), item)
	if err != nil {
		if errors.Is(err, postgres.ErrDuplicateIndexerURL) {
			writeError(writer, http.StatusConflict, "duplicate_indexer_url", err.Error())
			return
		}
		writeError(writer, 422, "invalid_integration", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]string{"warning": "setting saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) commissionIntegration(writer http.ResponseWriter, request *http.Request) {
	if server.config.CommissionNewznab == nil {
		writeError(writer, http.StatusServiceUnavailable, "commissioning_unavailable", "Newznab commissioning is unavailable")
		return
	}
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	integration, err := server.config.CommissionNewznab(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "integration was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadGateway, "commission_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: integration})
}
func (server *Server) listLibraryRoots(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListLibraryRoots(request.Context())
	if err != nil {
		writeError(writer, 500, "database_error", "could not load library roots")
		return
	}
	writeJSON(writer, 200, dataEnvelope{Data: items})
}
func (server *Server) createLibraryRoot(writer http.ResponseWriter, request *http.Request) {
	var item postgres.LibraryRoot
	if err := decodeJSON(request, &item, 256<<10); err != nil {
		writeError(writer, 400, "invalid_request", "request body is invalid")
		return
	}
	created, err := server.store.CreateLibraryRoot(request.Context(), item)
	if err != nil {
		writeError(writer, 422, "invalid_library_root", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]string{"warning": "setting saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}
func (server *Server) listQualityProfiles(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListQualityProfiles(request.Context())
	if err != nil {
		writeError(writer, 500, "database_error", "could not load quality profiles")
		return
	}
	writeJSON(writer, 200, dataEnvelope{Data: items})
}
func (server *Server) createQualityProfile(writer http.ResponseWriter, request *http.Request) {
	var item postgres.QualityProfile
	if err := decodeJSON(request, &item, 512<<10); err != nil {
		writeError(writer, 400, "invalid_request", "request body is invalid")
		return
	}
	created, err := server.store.CreateQualityProfile(request.Context(), item)
	if err != nil {
		writeError(writer, 422, "invalid_quality_profile", err.Error())
		return
	}
	requeued, requeueErr := server.store.RequeueSeerrNeedsConfiguration(request.Context(), time.Now().UTC())
	if requeueErr != nil {
		writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]string{"warning": "setting saved; Seerr requests could not be requeued"}})
		return
	}
	writeJSON(writer, 201, dataEnvelope{Data: created, Meta: map[string]int{"seerr_requests_requeued": requeued}})
}

func (server *Server) updateQualityProfile(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "quality profile ID is invalid")
		return
	}
	var item postgres.QualityProfile
	if err := decodeJSON(request, &item, 512<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	item.ID = id
	updated, err := server.store.UpdateQualityProfile(request.Context(), item)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "quality profile was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_quality_profile", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: updated})
}

func (server *Server) listSubtitleVerifications(writer http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	items, err := server.store.ListSubtitleVerifications(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load subtitle verifications")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: items})
}

func (server *Server) seerrWebhook(writer http.ResponseWriter, request *http.Request) {
	if !server.config.SeerrEnabled || len(server.config.SeerrWebhookSecret) < 32 || server.config.SeerrInstanceID == "" {
		writeError(writer, http.StatusServiceUnavailable, "not_configured", "Seerr webhook intake is not configured")
		return
	}
	if err := seerr.AuthenticateWebhook(request.Header.Get("Authorization"), server.config.SeerrWebhookSecret); err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "webhook authorization failed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, (256<<10)+1))
	if err != nil || len(body) > 256<<10 {
		writeError(writer, http.StatusRequestEntityTooLarge, "payload_too_large", "webhook body exceeds 256 KiB")
		return
	}
	event, err := seerr.ParseWebhook(body)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_event", "webhook payload is invalid")
		return
	}
	created, err := server.store.RecordSeerrDelivery(request.Context(), server.config.SeerrInstanceID, event.RequestID.String(), request.Header.Get("X-Seerr-Event-ID"), body, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "intake_failed", "webhook could not be stored")
		return
	}
	if !created {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "accepted", "request_id": event.RequestID.String()}})
}

func (server *Server) requireOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "Bearer "
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, prefix) || !secureEqual(strings.TrimPrefix(authorization, prefix), server.config.OwnerToken) {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "owner authorization is required")
			return
		}
		next(writer, request)
	}
}

func (server *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

func secureEqual(actual, expected string) bool {
	left, right := sha256.Sum256([]byte(actual)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func optionalUUID(value string) (*uuid.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func decodeJSON(request *http.Request, output any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON documents")
	}
	return nil
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	response := errorEnvelope{}
	response.Error.Code, response.Error.Message = code, message
	writeJSON(writer, status, response)
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
