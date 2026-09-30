package postgres

import (
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestMediaDetailListsExpectedEpisodes proves the title page receives each
// expected episode of a monitored season once, with its title and air date.
func TestMediaDetailListsExpectedEpisodes(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	item, _ := fixture.seriesWorkflow(t, "Episode Listing Series", "990001", []ManualEpisode{
		{Number: 1, Title: "Pilot", AirDate: "2020-01-05"},
		{Number: 2, Title: "Second Tide", AirDate: "2020-01-12"},
		{Number: 3, AirDate: "2099-01-19"},
	})

	detail, err := fixture.store.MediaDetail(fixture.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Seasons) != 1 {
		t.Fatalf("seasons=%d, want 1", len(detail.Seasons))
	}
	episodes := detail.Seasons[0].Episodes
	if len(episodes) != 3 {
		t.Fatalf("episodes=%+v, want 3", episodes)
	}
	if episodes[0].Number != 1 || episodes[0].Title != "Pilot" || episodes[0].AirDate == nil || episodes[0].AirDate.Format("2006-01-02") != "2020-01-05" {
		t.Fatalf("first episode=%+v", episodes[0])
	}
	if episodes[2].Number != 3 || episodes[2].Title != "" || episodes[2].AirDate == nil || episodes[2].AirDate.Format("2006-01-02") != "2099-01-19" {
		t.Fatalf("third episode=%+v", episodes[2])
	}
}
