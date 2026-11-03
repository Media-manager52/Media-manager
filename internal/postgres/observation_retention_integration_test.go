package postgres

import (
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestPruneRepeatedObservationsRemovesOnlyUnreferencedRepeatsAndArchivesThem
// pins the retention rule: only a repeat of an earlier, identical observation
// of the same provider release may go, and only when nothing refers to it and
// it is older than the cutoff. Every removed row reaches the archive first.
func TestPruneRepeatedObservationsRemovesOnlyUnreferencedRepeatsAndArchivesThem(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx := fixture.ctx
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-30 * 24 * time.Hour)

	// A held candidate gives one observation a release_candidates reference.
	workflow := fixture.newMovieWorkflow(t, "Observation Retention", "901201")
	workflow, err := fixture.store.TransitionWorkflow(ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "test retention", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fixture.store.LoadSearchContext(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidateObservation := releases.Observation{
		ID: uuid.NewV7(), IndexerID: fixture.indexer.ID, Operation: "search", NativeGUID: "retention-guid",
		Title: "Retention.Movie.2026.1080p.WEB-DL", Fields: []releases.RawField{}, Facts: []releases.Fact{},
		BodyHash: "candidate", ObservedAt: old.Add(5 * time.Hour), ParseVersion: "test", Problems: []string{},
	}
	if _, err := fixture.store.PersistSearch(ctx, search, []byte("retention"), []EvaluatedObservation{{
		Observation: candidateObservation,
		Evaluation:  releases.Evaluation{ObservationID: candidateObservation.ID, Verdict: releases.VerdictHeld, Explanations: []releases.Explanation{}},
	}}, SearchPersistenceOptions{IndexerOutcomes: []SearchIndexerOutcome{
		{IndexerInstanceID: fixture.indexer.ID, State: "succeeded"},
		{IndexerInstanceID: fixture.indexerTwo.ID, State: "succeeded"},
	}}, now); err != nil {
		t.Fatal(err)
	}
	var candidateReference string
	if err := fixture.store.Pool.QueryRow(ctx, `SELECT observation_id::text FROM release_candidates WHERE workflow_id=$1`, workflow.ID.String()).Scan(&candidateReference); err != nil {
		t.Fatal(err)
	}

	facts := func(seen time.Time, size string) string {
		encoded, _ := json.Marshal([]map[string]any{
			{"name": "size", "state": "observed", "values": []string{size}, "evidence_at": []int{2}, "observed_at": seen},
			{"name": "resolution", "state": "inferred", "values": []string{"1080p"}, "evidence_at": nil, "observed_at": seen.Add(time.Millisecond)},
		})
		return string(encoded)
	}
	raw := func(grabs string) string {
		return `[{"name":"size","value":"2071091000"},{"name":"grabs","value":"` + grabs + `"}]`
	}
	insert := func(indexer uuid.UUID, guid string, seen time.Time, rawFields, normalizedFacts string) string {
		t.Helper()
		id := uuid.NewV7().String()
		if _, err := fixture.store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at)
			VALUES($1,$2,'search',$3,'Retention.Movie.2026.1080p.WEB-DL',$4,$5,$6,'test','[]',$7)`,
			id, indexer.String(), guid, []byte(id), rawFields, normalizedFacts, seen); err != nil {
			t.Fatal(err)
		}
		return id
	}

	first := insert(fixture.indexer.ID, "retention-guid", old, raw("1"), facts(old, "2071091000"))
	repeat := insert(fixture.indexer.ID, "retention-guid", old.Add(time.Hour), raw("1"), facts(old.Add(time.Hour), "2071091000"))
	retryReferenced := insert(fixture.indexer.ID, "retention-guid", old.Add(2*time.Hour), raw("1"), facts(old.Add(2*time.Hour), "2071091000"))
	changedRaw := insert(fixture.indexer.ID, "retention-guid", old.Add(3*time.Hour), raw("2"), facts(old.Add(3*time.Hour), "2071091000"))
	changedRawRepeat := insert(fixture.indexer.ID, "retention-guid", old.Add(4*time.Hour), raw("2"), facts(old.Add(4*time.Hour), "2071091000"))
	changedFacts := insert(fixture.indexer.ID, "retention-guid", old.Add(6*time.Hour), raw("1"), facts(old.Add(6*time.Hour), "2071091001"))
	recentRepeat := insert(fixture.indexer.ID, "retention-guid", now.Add(-time.Hour), raw("1"), facts(now.Add(-time.Hour), "2071091000"))
	otherIndexer := insert(fixture.indexerTwo.ID, "retention-guid", old.Add(time.Hour), raw("1"), facts(old.Add(time.Hour), "2071091000"))
	single := insert(fixture.indexer.ID, "retention-single", old, raw("1"), facts(old, "2071091000"))

	// Partial-search retry evidence names one repeat; it must stay.
	evidence, _ := json.Marshal(map[string][]map[string]string{
		fixture.indexer.ID.String(): {{"candidate_id": uuid.New().String(), "observation_id": retryReferenced}},
	})
	if _, err := fixture.store.Pool.Exec(ctx, `INSERT INTO search_partial_retries(id,workflow_id,round_id,mode,intent_fingerprint,planned_indexer_ids,completed_indexers,created_at,updated_at,expires_at)
		SELECT $1,$2,id,'targeted',$3,ARRAY[$4::uuid],$5,$6,$6,$7 FROM acquisition_rounds WHERE workflow_id=$2 AND state='open'`,
		uuid.New().String(), workflow.ID.String(), make([]byte, 32), fixture.indexer.ID.String(), evidence, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	remaining := func() map[string]bool {
		t.Helper()
		rows, err := fixture.store.Pool.Query(ctx, `SELECT id::text FROM release_observations`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		ids := map[string]bool{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids[id] = true
		}
		return ids
	}
	before := remaining()
	cutoff := now.Add(-14 * 24 * time.Hour)

	if _, err := fixture.store.PruneRepeatedObservations(ctx, ObservationPruneOptions{ObservedBefore: cutoff, ReleasesPerBatch: 10, Apply: true}); err == nil {
		t.Fatal("deleting without an archive must be refused")
	}

	counted, err := fixture.store.PruneRepeatedObservations(ctx, ObservationPruneOptions{ObservedBefore: cutoff, ReleasesPerBatch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if counted.Repeats != 2 || counted.Deleted != 0 || !counted.Completed || counted.Releases != 3 {
		t.Fatalf("dry run=%+v, want 2 repeats over 3 releases and nothing deleted", counted)
	}
	if len(remaining()) != len(before) {
		t.Fatal("a dry run removed observations")
	}
	for _, scoped := range []struct {
		indexer           string
		releases, repeats int64
	}{{fixture.indexer.ID.String(), 2, 2}, {fixture.indexerTwo.ID.String(), 1, 0}} {
		result, err := fixture.store.PruneRepeatedObservations(ctx, ObservationPruneOptions{ObservedBefore: cutoff, ReleasesPerBatch: 1, IndexerInstanceID: scoped.indexer})
		if err != nil {
			t.Fatal(err)
		}
		if result.Releases != scoped.releases || result.Repeats != scoped.repeats {
			t.Fatalf("pass limited to %s=%+v, want %d releases and %d repeats", scoped.indexer, result, scoped.releases, scoped.repeats)
		}
	}

	failing := errors.New("disk full")
	if _, err := fixture.store.PruneRepeatedObservations(ctx, ObservationPruneOptions{ObservedBefore: cutoff, ReleasesPerBatch: 10, Apply: true,
		Archive: func([]ArchivedObservation) error { return failing }}); !errors.Is(err, failing) {
		t.Fatalf("archive failure error=%v", err)
	}
	if len(remaining()) != len(before) {
		t.Fatal("observations were removed although archiving failed")
	}

	var archived []ArchivedObservation
	pruned, err := fixture.store.PruneRepeatedObservations(ctx, ObservationPruneOptions{ObservedBefore: cutoff, ReleasesPerBatch: 1, Apply: true,
		Archive: func(rows []ArchivedObservation) error { archived = append(archived, rows...); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if pruned.Deleted != 2 || pruned.Repeats != 2 {
		t.Fatalf("prune=%+v, want two deletions", pruned)
	}
	archivedIDs := []string{}
	for _, row := range archived {
		archivedIDs = append(archivedIDs, row.ID)
		if row.Title != "Retention.Movie.2026.1080p.WEB-DL" || len(row.NormalizedFacts) == 0 || len(row.RawFields) == 0 || len(row.BodySHA256) == 0 || row.ObservedAt.IsZero() {
			t.Fatalf("archived row lost columns: %+v", row)
		}
	}
	want := []string{repeat, changedRawRepeat}
	sort.Strings(archivedIDs)
	sort.Strings(want)
	if len(archivedIDs) != 2 || archivedIDs[0] != want[0] || archivedIDs[1] != want[1] {
		t.Fatalf("archived=%v, want exactly %v", archivedIDs, want)
	}
	after := remaining()
	for _, id := range []string{first, retryReferenced, changedRaw, changedFacts, recentRepeat, otherIndexer, single, candidateReference} {
		if !after[id] {
			t.Fatalf("observation %s must be kept", id)
		}
	}
	for _, id := range want {
		if after[id] {
			t.Fatalf("repeat %s was not removed", id)
		}
	}
	if len(after) != len(before)-2 {
		t.Fatalf("removed %d observations, want 2", len(before)-len(after))
	}

	again, err := fixture.store.PruneRepeatedObservations(ctx, ObservationPruneOptions{ObservedBefore: cutoff, ReleasesPerBatch: 1000, Apply: true,
		Archive: func(rows []ArchivedObservation) error { return nil }})
	if err != nil || again.Deleted != 0 {
		t.Fatalf("second pass=%+v err=%v, want nothing left to remove", again, err)
	}
}
