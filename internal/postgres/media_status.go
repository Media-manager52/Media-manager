package postgres

import (
	"context"
	"fmt"
	"regexp"
	"uuid"
)

// MediaStatus is the library overview of one title: whether Plex has any of
// it, how many scopes (the film, a season or an episode) are still wanted
// without a file, and whether any open work needs the owner.
type MediaStatus struct {
	MediaID   uuid.UUID `json:"media_id"`
	InPlex    bool      `json:"in_plex"`
	Missing   int       `json:"missing"`
	Attention bool      `json:"attention"`
}

var (
	seasonScope  = regexp.MustCompile(`^season:\d+$`)
	episodeScope = regexp.MustCompile(`^(season:\d+):episode:\d+$`)
)

type fileScope struct {
	media            uuid.UUID
	variant, edition string
}

// MediaStatuses returns the library overview of every active title. A wanted
// scope counts as missing only when nothing in the library covers it: a season
// whose expected episodes are all present as episode files is not missing, nor
// is an episode inside a published season pack. An upgrade of a file that is
// present never counts as missing.
func (store *Store) MediaStatuses(ctx context.Context) ([]MediaStatus, error) {
	present := map[fileScope]bool{}
	rows, err := store.Pool.Query(ctx, `SELECT DISTINCT workflow.media_id::text,workflow.variant,workflow.edition FROM library_link_versions version JOIN workflows workflow ON workflow.id=version.workflow_id WHERE version.state='current'
		UNION SELECT media_id::text,variant,'standard' FROM adopted_files WHERE state='current'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var mediaText string
		var scope fileScope
		if err := rows.Scan(&mediaText, &scope.variant, &scope.edition); err != nil {
			rows.Close()
			return nil, err
		}
		scope.media, _ = uuid.Parse(mediaText)
		present[scope] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	expected := map[uuid.UUID][]int{}
	rows, err = store.Pool.Query(ctx, `SELECT expectation.workflow_id::text,episode.episode FROM pack_expectation_sets expectation JOIN pack_expected_episodes episode ON episode.expectation_id=expectation.id JOIN workflows workflow ON workflow.id=expectation.workflow_id WHERE workflow.terminal_at IS NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var workflowText string
		var episode int
		if err := rows.Scan(&workflowText, &episode); err != nil {
			rows.Close()
			return nil, err
		}
		id, _ := uuid.Parse(workflowText)
		expected[id] = append(expected[id], episode)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	statuses := map[uuid.UUID]*MediaStatus{}
	order := []uuid.UUID{}
	rows, err = store.Pool.Query(ctx, `SELECT media.id::text,workflow.id::text,COALESCE(workflow.variant,''),COALESCE(workflow.edition,''),COALESCE(workflow.state,'') FROM media_items media LEFT JOIN workflows workflow ON workflow.media_id=media.id AND workflow.terminal_at IS NULL WHERE media.deleted_at IS NULL ORDER BY media.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mediaText string
		var workflowText *string
		var variant, edition, state string
		if err := rows.Scan(&mediaText, &workflowText, &variant, &edition, &state); err != nil {
			return nil, err
		}
		mediaID, _ := uuid.Parse(mediaText)
		status := statuses[mediaID]
		if status == nil {
			status = &MediaStatus{MediaID: mediaID}
			statuses[mediaID] = status
			order = append(order, mediaID)
		}
		if workflowText == nil {
			continue
		}
		workflowID, _ := uuid.Parse(*workflowText)
		switch state {
		case "needs_attention", "blocked", "quarantined", "transport_unknown":
			status.Attention = true
		}
		if scopeCovered(present, expected[workflowID], mediaID, variant, edition) {
			continue
		}
		status.Missing++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for scope := range present {
		if status := statuses[scope.media]; status != nil {
			status.InPlex = true
		}
	}
	result := make([]MediaStatus, 0, len(order))
	for _, id := range order {
		result = append(result, *statuses[id])
	}
	return result, nil
}

// scopeCovered reports whether the library already has a file for a wanted
// scope: its own file, the season pack holding an episode, or every expected
// episode of a season as separate episode files.
func scopeCovered(present map[fileScope]bool, expectedEpisodes []int, media uuid.UUID, variant, edition string) bool {
	if present[fileScope{media, variant, edition}] {
		return true
	}
	if match := episodeScope.FindStringSubmatch(variant); match != nil {
		return present[fileScope{media, match[1], edition}]
	}
	if seasonScope.MatchString(variant) && len(expectedEpisodes) > 0 {
		for _, episode := range expectedEpisodes {
			if !present[fileScope{media, fmt.Sprintf("%s:episode:%02d", variant, episode), edition}] {
				return false
			}
		}
		return true
	}
	return false
}
