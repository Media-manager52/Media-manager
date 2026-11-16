package acquisition

import (
	"fmt"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

func TestFeedStreamsForReadsOnlyTheMeasuredIndexerAsTwoRequests(t *testing.T) {
	single := feedStreamsFor(indexerConfig{BaseURL: "https://msgnews.live/api"}, nil)
	if len(single) != 1 || single[0].Name != "" || single[0].Mode != "search" || fmt.Sprint(single[0].Categories) != "[2000 5000]" {
		t.Fatalf("the ordinary request changed: %+v", single)
	}
	split := feedStreamsFor(indexerConfig{BaseURL: "https://abnzb.com/api"}, nil)
	if len(split) != 2 || split[0].Name != "movie" || split[0].Mode != "movie" || fmt.Sprint(split[0].Categories) != "[2000]" ||
		split[1].Name != "series" || split[1].Mode != "tvsearch" || fmt.Sprint(split[1].Categories) != "[5000]" {
		t.Fatalf("split=%+v", split)
	}
}

func TestFeedStreamCategoriesStayInsideTheStreamsRoot(t *testing.T) {
	movie := newznab.FeedStream{Name: newznab.FeedStreamMovie, Mode: "movie", RootCategory: 2000}
	series := newznab.FeedStream{Name: newznab.FeedStreamSeries, Mode: "tvsearch", RootCategory: 5000}
	for _, test := range []struct {
		name              string
		config            indexerConfig
		wantMovie, wantTV []int
	}{
		{"nothing configured", indexerConfig{}, []int{2000}, []int{5000}},
		{"search categories are split by root", indexerConfig{Categories: []int{2040, 5040, 5045, 7000}}, []int{2040}, []int{5040, 5045}},
		{"search categories of one kind only", indexerConfig{Categories: []int{5040}}, []int{2000}, []int{5040}},
		{"owner selection", indexerConfig{MovieCategoryIDs: []int{2040, 2045}, SeriesCategoryIDs: []int{5040}}, []int{2040, 2045}, []int{5040}},
		{"owner selection awaiting review", indexerConfig{MovieCategoryIDs: []int{2040}, SeriesCategoryIDs: []int{5040}, CategorySelectionReviewRequired: true}, []int{2000}, []int{5000}},
		{"only movies selected", indexerConfig{MovieCategoryIDs: []int{2040}}, []int{2000}, []int{5000}},
	} {
		if got := feedStreamCategories(test.config, movie); fmt.Sprint(got) != fmt.Sprint(test.wantMovie) {
			t.Fatalf("%s: movie categories=%v want %v", test.name, got, test.wantMovie)
		}
		if got := feedStreamCategories(test.config, series); fmt.Sprint(got) != fmt.Sprint(test.wantTV) {
			t.Fatalf("%s: series categories=%v want %v", test.name, got, test.wantTV)
		}
	}
}

// Two requests share the same tenth of the query allowance: where the
// allowance sets the floor, each request's floor is twice as long, so the two
// together cost what the one request cost.
func TestFeedStreamIntervalKeepsTwoRequestsInsideTheSameShare(t *testing.T) {
	tight := indexers.NewznabBudget(3000, 2000, 0, 86400)
	one, two := feedStreamInterval(time.Second, tight, 1), feedStreamInterval(time.Second, tight, 2)
	if one != feedInterval(time.Second, tight) || two != 2*one {
		t.Fatalf("one request every %s, each of two every %s", one, two)
	}
	perDay := func(interval time.Duration, requests int) int {
		return int(24*time.Hour/interval) * feedBudgetPages * requests
	}
	if share := tight.QueryLimit / feedBudgetShare; perDay(one, 1) > share || perDay(two, 2) > share {
		t.Fatalf("pages a day: one=%d two=%d, share=%d", perDay(one, 1), perDay(two, 2), share)
	}
	// With a daily allowance of 10,000 one request may be read every five
	// minutes; each of two no more often than every 8 minutes 38 seconds.
	roomy := indexers.NewznabBudget(3000, 10000, 0, 86400)
	if got := feedStreamInterval(time.Second, roomy, 1); got != feedMinInterval {
		t.Fatalf("lower bound with one request=%s", got)
	}
	if got := feedStreamInterval(time.Second, roomy, 2); got != 518400*time.Millisecond {
		t.Fatalf("lower bound with two requests=%s", got)
	}
	if got := nextFeedStreamInterval(10*time.Minute, 10, 100, false, roomy, 2); got != 15*time.Minute {
		t.Fatalf("quiet stream interval=%s", got)
	}
}
