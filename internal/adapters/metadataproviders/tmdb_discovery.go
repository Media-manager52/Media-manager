package metadataproviders

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
)

// The methods below let TMDB answer title search and discovery directly when
// no Seerr is connected. They return the same shapes as Seerr's TMDB proxy
// (Seerr's routes are a thin layer over these TMDB routes), so search, the
// title page and adding a title behave the same with or without Seerr. The
// language is en-US, as Seerr's default locale, so a title keeps the same
// catalogue name whichever source found it.

const discoveryLanguage = "en-US"

type tmdbSearchPage struct {
	Page         int `json:"page"`
	TotalPages   int `json:"total_pages"`
	TotalResults int `json:"total_results"`
	Results      []struct {
		ID           int64  `json:"id"`
		MediaType    string `json:"media_type"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		Overview     string `json:"overview"`
		PosterPath   string `json:"poster_path"`
	} `json:"results"`
}

type tmdbSeason struct {
	ID           int64 `json:"id"`
	SeasonNumber int16 `json:"season_number"`
	Episodes     []struct {
		ID            int64  `json:"id"`
		EpisodeNumber int16  `json:"episode_number"`
		Name          string `json:"name"`
		AirDate       string `json:"air_date"`
	} `json:"episodes"`
}

type tmdbDiscoveryPayload struct {
	tmdbPayload
	IMDbID  string `json:"imdb_id"`
	Seasons []struct {
		SeasonNumber int `json:"season_number"`
	} `json:"seasons"`
	// Networks shadows the embedded field to also read each network's ID.
	Networks []struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		OriginCountry string `json:"origin_country"`
	} `json:"networks"`
}

func (client *TMDBClient) get(ctx context.Context, path string, values map[string]string, target any) error {
	endpoint := client.client.endpoint(path)
	query := endpoint.Query()
	for key, value := range values {
		query.Set(key, value)
	}
	// TMDB, like Seerr, wants %20 rather than '+' for spaces in a query.
	endpoint.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ErrProviderUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	response, err := client.client.http.Do(request)
	if err != nil {
		return ErrProviderUnavailable
	}
	return client.client.decode(response, target)
}

// Check confirms that the read access token is accepted.
func (client *TMDBClient) Check(ctx context.Context) error {
	var payload struct {
		Success bool `json:"success"`
	}
	if err := client.get(ctx, "/3/authentication", nil, &payload); err != nil {
		return err
	}
	if !payload.Success {
		return ErrProviderUnavailable
	}
	return nil
}

// Search finds movies and series by title, like Seerr's /api/v1/search.
func (client *TMDBClient) Search(ctx context.Context, query string, page int) (seerr.SearchPage, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 || len(query) > 200 || page < 1 || page > 100 {
		return seerr.SearchPage{}, ErrInvalidConfig
	}
	var payload tmdbSearchPage
	if err := client.get(ctx, "/3/search/multi", map[string]string{"query": query, "page": strconv.Itoa(page), "language": discoveryLanguage, "include_adult": "false"}, &payload); err != nil {
		return seerr.SearchPage{}, err
	}
	result := seerr.SearchPage{Page: payload.Page, TotalPages: payload.TotalPages, TotalResults: payload.TotalResults, Results: make([]seerr.SearchResult, 0, len(payload.Results))}
	for _, item := range payload.Results {
		result.Results = append(result.Results, seerr.SearchResult{ID: item.ID, MediaType: item.MediaType, Title: item.Title, Name: item.Name, ReleaseDate: item.ReleaseDate, FirstAirDate: item.FirstAirDate, Overview: item.Overview, PosterPath: item.PosterPath})
	}
	return result, nil
}

// Metadata returns one movie or series, like Seerr's /api/v1/movie/{id} and
// /api/v1/tv/{id}.
func (client *TMDBClient) Metadata(ctx context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error) {
	if tmdbID <= 0 || mediaType != "movie" && mediaType != "tv" {
		return seerr.Metadata{}, ErrInvalidConfig
	}
	var payload tmdbDiscoveryPayload
	if err := client.get(ctx, "/3/"+mediaType+"/"+strconv.FormatInt(tmdbID, 10), map[string]string{"language": discoveryLanguage, "append_to_response": "external_ids"}, &payload); err != nil {
		return seerr.Metadata{}, err
	}
	if payload.ID != tmdbID {
		return seerr.Metadata{}, ErrIdentityConflict
	}
	value := seerr.Metadata{
		ID: payload.ID, Title: payload.Title, Name: payload.Name, OriginalTitle: payload.OriginalTitle, OriginalName: payload.OriginalName,
		Overview: payload.Overview, OriginalLanguage: payload.OriginalLanguage, ReleaseDate: payload.ReleaseDate, FirstAirDate: payload.FirstAirDate,
		Status: payload.Status, PosterPath: payload.PosterPath, BackdropPath: payload.BackdropPath, VoteAverage: payload.VoteAverage, VoteCount: payload.VoteCount,
		OriginCountry: append([]string(nil), payload.OriginCountry...),
	}
	if payload.Runtime > 0 && payload.Runtime <= 1_440 {
		runtime := uint16(payload.Runtime)
		value.Runtime = &runtime
	}
	for _, minutes := range payload.EpisodeRunTime {
		if minutes > 0 && minutes <= 1_440 {
			value.EpisodeRunTime = append(value.EpisodeRunTime, uint16(minutes))
		}
	}
	for _, genre := range payload.Genres {
		value.Genres = append(value.Genres, seerr.Genre{ID: genre.ID, Name: genre.Name})
	}
	for _, country := range payload.ProductionCountries {
		value.ProductionCountries = append(value.ProductionCountries, seerr.Country{Code: country.Code, Name: country.Name})
	}
	for _, network := range payload.Networks {
		value.Networks = append(value.Networks, seerr.Network{ID: network.ID, Name: network.Name, OriginCountry: network.OriginCountry})
	}
	value.ExternalIDs.IMDbID = strings.TrimSpace(payload.ExternalIDs.IMDbID)
	if value.ExternalIDs.IMDbID == "" {
		value.ExternalIDs.IMDbID = strings.TrimSpace(payload.IMDbID)
	}
	if tvdb, err := strconv.ParseInt(string(payload.ExternalIDs.TVDBID), 10, 64); err == nil && tvdb > 0 {
		value.ExternalIDs.TVDBID = &tvdb
	}
	for _, season := range payload.Seasons {
		value.Seasons = append(value.Seasons, seerr.SeasonSummary{SeasonNumber: season.SeasonNumber})
	}
	return value, nil
}

// SeasonMetadata returns one season's episodes, like Seerr's
// /api/v1/tv/{id}/season/{n}.
func (client *TMDBClient) SeasonMetadata(ctx context.Context, tmdbID int64, season int16) (seerr.SeasonMetadata, error) {
	if tmdbID <= 0 || season < 0 {
		return seerr.SeasonMetadata{}, ErrInvalidConfig
	}
	var payload tmdbSeason
	if err := client.get(ctx, "/3/tv/"+strconv.FormatInt(tmdbID, 10)+"/season/"+strconv.Itoa(int(season)), map[string]string{"language": discoveryLanguage}, &payload); err != nil {
		return seerr.SeasonMetadata{}, err
	}
	result := seerr.SeasonMetadata{ID: payload.ID, SeasonNumber: payload.SeasonNumber, Episodes: make([]seerr.EpisodeMetadata, 0, len(payload.Episodes))}
	for _, episode := range payload.Episodes {
		result.Episodes = append(result.Episodes, seerr.EpisodeMetadata{ID: episode.ID, EpisodeNumber: episode.EpisodeNumber, Name: episode.Name, AirDate: episode.AirDate})
	}
	return result, nil
}
