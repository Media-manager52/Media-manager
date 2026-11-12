package acquisition

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/releases"
)

type pagingDeferral struct{ at time.Time }

func (deferral pagingDeferral) Error() string      { return "indexer_pacing_deferred" }
func (deferral pagingDeferral) RetryAt() time.Time { return deferral.at }

func observations(prefix string, count int) []releases.Observation {
	out := make([]releases.Observation, count)
	for i := range out {
		out[i] = releases.Observation{NativeGUID: fmt.Sprintf("%s-%d", prefix, i)}
	}
	return out
}

func page(prefix string, count int) newznab.SearchPage {
	return newznab.SearchPage{Observations: observations(prefix, count)}
}

func TestSearchFollowUpPagesReadsFurtherPagesOnlyAfterAFullPage(t *testing.T) {
	t.Parallel()
	request := newznab.SearchRequest{Limit: 100}
	var offsets []int
	run := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		offsets = append(offsets, next.Offset)
		if next.Offset == 100 {
			// Three items repeat the first page, as live providers do.
			return newznab.SearchPage{Observations: append(observations("first", 3), observations("second", 40)...)}, nil
		}
		return newznab.SearchPage{}, errors.New("unexpected page")
	}
	budget := searchPagingWait
	if got := searchFollowUpPages(context.Background(), request, page("first", 60), &budget, run); len(got) != 60 || len(offsets) != 0 {
		t.Fatalf("a partly filled page must not page: %d results, offsets %v", len(got), offsets)
	}
	got := searchFollowUpPages(context.Background(), request, page("first", 99), &budget, run)
	if len(got) != 139 || len(offsets) != 1 || offsets[0] != 100 {
		t.Fatalf("want 99 + 40 new results from one follow-up page, got %d, offsets %v", len(got), offsets)
	}
}

func TestSearchFollowUpPagesIsBoundedAndKeepsEarlierPagesOnFailure(t *testing.T) {
	t.Parallel()
	request := newznab.SearchRequest{Limit: 100}
	calls := 0
	endless := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		calls++
		return page(fmt.Sprintf("p%d", next.Offset), 100), nil
	}
	budget := searchPagingWait
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, endless); len(got) != 100*maxSearchPages || calls != maxSearchPages-1 {
		t.Fatalf("paging must stop at %d pages: %d results, %d calls", maxSearchPages, len(got), calls)
	}
	same := func(newznab.SearchRequest) (newznab.SearchPage, error) { calls++; return page("first", 100), nil }
	calls = 0
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, same); len(got) != 100 || calls != 1 {
		t.Fatalf("a provider that ignores the offset must stop paging: %d results, %d calls", len(got), calls)
	}
	failing := func(newznab.SearchRequest) (newznab.SearchPage, error) {
		return newznab.SearchPage{}, errors.New("http 500")
	}
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, failing); len(got) != 100 {
		t.Fatalf("a failed follow-up page must keep the first page, got %d", len(got))
	}
	// A deferral longer than the remaining wait budget is not waited out.
	calls = 0
	late := func(newznab.SearchRequest) (newznab.SearchPage, error) {
		calls++
		return newznab.SearchPage{}, pagingDeferral{at: time.Now().Add(time.Hour)}
	}
	started := time.Now()
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, late); len(got) != 100 || calls != 1 || time.Since(started) > time.Second || budget != searchPagingWait {
		t.Fatalf("long deferral must return at once with the budget untouched: %d results, %d calls", len(got), calls)
	}
	// A short pacing deferral is waited out once and charged to the budget.
	calls = 0
	paced := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		calls++
		if calls == 1 {
			return newznab.SearchPage{}, pagingDeferral{at: time.Now().Add(30 * time.Millisecond)}
		}
		return page("second", 10), nil
	}
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, paced); len(got) != 110 || calls != 2 || budget >= searchPagingWait {
		t.Fatalf("short deferral must be retried once: %d results, %d calls, budget %s", len(got), calls, budget)
	}
}

func TestSearchFollowUpPagesFollowsTheStatedTotalThroughCappedPages(t *testing.T) {
	t.Parallel()
	// Asked for 500 per page, the provider answers with 100 and states 227.
	request := newznab.SearchRequest{Limit: 500}
	var offsets []int
	capped := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		offsets = append(offsets, next.Offset)
		size := 227 - next.Offset
		if size > 100 {
			size = 100
		}
		return newznab.SearchPage{Observations: observations(fmt.Sprintf("p%d", next.Offset), size), Total: 227}, nil
	}
	budget := searchPagingWait
	first := newznab.SearchPage{Observations: observations("p0", 100), Total: 227}
	got := searchFollowUpPages(context.Background(), request, first, &budget, capped)
	if len(got) != 227 || len(offsets) != 2 || offsets[0] != 100 || offsets[1] != 200 {
		t.Fatalf("capped pages must be followed by the stated total: %d results, offsets %v", len(got), offsets)
	}
	// A total within a tenth of what was read costs no extra request.
	offsets = nil
	exact := newznab.SearchPage{Observations: observations("p0", 85), Total: 89}
	if got := searchFollowUpPages(context.Background(), request, exact, &budget, capped); len(got) != 85 || len(offsets) != 0 {
		t.Fatalf("a nearly complete answer must not page: %d results, offsets %v", len(got), offsets)
	}
	// Without a stated total a short page ends the search, as before.
	if got := searchFollowUpPages(context.Background(), request, page("p0", 100), &budget, capped); len(got) != 100 || len(offsets) != 0 {
		t.Fatalf("a short page without a total must not page: %d results, offsets %v", len(got), offsets)
	}
}

func TestSearchFollowUpPagesStopsAtTheResultBound(t *testing.T) {
	t.Parallel()
	request := newznab.SearchRequest{Limit: 500}
	calls := 0
	endless := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		calls++
		return page(fmt.Sprintf("p%d", next.Offset), 500), nil
	}
	budget := searchPagingWait
	if got := searchFollowUpPages(context.Background(), request, page("first", 500), &budget, endless); len(got) != maxSearchResults || calls != 1 {
		t.Fatalf("paging must stop at %d results: %d results, %d calls", maxSearchResults, len(got), calls)
	}
}

func TestSearchPageLimitFollowsOwnerThenProviderWithinBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		maximum, stated int
		want            int
	}{
		{"nothing stated", 0, 0, 100},
		{"provider advertises fewer", 50, 0, 50},
		{"provider advertises more", 500, 0, 500},
		{"provider advertises far more", 5000, 0, 500},
		{"owner states more than the provider advertises", 100, 300, 300},
		{"owner states fewer", 500, 100, 100},
		{"owner states far more", 100, 9000, 500},
	}
	for _, test := range cases {
		if got := searchPageLimit(indexerConfig{MaximumLimit: test.maximum, PageLimit: test.stated}); got != test.want {
			t.Fatalf("%s: got %d, want %d", test.name, got, test.want)
		}
	}
}

func TestSearchAllPagesIsIncompleteWhenARepeatedPageLeavesTheStatedTotalUnread(t *testing.T) {
	t.Parallel()
	request := newznab.SearchRequest{Limit: 100}
	// The provider ignores the offset: every page repeats the first, while
	// its total says 300 results exist.
	repeating := func(newznab.SearchRequest) (newznab.SearchPage, error) {
		return newznab.SearchPage{Observations: observations("p0", 100), Total: 300}, nil
	}
	budget := searchPagingWait
	first := newznab.SearchPage{Observations: observations("p0", 100), Total: 300}
	got, complete := searchAllPages(context.Background(), request, first, &budget, repeating)
	if len(got) != 100 || complete {
		t.Fatalf("a repeated page with more stated must not count as complete: %d results, complete=%v", len(got), complete)
	}
	// Without a stated total the repeated page ends the answer as complete.
	unstated := func(newznab.SearchRequest) (newznab.SearchPage, error) { return page("p0", 100), nil }
	if got, complete := searchAllPages(context.Background(), request, page("p0", 100), &budget, unstated); len(got) != 100 || !complete {
		t.Fatalf("a repeated page without a total: %d results, complete=%v", len(got), complete)
	}
}
