package postgres

import (
	"testing"
	"time"
)

func TestErrorLogMergesStoredFailuresNewestFirst(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC()
	workflow := f.newMovieWorkflow(t, "Errorlog Movie", "915090")
	retrieval := seedRetrievalCandidate(t, f.ctx, f.store, workflow.ID, f.indexer.ID, "error-log")
	intent, err := f.store.BeginAltMountSubmission(f.ctx, retrieval, testNZBPackageIdentity(190), now)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.store.Pool.Exec(f.ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE release_candidates SET title='Errorlog.Movie.2026.1080p.WEB-GROUP' WHERE id=$1`, retrieval.CandidateID.String())
	exec(`UPDATE altmount_jobs SET state='needs_attention',error_code='subtitle_requirements_unverified',error_detail='Movie.mkv has no verified da subtitle',submitted_at=$2,last_reconciled_at=$2 WHERE id=$1`, intent.JobID.String(), now.Add(-time.Minute))
	exec(`INSERT INTO integration_connection_checks(integration_id,checked_at,ok) VALUES ($1,$2,false) ON CONFLICT (integration_id) DO UPDATE SET checked_at=$2,ok=false`, f.indexer.ID.String(), now.Add(-2*time.Minute))
	exec(`INSERT INTO backup_records(id,state,format,last_error,created_at) VALUES ('11111111-1111-4111-8111-111111111190','failed','postgres_custom_aes256gcm_v1','disk full',$1)`, now.Add(-3*time.Minute))
	exec(`INSERT INTO backup_records(id,state,format,last_error,created_at) VALUES ('11111111-1111-4111-8111-111111111191','failed','postgres_custom_aes256gcm_v1','too old',$1)`, now.Add(-ErrorLogWindow-time.Hour))

	log, err := f.store.ErrorLog(f.ctx, ErrorLogFilter{Limit: 50}, now)
	if err != nil {
		t.Fatal(err)
	}
	if log.Total != 3 || len(log.Entries) != 3 {
		t.Fatalf("total=%d entries=%+v", log.Total, log.Entries)
	}
	first := log.Entries[0]
	if first.Area != "language" || first.Code != "subtitle_requirements_unverified" || first.Title != "Errorlog Movie" || first.Subject != "Errorlog.Movie.2026.1080p.WEB-GROUP" || first.WorkflowID != workflow.ID.String() || first.Open {
		t.Fatalf("job entry=%+v", first)
	}
	if log.Entries[1].Area != "connection" || !log.Entries[1].Open || log.Entries[1].Subject != f.indexer.Name {
		t.Fatalf("connection entry=%+v", log.Entries[1])
	}
	if log.Entries[2].Area != "backup" || log.Entries[2].Detail != "disk full" {
		t.Fatalf("backup entry=%+v", log.Entries[2])
	}

	filtered, err := f.store.ErrorLog(f.ctx, ErrorLogFilter{Area: "backup", Limit: 50}, now)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Entries) != 1 || filtered.Areas["language"] != 1 {
		t.Fatalf("area filter: %+v", filtered)
	}
	searched, err := f.store.ErrorLog(f.ctx, ErrorLogFilter{Query: "ERRORLOG.movie", Limit: 50}, now)
	if err != nil {
		t.Fatal(err)
	}
	if searched.Total != 1 || searched.Entries[0].Area != "language" {
		t.Fatalf("search: %+v", searched)
	}
	limited, err := f.store.ErrorLog(f.ctx, ErrorLogFilter{Limit: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if limited.Total != 3 || len(limited.Entries) != 1 {
		t.Fatalf("limit keeps the total: %+v", limited)
	}
}
