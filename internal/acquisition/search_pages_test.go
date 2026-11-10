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

func page(prefix string, count int) []releases.Observation {
	out := make([]releases.Observation, count)
	for i := range out {
		out[i] = releases.Observation{NativeGUID: fmt.Sprintf("%s-%d", prefix, i)}
	}
	return out
}

func TestSearchFollowUpPagesReadsFurtherPagesOnlyAfterAFullPage(t *testing.T) {
	t.Parallel()
	request := newznab.SearchRequest{Limit: 100}
	var offsets []int
	run := func(next newznab.SearchRequest) ([]releases.Observation, error) {
		offsets = append(offsets, next.Offset)
		if next.Offset == 100 {
			// Three items repeat the first page, as live providers do.
			return append(page("first", 3), page("second", 40)...), nil
		}
		return nil, errors.New("unexpected page")
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
	endless := func(next newznab.SearchRequest) ([]releases.Observation, error) {
		calls++
		return page(fmt.Sprintf("p%d", next.Offset), 100), nil
	}
	budget := searchPagingWait
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, endless); len(got) != 100*maxSearchPages || calls != maxSearchPages-1 {
		t.Fatalf("paging must stop at %d pages: %d results, %d calls", maxSearchPages, len(got), calls)
	}
	same := func(newznab.SearchRequest) ([]releases.Observation, error) { calls++; return page("first", 100), nil }
	calls = 0
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, same); len(got) != 100 || calls != 1 {
		t.Fatalf("a provider that ignores the offset must stop paging: %d results, %d calls", len(got), calls)
	}
	failing := func(newznab.SearchRequest) ([]releases.Observation, error) { return nil, errors.New("http 500") }
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, failing); len(got) != 100 {
		t.Fatalf("a failed follow-up page must keep the first page, got %d", len(got))
	}
	// A deferral longer than the remaining wait budget is not waited out.
	calls = 0
	late := func(newznab.SearchRequest) ([]releases.Observation, error) {
		calls++
		return nil, pagingDeferral{at: time.Now().Add(time.Hour)}
	}
	started := time.Now()
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, late); len(got) != 100 || calls != 1 || time.Since(started) > time.Second || budget != searchPagingWait {
		t.Fatalf("long deferral must return at once with the budget untouched: %d results, %d calls", len(got), calls)
	}
	// A short pacing deferral is waited out once and charged to the budget.
	calls = 0
	paced := func(next newznab.SearchRequest) ([]releases.Observation, error) {
		calls++
		if calls == 1 {
			return nil, pagingDeferral{at: time.Now().Add(30 * time.Millisecond)}
		}
		return page("second", 10), nil
	}
	if got := searchFollowUpPages(context.Background(), request, page("first", 100), &budget, paced); len(got) != 110 || calls != 2 || budget >= searchPagingWait {
		t.Fatalf("short deferral must be retried once: %d results, %d calls, budget %s", len(got), calls, budget)
	}
}
