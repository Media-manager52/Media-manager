package postgres

import (
	"context"
	"time"
	"uuid"
)

// PlexBulkReconfirmation reports an owner's "re-check Plex" pass: the
// workflows whose exact-path confirmation was queued again, and the ones that
// were left alone with the reason RequestPlexReconfirmation gave.
type PlexBulkReconfirmation struct {
	Queued  []uuid.UUID            `json:"queued"`
	Skipped []PlexReconfirmSkipped `json:"skipped"`
}

type PlexReconfirmSkipped struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
	Reason     string    `json:"reason"`
}

// maxBulkPlexReconfirmations bounds one owner pass so a single click can never
// fan out into an unbounded number of Plex requests.
const maxBulkPlexReconfirmations = 50

// ReconfirmTimedOutPlex re-queues exact-path Plex confirmation for every
// workflow whose previous confirmation timed out. Each workflow goes through
// RequestPlexReconfirmation, so the verified-links check and retry limit
// apply unchanged; no library-wide scan is ever requested.
func (store *Store) ReconfirmTimedOutPlex(ctx context.Context, actor string, now time.Time) (PlexBulkReconfirmation, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text FROM workflows WHERE state='needs_attention' AND attention_code='plex_timeout' AND terminal_at IS NULL ORDER BY updated_at,id LIMIT $1`, maxBulkPlexReconfirmations)
	if err != nil {
		return PlexBulkReconfirmation{}, err
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			rows.Close()
			return PlexBulkReconfirmation{}, err
		}
		id, err := uuid.Parse(text)
		if err != nil {
			rows.Close()
			return PlexBulkReconfirmation{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PlexBulkReconfirmation{}, err
	}
	result := PlexBulkReconfirmation{Queued: []uuid.UUID{}, Skipped: []PlexReconfirmSkipped{}}
	for _, id := range ids {
		if _, err := store.RequestPlexReconfirmation(ctx, id, actor, now); err != nil {
			result.Skipped = append(result.Skipped, PlexReconfirmSkipped{WorkflowID: id, Reason: err.Error()})
			continue
		}
		result.Queued = append(result.Queued, id)
	}
	return result, nil
}
