package postgres

import "time"

const (
	noSelectionRetryBase = 30 * time.Minute
	// A title that keeps finding nothing is searched at most weekly. Episode
	// workflows are created only after their air date, so a new episode
	// always starts again at the 30-minute base.
	noSelectionRetryCap = 7 * 24 * time.Hour
	// Recent titles (a movie requested before its digital release) keep a
	// daily search so a new release is found within a day.
	noSelectionRecentRetryCap = 24 * time.Hour
	// Recovery rescans whose coverage was cut short by pacing or exhausted
	// budgets wait at least this long. Retrying at the next pacing slot made
	// every waiting title rescan every few minutes while the background
	// budget was exhausted, without ever reaching the deferred indexers. A
	// two-hour floor still produced about 3,500 continuation runs a day that
	// mostly reused saved answers and were deferred again; these titles have
	// already used a whole round, rescan daily anyway, and the newest-uploads
	// reader brings a rescan forward as soon as a fitting upload appears.
	recoveryPartialRetryFloor = 12 * time.Hour
)

// recoveryPartialRetryAt schedules an incomplete recovery rescan at the
// latest provider deferral, but never sooner than recoveryPartialRetryFloor.
func recoveryPartialRetryAt(providerRetryAt, now time.Time) time.Time {
	next := now.UTC().Add(recoveryPartialRetryFloor)
	if providerRetryAt.After(next) {
		next = providerRetryAt.UTC()
	}
	return next
}

// recentRelease reports whether a title's year is this year or last year.
func recentRelease(year *uint16, now time.Time) bool {
	return year != nil && int(*year) >= now.UTC().Year()-1
}

// Retry incomplete coverage without counting it as evidence of absence or
// increasing the empty-search backoff. Keep a 30-minute floor because this
// path is also used by direct repository callers without durable partial state.
func incompleteSearchRetryAt(outcomes []SearchIndexerOutcome, now time.Time) time.Time {
	floor := now.UTC().Add(noSelectionRetryBase)
	var earliest *time.Time
	for _, outcome := range outcomes {
		if outcome.State == "succeeded" {
			continue
		}
		retry := floor
		if outcome.RetryAt != nil && outcome.RetryAt.After(retry) {
			retry = outcome.RetryAt.UTC()
		}
		if earliest == nil || retry.Before(*earliest) {
			earliest = &retry
		}
	}
	if earliest != nil {
		return *earliest
	}
	return floor
}

// noSelectionRetryDelay paces the next targeted search after runs in the
// current round that selected nothing: 30 minutes after the first, doubling
// per run up to a week (a day for recent titles). Every search queries every
// indexer; the former 12-hour cap searched about 2,000 waiting titles twice a
// day forever, about 3,000 queries per indexer per day, almost all empty.
func noSelectionRetryDelay(roundRuns int, recent bool) time.Duration {
	limit := noSelectionRetryCap
	if recent {
		limit = noSelectionRecentRetryCap
	}
	delay := noSelectionRetryBase
	for run := 1; run < roundRuns && delay < limit; run++ {
		delay *= 2
	}
	if delay > limit {
		delay = limit
	}
	return delay
}
