package acquisition

import (
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

func feedUploads(prefix string, count int) newznab.SearchPage {
	page := newznab.SearchPage{}
	for index := 0; index < count; index++ {
		page.Observations = append(page.Observations, releases.Observation{NativeGUID: fmt.Sprintf("%s-%d", prefix, index), Title: fmt.Sprintf("%s.%d", prefix, index)})
	}
	return page
}

func TestReadFeedPagesReadsOnOnlyWhileAWholePageIsNew(t *testing.T) {
	request := newznab.SearchRequest{Mode: "search", Limit: 3}
	seen := &feedSeen{known: map[string]bool{}}
	var offsets []int
	answers := []newznab.SearchPage{feedUploads("a", 3)}
	fetch := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		offsets = append(offsets, next.Offset)
		if len(offsets) > len(answers) {
			return newznab.SearchPage{}, nil
		}
		return answers[len(offsets)-1], nil
	}

	// The first read ever has nothing to catch up with: one page.
	pages, err := readFeedPages(request, seen, fetch)
	if err != nil || len(offsets) != 1 || pages.items != 3 || len(pages.fresh) != 3 || pages.firstPageFresh != 3 || pages.firstPageSize != 3 {
		t.Fatalf("first read pages=%+v offsets=%v err=%v", pages, offsets, err)
	}

	// Part of the page is known: nothing was missed, one request.
	offsets, answers = nil, []newznab.SearchPage{{Observations: append(feedUploads("b", 2).Observations, feedUploads("a", 1).Observations...)}}
	pages, err = readFeedPages(request, seen, fetch)
	if err != nil || len(offsets) != 1 || len(pages.fresh) != 2 || pages.firstPageFresh != 2 {
		t.Fatalf("overlapping read pages=%+v offsets=%v err=%v", pages, offsets, err)
	}

	// A whole page of new uploads: read on from where the page ended until a
	// known upload appears.
	offsets, answers = nil, []newznab.SearchPage{feedUploads("c", 3), {Observations: append(feedUploads("d", 2).Observations, feedUploads("b", 1).Observations...)}}
	pages, err = readFeedPages(request, seen, fetch)
	if err != nil || len(offsets) != 2 || offsets[1] != 3 || pages.items != 6 || len(pages.fresh) != 5 || pages.firstPageFresh != 3 {
		t.Fatalf("catch-up read pages=%+v offsets=%v err=%v", pages, offsets, err)
	}

	// The catch-up is bounded.
	offsets, answers = nil, []newznab.SearchPage{feedUploads("e", 3), feedUploads("f", 3), feedUploads("g", 3), feedUploads("h", 3)}
	pages, err = readFeedPages(request, seen, fetch)
	if err != nil || len(offsets) != feedMaxPages || len(pages.fresh) != 3*feedMaxPages {
		t.Fatalf("bounded read pages=%+v offsets=%v err=%v", pages, offsets, err)
	}
}

func TestReadFeedPagesKeepsTheFirstPageWhenALaterPageFails(t *testing.T) {
	seen := &feedSeen{known: map[string]bool{"old": true}, order: []string{"old"}}
	calls := 0
	pages, err := readFeedPages(newznab.SearchRequest{Limit: 2}, seen, func(newznab.SearchRequest) (newznab.SearchPage, error) {
		calls++
		if calls == 2 {
			return newznab.SearchPage{}, errors.New("provider failed")
		}
		return feedUploads("new", 2), nil
	})
	if err != nil || calls != 2 || len(pages.fresh) != 2 {
		t.Fatalf("pages=%+v calls=%d err=%v", pages, calls, err)
	}

	failure := errors.New("provider failed")
	if _, err := readFeedPages(newznab.SearchRequest{Limit: 2}, seen, func(newznab.SearchRequest) (newznab.SearchPage, error) { return newznab.SearchPage{}, failure }); !errors.Is(err, failure) {
		t.Fatalf("a failed first page must be the read's error, got %v", err)
	}
}

func TestReadFeedPagesSkipsUploadsWithoutAProviderID(t *testing.T) {
	seen := &feedSeen{known: map[string]bool{}}
	page := newznab.SearchPage{Observations: []releases.Observation{{NativeGUID: "weak:abc", Title: "Weak"}, {NativeGUID: "", Title: "Empty"}, {NativeGUID: "real", Title: "Real"}}}
	pages, err := readFeedPages(newznab.SearchRequest{Limit: 3}, seen, func(newznab.SearchRequest) (newznab.SearchPage, error) { return page, nil })
	if err != nil || len(pages.fresh) != 1 || pages.fresh[0].NativeGUID != "real" || pages.items != 3 {
		t.Fatalf("pages=%+v err=%v", pages, err)
	}
}

func TestFeedSeenForgetsTheOldestFirst(t *testing.T) {
	seen := &feedSeen{known: map[string]bool{}}
	for index := 0; index < feedSeenLimit+10; index++ {
		if !seen.add(fmt.Sprintf("id-%d", index)) {
			t.Fatalf("id-%d was not new", index)
		}
	}
	if len(seen.known) != feedSeenLimit || len(seen.order) != feedSeenLimit {
		t.Fatalf("remembered %d/%d, want %d", len(seen.known), len(seen.order), feedSeenLimit)
	}
	if seen.add(fmt.Sprintf("id-%d", feedSeenLimit+9)) {
		t.Fatal("the newest upload was forgotten")
	}
	if !seen.add("id-0") {
		t.Fatal("the oldest upload was not forgotten")
	}
}

func TestFeedIntervalFollowsUploadRateWithinBoundsAndBudget(t *testing.T) {
	roomy := indexers.NewznabBudget(3000, 10000, 0, 86400)
	if got := feedInterval(0, roomy); got != feedStartInterval {
		t.Fatalf("start interval=%s", got)
	}
	if got := feedInterval(time.Second, roomy); got != feedMinInterval {
		t.Fatalf("lower bound=%s", got)
	}
	if got := feedInterval(time.Hour, roomy); got != feedMaxInterval {
		t.Fatalf("upper bound=%s", got)
	}
	// Mostly new: sooner. Mostly known: later. In between and on the first
	// read: unchanged.
	if got := nextFeedInterval(9*time.Minute, 70, 100, false, roomy); got != 6*time.Minute {
		t.Fatalf("busy indexer interval=%s", got)
	}
	if got := nextFeedInterval(10*time.Minute, 10, 100, false, roomy); got != 15*time.Minute {
		t.Fatalf("quiet indexer interval=%s", got)
	}
	if got := nextFeedInterval(10*time.Minute, 40, 100, false, roomy); got != 10*time.Minute {
		t.Fatalf("steady indexer interval=%s", got)
	}
	if got := nextFeedInterval(10*time.Minute, 100, 100, true, roomy); got != 10*time.Minute {
		t.Fatalf("first read interval=%s", got)
	}
	if got := nextFeedInterval(10*time.Minute, 0, 0, false, roomy); got != 10*time.Minute {
		t.Fatalf("empty page interval=%s", got)
	}
	if got := nextFeedInterval(feedMinInterval, 100, 100, false, roomy); got != feedMinInterval {
		t.Fatalf("interval fell below the bound: %s", got)
	}

	// The reads never use more than a tenth of the query allowance, counting
	// every page a read may take: 1,000 queries a day allow 100 requests, so
	// 33 reads of up to three pages, one every 43 m 12 s, even above the usual
	// upper bound.
	tight := indexers.NewznabBudget(3000, 1000, 0, 86400)
	tightFloor := 86400 * time.Second * feedMaxPages * feedBudgetShare / 1000
	if got := feedInterval(time.Minute, tight); got != tightFloor || got != 43*time.Minute+12*time.Second {
		t.Fatalf("budget floor=%s", got)
	}
	if got := nextFeedInterval(tightFloor, 100, 100, false, tight); got != tightFloor {
		t.Fatalf("a busy indexer broke its budget floor: %s", got)
	}
	// Whatever the allowance, a day of reads at the lower bound stays inside
	// the share even when every read takes all its pages.
	for _, limit := range []int{1, 7, 10, 250, 1000, 8640, 10000, 30000} {
		budget := indexers.NewznabBudget(3000, limit, 0, 86400)
		interval := feedInterval(time.Second, budget)
		reads := int(budget.LimitWindow / interval)
		if requests := reads * feedMaxPages; requests*feedBudgetShare > limit {
			t.Fatalf("limit %d: %d requests a window exceed a tenth", limit, requests)
		}
		if interval < feedMinInterval {
			t.Fatalf("limit %d: interval %s below the lower bound", limit, interval)
		}
	}
	// The owner's indexers (10,000 or more a day) keep the five-minute bound.
	if got := feedInterval(time.Second, indexers.NewznabBudget(3000, 10000, 0, 86400)); got != feedMinInterval {
		t.Fatalf("roomy lower bound=%s", got)
	}
}

func TestFeedFailureDelayDoublesToItsCap(t *testing.T) {
	want := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for index, expected := range want {
		if got := feedFailureDelay(index + 1); got != expected {
			t.Fatalf("failure %d delay=%s want %s", index+1, got, expected)
		}
	}
	if got := feedFailureDelay(1000); got != feedFailureCap {
		t.Fatalf("delay after many failures=%s", got)
	}
}

func feedTestMedia(t *testing.T, kind catalog.MediaKind, title string, year uint16, identities ...catalog.ExternalIdentity) catalog.MediaItem {
	t.Helper()
	item, err := catalog.New(kind, title, &year, identities, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func providerID(name, value string) releases.Fact {
	return releases.Fact{Name: name, Values: []string{value}, State: releases.FactObserved}
}

// TestFeedMatchUsesTheSearchChecksForTitleIdentityAndScope proves a new upload
// only counts for a waiting title when the search's own checks would let it
// through as far as the name and the provider's IDs can tell. Language and
// quality are not judged here at all: the search that follows does that.
func TestFeedMatchUsesTheSearchChecksForTitleIdentityAndScope(t *testing.T) {
	movie := feedTestMedia(t, catalog.KindMovie, "Black Harbour", 2025, catalog.ExternalIdentity{Namespace: "imdb", Value: "tt7654321", State: catalog.IdentityVerified, Source: "test"})
	series := feedTestMedia(t, catalog.KindSeries, "North Quay", 2024, catalog.ExternalIdentity{Namespace: "tvdb", Value: "445566", State: catalog.IdentityVerified, Source: "test"})
	movieWorkflow, seasonOne, seasonTwo, episode, broken := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	titles := newFeedTitles([]postgres.FeedWorkflow{
		{WorkflowID: movieWorkflow, Variant: "", Media: movie},
		{WorkflowID: seasonOne, Variant: "season:1", Media: series},
		{WorkflowID: seasonTwo, Variant: "season:2", Media: series},
		{WorkflowID: episode, Variant: "season:2:episode:3", Media: series},
		{WorkflowID: broken, Variant: "season:x", Media: series},
	})
	cases := []struct {
		name   string
		upload releases.Observation
		want   []uuid.UUID
	}{
		{"movie with its year", releases.Observation{Title: "Black.Harbour.2025.1080p.WEB-DL.DDP5.1.H.264-GRP"}, []uuid.UUID{movieWorkflow}},
		{"movie with its provider ID and no year", releases.Observation{Title: "Black.Harbour.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("imdb_id", "tt7654321")}}, []uuid.UUID{movieWorkflow}},
		{"movie without year or ID", releases.Observation{Title: "Black.Harbour.1080p.WEB-DL-GRP"}, nil},
		{"another film of the same name", releases.Observation{Title: "Black.Harbour.1998.1080p.BluRay-GRP"}, nil},
		{"another film by provider ID", releases.Observation{Title: "Black.Harbour.2025.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("imdb_id", "tt0000001")}}, nil},
		// The search accepts a release that begins with the saved title and
		// carries its year; the list follows the search, and the search that
		// is brought forward decides with the provider's IDs.
		{"a longer title with the same start and year, as in the search", releases.Observation{Title: "Black.Harbour.Nights.2025.1080p.WEB-DL-GRP"}, []uuid.UUID{movieWorkflow}},
		{"a longer title of another year", releases.Observation{Title: "Black.Harbour.Nights.2023.1080p.WEB-DL-GRP"}, nil},
		{"a title that only shares its first word", releases.Observation{Title: "Black.Water.2025.1080p.WEB-DL-GRP"}, nil},
		{"season pack with the series ID", releases.Observation{Title: "North.Quay.S01.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("tvdb_id", "445566")}}, []uuid.UUID{seasonOne}},
		{"season pack without identity evidence", releases.Observation{Title: "North.Quay.S01.1080p.WEB-DL-GRP"}, nil},
		{"season pack with the first-air year", releases.Observation{Title: "North.Quay.2024.S02.1080p.WEB-DL-GRP"}, []uuid.UUID{seasonTwo}},
		{"single episode", releases.Observation{Title: "North.Quay.S02E03.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("tvdb_id", "445566")}}, []uuid.UUID{episode}},
		{"episode nobody waits for", releases.Observation{Title: "North.Quay.S02E04.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("tvdb_id", "445566")}}, nil},
		{"season nobody waits for", releases.Observation{Title: "North.Quay.S03.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("tvdb_id", "445566")}}, nil},
		{"another series by provider ID", releases.Observation{Title: "North.Quay.S01.1080p.WEB-DL-GRP", Facts: []releases.Fact{providerID("tvdb_id", "999")}}, nil},
		{"unrelated upload", releases.Observation{Title: "Something.Else.2025.1080p.WEB-DL-GRP"}, nil},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			test.upload.NativeGUID = fmt.Sprintf("guid-%d", index)
			matches := titles.match([]releases.Observation{test.upload}, time.Now().UTC())
			if len(matches) != len(test.want) {
				t.Fatalf("matches=%+v want workflows %v", matches, test.want)
			}
			for position, match := range matches {
				if match.WorkflowID != test.want[position] || match.NativeGUID != test.upload.NativeGUID || match.ReleaseTitle != test.upload.Title {
					t.Fatalf("match=%+v want workflow %s", match, test.want[position])
				}
			}
		})
	}
}

// TestFeedMatchAgreesWithConstrainMediaMatch keeps the list's matching tied
// to the search: whatever the list matches, the search's identity check calls
// eligible for the same media.
func TestFeedMatchAgreesWithConstrainMediaMatch(t *testing.T) {
	movie := feedTestMedia(t, catalog.KindMovie, "Black Harbour", 2025)
	workflow := uuid.NewV7()
	titles := newFeedTitles([]postgres.FeedWorkflow{{WorkflowID: workflow, Media: movie}})
	now := time.Now().UTC()
	for _, title := range []string{"Black.Harbour.2025.1080p.WEB-DL-GRP", "Black.Harbour.1080p.WEB-DL-GRP", "Black.Harbour.2019.1080p.WEB-DL-GRP", "Harbour.Black.2025.1080p-GRP", "Black Harbour (2025) 2160p"} {
		upload := releases.Observation{NativeGUID: title, Title: title}
		observation := releases.InferTitleFacts(upload, now)
		eligible := constrainMediaMatch(observation, releases.Evaluation{Verdict: releases.VerdictEligible}, movie).Verdict == releases.VerdictEligible
		if matched := len(titles.match([]releases.Observation{upload}, now)) == 1; matched != eligible {
			t.Fatalf("%q: list matched=%v, search identity check eligible=%v", title, matched, eligible)
		}
	}
}

func TestFeedRequestCategoriesFollowTheIndexerThenTheOwnerThenTheStandardRoots(t *testing.T) {
	cases := []struct {
		name   string
		config indexerConfig
		want   []int
	}{
		{"nothing configured", indexerConfig{}, []int{2000, 5000}},
		{"search categories", indexerConfig{Categories: []int{2040, 5040}, MovieCategoryIDs: []int{2000}, SeriesCategoryIDs: []int{5000}}, []int{2040, 5040}},
		{"owner selection", indexerConfig{MovieCategoryIDs: []int{2040, 2045}, SeriesCategoryIDs: []int{5040}}, []int{2040, 2045, 5040}},
		{"owner selection awaiting review", indexerConfig{MovieCategoryIDs: []int{2040}, SeriesCategoryIDs: []int{5040}, CategorySelectionReviewRequired: true}, []int{2000, 5000}},
		{"only movies selected", indexerConfig{MovieCategoryIDs: []int{2040}}, []int{2000, 5000}},
	}
	for _, test := range cases {
		if got := feedRequestCategories(test.config); fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Fatalf("%s: categories=%v want %v", test.name, got, test.want)
		}
	}
}
