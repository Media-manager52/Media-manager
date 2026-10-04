package postgres

import (
	"context"
	"github.com/media-manager/media-manager/internal/packs"
	"uuid"
)

// WorkflowEpisodeNumbering is available only when every regular catalogue
// season has a frozen snapshot and annual numbering is unambiguous across all
// those seasons. A child must still match its parent's frozen episode facts.
// Missing metadata, split years, overlapping seasons and snapshot drift keep
// the existing canonical-only path. No live metadata lookup is performed.
func (store *Store) WorkflowEpisodeNumbering(ctx context.Context, workflowID uuid.UUID) (packs.EpisodeNumbering, error) {
	expectation, series, season, selected, err := store.PackExpectation(ctx, workflowID)
	if err == ErrNotFound {
		return packs.EpisodeNumbering{}, nil
	}
	if err != nil {
		return packs.EpisodeNumbering{}, err
	}
	var mediaText string
	var revision int64
	if err = store.Pool.QueryRow(ctx, `SELECT workflow.media_id::text,expectation.source_revision FROM workflows workflow JOIN pack_expectation_sets expectation ON expectation.id=$2 WHERE workflow.id=$1`, workflowID.String(), expectation.String()).Scan(&mediaText, &revision); err != nil {
		return packs.EpisodeNumbering{}, err
	}
	mediaID, _ := uuid.Parse(mediaText)
	metadata, err := store.metadata(ctx, mediaID)
	if err != nil {
		return packs.EpisodeNumbering{}, err
	}
	if metadata == nil || metadata.SeasonCount == nil || *metadata.SeasonCount == 0 {
		return packs.EpisodeNumbering{}, nil
	}
	rows, err := store.Pool.Query(ctx, `WITH latest AS (
 SELECT DISTINCT ON (expectation.season) expectation.id,expectation.season,expectation.series_identity,expectation.source_revision
 FROM pack_expectation_sets expectation JOIN workflows workflow ON workflow.id=expectation.workflow_id
 WHERE workflow.media_id=$1 AND expectation.season>0 AND workflow.variant ~ '^season:[0-9]+$'
 ORDER BY expectation.season,(workflow.terminal_at IS NULL) DESC,expectation.frozen_at DESC,expectation.id DESC)
 SELECT latest.season,latest.series_identity,latest.source_revision,expected.episode,COALESCE(expected.provider_episode_id,''),expected.title,COALESCE(expected.air_date::text,'')
 FROM latest JOIN pack_expected_episodes expected ON expected.expectation_id=latest.id ORDER BY latest.season,expected.episode`, mediaText)
	if err != nil {
		return packs.EpisodeNumbering{}, err
	}
	defer rows.Close()
	snapshots := map[uint16][]packs.ExpectedEpisode{}
	var selectedRevision int64
	for rows.Next() {
		var s, e int16
		var id string
		var r int64
		var item packs.ExpectedEpisode
		if err = rows.Scan(&s, &id, &r, &e, &item.ProviderEpisodeID, &item.Title, &item.AirDate); err != nil {
			return packs.EpisodeNumbering{}, err
		}
		if id != series {
			return packs.EpisodeNumbering{}, nil
		}
		item.Key = packs.EpisodeKey{SeriesID: id, Season: uint16(s), Episode: uint16(e)}
		snapshots[uint16(s)] = append(snapshots[uint16(s)], item)
		if uint16(s) == season {
			selectedRevision = r
		}
	}
	if err = rows.Err(); err != nil {
		return packs.EpisodeNumbering{}, err
	}
	if len(snapshots) != int(*metadata.SeasonCount) || selectedRevision != revision {
		return packs.EpisodeNumbering{}, nil
	}
	years := map[uint16]bool{}
	var result packs.EpisodeNumbering
	for s := uint16(1); s <= *metadata.SeasonCount; s++ {
		year := packs.SeasonAirYear(snapshots[s])
		if year == 0 || years[year] {
			return packs.EpisodeNumbering{}, nil
		}
		years[year] = true
		if s == season {
			result = packs.AnnualEpisodeNumbering(s, snapshots[s])
		}
	}
	for _, e := range selected {
		found := false
		for _, parent := range snapshots[season] {
			if parent == e {
				found = true
				break
			}
		}
		if !found {
			return packs.EpisodeNumbering{}, nil
		}
	}
	return result, nil
}
