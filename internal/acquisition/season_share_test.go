package acquisition

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

func TestSearchAllPagesReportsWhetherEverythingWasRead(t *testing.T) {
	t.Parallel()
	request := newznab.SearchRequest{Limit: 100}
	budget := searchPagingWait
	if _, complete := searchAllPages(context.Background(), request, page("one", 40), &budget, nil); !complete {
		t.Fatal("a short first page with nothing more stated is everything")
	}
	stated := newznab.SearchPage{Observations: observations("stated", 40), Total: 400}
	failing := func(newznab.SearchRequest) (newznab.SearchPage, error) {
		return newznab.SearchPage{}, errors.New("down")
	}
	if _, complete := searchAllPages(context.Background(), request, stated, &budget, failing); complete {
		t.Fatal("a failed follow-up page leaves the answer incomplete")
	}
	endless := func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		return page("p"+string(rune('a'+next.Offset/100)), 100), nil
	}
	if _, complete := searchAllPages(context.Background(), request, page("full", 100), &budget, endless); complete {
		t.Fatal("stopping at the page bound leaves the answer incomplete")
	}
	last := func(next newznab.SearchRequest) (newznab.SearchPage, error) { return page("last", 20), nil }
	if got, complete := searchAllPages(context.Background(), request, page("full", 100), &budget, last); !complete || len(got) != 120 {
		t.Fatalf("a short last page is everything: complete=%v results=%d", complete, len(got))
	}
	if _, complete := searchAllPages(context.Background(), newznab.SearchRequest{}, page("unbounded", 10), &budget, nil); complete {
		t.Fatal("without a page size completeness is unknown")
	}
}

func TestSeasonRequestDropsOnlyTheEpisode(t *testing.T) {
	t.Parallel()
	episode := newznab.SearchRequest{Mode: "tvsearch", Limit: 100, Categories: []int{5000}, Params: []newznab.Param{{Name: "q", Value: "Example"}, {Name: "tvdbid", Value: "1"}, {Name: "season", Value: "2"}, {Name: "ep", Value: "5"}}}
	season, ok := seasonRequest(episode, "")
	if !ok || len(season.Params) != 3 {
		t.Fatalf("season request=%+v ok=%v", season, ok)
	}
	for _, param := range season.Params {
		if param.Name == "ep" {
			t.Fatal("the season request still names the episode")
		}
	}
	if len(episode.Params) != 4 {
		t.Fatal("the episode request was changed")
	}
	// Another episode that rotated to another name asks with the season's title.
	other, _ := seasonRequest(newznab.SearchRequest{Mode: "tvsearch", Limit: 100, Categories: []int{5000}, Params: []newznab.Param{{Name: "q", Value: "Eksempel"}, {Name: "tvdbid", Value: "1"}, {Name: "season", Value: "2"}, {Name: "ep", Value: "6"}}}, "Example")
	indexer := uuid.NewV7()
	if !bytes.Equal(seasonRequestFingerprint(indexer, season), seasonRequestFingerprint(indexer, other)) {
		t.Fatal("two episodes of one season must share one season request")
	}
	if bytes.Equal(seasonRequestFingerprint(indexer, season), seasonRequestFingerprint(uuid.NewV7(), season)) {
		t.Fatal("indexers must not share answers")
	}
	for _, request := range []newznab.SearchRequest{
		{Mode: "search", Params: []newznab.Param{{Name: "q", Value: "Example S02E05"}}},
		{Mode: "tvsearch", Params: []newznab.Param{{Name: "q", Value: "Example"}, {Name: "season", Value: "2"}}},
		{Mode: "tvsearch", Params: []newznab.Param{{Name: "q", Value: "Example"}, {Name: "ep", Value: "5"}}},
	} {
		if _, ok := seasonRequest(request, "Example"); ok {
			t.Fatalf("%+v must not become a season request", request)
		}
	}
}

func TestSeasonTitleIsSharedByEpisodesAndRotatesOverTime(t *testing.T) {
	t.Parallel()
	media := catalog.MediaItem{Kind: catalog.KindSeries, Title: "Example", Metadata: &catalog.MediaMetadata{OriginalTitle: "Example", Aliases: []catalog.MetadataAlias{{Name: "Eksempel", Language: "da", Source: "tvdb"}}}}
	first := postgres.SearchContext{Media: media, CompleteSearches: 3}
	second := postgres.SearchContext{Media: media, CompleteSearches: 8}
	if searchTitle(first) == searchTitle(second) {
		t.Fatal("the fixture must give the two episodes different rotating names")
	}
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	if seasonTitle(first, now) != seasonTitle(second, now) {
		t.Fatal("episodes of a season must ask with one title at a time")
	}
	if seasonTitle(first, now) == seasonTitle(first, now.Add(seasonShareLifetime)) {
		t.Fatal("the season title must still rotate through the names")
	}
}
