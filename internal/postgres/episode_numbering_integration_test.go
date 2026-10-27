package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

func TestFrozenAnnualMappingUsesAvailableSeasonSnapshotsAndExactChild(t *testing.T) {
	store, root, profile, client := openAiringTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	series, workflow := createAiringSeries(t, ctx, store, root, profile, client, "Annual Show", []ManualEpisode{{Number: 1, ProviderID: "tmdb:1", Title: "One", AirDate: "2024-04-01"}, {Number: 2, ProviderID: "tmdb:2", Title: "Two", AirDate: "2024-04-08"}})
	count := uint16(2)
	if _, err := store.RefreshMediaMetadata(ctx, series.ID, &catalog.MediaMetadata{SeasonCount: &count}, nil, "test", now); err != nil {
		t.Fatal(err)
	}
	if n, err := store.WorkflowEpisodeNumbering(ctx, workflow); err != nil || n.Year != 2024 {
		t.Fatalf("available season snapshot mapping=%+v err=%v", n, err)
	}
	season := &ManualSeason{Number: 2, Monitored: true, PreferSeasonPack: true, Episodes: []ManualEpisode{{Number: 1, ProviderID: "tmdb:3", Title: "Three", AirDate: "2025-04-01"}}}
	if _, err := store.CreateManualWorkflowForExisting(ctx, series, root, profile, client, season, false, "", "test", now); err != nil {
		t.Fatal(err)
	}
	n, err := store.WorkflowEpisodeNumbering(ctx, workflow)
	if err != nil || n.Year != 2024 {
		t.Fatalf("full mapping=%+v err=%v", n, err)
	}
	// An unrelated season's undated episode cannot establish a competing year,
	// but the mapped season itself must be fully dated.
	if _, err = store.Pool.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,title) SELECT id,2,2,'Undated' FROM pack_expectation_sets WHERE season=2`); err != nil {
		t.Fatal(err)
	}
	if n, err = store.WorkflowEpisodeNumbering(ctx, workflow); err != nil || n.Year != 2024 {
		t.Fatalf("other season's undated episode erased known year: %+v err=%v", n, err)
	}
	// Force a completed season into the existing episode path with reviewed scope
	// evidence, then verify the child keeps the full parent's numbering context.
	if _, err = store.Pool.Exec(ctx, `UPDATE workflows SET state='waiting' WHERE id=$1`, workflow.String()); err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Test", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "TEST_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	candidate := seedEligibleCandidate(t, ctx, store, workflow, indexer.ID, "annual-episode", 1, now)
	if _, err = store.Pool.Exec(ctx, `UPDATE release_observations SET normalized_facts='[{"name":"release_scope","values":["episode"],"state":"inferred"},{"name":"season_number","values":["1"],"state":"inferred"}]' WHERE id=(SELECT observation_id FROM release_candidates WHERE id=$1)`, candidate.String()); err != nil {
		t.Fatal(err)
	}
	if dispatched, err := store.DispatchAiringSeason(ctx, workflow, now); err != nil || !dispatched {
		t.Fatalf("dispatch=%v err=%v", dispatched, err)
	}
	var childText string
	if err = store.Pool.QueryRow(ctx, `SELECT id::text FROM workflows WHERE media_id=$1 AND variant='season:01:episode:02'`, series.ID.String()).Scan(&childText); err != nil {
		t.Fatal(err)
	}
	child, err := uuid.Parse(childText)
	if err != nil {
		t.Fatal(err)
	}
	if n, err = store.WorkflowEpisodeNumbering(ctx, child); err != nil || n.Year != 2024 || !n.Episodes[1] {
		t.Fatalf("child lost full parent mapping: %+v err=%v", n, err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE pack_expected_episodes SET air_date='2024-04-09' WHERE expectation_id IN (SELECT id FROM pack_expectation_sets WHERE workflow_id=$1)`, child.String()); err != nil {
		t.Fatal(err)
	}
	if n, err = store.WorkflowEpisodeNumbering(ctx, child); err != nil || n.Year != 0 {
		t.Fatalf("drift accepted: %+v err=%v", n, err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE pack_expected_episodes SET air_date='2024-05-01' WHERE season=2`); err != nil {
		t.Fatal(err)
	}
	if n, err = store.WorkflowEpisodeNumbering(ctx, workflow); err != nil || n.Year != 0 {
		t.Fatalf("overlapping annual seasons accepted: %+v err=%v", n, err)
	}
}
