package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
)

func TestPartialSearchDoesNotReportAbsenceOrIncreaseBackoff(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Partial Search", "900778")
	for _, partial := range []bool{true, true, false} {
		var err error
		w, err = f.store.TransitionWorkflow(f.ctx, w.ID, w.Revision, workflows.StateSearching, "test search", "test", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		search, err := f.store.LoadSearchContext(f.ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		if search.CompleteSearches != 0 {
			t.Fatalf("partial searches advanced query rotation: %d", search.CompleteSearches)
		}
		outcomes := []SearchIndexerOutcome{{IndexerInstanceID: f.indexer.ID, State: "succeeded"}, {IndexerInstanceID: f.indexerTwo.ID, State: "succeeded"}}
		if partial {
			outcomes[1].State = "deferred"
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		w, err = f.store.PersistSearch(f.ctx, search, []byte("partial-search"), nil, SearchPersistenceOptions{IndexerOutcomes: outcomes}, now)
		if err != nil {
			t.Fatal(err)
		}
		var reason string
		var next time.Time
		if err := f.store.Pool.QueryRow(f.ctx, `SELECT payload->>'reason',available_at FROM outbox_events WHERE aggregate_id=$1 AND aggregate_revision=$2 AND event_type='workflow.search_requested'`, w.ID.String(), int64(w.Revision)).Scan(&reason, &next); err != nil {
			t.Fatal(err)
		}
		want := "no eligible release yet"
		if partial {
			want = "Search incomplete: some indexers were deferred or failed; retrying."
		}
		if reason != want || !next.Equal(now.Add(30*time.Minute)) {
			t.Fatalf("reason=%q next=%s want reason=%q next=%s", reason, next, want, now.Add(30*time.Minute))
		}
	}
	search, err := f.store.LoadSearchContext(f.ctx, w.ID)
	if err != nil || search.CompleteSearches != 1 {
		t.Fatalf("complete searches=%d err=%v", search.CompleteSearches, err)
	}
}

// TestRepeatedIncompleteSearchesFallBackToTheNormalSchedule proves that an
// indexer that keeps failing does not make a waiting title search every 30
// minutes for good: after three quick retries in a row the next search waits
// for the normal schedule, and a complete search starts the count again.
func TestRepeatedIncompleteSearchesFallBackToTheNormalSchedule(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Partial Streak", "900779")
	base := time.Now().UTC().Truncate(time.Microsecond)
	run := func(step int, partial bool) time.Duration {
		t.Helper()
		var err error
		at := base.Add(time.Duration(step) * time.Minute)
		w, err = f.store.TransitionWorkflow(f.ctx, w.ID, w.Revision, workflows.StateSearching, "test search", "test", at)
		if err != nil {
			t.Fatal(err)
		}
		search, err := f.store.LoadSearchContext(f.ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		outcomes := []SearchIndexerOutcome{{IndexerInstanceID: f.indexer.ID, State: "succeeded"}, {IndexerInstanceID: f.indexerTwo.ID, State: "succeeded"}}
		if partial {
			outcomes[1].State = "failed"
		}
		w, err = f.store.PersistSearch(f.ctx, search, []byte("partial-streak"), nil, SearchPersistenceOptions{IndexerOutcomes: outcomes}, at)
		if err != nil {
			t.Fatal(err)
		}
		var next time.Time
		if err := f.store.Pool.QueryRow(f.ctx, `SELECT available_at FROM outbox_events WHERE aggregate_id=$1 AND aggregate_revision=$2 AND event_type='workflow.search_requested'`, w.ID.String(), int64(w.Revision)).Scan(&next); err != nil {
			t.Fatal(err)
		}
		return next.Sub(at)
	}
	// One complete search: the normal schedule's next step is beyond 30 min.
	run(0, false)
	normal := run(1, false)
	if normal <= 30*time.Minute {
		t.Fatalf("normal schedule after two complete searches=%s, want more than 30m", normal)
	}
	for step := 2; step <= 4; step++ {
		if got := run(step, true); got != 30*time.Minute {
			t.Fatalf("incomplete search %d retried after %s, want 30m", step-1, got)
		}
	}
	if got := run(5, true); got != normal {
		t.Fatalf("fourth incomplete search in a row retried after %s, want the normal %s", got, normal)
	}
	run(6, false)
	if got := run(7, true); got != 30*time.Minute {
		t.Fatalf("an incomplete search after a complete one retried after %s, want 30m", got)
	}
}
