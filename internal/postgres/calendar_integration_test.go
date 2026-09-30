package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestListCalendarListsEachEpisodeOnce proves an episode tracked by both its
// season workflow and its own episode workflow appears once, represented by the
// most informative workflow: a Plex-confirmed one first, and never a cancelled
// duplicate while a live workflow still tracks the episode.
func TestListCalendarListsEachEpisodeOnce(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	ctx, store := fixture.ctx, fixture.store
	today := time.Now().UTC()
	first, second := today.AddDate(0, 0, 2).Format("2006-01-02"), today.AddDate(0, 0, 9).Format("2006-01-02")
	item, seasonWorkflow := fixture.seriesWorkflow(t, "Calendar Once Series", "990101", []ManualEpisode{
		{Number: 1, Title: "One", AirDate: first},
		{Number: 2, Title: "Two", AirDate: second},
	})
	var seasonState string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1`, seasonWorkflow.String()).Scan(&seasonState); err != nil {
		t.Fatal(err)
	}

	// An episode workflow per episode, cloned from the season workflow the way
	// airing dispatch scopes them, each with its own one-episode expectation.
	episodeWorkflow := func(episode int, state string, airDate string) uuid.UUID {
		t.Helper()
		id, expectation := uuid.NewV7(), uuid.NewV7()
		variant := "season:01:episode:0" + string(rune('0'+episode))
		if _, err := store.Pool.Exec(ctx, `INSERT INTO workflows SELECT (jsonb_populate_record(NULL::workflows, to_jsonb(original) || jsonb_build_object('id',$2::text,'variant',$3::text,'state',$4::text,'updated_at',now(),'terminal_at',now()))).* FROM workflows original WHERE original.id=$1`, seasonWorkflow.String(), id.String(), variant, state); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at)
			SELECT $2,$3,series_identity,season,source_revision,$4,now(),now() FROM pack_expectation_sets WHERE workflow_id=$1 ORDER BY frozen_at DESC LIMIT 1`, seasonWorkflow.String(), expectation.String(), id.String(), []byte{byte(episode), 9}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,title,air_date) VALUES($1,1,$2,'Episode',$3)`, expectation.String(), episode, airDate); err != nil {
			t.Fatal(err)
		}
		return id
	}
	confirmed := episodeWorkflow(1, "plex_confirmed", first)
	episodeWorkflow(2, "cancelled", second)

	calendar, err := store.ListCalendar(ctx, today.AddDate(0, 0, -1), today.AddDate(0, 0, 30), 100)
	if err != nil {
		t.Fatal(err)
	}
	var rows []CalendarEntry
	for _, entry := range calendar {
		if entry.MediaID == item.ID {
			rows = append(rows, entry)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("calendar rows=%+v, want exactly one per episode", rows)
	}
	if rows[0].Episode != 1 || rows[0].WorkflowID != confirmed || rows[0].WorkflowState != "plex_confirmed" {
		t.Fatalf("episode 1 row=%+v, want the Plex-confirmed episode workflow", rows[0])
	}
	if rows[1].Episode != 2 || rows[1].WorkflowID != seasonWorkflow || rows[1].WorkflowState != seasonState {
		t.Fatalf("episode 2 row=%+v, want the live season workflow (%s), not the cancelled duplicate", rows[1], seasonState)
	}
}
