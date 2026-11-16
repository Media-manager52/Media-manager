package indexers

import (
	"testing"
	"time"
)

func TestProviderProbesNeverAccumulateUnusedHours(t *testing.T) {
	observed := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	reading := ProviderLimits{ObservedAt: observed, QueryUsed: 100, QueryMax: 100, GrabUsed: -1, GrabMax: -1}
	at := observed.Add(5 * time.Hour)
	limit, used, retry := ProviderLimitDecision(reading, "query", 1000, 0, 0, 24*time.Hour, at)
	if !retry.IsZero() || used >= limit {
		t.Fatalf("first probe was refused: %d/%d retry=%v", used, limit, retry)
	}
	reading.QueryLastProbeAt = at
	for _, after := range []time.Duration{0, time.Second, 59 * time.Minute} {
		_, _, retry = ProviderLimitDecision(reading, "query", 1000, 1, 1, 24*time.Hour, at.Add(after))
		if !retry.Equal(at.Add(time.Hour)) {
			t.Fatalf("after %s retry=%v, want one hour after actual probe", after, retry)
		}
	}
	if _, _, retry = ProviderLimitDecision(reading, "query", 1000, 1, 1, 24*time.Hour, at.Add(time.Hour)); !retry.IsZero() {
		t.Fatal("next hourly probe was refused")
	}
}

func TestProviderLimitDecisionOnlyTightens(t *testing.T) {
	t.Parallel()
	window := 24 * time.Hour
	observed := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	reading := func(used, maximum int) ProviderLimits {
		return ProviderLimits{ObservedAt: observed, QueryUsed: used, QueryMax: maximum, GrabUsed: -1, GrabMax: -1}
	}
	for _, test := range []struct {
		name                     string
		limits                   ProviderLimits
		ownLimit, ownUsed, since int
		after                    time.Duration
		wantLimit, wantUsed      int
		wantBlocked              bool
	}{
		{name: "nothing stated", limits: ProviderLimits{ObservedAt: observed, QueryUsed: -1, QueryMax: -1}, ownLimit: 100, ownUsed: 10, wantLimit: 100, wantUsed: 10},
		{name: "indexer counted fewer: own count stays", limits: reading(5, 1000), ownLimit: 100, ownUsed: 40, wantLimit: 100, wantUsed: 40},
		{name: "indexer allows more: own limit stays", limits: reading(5, 1000), ownLimit: 100, ownUsed: 99, wantLimit: 100, wantUsed: 99},
		{name: "indexer counted more: its count is used", limits: reading(80, 1000), ownLimit: 100, ownUsed: 40, wantLimit: 100, wantUsed: 80},
		{name: "indexer allows less: its maximum is used", limits: reading(5, 50), ownLimit: 100, ownUsed: 40, wantLimit: 50, wantUsed: 40},
		{name: "own reservations after the reading are added", limits: reading(80, 1000), ownLimit: 100, ownUsed: 40, since: 7, wantLimit: 100, wantUsed: 87},
		{name: "a delayed counter still fills up through own reservations", limits: reading(95, 100), ownLimit: 100, ownUsed: 10, since: 5, after: 10 * time.Minute, wantLimit: 100, wantUsed: 100, wantBlocked: true},
		{name: "a stated maximum without a count only caps the limit", limits: reading(-1, 50), ownLimit: 100, ownUsed: 60, wantLimit: 50, wantUsed: 60},
		{name: "an impossible maximum is not a reading", limits: reading(5, maxProviderLimit+1), ownLimit: 100, ownUsed: 10, wantLimit: 100, wantUsed: 10},
		{name: "still applied after 15 minutes", limits: reading(100, 100), ownLimit: 1000, ownUsed: 3, after: 16 * time.Minute, wantLimit: 100, wantUsed: 100, wantBlocked: true},
		{name: "still applied after 23 hours without a probe left", limits: ProviderLimits{ObservedAt: observed, QueryUsed: 100, QueryMax: 100, QueryLastProbeAt: observed.Add(23 * time.Hour)}, ownLimit: 1000, ownUsed: 3, since: 23, after: 23 * time.Hour, wantLimit: 100, wantUsed: 100, wantBlocked: true},
		{name: "dropped only when a whole window old", limits: reading(100, 100), ownLimit: 1000, ownUsed: 3, after: window, wantLimit: 1000, wantUsed: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit, used, retryAt := ProviderLimitDecision(test.limits, "query", test.ownLimit, test.ownUsed, test.since, window, observed.Add(test.after))
			if limit != test.wantLimit || used != test.wantUsed || !retryAt.IsZero() != test.wantBlocked {
				t.Fatalf("limit=%d used=%d retryAt=%v, want %d %d blocked=%v", limit, used, retryAt, test.wantLimit, test.wantUsed, test.wantBlocked)
			}
			if limit > test.ownLimit || used < test.ownUsed {
				t.Fatalf("a reading loosened the own limit or count: limit=%d used=%d", limit, used)
			}
			if test.wantBlocked && (!retryAt.After(observed.Add(test.after)) || retryAt.After(observed.Add(window))) {
				t.Fatalf("retryAt=%v is not between now and the end of the reading's window", retryAt)
			}
		})
	}
}

// A reading that shows the allowance used up is never dropped after some
// short time. It blocks until a newer reading replaces it; when none can
// arrive, one request an hour is let through as a probe, from the later of
// the indexer's own release time and an hour after the reading.
func TestProviderLimitDecisionUsedUpAllowsOnlyHourlyProbes(t *testing.T) {
	t.Parallel()
	window := 24 * time.Hour
	observed := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	usedUp := ProviderLimits{ObservedAt: observed, QueryUsed: 100, QueryMax: 100, GrabUsed: 30, GrabMax: 30,
		QueryOldest: observed.Add(-window).Add(3 * time.Hour), GrabOldest: observed.Add(-30 * time.Hour)}
	blocked := func(family string, since int, after time.Duration) (bool, time.Time) {
		sample := usedUp
		if since > 0 {
			at := observed.Add(time.Duration(2+since) * time.Hour)
			if family == "grab" {
				at = observed.Add(time.Duration(since) * time.Hour)
			}
			if family == "grab" {
				sample.GrabLastProbeAt = at
			} else {
				sample.QueryLastProbeAt = at
			}
		}
		_, _, retryAt := ProviderLimitDecision(sample, family, 1000, 0, since, window, observed.Add(after))
		return !retryAt.IsZero(), retryAt
	}
	// Queries: the indexer says its oldest request leaves in three hours.
	for _, step := range []struct {
		after time.Duration
		since int
		want  bool
	}{
		{time.Minute, 0, true}, {16 * time.Minute, 0, true}, {2*time.Hour + 59*time.Minute, 0, true},
		{3 * time.Hour, 0, false},            // the first probe
		{3*time.Hour + time.Minute, 1, true}, // it was used
		{3*time.Hour + 59*time.Minute, 1, true},
		{4 * time.Hour, 1, false}, // the next probe, an hour later
		{4 * time.Hour, 2, true},
	} {
		if got, retryAt := blocked("query", step.since, step.after); got != step.want {
			t.Fatalf("query after %s with %d probe(s) used: blocked=%v (retry %v), want %v", step.after, step.since, got, retryAt, step.want)
		}
	}
	if _, retryAt := blocked("query", 0, time.Minute); !retryAt.Equal(observed.Add(3 * time.Hour)) {
		t.Fatalf("retry at %v, want the indexer's stated release time", retryAt)
	}
	// Downloads: the stated release time is already past (indexers were
	// measured stating local time as UTC). It is not believed before an hour
	// has passed since the reading.
	if got, _ := blocked("grab", 0, 59*time.Minute); !got {
		t.Fatal("a used-up reading with a past release time let a download through within the hour")
	}
	if got, _ := blocked("grab", 0, time.Hour); got {
		t.Fatal("no probe was allowed an hour after a used-up reading")
	}
	// A probe leaves room for exactly one request.
	if limit, used, _ := ProviderLimitDecision(usedUp, "grab", 1000, 0, 0, window, observed.Add(time.Hour)); limit != 30 || used != 29 {
		t.Fatalf("probe limit=%d used=%d, want 30 29", limit, used)
	}
	// A limit of Media Manager's own that is lower than the indexer's maximum
	// (background searches use part of the allowance) gets no probe.
	if _, _, retryAt := ProviderLimitDecision(usedUp, "query", 60, 0, 0, window, observed.Add(3*time.Hour)); retryAt.IsZero() {
		t.Fatal("a probe was let through for a request with a lower limit of its own")
	}
}
