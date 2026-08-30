package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
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
	CommissionNewznab  func(context.Context, uuid.UUID) (postgres.Integration, error)
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
	server.mux.HandleFunc("GET /api/v1/discovery/search", server.requireOwner(server.discoverySearch))
	server.mux.HandleFunc("GET /api/v1/discovery/{kind}/{id}", server.requireOwner(server.discoveryMetadata))
	server.mux.HandleFunc("GET /api/v1/media", server.requireOwner(server.listMedia))
	server.mux.HandleFunc("POST /api/v1/media", server.requireOwner(server.createMedia))
	server.mux.HandleFunc("GET /api/v1/media/{id}", server.requireOwner(server.getMedia))
	server.mux.HandleFunc("GET /api/v1/media/{id}/detail", server.requireOwner(server.getMediaDetail))
	server.mux.HandleFunc("DELETE /api/v1/media/{id}", server.requireOwner(server.recycleMedia))
	server.mux.HandleFunc("POST /api/v1/media/{id}/workflows", server.requireOwner(server.createMediaWorkflow))
	server.mux.HandleFunc("GET /api/v1/workflows", server.requireOwner(server.listWorkflows))
	server.mux.HandleFunc("GET /api/v1/workflows/{id}", server.requireOwner(server.getWorkflow))
	server.mux.HandleFunc("POST /api/v1/workflows/{id}/search", server.requireOwner(server.searchWorkflow))
	server.mux.HandleFunc("POST /api/v1/workflows/{id}/cancel", server.requireOwner(server.cancelWorkflow))
	server.mux.HandleFunc("GET /api/v1/activity", server.requireOwner(server.listActivity))
	server.mux.HandleFunc("GET /api/v1/files", server.requireOwner(server.listManagedFiles))
	server.mux.HandleFunc("GET /api/v1/files/{id}", server.requireOwner(server.getManagedFile))
	server.mux.HandleFunc("GET /api/v1/intelligence/releases", server.requireOwner(server.listReleaseCandidates))
	server.mux.HandleFunc("GET /api/v1/wanted", server.requireOwner(server.listWanted))
	server.mux.HandleFunc("GET /api/v1/sources", server.requireOwner(server.listSources))
	server.mux.HandleFunc("GET /api/v1/system/health", server.requireOwner(server.systemHealth))
	server.mux.HandleFunc("GET /api/v1/system/tasks", server.requireOwner(server.listTasks))
	server.mux.HandleFunc("GET /api/v1/system/info", server.requireOwner(server.systemInfo))
	server.mux.HandleFunc("GET /api/v1/recycle", server.requireOwner(server.listRecycle))
	server.mux.HandleFunc("POST /api/v1/recycle/{id}/restore", server.requireOwner(server.restoreRecycle))
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
	server.mux.HandleFunc("POST /api/v1/integrations/seerr/webhook", server.seerrWebhook)
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
	var year *uint16
	if parsed, parseErr := time.Parse("2006-01-02", date); parseErr == nil && parsed.Year() >= 1800 && parsed.Year() <= 9999 {
		value := uint16(parsed.Year())
		year = &value
	}
	response := map[string]any{"kind": mediaKind, "title": title, "year": year, "identities": identities, "tmdb_id": tmdbID}
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
		RootID            string `json:"root_id"`
		QualityProfileID  string `json:"quality_profile_id"`
		DownloadClientID  string `json:"download_client_id"`
		SearchImmediately *bool  `json:"search_immediately"`
		Season            *struct {
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
	workflow, err := server.store.CreateManualWorkflowForExisting(request.Context(), item, resolvedRoot, resolvedProfile, resolvedClient, season, search, "owner", time.Now().UTC())
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
	if err := server.store.RequestWorkflowSearch(request.Context(), id, time.Now().UTC()); errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "workflow was not found")
		return
	} else if err != nil {
		writeError(writer, http.StatusConflict, "search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]string{"status": "search_queued", "id": id.String()}})
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
