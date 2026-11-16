package postgres

import (
	"context"
	"time"
)

// OutboxTiming is how the finished events of one type spent their time, in
// milliseconds. The four parts of an event add up to its whole life from
// creation to completion:
//
//   - Planned: from creation until it first became due (a scheduled wait,
//     such as a search planned for later);
//   - ReadyWait: due but not yet taken by a worker (the queue);
//   - Active: held by a worker (the work itself);
//   - Retry: everything else between the first claim and completion, which is
//     the scheduled delay before further attempts (pacing, a failed attempt's
//     backoff) and any attempt whose worker never reported back.
//
// Only events created after the timing columns existed are counted.
type OutboxTiming struct {
	EventType string `json:"event_type"`
	Events    int64  `json:"events"`
	// Retried counts events that needed more than one attempt; Attempts is the
	// number of attempts of all events together.
	Retried  int64 `json:"retried"`
	Attempts int64 `json:"attempts"`

	PlannedP50MS   int64 `json:"planned_p50_ms"`
	PlannedP90MS   int64 `json:"planned_p90_ms"`
	ReadyWaitP50MS int64 `json:"ready_wait_p50_ms"`
	ReadyWaitP90MS int64 `json:"ready_wait_p90_ms"`
	ActiveP50MS    int64 `json:"active_p50_ms"`
	ActiveP90MS    int64 `json:"active_p90_ms"`
	RetryP50MS     int64 `json:"retry_p50_ms"`
	RetryP90MS     int64 `json:"retry_p90_ms"`
	// The sums let a caller see where a worker's time went overall.
	ReadyWaitTotalMS int64 `json:"ready_wait_total_ms"`
	ActiveTotalMS    int64 `json:"active_total_ms"`
}

// OutboxTimings summarises the events completed since `since`, per event
// type. It only reads.
func (store *Store) OutboxTimings(ctx context.Context, since time.Time) ([]OutboxTiming, error) {
	rows, err := store.Pool.Query(ctx, `WITH timed AS (
			SELECT event_type,claim_count,ready_wait_ms,active_ms,
				GREATEST(0,(extract(epoch FROM first_ready_at-occurred_at)*1000)::bigint) planned_ms,
				GREATEST(0,(extract(epoch FROM completed_at-first_ready_at)*1000)::bigint-ready_wait_ms-active_ms) retry_ms
			FROM outbox_events WHERE completed_at IS NOT NULL AND completed_at>$1 AND first_ready_at IS NOT NULL)
		SELECT event_type,count(*),count(*) FILTER (WHERE claim_count>1),COALESCE(sum(claim_count),0)::bigint,
			percentile_disc(0.5) WITHIN GROUP (ORDER BY planned_ms),percentile_disc(0.9) WITHIN GROUP (ORDER BY planned_ms),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY ready_wait_ms),percentile_disc(0.9) WITHIN GROUP (ORDER BY ready_wait_ms),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY active_ms),percentile_disc(0.9) WITHIN GROUP (ORDER BY active_ms),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY retry_ms),percentile_disc(0.9) WITHIN GROUP (ORDER BY retry_ms),
			COALESCE(sum(ready_wait_ms),0)::bigint,COALESCE(sum(active_ms),0)::bigint
		FROM timed GROUP BY event_type ORDER BY event_type`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []OutboxTiming{}
	for rows.Next() {
		var timing OutboxTiming
		if err := rows.Scan(&timing.EventType, &timing.Events, &timing.Retried, &timing.Attempts,
			&timing.PlannedP50MS, &timing.PlannedP90MS, &timing.ReadyWaitP50MS, &timing.ReadyWaitP90MS,
			&timing.ActiveP50MS, &timing.ActiveP90MS, &timing.RetryP50MS, &timing.RetryP90MS,
			&timing.ReadyWaitTotalMS, &timing.ActiveTotalMS); err != nil {
			return nil, err
		}
		result = append(result, timing)
	}
	return result, rows.Err()
}
