package commissioningstore

import (
	"context"
	"errors"
	"github.com/media-manager/media-manager/internal/postgres/dbcore"
	"sync/atomic"
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
	// The indexer's own stated counters may tighten the limit and the count,
	// never loosen them. The decision is taken under the same lock as the
	// reservation, so two processes cannot both take the last request.
	stated, found := indexers.ProviderLimits{}, false
	if !IgnoreProviderLimits.Load() {
		if stated, found, err = loadProviderLimits(ctx, transaction, indexerID); err != nil {
			return time.Time{}, err
		}
	}
	if found {
		var reservedSince int64
		if err := transaction.QueryRow(ctx, `SELECT count(*) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family=$2 AND reserved_at > $3`, indexerID, family, indexers.ProviderReservationsSince(stated, family)).Scan(&reservedSince); err != nil {
			return time.Time{}, err
		}
		tightLimit, tightUsed, statedRetry := indexers.ProviderLimitDecision(stated, family, limit, int(used), int(reservedSince), window, now)
		if !statedRetry.IsZero() {
			if err := transaction.Commit(ctx); err != nil {
				return time.Time{}, err
			}
			return statedRetry, nil
		}
		limit, used = tightLimit, int64(tightUsed)
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
	if found && stated.Exhausted(family, window, now) {
		column := "query_last_probe_at"
		if family == "grab" {
			column = "grab_last_probe_at"
		}
		if _, err := transaction.Exec(ctx, `UPDATE indexer_provider_limits SET `+column+`=$2 WHERE indexer_instance_id=$1`, indexerID, now.UTC()); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, transaction.Commit(ctx)
}

// IgnoreProviderLimits switches the indexers' own stated counters off for the
// whole process: no reading is saved or applied. It is set once at start-up.
var IgnoreProviderLimits atomic.Bool

// querier is what loadProviderLimits needs from a pool or a transaction.
type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadProviderLimits(ctx context.Context, db querier, indexerID string) (indexers.ProviderLimits, bool, error) {
	limits := indexers.ProviderLimits{QueryUsed: -1, QueryMax: -1, GrabUsed: -1, GrabMax: -1}
	var queryUsed, queryMax, grabUsed, grabMax, queryCounterMax, grabCounterMax *int
	var queryOldest, grabOldest, queryAt, grabAt, queryLimitAt, grabLimitAt, queryProbe, grabProbe *time.Time
	err := db.QueryRow(ctx, `SELECT observed_at,query_used,query_max,query_oldest_at,grab_used,grab_max,grab_oldest_at,query_observed_at,grab_observed_at,query_limit_observed_at,grab_limit_observed_at,query_last_probe_at,grab_last_probe_at,query_counter_max,grab_counter_max FROM indexer_provider_limits WHERE indexer_instance_id=$1`, indexerID).
		Scan(&limits.ObservedAt, &queryUsed, &queryMax, &queryOldest, &grabUsed, &grabMax, &grabOldest, &queryAt, &grabAt, &queryLimitAt, &grabLimitAt, &queryProbe, &grabProbe, &queryCounterMax, &grabCounterMax)
	if errors.Is(err, pgx.ErrNoRows) {
		return indexers.ProviderLimits{}, false, nil
	}
	if err != nil {
		return indexers.ProviderLimits{}, false, err
	}
	for _, field := range []struct {
		from *int
		to   *int
	}{{queryUsed, &limits.QueryUsed}, {queryMax, &limits.QueryMax}, {grabUsed, &limits.GrabUsed}, {grabMax, &limits.GrabMax}} {
		if field.from != nil {
			*field.to = *field.from
		}
	}
	if queryCounterMax != nil {
		limits.QueryCounterMax = *queryCounterMax
	}
	if grabCounterMax != nil {
		limits.GrabCounterMax = *grabCounterMax
	}
	if queryOldest != nil {
		limits.QueryOldest = *queryOldest
	}
	if grabOldest != nil {
		limits.GrabOldest = *grabOldest
	}
	for _, field := range []struct {
		from *time.Time
		to   *time.Time
	}{
		{queryAt, &limits.QueryObservedAt}, {grabAt, &limits.GrabObservedAt},
		{queryLimitAt, &limits.QueryLimitObservedAt}, {grabLimitAt, &limits.GrabLimitObservedAt},
		{queryProbe, &limits.QueryLastProbeAt}, {grabProbe, &limits.GrabLastProbeAt},
	} {
		if field.from != nil {
			*field.to = *field.from
		}
	}
	return limits, true, nil
}

// LoadProviderLimits returns the newest reading of an indexer's own stated
// counters, if it ever stated any.
func (store *Commissioning) LoadProviderLimits(ctx context.Context, indexerID string) (indexers.ProviderLimits, bool, error) {
	return loadProviderLimits(ctx, store.Pool, indexerID)
}

// SaveProviderLimits keeps a reading unless a newer one is already saved: an
// answer that arrives late must not put older numbers back.
func (store *Commissioning) SaveProviderLimits(ctx context.Context, indexerID string, limits indexers.ProviderLimits) error {
	if !limits.Valid() || IgnoreProviderLimits.Load() {
		return nil
	}
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ReservationLockKey(indexerID)); err != nil {
		return err
	}
	saved, found, err := loadProviderLimits(ctx, tx, indexerID)
	if err != nil {
		return err
	}
	if !found {
		saved = indexers.ProviderLimits{QueryUsed: -1, QueryMax: -1, GrabUsed: -1, GrabMax: -1}
	}
	mergeProviderLimits(&saved, limits)
	if saved.QueryCounterMax == 0 {
		saved.QueryCounterMax = -1
	}
	if saved.GrabCounterMax == 0 {
		saved.GrabCounterMax = -1
	}
	number := func(value int) any {
		if value < 0 {
			return nil
		}
		return value
	}
	_, err = tx.Exec(ctx, `INSERT INTO indexer_provider_limits(indexer_instance_id,observed_at,query_used,query_max,query_oldest_at,grab_used,grab_max,grab_oldest_at,
        query_observed_at,grab_observed_at,query_limit_observed_at,grab_limit_observed_at,query_counter_max,grab_counter_max)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
        ON CONFLICT(indexer_instance_id) DO UPDATE SET observed_at=EXCLUDED.observed_at,query_used=EXCLUDED.query_used,query_max=EXCLUDED.query_max,query_oldest_at=EXCLUDED.query_oldest_at,
            grab_used=EXCLUDED.grab_used,grab_max=EXCLUDED.grab_max,grab_oldest_at=EXCLUDED.grab_oldest_at,
            query_observed_at=EXCLUDED.query_observed_at,grab_observed_at=EXCLUDED.grab_observed_at,
            query_limit_observed_at=EXCLUDED.query_limit_observed_at,grab_limit_observed_at=EXCLUDED.grab_limit_observed_at,query_counter_max=EXCLUDED.query_counter_max,grab_counter_max=EXCLUDED.grab_counter_max`,
		indexerID, saved.ObservedAt.UTC(), number(saved.QueryUsed), number(saved.QueryMax), dbcore.NullableTime(saved.QueryOldest), number(saved.GrabUsed), number(saved.GrabMax), dbcore.NullableTime(saved.GrabOldest),
		dbcore.NullableTime(saved.QueryObservedAt), dbcore.NullableTime(saved.GrabObservedAt), dbcore.NullableTime(saved.QueryLimitObservedAt), dbcore.NullableTime(saved.GrabLimitObservedAt), number(saved.QueryCounterMax), number(saved.GrabCounterMax))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// A missing family or count is not a newer observation of that value. A
// maximum-only answer updates its own ceiling, but the paired maximum
// preserves whether the earlier counter was exhausted. Each family's clock is independent.
func mergeProviderLimits(saved *indexers.ProviderLimits, next indexers.ProviderLimits) {
	for _, fields := range []struct {
		family                          string
		used, maximum, paired           *int
		observed, limitObserved, oldest *time.Time
	}{
		{"query", &saved.QueryUsed, &saved.QueryMax, &saved.QueryCounterMax, &saved.QueryObservedAt, &saved.QueryLimitObservedAt, &saved.QueryOldest},
		{"grab", &saved.GrabUsed, &saved.GrabMax, &saved.GrabCounterMax, &saved.GrabObservedAt, &saved.GrabLimitObservedAt, &saved.GrabOldest},
	} {
		used, maximum, ok := next.Family(fields.family)
		if !ok {
			continue
		}
		if next.ObservedAt.After(*fields.limitObserved) {
			*fields.maximum = maximum
			*fields.limitObserved = next.ObservedAt.UTC()
		}
		if used >= 0 && next.ObservedAt.After(*fields.observed) && !next.ObservedAt.Before(*fields.limitObserved) {
			*fields.used, *fields.observed, *fields.paired = used, next.ObservedAt.UTC(), maximum
			if fields.family == "grab" {
				*fields.oldest = next.GrabOldest
			} else {
				*fields.oldest = next.QueryOldest
			}
		}
	}
	if next.ObservedAt.After(saved.ObservedAt) {
		saved.ObservedAt = next.ObservedAt.UTC()
	}
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
