package postgres

import (
	"context"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres/commissioningstore"
)

// IndexerAllowance is the same decision a reservation would make, without
// reserving anything. Configured budgets and local counts remain separate.
type IndexerAllowance struct {
	Limit     int        `json:"limit"`
	Used      int        `json:"used"`
	Remaining int        `json:"remaining"`
	Blocked   bool       `json:"blocked"`
	RetryAt   *time.Time `json:"retry_at,omitempty"`
}

type IndexerAllowances struct {
	Queries           IndexerAllowance `json:"queries"`
	Grabs             IndexerAllowance `json:"grabs"`
	BackgroundQueries IndexerAllowance `json:"background_queries"`
}

type IndexerProviderCounter struct {
	Maximum           int        `json:"maximum"`
	Used              *int       `json:"used,omitempty"`
	CountMaximum      *int       `json:"count_maximum,omitempty"`
	ObservedAt        *time.Time `json:"observed_at,omitempty"`
	MaximumObservedAt time.Time  `json:"maximum_observed_at"`
}

type IndexerProviderCounters struct {
	Queries *IndexerProviderCounter `json:"queries,omitempty"`
	Grabs   *IndexerProviderCounter `json:"grabs,omitempty"`
}

func (store *Store) populateIndexerAllowances(ctx context.Context, item *IndexerUsage, now time.Time) error {
	stated := indexers.ProviderLimits{}
	if !commissioningstore.IgnoreProviderLimits.Load() {
		var err error
		stated, _, err = store.LoadProviderLimits(ctx, item.ID.String())
		if err != nil {
			return err
		}
	}
	window := time.Duration(item.Budget.WindowSeconds) * time.Second
	item.EffectiveBudget = &IndexerAllowances{}
	for _, family := range []string{"query", "grab"} {
		var own, since int
		var oldest *time.Time
		if err := store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE reserved_at>$4),min(reserved_at)
			FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family=$2 AND reserved_at>$3`,
			item.ID.String(), family, now.Add(-window), indexers.ProviderReservationsSince(stated, family)).Scan(&own, &since, &oldest); err != nil {
			return err
		}
		if oldest != nil && (item.Usage.OldestReservationAt == nil || oldest.Before(*item.Usage.OldestReservationAt)) {
			item.Usage.OldestReservationAt = oldest
		}
		ownLimit := item.Budget.QueryLimit
		if family == "grab" {
			ownLimit = item.Budget.GrabLimit
		}
		allowance := indexerAllowance(stated, family, ownLimit, own, since, window, oldest, now)
		if family == "grab" {
			item.Usage.GrabsUsed, item.Usage.GrabsRemaining = own, allowance.Remaining
			item.EffectiveBudget.Grabs = allowance
		} else {
			item.Usage.QueriesUsed, item.Usage.QueriesRemaining = own, allowance.Remaining
			item.EffectiveBudget.Queries = allowance
			background := indexerAllowance(stated, family, item.Budget.BackgroundQueryLimit, own, since, window, oldest, now)
			item.EffectiveBudget.BackgroundQueries = background
			item.Usage.BackgroundQueriesRemaining = background.Remaining
		}
	}
	counters := &IndexerProviderCounters{}
	for _, field := range []struct {
		family string
		target **IndexerProviderCounter
	}{{"query", &counters.Queries}, {"grab", &counters.Grabs}} {
		used, maximum, ok := stated.Family(field.family)
		if !ok {
			continue
		}
		view := &IndexerProviderCounter{Maximum: maximum, MaximumObservedAt: stated.MaximumObservedAt(field.family)}
		if used >= 0 {
			at := stated.CounterObservedAt(field.family)
			view.Used, view.ObservedAt = &used, &at
			paired := stated.QueryCounterMax
			if field.family == "grab" {
				paired = stated.GrabCounterMax
			}
			if paired <= 0 {
				paired = maximum
			}
			view.CountMaximum = &paired
		}
		*field.target = view
	}
	if counters.Queries != nil || counters.Grabs != nil {
		item.ProviderCounters = counters
	}
	return nil
}

func indexerAllowance(stated indexers.ProviderLimits, family string, ownLimit, own, since int, window time.Duration, oldest *time.Time, now time.Time) IndexerAllowance {
	limit, used, retry := indexers.ProviderLimitDecision(stated, family, ownLimit, own, since, window, now)
	result := IndexerAllowance{Limit: limit, Used: used, Remaining: max(limit-used, 0), Blocked: used >= limit || !retry.IsZero()}
	if result.Blocked {
		result.Remaining = 0
		if retry.IsZero() {
			retry = now.Add(window)
			if oldest != nil {
				retry = oldest.Add(window)
			}
		}
		result.RetryAt = &retry
	}
	return result
}
