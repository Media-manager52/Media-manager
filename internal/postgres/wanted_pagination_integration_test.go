package postgres

import "testing"

func TestListWantedPageHasStableTieBreakerAcrossEqualTimestamps(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	first := fixture.newMovieWorkflow(t, "Wanted Page One", "901701")
	second := fixture.newMovieWorkflow(t, "Wanted Page Two", "901702")
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE workflows SET updated_at='2026-10-30T10:00:00Z' WHERE id=ANY($1::uuid[])`, []string{first.ID.String(), second.ID.String()}); err != nil {
		t.Fatal(err)
	}
	var paged []string
	for offset := 0; offset < 2; offset++ {
		page, err := fixture.store.ListWantedPage(fixture.ctx, 1, offset)
		if err != nil || len(page) != 1 {
			t.Fatalf("wanted page offset=%d=%+v err=%v", offset, page, err)
		}
		paged = append(paged, page[0].WorkflowID.String())
	}
	if paged[0] == paged[1] || (paged[0] != first.ID.String() && paged[0] != second.ID.String()) || (paged[1] != first.ID.String() && paged[1] != second.ID.String()) {
		t.Fatalf("wanted pages=%v, want both workflow identities once", paged)
	}
}
