package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

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
	discovered, err := server.config.MetadataEnricher.Discover(request.Context(), server.config.MetadataProvider, kind, tmdbID, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusBadGateway, "metadata_error", "Seerr metadata lookup failed or could not be normalized safely")
		return
	}
	mediaKind, title, year, identities, enriched := discovered.Kind, discovered.Title, discovered.Year, discovered.Identities, discovered.Result
	response := map[string]any{"kind": mediaKind, "title": title, "year": year, "identities": identities, "tmdb_id": tmdbID, "metadata": enriched.Metadata, "enrichment_state": enriched.EnrichmentState, "provider_states": enriched.ProviderStates}
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
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{"media": updated, "enrichment_state": enriched.EnrichmentState, "provider_states": enriched.ProviderStates}})
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
	var input createMediaInput
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

// archiveMedia retires a confirmed unwanted media record (for example test or
// acceptance residue) from the active catalog. It cancels the media's
// non-terminal workflows and soft-deletes the media row, but never deletes a
// file, AltMount source, or durable history. It refuses when the media has
// current published files or in-flight work, so legitimate media is protected.
func (server *Server) archiveMedia(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	result, err := server.store.ArchiveMedia(request.Context(), id, time.Now().UTC())
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	case errors.Is(err, postgres.ErrMediaHasPublishedFiles):
		writeError(writer, http.StatusConflict, "archive_refused", "media has current published files; resolve the publication before archiving")
		return
	case errors.Is(err, postgres.ErrMediaHasInFlightWork):
		writeError(writer, http.StatusConflict, "archive_refused", "media has in-flight work that cannot be archived safely")
		return
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "database_error", "media could not be archived")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: result})
}

// unarchiveMedia returns archived media to the active catalog. Workflows the
// archive cancelled stay cancelled; acquisition starts again only when asked.
func (server *Server) unarchiveMedia(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "media ID is invalid")
		return
	}
	result, err := server.store.UnarchiveMedia(request.Context(), id, time.Now().UTC())
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "media was not found")
		return
	case errors.Is(err, postgres.ErrMediaInRecycleBin):
		writeError(writer, http.StatusConflict, "unarchive_refused", "media is in the recycle bin; restore it from there")
		return
	case errors.Is(err, postgres.ErrConcurrentModification):
		writeError(writer, http.StatusConflict, "concurrent_modification", "media changed while it was being restored; try again")
		return
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "database_error", "media could not be restored")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: result})
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
	var input confirmRealFilePurgeInput
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
