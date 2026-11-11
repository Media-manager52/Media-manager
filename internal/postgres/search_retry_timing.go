package postgres

import "time"

const (
	noSelectionRetryBase = 30 * time.Minute
	// After the first searches a title that keeps finding nothing is
	// searched monthly as a safety net: a release uploaded later is seen by
	// the newest-uploads reader, which brings the title's search forward at
	// once. Of the titles that were found at all, about 97 % were found
	// within the first three searches. Episode workflows are created only
	// after their air date, so a new episode always starts again at the
	// beginning of the schedule.
	noSelectionRetryCap = 30 * 24 * time.Hour
	// Recent titles (a movie requested before its digital release) keep a
	// weekly safety search.
	noSelectionRecentRetryCap = 7 * 24 * time.Hour
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

// noSelectionSchedule is the wait after the first, second and third targeted
// search in a round that selected nothing.
var noSelectionSchedule = [...]time.Duration{noSelectionRetryBase, 6 * time.Hour, 24 * time.Hour}

// noSelectionRetryDelay paces the next targeted search after runs in the
// current round that selected nothing: 30 minutes, 6 hours and a day after the
// first three, then a monthly safety search (weekly for recent titles). Every
// search queries every indexer; the former doubling schedule searched each new
// title about six times on its first day and every waiting title weekly, most
// of the daily queries, almost all empty.
func noSelectionRetryDelay(roundRuns int, recent bool) time.Duration {
	if roundRuns < 1 {
		roundRuns = 1
	}
	if roundRuns <= len(noSelectionSchedule) {
		return noSelectionSchedule[roundRuns-1]
	}
	if recent {
		return noSelectionRecentRetryCap
	}
	return noSelectionRetryCap
}
