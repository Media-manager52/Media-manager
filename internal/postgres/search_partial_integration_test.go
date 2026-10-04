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
