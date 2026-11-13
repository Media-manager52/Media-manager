package commissioningstore

import (
	"context"
	"errors"
	"github.com/media-manager/media-manager/internal/postgres/dbcore"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/indexers"
)

func (store *Commissioning) LoadIndexerBudget(ctx context.Context, indexerID string) (indexers.Snapshot, error) {
	var snapshot indexers.Snapshot
	var next, cooldown, outcomeAt *time.Time
	var lastOutcome *string
	var minimumIntervalMS int64
	var revision *int64
	err := store.Pool.QueryRow(ctx, `SELECT minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,last_outcome_at,updated_at,revision FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID).Scan(&minimumIntervalMS, &next, &cooldown, &snapshot.ConsecutiveFailure, &lastOutcome, &outcomeAt, &snapshot.UpdatedAt, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return indexers.Snapshot{IndexerID: indexerID}, nil
	}
	if err != nil {
		return indexers.Snapshot{}, err
	}
	snapshot.IndexerID = indexerID
	snapshot.MinimumInterval = time.Duration(minimumIntervalMS) * time.Millisecond
	if lastOutcome != nil {
		snapshot.LastOutcome = indexers.Outcome(*lastOutcome)
	}
	if outcomeAt != nil {
		snapshot.LastOutcomeAt = *outcomeAt
	}
	if next != nil {
		snapshot.NextAllowedAt = *next
	}
	if cooldown != nil {
		snapshot.CooldownUntil = *cooldown
	}
	if revision != nil && *revision > 0 {
		snapshot.Revision = uint64(*revision)
	}
	return snapshot, nil
}

// SaveIndexerBudget is LEGACY-ONLY persistence for the legacy Record path,
// which holds the lane while writing. It MUST NEVER be used for commissioning
// transmission or finalization: MarkClaimedStepTransmitting and
// CompleteLiveAttempt go through their own transactional SQL (M-7).
func (store *Commissioning) SaveIndexerBudget(ctx context.Context, snapshot indexers.Snapshot) error {
	minimumIntervalMS := snapshot.MinimumInterval.Milliseconds()
	if minimumIntervalMS < 0 {
		minimumIntervalMS = 0
	}
	_, err := store.Pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,last_outcome_at,revision,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8) ON CONFLICT(indexer_instance_id) DO UPDATE SET minimum_interval_ms=EXCLUDED.minimum_interval_ms,next_allowed_at=EXCLUDED.next_allowed_at,cooldown_until=EXCLUDED.cooldown_until,consecutive_failure=EXCLUDED.consecutive_failure,last_outcome=EXCLUDED.last_outcome,last_outcome_at=GREATEST(indexer_budget_state.last_outcome_at,EXCLUDED.last_outcome_at),revision=indexer_budget_state.revision+1,updated_at=EXCLUDED.updated_at`, snapshot.IndexerID, minimumIntervalMS, dbcore.NullableTime(snapshot.NextAllowedAt), dbcore.NullableTime(snapshot.CooldownUntil), snapshot.ConsecutiveFailure, snapshot.LastOutcome, dbcore.NullableTime(snapshot.LastOutcomeAt), snapshot.UpdatedAt)
	return err
}

func (store *Commissioning) ReserveIndexerRequest(ctx context.Context, indexerID, family string, limit int, window time.Duration, now time.Time) (time.Time, error) {
	if (family != "query" && family != "grab") || limit <= 0 || window <= 0 {
		return time.Time{}, indexers.ErrInvalidBudget
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ReservationLockKey(indexerID)); err != nil {
		return time.Time{}, err
	}
	windowStart := now.UTC().Add(-window)
	if _, err := transaction.Exec(ctx, `DELETE FROM indexer_request_reservations WHERE id IN (SELECT id FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND reserved_at <= $2 ORDER BY reserved_at LIMIT 1000)`, indexerID, windowStart); err != nil {
		return time.Time{}, err
	}
	var used int64
	var oldest *time.Time
	if err := transaction.QueryRow(ctx, `SELECT count(*),min(reserved_at) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family=$2 AND reserved_at > $3`, indexerID, family, windowStart).Scan(&used, &oldest); err != nil {
		return time.Time{}, err
	}
	if used >= int64(limit) {
		retryAt := now.UTC().Add(window)
		if oldest != nil {
			retryAt = oldest.UTC().Add(window)
		}
		if err := transaction.Commit(ctx); err != nil {
			return time.Time{}, err
		}
		return retryAt, nil
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,$3,$4)`, uuid.NewV7().String(), indexerID, family, now.UTC()); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, transaction.Commit(ctx)
}

// ReservationLockKey is the advisory-lock key that serializes request budget
// reservations for one indexer.
func ReservationLockKey(indexerID string) string {
	if parsed, err := uuid.Parse(indexerID); err == nil {
		return "indexer-request-budget:" + parsed.String()
	}
	return "indexer-request-budget:" + indexerID
}

// ReservationLockKeyForTest exposes the shared lock-key derivation for
// focused unit coverage without a database.
func ReservationLockKeyForTest(indexerID string) string { return ReservationLockKey(indexerID) }
