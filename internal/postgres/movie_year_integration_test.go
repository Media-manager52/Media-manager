package postgres

import (
	"testing"
	"time"
)

func TestMovieYearConflictStopsPreviouslyEligibleGrab(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "The Fantastic Four", "915003")
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE media_items SET year=1994 WHERE id=$1`, w.MediaID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO media_metadata(media_id,data,revision,created_at,updated_at) VALUES($1,'{"aliases":[{"name":"Fantastic Four","source":"tmdb"}]}',1,now(),now())`, w.MediaID.String()); err != nil {
		t.Fatal(err)
	}
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "wrong-remake")
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE release_candidates SET title='Fantastic.Four.2015.1080p.WEB' WHERE id=$1`, r.CandidateID.String()); err != nil {
		t.Fatal(err)
	}
	allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC())
	if err != nil || allowed {
		t.Fatalf("wrong remake admission=%v err=%v", allowed, err)
	}
	var code string
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT attention_code FROM workflows WHERE id=$1`, w.ID.String()).Scan(&code); err != nil || code != "release_movie_year_conflict" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, w.ID.String()); got != 0 {
		t.Fatalf("wrong movie created job: %d", got)
	}
}
