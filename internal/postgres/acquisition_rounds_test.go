package postgres

import (
	"testing"
	"time"
	"uuid"
)

func TestRetryDiscoveryScheduleIsStableBoundedAndBacksOff(t *testing.T) {
	t.Parallel()
	workflowID := uuid.MustParse("018f28f6-4f21-7c62-8a31-ef6ff9856d12")
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

	first := retryDiscoveryAt(workflowID, 1, 0, now)
	if repeated := retryDiscoveryAt(workflowID, 1, 0, now); !repeated.Equal(first) {
		t.Fatalf("same workflow generation was not stable: %s != %s", first, repeated)
	}
	if delay := first.Sub(now); delay < 19*time.Hour+12*time.Minute || delay > 28*time.Hour+48*time.Minute {
		t.Fatalf("24-hour schedule escaped ±20%% jitter: %s", delay)
	}

	eightDays := retryDiscoveryAt(workflowID, 2, 42, now).Sub(now)
	if eightDays < 6*24*time.Hour+9*time.Hour+36*time.Minute || eightDays > 9*24*time.Hour+14*time.Hour+24*time.Minute {
		t.Fatalf("backed-off schedule escaped ±20%% of eight days: %s", eightDays)
	}

	capped := retryDiscoveryAt(workflowID, 3, 500, now).Sub(now)
	if capped < 11*24*time.Hour+4*time.Hour+48*time.Minute || capped > 16*24*time.Hour+19*time.Hour+12*time.Minute {
		t.Fatalf("14-day capped schedule escaped ±20%% jitter: %s", capped)
	}
}

func TestSearchRunCompleteRequiresEveryPlannedIndexer(t *testing.T) {
	t.Parallel()
	indexerA, indexerB := uuid.NewV7(), uuid.NewV7()
	if searchRunComplete(nil) {
		t.Fatal("empty outcome set was treated as complete")
	}
	if searchRunComplete([]SearchIndexerOutcome{{IndexerInstanceID: indexerA, State: "succeeded"}, {IndexerInstanceID: indexerB, State: "deferred"}}) {
		t.Fatal("partial indexer run was treated as complete")
	}
	if !searchRunComplete([]SearchIndexerOutcome{{IndexerInstanceID: indexerA, State: "succeeded"}, {IndexerInstanceID: indexerB, State: "succeeded"}}) {
		t.Fatal("all-successful indexer run was not complete")
	}
}

func TestSearchOutcomesMatchExactPlannedIndexerSet(t *testing.T) {
	t.Parallel()
	indexerA, indexerB := uuid.NewV7(), uuid.NewV7()
	planned := []Integration{{ID: indexerA}, {ID: indexerB}}
	if validSearchIndexerOutcomes(nil, nil) {
		t.Fatal("empty search plan was accepted as persisted evidence")
	}
	if !validSearchIndexerOutcomes(planned, []SearchIndexerOutcome{{IndexerInstanceID: indexerA, State: "succeeded"}, {IndexerInstanceID: indexerB, State: "deferred"}}) {
		t.Fatal("exact planned outcome set was rejected")
	}
	if validSearchIndexerOutcomes(planned, []SearchIndexerOutcome{{IndexerInstanceID: indexerA, State: "succeeded"}}) {
		t.Fatal("missing indexer outcome was accepted")
	}
	if validSearchIndexerOutcomes(planned, []SearchIndexerOutcome{{IndexerInstanceID: indexerA, State: "succeeded"}, {IndexerInstanceID: indexerA, State: "failed"}}) {
		t.Fatal("duplicate indexer outcome was accepted")
	}
	if validSearchIndexerOutcomes(planned, []SearchIndexerOutcome{{IndexerInstanceID: indexerA, State: "succeeded"}, {IndexerInstanceID: indexerB, State: "planned"}}) {
		t.Fatal("unfinished planned outcome was accepted as a persisted result")
	}
}
