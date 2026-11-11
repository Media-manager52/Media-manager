package postgres

import (
	"crypto/sha256"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

func seasonTestObservations(indexerID uuid.UUID, count int, now time.Time) []releases.Observation {
	out := make([]releases.Observation, count)
	for i := range out {
		out[i] = releases.Observation{ID: uuid.NewV7(), IndexerID: indexerID, Operation: "search", NativeGUID: "season-guid-" + string(rune('a'+i)), Title: "Example.S01E0" + string(rune('1'+i)) + ".1080p.WEB-DL", BodyHash: "00", ParseVersion: "test", ObservedAt: now, DownloadURL: "https://indexer.invalid/getnzb/" + string(rune('a'+i))}
	}
	return out
}

// TestSharedSeasonResponseServesOnlyWhatAnEpisodeSearchWouldStillFind proves
// when a stored season answer may stand in for an episode search: fresh,
// complete, newer than the workflow, and older than no new upload matched to
// it and no search the owner asked for. Anything else asks the indexer again.
func TestSharedSeasonResponseServesOnlyWhatAnEpisodeSearchWouldStillFind(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	w := f.newMovieWorkflow(t, "Season Share", "904001")
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET created_at=$2 WHERE id=$1`, w.ID.String(), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("season request"))
	fetched := now.Add(-time.Hour)
	stored, locators, err := f.store.SaveSeasonResponse(f.ctx, f.indexer.ID, fingerprint[:], seasonTestObservations(f.indexer.ID, 3, fetched), []string{"native:a", "native:b", "native:c"}, true, fetched)
	if err != nil || len(stored) != 3 || len(locators) != 3 {
		t.Fatalf("stored=%d locators=%d err=%v", len(stored), len(locators), err)
	}
	lookup := func(notBefore time.Time) (SeasonResponse, bool) {
		t.Helper()
		response, found, err := f.store.SharedSeasonResponse(f.ctx, f.indexer.ID, fingerprint[:], w.ID, notBefore)
		if err != nil {
			t.Fatal(err)
		}
		return response, found
	}
	response, found := lookup(now.Add(-12 * time.Hour))
	if !found || !response.Complete || len(response.Observations) != 3 || response.Observations[1].ID != stored[1].ID || response.Locators[2] != locators[2] || locators[2] == "" {
		t.Fatalf("fresh answer: found=%v response=%+v", found, response)
	}
	if _, found := lookup(now.Add(-30 * time.Minute)); found {
		t.Fatal("an answer older than its lifetime was served")
	}
	if _, found, _ := f.store.SharedSeasonResponse(f.ctx, f.indexerTwo.ID, fingerprint[:], w.ID, now.Add(-12*time.Hour)); found {
		t.Fatal("another indexer's request was served this indexer's answer")
	}

	// The observations stay protected from pruning while an answer names them.
	if n := f.count(t, `SELECT count(*) FROM release_observations WHERE id=ANY($1::uuid[])`, []string{stored[0].ID.String(), stored[1].ID.String(), stored[2].ID.String()}); n != 3 {
		t.Fatalf("stored observations=%d", n)
	}

	// A workflow created after the answer: its episode may be newer.
	newer := f.newMovieWorkflow(t, "Season Share Newer", "904002")
	if _, found, _ := f.store.SharedSeasonResponse(f.ctx, f.indexer.ID, fingerprint[:], newer.ID, now.Add(-12*time.Hour)); found {
		t.Fatal("an answer older than the workflow was served")
	}

	// The owner asked for a search after the answer was fetched.
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,99,'waiting','searching','owner requested search','owner',$2)`, w.ID.String(), now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, found := lookup(now.Add(-12 * time.Hour)); found {
		t.Fatal("an answer older than the owner's search was served")
	}
	if _, err := f.store.Pool.Exec(f.ctx, `DELETE FROM workflow_transitions WHERE workflow_id=$1 AND actor='owner' AND to_revision=99`, w.ID.String()); err != nil {
		t.Fatal(err)
	}

	// A new upload was matched to the workflow after the answer was fetched.
	recordFeedMatch(t, f, w.ID, "season-share-feed", now.Add(-5*time.Minute))
	if _, found := lookup(now.Add(-12 * time.Hour)); found {
		t.Fatal("an answer older than a matched new upload was served")
	}
	if _, err := f.store.Pool.Exec(f.ctx, `DELETE FROM indexer_feed_matches WHERE workflow_id=$1`, w.ID.String()); err != nil {
		t.Fatal(err)
	}

	// Evidence the answer named is gone: ask again.
	if _, err := f.store.Pool.Exec(f.ctx, `DELETE FROM release_observations WHERE id=$1`, stored[0].ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, found := lookup(now.Add(-12 * time.Hour)); found {
		t.Fatal("an answer with missing evidence was served")
	}

	// An incomplete answer is remembered but carries nothing to share.
	if _, _, err := f.store.SaveSeasonResponse(f.ctx, f.indexer.ID, fingerprint[:], seasonTestObservations(f.indexer.ID, 2, now), []string{"native:a", "native:b"}, false, now); err != nil {
		t.Fatal(err)
	}
	response, found = lookup(now.Add(-12 * time.Hour))
	if !found || response.Complete || len(response.Observations) != 0 {
		t.Fatalf("incomplete answer: found=%v response=%+v", found, response)
	}
	if deleted, err := f.store.DeleteExpiredSeasonResponses(f.ctx, now.Add(time.Minute)); err != nil || deleted != 1 {
		t.Fatalf("expired answers deleted=%d err=%v", deleted, err)
	}
}
