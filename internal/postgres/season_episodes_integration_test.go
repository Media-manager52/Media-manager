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
	// The season counts describe the same corrected snapshot as the list.
	if season := detail.Seasons[0]; season.ExpectedEpisodes != 2 || season.ReleasedEpisodes != 2 {
		t.Fatalf("season counts expected=%d released=%d, want 2 and 2 from the corrected snapshot", season.ExpectedEpisodes, season.ReleasedEpisodes)
	}
}

// TestMediaDetailCountsUnknownAirDatesAsUndated proves an episode without a
// provider air date is counted as undated, not as a future episode, so a
// finished season whose snapshot lacks dates never looks like it is airing.
func TestMediaDetailCountsUnknownAirDatesAsUndated(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	item, _ := fixture.seriesWorkflow(t, "Undated Episode Series", "990003", []ManualEpisode{
		{Number: 1, Title: "Aired", AirDate: "2020-03-01"},
		{Number: 2, Title: "Announced", AirDate: "2099-03-08"},
		{Number: 3, Title: "Unscheduled"},
	})
	detail, err := fixture.store.MediaDetail(fixture.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	season := detail.Seasons[0]
	if season.ExpectedEpisodes != 3 || season.ReleasedEpisodes != 1 || season.FutureEpisodes != 1 || season.UndatedEpisodes != 1 {
		t.Fatalf("season counts expected=%d released=%d future=%d undated=%d, want 3/1/1/1", season.ExpectedEpisodes, season.ReleasedEpisodes, season.FutureEpisodes, season.UndatedEpisodes)
	}
}

// TestMediaDetailIgnoresNewerCancelledSeasonDuplicate proves a cancelled
// duplicate season workflow, even one updated more recently, does not replace
// the live season workflow and its expectation on the title page.
func TestMediaDetailIgnoresNewerCancelledSeasonDuplicate(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	item, workflowID := fixture.seriesWorkflow(t, "Duplicate Season Series", "990004", []ManualEpisode{
		{Number: 1, Title: "One", AirDate: "2020-04-01"},
		{Number: 2, Title: "Two", AirDate: "2020-04-08"},
	})
	duplicate := uuid.NewV7().String()
	later := time.Now().UTC().Add(time.Hour)
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO workflows SELECT (jsonb_populate_record(NULL::workflows, to_jsonb(original) || jsonb_build_object('id',$2::text,'state','cancelled','updated_at',$3::timestamptz,'terminal_at',$3::timestamptz))).* FROM workflows original WHERE original.id=$1`, workflowID.String(), duplicate, later); err != nil {
		t.Fatal(err)
	}
	blank := uuid.NewV7().String()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at)
		SELECT $2,$3,series_identity,season,source_revision+1,'\x0304',now(),$4 FROM pack_expectation_sets WHERE workflow_id=$1 ORDER BY frozen_at DESC LIMIT 1`, workflowID.String(), blank, duplicate, later); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode) SELECT $1,1,generate_series(1,10)`, blank); err != nil {
		t.Fatal(err)
	}

	detail, err := fixture.store.MediaDetail(fixture.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	season := detail.Seasons[0]
	if season.WorkflowID == nil || season.WorkflowID.String() != workflowID.String() {
		t.Fatalf("season workflow=%v, want the live workflow %s", season.WorkflowID, workflowID)
	}
	if season.ExpectedEpisodes != 2 || len(season.Episodes) != 2 || season.UndatedEpisodes != 0 {
		t.Fatalf("season expected=%d listed=%d undated=%d, want the live 2-episode snapshot", season.ExpectedEpisodes, len(season.Episodes), season.UndatedEpisodes)
	}
}
