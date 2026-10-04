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
		6:  12 * time.Hour,
		48: 12 * time.Hour,
	} {
		if got := noSelectionRetryDelay(runs); got != want {
			t.Fatalf("noSelectionRetryDelay(%d)=%s, want %s", runs, got, want)
		}
	}
}
