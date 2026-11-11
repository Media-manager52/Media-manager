package postgres

import (
	"testing"
	"time"
)

func TestIncompleteSearchRetryRespectsProviderDeferralsAndFloor(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	soon, later := now.Add(3*time.Second), now.Add(4*time.Hour)
	for _, tc := range []struct {
		name     string
		outcomes []SearchIndexerOutcome
		want     time.Time
	}{
		{"pacing", []SearchIndexerOutcome{{State: "succeeded"}, {State: "deferred", RetryAt: &soon}}, now.Add(30 * time.Minute)},
		{"budget", []SearchIndexerOutcome{{State: "succeeded"}, {State: "deferred", RetryAt: &later}}, later},
		{"failure", []SearchIndexerOutcome{{State: "failed"}, {State: "deferred", RetryAt: &later}}, now.Add(30 * time.Minute)},
	} {
		if got := incompleteSearchRetryAt(tc.outcomes, now); !got.Equal(tc.want) {
			t.Fatalf("%s retry=%s want=%s", tc.name, got, tc.want)
		}
	}
}

func TestNoSelectionRetryDelayBacksOffToCap(t *testing.T) {
	t.Parallel()
	for runs, want := range map[int]time.Duration{
		0:  30 * time.Minute,
		1:  30 * time.Minute,
		2:  time.Hour,
		3:  2 * time.Hour,
		4:  4 * time.Hour,
		5:  8 * time.Hour,
		6:  16 * time.Hour,
		7:  32 * time.Hour,
		9:  128 * time.Hour,
		10: 7 * 24 * time.Hour,
		48: 7 * 24 * time.Hour,
	} {
		if got := noSelectionRetryDelay(runs, false); got != want {
			t.Fatalf("noSelectionRetryDelay(%d)=%s, want %s", runs, got, want)
		}
	}
	for runs, want := range map[int]time.Duration{1: 30 * time.Minute, 6: 16 * time.Hour, 7: 24 * time.Hour, 48: 24 * time.Hour} {
		if got := noSelectionRetryDelay(runs, true); got != want {
			t.Fatalf("recent noSelectionRetryDelay(%d)=%s, want %s", runs, got, want)
		}
	}
}

func TestRecentReleaseUsesThisAndLastYear(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	year := func(y uint16) *uint16 { return &y }
	for _, tc := range []struct {
		year *uint16
		want bool
	}{{year(2026), true}, {year(2025), true}, {year(2024), false}, {year(1999), false}, {nil, false}} {
		if got := recentRelease(tc.year, now); got != tc.want {
			t.Fatalf("recentRelease(%v)=%v want %v", tc.year, got, tc.want)
		}
	}
}

func TestRecoveryPartialRetryWaitsForFloor(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	// A pacing slot seconds away must not trigger a rescan every few minutes.
	if got := recoveryPartialRetryAt(now.Add(3*time.Second), now); !got.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("pacing retry=%s", got)
	}
	// A budget reset within the floor waits for the floor too.
	if got := recoveryPartialRetryAt(now.Add(9*time.Hour), now); !got.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("budget retry within the floor=%s", got)
	}
	// A later budget reset is respected as is.
	if got := recoveryPartialRetryAt(now.Add(15*time.Hour), now); !got.Equal(now.Add(15 * time.Hour)) {
		t.Fatalf("budget retry=%s", got)
	}
}
