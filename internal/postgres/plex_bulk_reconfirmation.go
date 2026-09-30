package postgres

import (
	"context"
	"errors"
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

// maxBulkPlexReconfirmations bounds how many confirmations one owner pass
// queues, so a single click can never fan out into an unbounded number of
// Plex requests. maxBulkPlexCandidates bounds how many timed-out workflows
// one pass examines: workflows that cannot be retried (retry limit reached,
// links not verified, no Plex library) are skipped and must not keep newer,
// retryable workflows from ever being reached.
var (
	maxBulkPlexReconfirmations = 50
	maxBulkPlexCandidates      = 500
)

// plexReconfirmSkippable reports whether a workflow was left alone for a
// known, owner-explainable reason. Any other error (database, cancelled
// request) is a failure of the whole pass, never a quiet "skipped".
func plexReconfirmSkippable(err error) bool {
	return errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrPlexReconfirmationNotTimedOut) ||
		errors.Is(err, ErrPlexReconfirmationUnverified) ||
		errors.Is(err, ErrPlexReconfirmationLimit) ||
		errors.Is(err, ErrPlexReconfirmationNoLibrary) ||
		errors.Is(err, ErrConcurrentModification)
}

// ReconfirmTimedOutPlex re-queues exact-path Plex confirmation for workflows
// whose previous confirmation timed out, oldest first, until
// maxBulkPlexReconfirmations are queued. Each workflow goes through
// RequestPlexReconfirmation, so the verified-links check and retry limit
// apply unchanged; no library-wide scan is ever requested. Confirmations
// queued before an unexpected error stay queued; retrying the pass is safe
// because an already re-queued workflow is returned unchanged.
func (store *Store) ReconfirmTimedOutPlex(ctx context.Context, actor string, now time.Time) (PlexBulkReconfirmation, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text FROM workflows WHERE state='needs_attention' AND attention_code='plex_timeout' AND terminal_at IS NULL ORDER BY updated_at,id LIMIT $1`, maxBulkPlexCandidates)
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
		if len(result.Queued) >= maxBulkPlexReconfirmations {
			break
		}
		if _, err := store.RequestPlexReconfirmation(ctx, id, actor, now); err != nil {
			if !plexReconfirmSkippable(err) {
				return result, err
			}
			result.Skipped = append(result.Skipped, PlexReconfirmSkipped{WorkflowID: id, Reason: err.Error()})
			continue
		}
		result.Queued = append(result.Queued, id)
	}
	return result, nil
}
