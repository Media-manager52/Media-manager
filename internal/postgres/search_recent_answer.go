package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// Recent search answers.
//
// A selected release that fails verification (most often: no Danish in the
// download) sent the workflow straight back to a full search of every
// indexer, on average a few minutes after the search that found it. The
// indexers' answers had not changed, so the retry only cost queries. A
// complete targeted search that selects a release now keeps its per-indexer
// evidence; a retry after a failed release reuses it for
// RecentSearchAnswerLifetime and picks the next release from the same
// answers. Every reused release is evaluated again under the workflow's
// current rules and exclusions, so the failed release stays excluded.
//
// An answer is not reused in another acquisition round, for another indexer
// set, after the newest-uploads reader matched a new upload to the workflow,
// or after the owner asked for a search of it.

// RecentSearchAnswerLifetime is how long a retry after a failed release may
// reuse the search that selected it.
const RecentSearchAnswerLifetime = 2 * time.Hour

// saveRecentSearchAnswer keeps the evidence of a complete targeted search that
// selected a release. A search that reused every indexer keeps the answer it
// reused, with its original fetch time, so reuse never extends an answer's
// lifetime.
func saveRecentSearchAnswer(ctx context.Context, transaction pgx.Tx, retry *SearchPartialRetry, completed map[uuid.UUID][]SearchCandidateEvidence, reused map[uuid.UUID]bool, keep bool, now time.Time) error {
	if retry == nil || retry.Mode != "targeted" || retry.RoundID == (uuid.UUID{}) || !keep {
		return nil
	}
	fresh := false
	for _, id := range retry.PlannedIndexerIDs {
		if _, ok := completed[id]; !ok {
			return nil
		}
		fresh = fresh || !reused[id]
	}
	if !fresh {
		return nil
	}
	encoded, err := encodePartialEvidence(completed)
	if err != nil {
		return err
	}
	planned := make([]string, 0, len(retry.PlannedIndexerIDs))
	for _, id := range retry.PlannedIndexerIDs {
		planned = append(planned, id.String())
	}
	_, err = transaction.Exec(ctx, `INSERT INTO search_recent_answers(workflow_id,round_id,planned_indexer_ids,completed_indexers,fetched_at)
		VALUES($1,$2,$3::uuid[],$4,$5)
		ON CONFLICT(workflow_id) DO UPDATE SET round_id=EXCLUDED.round_id,planned_indexer_ids=EXCLUDED.planned_indexer_ids,completed_indexers=EXCLUDED.completed_indexers,fetched_at=EXCLUDED.fetched_at`,
		retry.WorkflowID.String(), retry.RoundID.String(), planned, string(encoded), now.UTC())
	return err
}

// RecentSearchAnswer returns the kept answer of the workflow's last complete
// search in roundID when a retry may still reuse it, as partial retry state
// with every indexer completed. found is false when there is none, it is
// older than RecentSearchAnswerLifetime, or a new upload match or an owner
// search came after it.
func (store *Store) RecentSearchAnswer(ctx context.Context, workflowID, roundID uuid.UUID, now time.Time) (SearchPartialRetry, bool, error) {
	if workflowID == (uuid.UUID{}) || roundID == (uuid.UUID{}) {
		return SearchPartialRetry{}, false, nil
	}
	var planned []string
	var completedRaw []byte
	err := store.Pool.QueryRow(ctx, `SELECT answer.planned_indexer_ids::text[],answer.completed_indexers
		FROM search_recent_answers answer
		WHERE answer.workflow_id=$1 AND answer.round_id=$2 AND answer.fetched_at>$3
		  AND NOT EXISTS(SELECT 1 FROM indexer_feed_matches hit WHERE hit.workflow_id=answer.workflow_id AND hit.seen_at>=answer.fetched_at)
		  AND NOT EXISTS(SELECT 1 FROM workflow_transitions transition WHERE transition.workflow_id=answer.workflow_id AND transition.actor='owner' AND transition.to_state='searching' AND transition.occurred_at>=answer.fetched_at)`,
		workflowID.String(), roundID.String(), now.Add(-RecentSearchAnswerLifetime).UTC()).Scan(&planned, &completedRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SearchPartialRetry{}, false, nil
	}
	if err != nil {
		return SearchPartialRetry{}, false, err
	}
	result := SearchPartialRetry{WorkflowID: workflowID, RoundID: roundID, Mode: "targeted"}
	for _, value := range planned {
		id, parseErr := uuid.Parse(value)
		if parseErr != nil {
			return SearchPartialRetry{}, false, nil
		}
		result.PlannedIndexerIDs = append(result.PlannedIndexerIDs, id)
	}
	result.CompletedIndexers, err = decodePartialEvidence(completedRaw)
	if err != nil {
		return SearchPartialRetry{}, false, nil
	}
	return result, true, nil
}

// DeleteExpiredRecentSearchAnswers removes answers fetched before cutoff. They
// are never reused after their lifetime anyway.
func (store *Store) DeleteExpiredRecentSearchAnswers(ctx context.Context, cutoff time.Time) (int64, error) {
	command, err := store.Pool.Exec(ctx, `DELETE FROM search_recent_answers WHERE fetched_at<$1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}
