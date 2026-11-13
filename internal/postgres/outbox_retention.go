package postgres

import (
	"context"
	"time"
)

// OutboxHistoryRetention is how long a completed outbox event is kept before
// DeleteCompletedOutboxHistory may remove it.
const OutboxHistoryRetention = 7 * 24 * time.Hour

const outboxHistoryBatch = 5000

// Completed outbox events are kept because several enqueue paths rely on them:
// an insert with ON CONFLICT DO NOTHING is skipped when an event with the same
// (aggregate, revision, type) already exists, and the Seerr reconcile path
// numbers its next event max(revision)+1. Every such check looks at the
// aggregate's current revision or at the newest event of a type, never at an
// older one.
//
// DeleteCompletedOutboxHistory therefore removes a completed event only when
// all of these hold:
//
//   - it is one of the high-volume types below, with the aggregate type that
//     type is always enqueued with;
//   - it completed before the cutoff;
//   - a newer event (higher revision) of the same aggregate and type exists,
//     so the newest event of every aggregate and type is always kept;
//   - the aggregate has moved past the event's revision, or no longer exists.
//
// Other event types (Seerr status sync, recycle, publication, Plex, ...) are
// never removed: some of them are deduplicated by their full history.
// It deletes at most maxBatches batches per call so one run never holds long
// locks, and returns the number of removed events.
func (store *Store) DeleteCompletedOutboxHistory(ctx context.Context, cutoff time.Time, maxBatches int) (int64, error) {
	var total int64
	for batch := 0; batch < maxBatches; batch++ {
		command, err := store.Pool.Exec(ctx, `WITH removable AS (
  SELECT event.id FROM outbox_events event
  WHERE event.completed_at IS NOT NULL AND event.completed_at < $1
    AND (
      (event.aggregate_type='workflow' AND event.event_type IN ('workflow.search_requested','workflow.transitioned')
        AND NOT EXISTS (SELECT 1 FROM workflows workflow WHERE workflow.id=event.aggregate_id AND workflow.revision<=event.aggregate_revision))
      OR (event.aggregate_type='workflow_retry_discovery' AND event.event_type='workflow.discovery_rescan_requested'
        AND NOT EXISTS (SELECT 1 FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=event.aggregate_id AND discovery.rescan_generation<=event.aggregate_revision))
      OR (event.aggregate_type='altmount_job' AND event.event_type='altmount.reconcile_requested'
        AND NOT EXISTS (SELECT 1 FROM altmount_jobs job WHERE job.id=event.aggregate_id AND job.revision<=event.aggregate_revision))
      OR (event.aggregate_type='seerr_delivery' AND event.event_type='seerr.reconcile')
    )
    AND EXISTS (
      SELECT 1 FROM outbox_events newer
      WHERE newer.aggregate_type=event.aggregate_type AND newer.aggregate_id=event.aggregate_id
        AND newer.event_type=event.event_type AND newer.aggregate_revision>event.aggregate_revision
    )
  LIMIT $2
)
DELETE FROM outbox_events event USING removable WHERE event.id=removable.id`, cutoff.UTC(), outboxHistoryBatch)
		if err != nil {
			return total, err
		}
		total += command.RowsAffected()
		if command.RowsAffected() < outboxHistoryBatch {
			break
		}
	}
	return total, nil
}
