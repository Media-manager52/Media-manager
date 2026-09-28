package postgres

import (
	"testing"
	"time"
)

func TestRetrievalRecoveryMigrationPreservesParkedWork(t *testing.T) {
	f := newFailureRecoveryFixture(t, 40)
	w := f.newMovieWorkflow(t, "Historical retrieval failure", "910040")
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "historical-invalid")
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,failure_kind,reason,created_at)
		VALUES($1,$2,$3,$4,'release_retrieval','historical malformed NZB',now())`, w.ID.String(), r.CandidateID.String(), f.indexer.ID.String(), r.NativeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state='needs_attention',attention_code='release_retrieval_failed' WHERE id=$1`, w.ID.String()); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String())
	if err := Migrate(f.ctx, f.store.Pool); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(f.ctx, f.store.Pool); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil || after.State != before.State || after.Revision != before.Revision {
		t.Fatalf("migration changed parked workflow: %+v %v", after, err)
	}
	if got := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String()); got != events {
		t.Fatal("migration queued work")
	}
	if got := f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND retrieval_round_id IS NULL AND retrieval_error_code IS NULL`, w.ID.String()); got != 1 {
		t.Fatal("migration rewrote historical failure")
	}
	// New rounds use the additive fields and the cross-workflow FK fails closed.
	other := f.newMovieWorkflow(t, "Other round", "910041")
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_release_exclusions SET retrieval_round_id=(SELECT id FROM acquisition_rounds WHERE workflow_id=$2),retrieval_error_code='newznab_invalid_nzb' WHERE workflow_id=$1`, w.ID.String(), other.ID.String()); err == nil {
		t.Fatal("accepted another workflow's round")
	}
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC()); err != nil || allowed {
		t.Fatalf("parked work admitted: %v %v", allowed, err)
	}
}
