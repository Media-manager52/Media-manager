package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"uuid"
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
	// The profile is visible before any file exists.
	for _, workflow := range detail.Workflows {
		if workflow.QualityProfile == "" {
			t.Fatalf("workflow %s has no quality profile name", workflow.Variant)
		}
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

// TestMediaDetailListsOnlyTheNewestEpisodeExpectation proves a corrected
// season snapshot replaces the old one on the title page: an episode the
// provider later removed must not linger as a missing episode.
func TestMediaDetailListsOnlyTheNewestEpisodeExpectation(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	item, workflowID := fixture.seriesWorkflow(t, "Corrected Episode Series", "990002", []ManualEpisode{
		{Number: 1, Title: "One", AirDate: "2020-02-01"},
		{Number: 2, Title: "Two", AirDate: "2020-02-08"},
		{Number: 3, Title: "Three", AirDate: "2020-02-15"},
	})
	corrected := uuid.NewV7().String()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at)
		SELECT $2,workflow_id,series_identity,season,source_revision+1,'\x0102',now(),$3 FROM pack_expectation_sets WHERE workflow_id=$1 ORDER BY frozen_at DESC LIMIT 1`, workflowID.String(), corrected, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,title,air_date) VALUES($1,1,1,'One','2020-02-01'),($1,1,2,'Two (corrected)','2020-02-08')`, corrected); err != nil {
		t.Fatal(err)
	}

	detail, err := fixture.store.MediaDetail(fixture.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	episodes := detail.Seasons[0].Episodes
	if len(episodes) != 2 || episodes[1].Title != "Two (corrected)" {
		t.Fatalf("episodes=%+v, want only the corrected two", episodes)
	}
}
