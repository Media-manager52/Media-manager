package postgres

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
	"uuid"
)

// LibraryGaps is what the library could still hold: seasons of the owner's
// series that are neither followed nor in the library, and films that have no
// file and no open work. It only reads; adding anything is the owner's
// ordinary "Tilføj sæson" or "Hent".
type LibraryGaps struct {
	Series []SeriesGap `json:"series"`
	Movies []MovieGap  `json:"movies"`
}

// SeriesGap is one series with seasons the provider lists that Media Manager
// neither follows nor has a file of. NewSeasons are after the highest season
// it knows; OlderSeasons are before it. A series with no known season at all
// lists every season as older, since nothing says which ones the owner wants.
type SeriesGap struct {
	GapTitle
	SeasonCount  int    `json:"season_count"`
	Status       string `json:"status,omitempty"`
	KnownSeasons []int  `json:"known_seasons"`
	NewSeasons   []int  `json:"new_seasons"`
	OlderSeasons []int  `json:"older_seasons"`
	// StoppedSeasons had work that ended without a file (for example
	// cancelled); they are also listed in NewSeasons or OlderSeasons.
	StoppedSeasons []int `json:"stopped_seasons"`
}

// MovieGap is a film without a file in any library folder and without open
// work, with how its last work ended.
type MovieGap struct {
	GapTitle
	LastState string     `json:"last_state,omitempty"`
	LastAt    *time.Time `json:"last_at,omitempty"`
}

// GapTitle carries what the page needs to show a title and to add work in the
// same folder and profile as the title's newest work.
type GapTitle struct {
	MediaID          uuid.UUID  `json:"media_id"`
	Title            string     `json:"title"`
	Year             *int16     `json:"year,omitempty"`
	TMDBID           string     `json:"tmdb_id,omitempty"`
	PosterURL        string     `json:"poster_url,omitempty"`
	RootID           *uuid.UUID `json:"root_id,omitempty"`
	QualityProfileID *uuid.UUID `json:"quality_profile_id,omitempty"`
	MetadataAt       *time.Time `json:"metadata_at,omitempty"`
}

// seasonOf returns the season number of a season or episode scope.
func seasonOf(variant string) (int, bool) {
	if !strings.HasPrefix(variant, "season:") {
		return 0, false
	}
	rest := strings.TrimPrefix(variant, "season:")
	if index := strings.IndexByte(rest, ':'); index >= 0 {
		rest = rest[:index]
	}
	number, err := strconv.Atoi(rest)
	if err != nil || number < 1 {
		return 0, false
	}
	return number, true
}

// LibraryGaps lists the gaps of every active title. A season counts as known
// when it has open work, is followed in season monitoring with open work, or
// has any current file (a season pack, an episode or an adopted file).
func (store *Store) LibraryGaps(ctx context.Context) (LibraryGaps, error) {
	result := LibraryGaps{Series: []SeriesGap{}, Movies: []MovieGap{}}
	titles := map[uuid.UUID]*GapTitle{}
	kinds := map[uuid.UUID]string{}
	seasonCounts := map[uuid.UUID]int{}
	statuses := map[uuid.UUID]string{}
	rows, err := store.Pool.Query(ctx, `SELECT media.id::text,media.kind,media.title,media.year,
		COALESCE((SELECT identity.value FROM external_identities identity WHERE identity.media_id=media.id AND identity.namespace='tmdb' ORDER BY identity.value LIMIT 1),''),
		COALESCE(metadata.data->>'poster_url',''),COALESCE(metadata.data->>'status',''),
		CASE WHEN COALESCE(metadata.data->>'season_count','') ~ '^[0-9]+$' THEN (metadata.data->>'season_count')::int ELSE 0 END,
		metadata.updated_at,
		(SELECT workflow.root_id::text FROM workflows workflow WHERE workflow.media_id=media.id AND workflow.edition='standard' ORDER BY workflow.terminal_at IS NULL DESC,workflow.updated_at DESC,workflow.id DESC LIMIT 1),
		(SELECT workflow.quality_profile_id::text FROM workflows workflow WHERE workflow.media_id=media.id AND workflow.edition='standard' ORDER BY workflow.terminal_at IS NULL DESC,workflow.updated_at DESC,workflow.id DESC LIMIT 1)
		FROM media_items media LEFT JOIN media_metadata metadata ON metadata.media_id=media.id
		WHERE media.deleted_at IS NULL`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var idText, kind, status string
		var title GapTitle
		var count int
		var rootText, profileText *string
		if err := rows.Scan(&idText, &kind, &title.Title, &title.Year, &title.TMDBID, &title.PosterURL, &status, &count, &title.MetadataAt, &rootText, &profileText); err != nil {
			rows.Close()
			return result, err
		}
		title.MediaID, err = uuid.Parse(idText)
		if err != nil {
			rows.Close()
			return result, err
		}
		if rootText != nil {
			if parsed, parseErr := uuid.Parse(*rootText); parseErr == nil {
				title.RootID = &parsed
			}
		}
		if profileText != nil {
			if parsed, parseErr := uuid.Parse(*profileText); parseErr == nil {
				title.QualityProfileID = &parsed
			}
		}
		titles[title.MediaID] = &title
		kinds[title.MediaID] = kind
		seasonCounts[title.MediaID] = count
		statuses[title.MediaID] = status
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	// Current files, open work and ended work per title and scope.
	hasFile := map[uuid.UUID]bool{}
	known := map[uuid.UUID]map[int]bool{}
	stopped := map[uuid.UUID]map[int]bool{}
	mark := func(target map[uuid.UUID]map[int]bool, media uuid.UUID, season int) {
		if target[media] == nil {
			target[media] = map[int]bool{}
		}
		target[media][season] = true
	}
	rows, err = store.Pool.Query(ctx, `SELECT workflow.media_id::text,workflow.variant,'file' FROM library_link_versions version JOIN workflows workflow ON workflow.id=version.workflow_id WHERE version.state='current'
		UNION SELECT media_id::text,variant,'file' FROM adopted_files WHERE state='current'
		UNION SELECT media_id::text,variant,CASE WHEN terminal_at IS NULL THEN 'open' ELSE 'ended' END FROM workflows`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var mediaText, variant, kind string
		if err := rows.Scan(&mediaText, &variant, &kind); err != nil {
			rows.Close()
			return result, err
		}
		media, parseErr := uuid.Parse(mediaText)
		if parseErr != nil {
			continue
		}
		if kind != "ended" {
			hasFile[media] = hasFile[media] || kind == "file"
			if kind == "open" {
				// Open work of a film also means nothing is missing yet.
				hasFile[media] = true
			}
		}
		season, isSeason := seasonOf(variant)
		if !isSeason {
			continue
		}
		if kind == "ended" {
			mark(stopped, media, season)
		} else {
			mark(known, media, season)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	// Ended work of films: how and when the newest one ended.
	lastWork := map[uuid.UUID]MovieGap{}
	rows, err = store.Pool.Query(ctx, `SELECT DISTINCT ON (workflow.media_id) workflow.media_id::text,workflow.state,workflow.updated_at
		FROM workflows workflow JOIN media_items media ON media.id=workflow.media_id AND media.kind='movie'
		ORDER BY workflow.media_id,workflow.updated_at DESC,workflow.id DESC`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var mediaText, state string
		var at time.Time
		if err := rows.Scan(&mediaText, &state, &at); err != nil {
			rows.Close()
			return result, err
		}
		if media, parseErr := uuid.Parse(mediaText); parseErr == nil {
			lastWork[media] = MovieGap{LastState: state, LastAt: &at}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	for media, title := range titles {
		switch kinds[media] {
		case "movie":
			if hasFile[media] {
				continue
			}
			gap := lastWork[media]
			gap.GapTitle = *title
			result.Movies = append(result.Movies, gap)
		case "series":
			count := seasonCounts[media]
			if count == 0 {
				continue
			}
			gap := SeriesGap{GapTitle: *title, SeasonCount: count, Status: statuses[media], KnownSeasons: []int{}, NewSeasons: []int{}, OlderSeasons: []int{}, StoppedSeasons: []int{}}
			highest := 0
			for season := range known[media] {
				gap.KnownSeasons = append(gap.KnownSeasons, season)
				if season > highest {
					highest = season
				}
			}
			sort.Ints(gap.KnownSeasons)
			for season := 1; season <= count; season++ {
				if known[media][season] {
					continue
				}
				if stopped[media][season] {
					gap.StoppedSeasons = append(gap.StoppedSeasons, season)
				}
				if highest > 0 && season > highest {
					gap.NewSeasons = append(gap.NewSeasons, season)
				} else {
					gap.OlderSeasons = append(gap.OlderSeasons, season)
				}
			}
			if len(gap.NewSeasons)+len(gap.OlderSeasons) > 0 {
				result.Series = append(result.Series, gap)
			}
		}
	}
	byTitle := func(left, right GapTitle) bool {
		if order := strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title)); order != 0 {
			return order < 0
		}
		return left.MediaID.String() < right.MediaID.String()
	}
	sort.Slice(result.Series, func(i, j int) bool { return byTitle(result.Series[i].GapTitle, result.Series[j].GapTitle) })
	sort.Slice(result.Movies, func(i, j int) bool { return byTitle(result.Movies[i].GapTitle, result.Movies[j].GapTitle) })
	return result, nil
}
