package postgres

import "testing"

func TestListManagedFilesPageAppliesOffset(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	_, version := fixture.publishedMovie(t, "Paged Files One", "901601")
	firstPage, err := fixture.store.ListManagedFilesPage(fixture.ctx, 1, 0)
	if err != nil || len(firstPage) != 1 {
		t.Fatalf("first page=%+v err=%v", firstPage, err)
	}
	if firstPage[0].ID != version {
		t.Fatalf("first page id=%s, want published version %s", firstPage[0].ID, version)
	}
	secondPage, err := fixture.store.ListManagedFilesPage(fixture.ctx, 1, 1)
	if err != nil || len(secondPage) != 0 {
		t.Fatalf("offset past end=%+v err=%v, want empty", secondPage, err)
	}
}
