package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/servarr"
)

// Media Manager implements the subset of the Radarr and Sonarr APIs that Seerr
// uses to configure a server, add requests, and scan availability. Seerr talks
// to /radarr/api/v3 and /sonarr/api/v3 on the API container, so Radarr and
// Sonarr are no longer part of the stack. The actual acquisition stays in
// Media Manager's own workflow engine: Seerr still records the request and the
// existing Seerr intake (webhook/poll) turns it into a workflow.

func (server *Server) requireServarr(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		configured := strings.TrimSpace(server.config.ServarrAPIKey)
		if configured == "" {
			writeError(writer, http.StatusServiceUnavailable, "not_configured", "Media Manager's Radarr/Sonarr-compatible API is not configured")
			return
		}
		provided := strings.TrimSpace(request.Header.Get("X-Api-Key"))
		if provided == "" {
			provided = strings.TrimSpace(request.URL.Query().Get("apikey"))
		}
		if !secureEqual(provided, configured) {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "apikey is required")
			return
		}
		next(writer, request)
	}
}

func servarrFlavor(request *http.Request) string {
	if strings.HasPrefix(request.URL.Path, "/sonarr/") {
		return "sonarr"
	}
	return "radarr"
}

func (server *Server) registerServarrRoutes() {
	for _, prefix := range []string{"/radarr", "/sonarr"} {
		server.mux.HandleFunc("GET "+prefix+"/api/v3/system/status", server.requireServarr(server.servarrSystemStatus))
		server.mux.HandleFunc("GET "+prefix+"/api/v3/qualityProfile", server.requireServarr(server.servarrQualityProfiles))
		server.mux.HandleFunc("GET "+prefix+"/api/v3/qualityprofile", server.requireServarr(server.servarrQualityProfiles))
		server.mux.HandleFunc("GET "+prefix+"/api/v3/rootfolder", server.requireServarr(server.servarrRootFolders))
		server.mux.HandleFunc("GET "+prefix+"/api/v3/tag", server.requireServarr(server.servarrTags))
		server.mux.HandleFunc("GET "+prefix+"/api/v3/queue", server.requireServarr(server.servarrQueue))
		server.mux.HandleFunc("POST "+prefix+"/api/v3/command", server.requireServarr(server.servarrCommand))
	}
	server.mux.HandleFunc("GET /radarr/api/v3/movie", server.requireServarr(server.servarrMovies))
	server.mux.HandleFunc("GET /radarr/api/v3/movie/lookup", server.requireServarr(server.servarrMovieLookup))
	server.mux.HandleFunc("GET /radarr/api/v3/movie/{id}", server.requireServarr(server.servarrMovieByID))
	server.mux.HandleFunc("POST /radarr/api/v3/movie", server.requireServarr(server.servarrAddMovie))
	server.mux.HandleFunc("PUT /radarr/api/v3/movie", server.requireServarr(server.servarrUpdateMovie))
	server.mux.HandleFunc("DELETE /radarr/api/v3/movie/{id}", server.requireServarr(server.servarrDeleteMovie))

	server.mux.HandleFunc("GET /sonarr/api/v3/languageprofile", server.requireServarr(server.servarrLanguageProfiles))
	server.mux.HandleFunc("GET /sonarr/api/v3/series", server.requireServarr(server.servarrSeries))
	server.mux.HandleFunc("GET /sonarr/api/v3/series/lookup", server.requireServarr(server.servarrSeriesLookup))
	server.mux.HandleFunc("GET /sonarr/api/v3/series/{id}", server.requireServarr(server.servarrSeriesByID))
	server.mux.HandleFunc("POST /sonarr/api/v3/series", server.requireServarr(server.servarrAddSeries))
	server.mux.HandleFunc("PUT /sonarr/api/v3/series", server.requireServarr(server.servarrUpdateSeries))
	server.mux.HandleFunc("DELETE /sonarr/api/v3/series/{id}", server.requireServarr(server.servarrDeleteSeries))
	server.mux.HandleFunc("GET /sonarr/api/v3/episode", server.requireServarr(server.servarrEpisodes))
	server.mux.HandleFunc("PUT /sonarr/api/v3/episode/monitor", server.requireServarr(server.servarrMonitorEpisodes))
}

func (server *Server) servarrSystemStatus(writer http.ResponseWriter, request *http.Request) {
	if servarrFlavor(request) == "sonarr" {
		writeJSON(writer, http.StatusOK, servarr.NewSystemStatus("Sonarr", servarr.SonarrVersion))
		return
	}
	writeJSON(writer, http.StatusOK, servarr.NewSystemStatus("Radarr", servarr.RadarrVersion))
}

func (server *Server) servarrQualityProfiles(writer http.ResponseWriter, request *http.Request) {
	kind := catalog.KindMovie
	if servarrFlavor(request) == "sonarr" {
		kind = catalog.KindSeries
	}
	profiles, err := server.store.ListQualityProfiles(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load quality profiles")
		return
	}
	result := []servarr.QualityProfile{}
	for _, profile := range profiles {
		if !profile.Enabled || profile.MediaKind != kind {
			continue
		}
		result = append(result, servarr.QualityProfile{
			ID:             servarr.SyntheticID("profile", profile.ID.String()),
			Name:           profile.Name,
			UpgradeAllowed: profile.UpgradesEnabled,
			Cutoff:         1,
			Items:          []struct{}{},
		})
	}
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) servarrRootFolders(writer http.ResponseWriter, request *http.Request) {
	kind := catalog.KindMovie
	if servarrFlavor(request) == "sonarr" {
		kind = catalog.KindSeries
	}
	roots, err := server.store.ListLibraryRoots(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load root folders")
		return
	}
	usable := false
	for _, root := range roots {
		if root.Enabled && root.MediaKind == kind && strings.TrimSpace(root.PlexLibraryID) != "" {
			usable = true
			break
		}
	}
	if !usable {
		writeJSON(writer, http.StatusOK, []servarr.RootFolder{})
		return
	}
	// Seerr sees exactly one root folder. Choosing it delegates routing to
	// Media Manager, which picks the themed library root from metadata.
	writeJSON(writer, http.StatusOK, []servarr.RootFolder{{
		ID:              servarr.SyntheticID("root", "auto"),
		Path:            servarr.AutoRootPath,
		Accessible:      true,
		FreeSpace:       0,
		UnmappedFolders: []struct{}{},
	}})
}

// decodeServarrJSON accepts the payloads a real Radarr/Sonarr client sends.
// Those clients include fields Media Manager does not need (profileId,
// minimumAvailability, addOptions, searchNow, seasonFolder, movieIds, ...), so
// unknown fields are ignored while malformed or multi-document bodies stay
// rejected. Only the fields the handler consumes are validated.
func decodeServarrJSON(request *http.Request, output any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, limit+1))
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON documents")
	}
	return nil
}

func (server *Server) servarrTags(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, []servarr.Tag{})
}

func (server *Server) servarrQueue(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, servarr.EmptyQueue())
}

func (server *Server) servarrCommand(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeServarrJSON(request, &body, 64<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "command body is invalid")
		return
	}
	writeJSON(writer, http.StatusCreated, servarr.CommandResponse{ID: 1, Name: body.Name, Status: "completed"})
}

func servarrMovie(movie postgres.ServarrMovie) servarr.Movie {
	year := 0
	if movie.Year != nil {
		year = int(*movie.Year)
	}
	tmdbID, _ := strconv.ParseInt(movie.TMDBID, 10, 64)
	return servarr.Movie{
		ID:        servarr.SyntheticID("movie", movie.TMDBID),
		Title:     movie.Title,
		TMDBID:    tmdbID,
		Year:      year,
		TitleSlug: servarr.Slug(movie.Title),
		Monitored: true,
		HasFile:   movie.HasFile,
		Tags:      []servarr.Tag{},
	}
}

func (server *Server) servarrMovies(writer http.ResponseWriter, request *http.Request) {
	if tmdbID := strings.TrimSpace(request.URL.Query().Get("tmdbId")); tmdbID != "" {
		movie, err := server.store.ServarrMovieByTMDB(request.Context(), tmdbID)
		if errors.Is(err, postgres.ErrNotFound) {
			writeJSON(writer, http.StatusOK, []servarr.Movie{})
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "internal_error", "could not load movie")
			return
		}
		writeJSON(writer, http.StatusOK, []servarr.Movie{servarrMovie(movie)})
		return
	}
	movies, err := server.store.ListServarrMovies(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load movies")
		return
	}
	result := make([]servarr.Movie, 0, len(movies))
	for _, movie := range movies {
		result = append(result, servarrMovie(movie))
	}
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) servarrMovieLookup(writer http.ResponseWriter, request *http.Request) {
	term := strings.TrimSpace(request.URL.Query().Get("term"))
	tmdbID, found := strings.CutPrefix(term, "tmdb:")
	tmdbID = strings.TrimSpace(tmdbID)
	if !found || tmdbID == "" {
		writeJSON(writer, http.StatusOK, []servarr.Movie{})
		return
	}
	movie, err := server.store.ServarrMovieByTMDB(request.Context(), tmdbID)
	if errors.Is(err, postgres.ErrNotFound) {
		number, _ := strconv.ParseInt(tmdbID, 10, 64)
		writeJSON(writer, http.StatusOK, []servarr.Movie{{ID: 0, TMDBID: number, Monitored: false, HasFile: false, Tags: []servarr.Tag{}}})
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load movie")
		return
	}
	writeJSON(writer, http.StatusOK, []servarr.Movie{servarrMovie(movie)})
}

func (server *Server) servarrMovieByID(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "movie id is invalid")
		return
	}
	movies, err := server.store.ListServarrMovies(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load movies")
		return
	}
	for _, movie := range movies {
		if result := servarrMovie(movie); result.ID == id {
			writeJSON(writer, http.StatusOK, result)
			return
		}
	}
	writeError(writer, http.StatusNotFound, "not_found", "movie not found")
}

// servarrAddMovie acknowledges Seerr's add. Media Manager deliberately does not
// create a second catalog row here: Seerr has already persisted the request
// (including the chosen root folder and quality profile), and the existing
// Seerr intake turns that request into the workflow. The returned id only has
// to be stable for Seerr's own bookkeeping.
func (server *Server) servarrAddMovie(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		TMDBID           int64  `json:"tmdbId"`
		Title            string `json:"title"`
		Year             int    `json:"year"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
	}
	if err := decodeServarrJSON(request, &body, 256<<10); err != nil || body.TMDBID <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "movie body is invalid")
		return
	}
	value := strconv.FormatInt(body.TMDBID, 10)
	writeJSON(writer, http.StatusCreated, servarr.Movie{
		ID:               servarr.SyntheticID("movie", value),
		Title:            body.Title,
		TMDBID:           body.TMDBID,
		Year:             body.Year,
		TitleSlug:        servarr.Slug(body.Title),
		Monitored:        true,
		HasFile:          false,
		QualityProfileID: body.QualityProfileID,
		RootFolderPath:   body.RootFolderPath,
		Tags:             []servarr.Tag{},
	})
}

func (server *Server) servarrUpdateMovie(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	if err := decodeServarrJSON(request, &body, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "movie body is invalid")
		return
	}
	body["monitored"] = true
	writeJSON(writer, http.StatusOK, body)
}

func (server *Server) servarrDeleteMovie(writer http.ResponseWriter, request *http.Request) {
	// Removal is reconciled through the Seerr request itself; there is no *arr
	// library row to delete.
	writeJSON(writer, http.StatusOK, map[string]any{})
}

func servarrSeries(series postgres.ServarrSeries) servarr.Series {
	year := 0
	if series.Year != nil {
		year = int(*series.Year)
	}
	tvdbID, _ := strconv.ParseInt(series.TVDBID, 10, 64)
	seasons := make([]servarr.SeriesSeason, 0, len(series.Seasons))
	for _, season := range series.Seasons {
		seasons = append(seasons, servarr.SeriesSeason{
			SeasonNumber: int(season.Number),
			Monitored:    season.Monitored,
			Statistics: servarr.SeasonStatistics{
				EpisodeFileCount:  season.EpisodeFiles,
				EpisodeCount:      season.TotalEpisodes,
				TotalEpisodeCount: season.TotalEpisodes,
			},
		})
	}
	return servarr.Series{
		ID:           servarr.SyntheticID("series", series.TVDBID),
		Title:        series.Title,
		TVDBID:       tvdbID,
		Year:         year,
		TitleSlug:    servarr.Slug(series.Title),
		Monitored:    true,
		SeasonFolder: true,
		Seasons:      seasons,
		Tags:         []servarr.Tag{},
	}
}

func (server *Server) servarrSeries(writer http.ResponseWriter, request *http.Request) {
	if tvdbID := strings.TrimSpace(request.URL.Query().Get("tvdbId")); tvdbID != "" {
		series, err := server.store.ServarrSeriesByTVDB(request.Context(), tvdbID)
		if errors.Is(err, postgres.ErrNotFound) {
			writeJSON(writer, http.StatusOK, []servarr.Series{})
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "internal_error", "could not load series")
			return
		}
		writeJSON(writer, http.StatusOK, []servarr.Series{servarrSeries(series)})
		return
	}
	series, err := server.store.ListServarrSeries(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load series")
		return
	}
	result := make([]servarr.Series, 0, len(series))
	for _, item := range series {
		result = append(result, servarrSeries(item))
	}
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) servarrSeriesLookup(writer http.ResponseWriter, request *http.Request) {
	term := strings.TrimSpace(request.URL.Query().Get("term"))
	tvdbID, found := strings.CutPrefix(term, "tvdb:")
	tvdbID = strings.TrimSpace(tvdbID)
	if !found || tvdbID == "" {
		writeJSON(writer, http.StatusOK, []servarr.Series{})
		return
	}
	series, err := server.store.ServarrSeriesByTVDB(request.Context(), tvdbID)
	if errors.Is(err, postgres.ErrNotFound) {
		number, _ := strconv.ParseInt(tvdbID, 10, 64)
		writeJSON(writer, http.StatusOK, []servarr.Series{{ID: 0, TVDBID: number, Monitored: false, Seasons: []servarr.SeriesSeason{}, Tags: []servarr.Tag{}}})
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load series")
		return
	}
	writeJSON(writer, http.StatusOK, []servarr.Series{servarrSeries(series)})
}

func (server *Server) servarrSeriesByID(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.Atoi(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "series id is invalid")
		return
	}
	series, err := server.store.ListServarrSeries(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "could not load series")
		return
	}
	for _, item := range series {
		if result := servarrSeries(item); result.ID == id {
			writeJSON(writer, http.StatusOK, result)
			return
		}
	}
	writeError(writer, http.StatusNotFound, "not_found", "series not found")
}

func (server *Server) servarrAddSeries(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		TVDBID            int64  `json:"tvdbId"`
		Title             string `json:"title"`
		QualityProfileID  int    `json:"qualityProfileId"`
		LanguageProfileID int    `json:"languageProfileId"`
		RootFolderPath    string `json:"rootFolderPath"`
	}
	if err := decodeServarrJSON(request, &body, 256<<10); err != nil || body.TVDBID <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "series body is invalid")
		return
	}
	value := strconv.FormatInt(body.TVDBID, 10)
	writeJSON(writer, http.StatusCreated, servarr.Series{
		ID:                servarr.SyntheticID("series", value),
		Title:             body.Title,
		TVDBID:            body.TVDBID,
		TitleSlug:         servarr.Slug(body.Title),
		Monitored:         true,
		SeasonFolder:      true,
		QualityProfileID:  body.QualityProfileID,
		LanguageProfileID: body.LanguageProfileID,
		RootFolderPath:    body.RootFolderPath,
		Seasons:           []servarr.SeriesSeason{},
		Tags:              []servarr.Tag{},
	})
}

func (server *Server) servarrUpdateSeries(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	if err := decodeServarrJSON(request, &body, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "series body is invalid")
		return
	}
	body["monitored"] = true
	writeJSON(writer, http.StatusOK, body)
}

func (server *Server) servarrDeleteSeries(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{})
}

// servarrEpisodes returns no synthetic episodes: Seerr only uses the episode
// list to re-monitor seasons on an existing series, and Media Manager's intake
// already knows the exact expected episodes from the Seerr request.
func (server *Server) servarrEpisodes(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, []servarr.Episode{})
}

func (server *Server) servarrMonitorEpisodes(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, []servarr.Episode{})
}

func (server *Server) servarrLanguageProfiles(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, []servarr.LanguageProfile{{ID: 1, Name: "English"}})
}
