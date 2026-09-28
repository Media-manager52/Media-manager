package postgres

import (
	"context"
	"uuid"
)

// Servarr read models back Media Manager's Radarr/Sonarr-compatible API. They
// report Media Manager's own catalog, library roots, and Plex-confirmed
// publications instead of an *arr database.

type ServarrMovie struct {
	MediaID uuid.UUID
	Title   string
	Year    *uint16
	TMDBID  string
	HasFile bool
}

type ServarrSeriesSeason struct {
	Number        uint16
	Monitored     bool
	TotalEpisodes int
	EpisodeFiles  int
}

type ServarrSeries struct {
	MediaID uuid.UUID
	Title   string
	Year    *uint16
	TVDBID  string
	TMDBID  string
	Seasons []ServarrSeriesSeason
}

const servarrMovieSelect = `SELECT media.id::text,media.title,media.year,identity.value,
	EXISTS(
		SELECT 1 FROM library_link_versions version
		JOIN library_links link ON link.id=version.library_link_id
		JOIN workflows workflow ON workflow.id=version.workflow_id
		WHERE workflow.media_id=media.id AND version.state='current'
	) OR EXISTS(
		SELECT 1 FROM adopted_files adopted WHERE adopted.media_id=media.id AND adopted.state='current'
	) AS has_file
	FROM media_items media
	JOIN external_identities identity ON identity.media_id=media.id AND identity.namespace='tmdb' AND identity.state='verified'
	WHERE media.kind='movie' AND media.deleted_at IS NULL`

const servarrSeriesSelect = `SELECT media.id::text,media.title,media.year,tvdb.value,COALESCE(tmdb.value,'')
	FROM media_items media
	JOIN external_identities tvdb ON tvdb.media_id=media.id AND tvdb.namespace='tvdb' AND tvdb.state='verified'
	LEFT JOIN external_identities tmdb ON tmdb.media_id=media.id AND tmdb.namespace='tmdb' AND tmdb.state='verified'
	WHERE media.kind='series' AND media.deleted_at IS NULL`

func scanServarrMovies(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ServarrMovie, error) {
	result := []ServarrMovie{}
	for rows.Next() {
		var movie ServarrMovie
		var idText string
		var year *int16
		if err := rows.Scan(&idText, &movie.Title, &year, &movie.TMDBID, &movie.HasFile); err != nil {
			return nil, err
		}
		parsed, err := uuid.Parse(idText)
		if err != nil {
			return nil, err
		}
		movie.MediaID = parsed
		if year != nil {
			converted := uint16(*year)
			movie.Year = &converted
		}
		result = append(result, movie)
	}
	return result, rows.Err()
}

func (store *Store) ListServarrMovies(ctx context.Context) ([]ServarrMovie, error) {
	rows, err := store.Pool.Query(ctx, servarrMovieSelect+` ORDER BY media.title,media.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanServarrMovies(rows)
}

func (store *Store) ServarrMovieByTMDB(ctx context.Context, tmdbID string) (ServarrMovie, error) {
	rows, err := store.Pool.Query(ctx, servarrMovieSelect+` AND identity.value=$1 ORDER BY media.id LIMIT 1`, tmdbID)
	if err != nil {
		return ServarrMovie{}, err
	}
	defer rows.Close()
	movies, err := scanServarrMovies(rows)
	if err != nil {
		return ServarrMovie{}, err
	}
	if len(movies) == 0 {
		return ServarrMovie{}, ErrNotFound
	}
	return movies[0], nil
}

// servarrSeasons returns the monitored seasons for every series together with
// the frozen expectation size and how many episode files Plex has confirmed.
// A confirmed complete-season workflow counts all its expected episodes;
// confirmed per-episode workflows count individually.
func (store *Store) servarrSeasons(ctx context.Context) (map[string][]ServarrSeriesSeason, error) {
	rows, err := store.Pool.Query(ctx, `SELECT monitoring.media_id::text,monitoring.season_number,monitoring.monitored,
		COALESCE(expectation.expected_episodes,0),
		(CASE WHEN workflow.state='plex_confirmed' THEN COALESCE(expectation.expected_episodes,0) ELSE 0 END) + COALESCE(episodes.confirmed_episodes,0)
		FROM season_monitoring monitoring
		JOIN media_items media ON media.id=monitoring.media_id AND media.kind='series' AND media.deleted_at IS NULL
		LEFT JOIN LATERAL (
			SELECT item.id,item.state FROM workflows item
			WHERE item.media_id=monitoring.media_id AND item.variant='season:' || lpad(monitoring.season_number::text,2,'0')
			ORDER BY item.updated_at DESC,item.id DESC LIMIT 1
		) workflow ON true
		LEFT JOIN LATERAL (
			SELECT count(*)::int AS expected_episodes FROM pack_expected_episodes episode
			JOIN pack_expectation_sets expectation_set ON expectation_set.id=episode.expectation_id
			WHERE expectation_set.workflow_id=workflow.id
		) expectation ON true
		LEFT JOIN LATERAL (
			SELECT count(*)::int AS confirmed_episodes FROM workflows episode
			WHERE episode.media_id=monitoring.media_id
				AND episode.variant LIKE 'season:' || lpad(monitoring.season_number::text,2,'0') || ':episode:%'
				AND episode.state='plex_confirmed'
		) episodes ON true
		ORDER BY monitoring.media_id,monitoring.season_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]ServarrSeriesSeason{}
	for rows.Next() {
		var mediaID string
		var season ServarrSeriesSeason
		var number int16
		if err := rows.Scan(&mediaID, &number, &season.Monitored, &season.TotalEpisodes, &season.EpisodeFiles); err != nil {
			return nil, err
		}
		season.Number = uint16(number)
		result[mediaID] = append(result[mediaID], season)
	}
	return result, rows.Err()
}

func scanServarrSeries(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}, seasons map[string][]ServarrSeriesSeason) ([]ServarrSeries, error) {
	result := []ServarrSeries{}
	for rows.Next() {
		var series ServarrSeries
		var idText string
		var year *int16
		if err := rows.Scan(&idText, &series.Title, &year, &series.TVDBID, &series.TMDBID); err != nil {
			return nil, err
		}
		parsed, err := uuid.Parse(idText)
		if err != nil {
			return nil, err
		}
		series.MediaID = parsed
		if year != nil {
			converted := uint16(*year)
			series.Year = &converted
		}
		series.Seasons = seasons[idText]
		if series.Seasons == nil {
			series.Seasons = []ServarrSeriesSeason{}
		}
		result = append(result, series)
	}
	return result, rows.Err()
}

func (store *Store) ListServarrSeries(ctx context.Context) ([]ServarrSeries, error) {
	seasons, err := store.servarrSeasons(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.Pool.Query(ctx, servarrSeriesSelect+` ORDER BY media.title,media.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanServarrSeries(rows, seasons)
}

func (store *Store) ServarrSeriesByTVDB(ctx context.Context, tvdbID string) (ServarrSeries, error) {
	seasons, err := store.servarrSeasons(ctx)
	if err != nil {
		return ServarrSeries{}, err
	}
	rows, err := store.Pool.Query(ctx, servarrSeriesSelect+` AND tvdb.value=$1 ORDER BY media.id LIMIT 1`, tvdbID)
	if err != nil {
		return ServarrSeries{}, err
	}
	defer rows.Close()
	series, err := scanServarrSeries(rows, seasons)
	if err != nil {
		return ServarrSeries{}, err
	}
	if len(series) == 0 {
		return ServarrSeries{}, ErrNotFound
	}
	return series[0], nil
}
