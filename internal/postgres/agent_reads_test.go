package postgres

import "testing"

func TestAcquiringWorkflowPagesCombineStates(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	for i, state := range []string{"searching", "queued", "publishing", "waiting", "needs_attention"} {
		w := f.newMovieWorkflow(t, "Paged workflow", string(rune('1'+i)))
		if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state=$2,updated_at=now()+($3 * interval '1 second') WHERE id=$1`, w.ID.String(), state, i); err != nil {
			t.Fatal(err)
		}
	}
	first, err := f.store.ListWorkflowSummariesPage(f.ctx, 2, 0, true, "acquiring", false)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	second, err := f.store.ListWorkflowSummariesPage(f.ctx, 2, 2, true, "acquiring", false)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page=%+v err=%v", second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first, second...) {
		if item.State != "searching" && item.State != "queued" && item.State != "publishing" {
			t.Fatalf("unrelated state %q in acquiring", item.State)
		}
		if seen[item.ID.String()] {
			t.Fatal("workflow repeated across pages")
		}
		seen[item.ID.String()] = true
	}
}
