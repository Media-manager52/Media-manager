package postgres

import (
	"context"
	"testing"
	"time"
	"uuid"
)

// TestSeasonCoveredByEpisodesIsNotActiveWork proves the owner views: a season
// acquired episode by episode leaves its season workflow waiting, and once
// every expected episode is in Plex it is reported as covered, drops out of the
// active list and the overview's active count, and stays visible in the full
// list. A season with one episode still missing stays active.
func TestSeasonCoveredByEpisodesIsNotActiveWork(t *testing.T) {
	store, rootID, profileID, clientID := openAiringTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	series, seasonID := createAiringSeries(t, ctx, store, rootID, profileID, clientID, "Covered Season", []ManualEpisode{
		{Number: 1, ProviderID: "tmdb:1", Title: "Episode 1", AirDate: now.AddDate(0, 0, -7).Format("2006-01-02")},
		{Number: 2, ProviderID: "tmdb:2", Title: "Episode 2", AirDate: now.AddDate(0, 0, -6).Format("2006-01-02")},
	})
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='waiting' WHERE id=$1`, seasonID.String()); err != nil {
		t.Fatal(err)
	}
	// Both episodes are acquired individually; only the first is in Plex yet.
	for episode, state := range map[uint16]string{1: "plex_confirmed", 2: "waiting"} {
		terminal := any(nil)
		if state == "plex_confirmed" {
			terminal = now
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at,terminal_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$8,$9)`,
			uuid.NewV7().String(), series.ID.String(), state, rootID.String(), profileID.String(), clientID.String(), episodeVariantForTest(1, episode), now, terminal); err != nil {
			t.Fatal(err)
		}
	}

	active := func() (bool, bool) {
		t.Helper()
		items, err := store.ListWorkflowSummariesPage(ctx, 500, 0, true, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == seasonID {
				return true, item.CoveredByEpisodes
			}
		}
		return false, false
	}
	activeCount := func() int {
		t.Helper()
		snapshot, err := store.Dashboard(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		return int(snapshot.Counts.ActiveWorkflows)
	}

	listed, covered := active()
	if !listed || covered {
		t.Fatalf("partly acquired season listed=%v covered=%v, want active and not covered", listed, covered)
	}
	before := activeCount()

	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='plex_confirmed',terminal_at=$3 WHERE media_id=$1 AND variant=$2`, series.ID.String(), episodeVariantForTest(1, 2), now); err != nil {
		t.Fatal(err)
	}
	if listed, _ := active(); listed {
		t.Fatal("season with every episode in Plex is still listed as active work")
	}
	// The second episode left active work and the covered season with it.
	if after := activeCount(); after != before-2 {
		t.Fatalf("active count before=%d after=%d, want the episode and the covered season gone", before, after)
	}
	summary, err := store.WorkflowSummary(ctx, seasonID)
	if err != nil || !summary.CoveredByEpisodes || summary.State != "waiting" {
		t.Fatalf("season summary=%+v err=%v, want waiting and covered", summary, err)
	}
}
