package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

// fakeFailingAltMount serves one queue item that keeps failing with the given
// message and counts the retry requests Media Manager sends.
type fakeFailingAltMount struct {
	mu      sync.Mutex
	message string
	retries int
}

func (fake *fakeFailingAltMount) handler(queueID int64) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/auth/config":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"login_required":false}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/user":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"id":"1","name":"admin","is_admin":true}}`))
		case request.Method == http.MethodGet && request.URL.Path == fmt.Sprintf("/api/queue/%d", queueID):
			body, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{
				"id": queueID, "status": "failed", "error_message": fake.message,
				"created_at": time.Now().UTC(), "updated_at": time.Now().UTC(),
			}})
			_, _ = writer.Write(body)
		case request.Method == http.MethodPost && request.URL.Path == fmt.Sprintf("/api/queue/%d/retry", queueID):
			fake.retries++
			_, _ = writer.Write([]byte(`{"success":true,"data":{}}`))
		default:
			http.NotFound(writer, request)
		}
	})
}

func (fake *fakeFailingAltMount) retryCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.retries
}

// TestFailedAltMountImportRetriesThenMovesOnWithoutOwner drives the real
// reconcile against a fake AltMount whose item keeps failing with a timeout:
// the failure never reaches owner attention, each retry is requested from
// AltMount only when due, and once the retries are used the release is
// excluded and the workflow returns to its round to try the next release.
func TestFailedAltMountImportRetriesThenMovesOnWithoutOwner(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	const queueID = 4242
	fake := &fakeFailingAltMount{message: "fast-fail validation inconclusive: context deadline exceeded"}
	server := httptest.NewServer(fake.handler(queueID))
	defer server.Close()

	indexer := configureMovieSearchIndexer(t, store, "http://indexer.invalid")
	workflow := createMovieSearchWorkflow(t, store)
	config, _ := json.Marshal(map[string]any{"base_url": server.URL, "allow_http": true, "authentication": "anonymous_admin"})
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$2 WHERE id=$1`, workflow.DownloadClientID.String(), config); err != nil {
		t.Fatal(err)
	}
	jobID := seedQueuedAltMountJob(t, store, workflow.ID, indexer.ID, workflow.DownloadClientID, queueID)
	service := &AltMountReconcileService{Store: store, CheckMount: func(string) error { return nil }}

	reconcile := func() postgres.AltMountJobContext {
		t.Helper()
		job, err := store.LoadAltMountJob(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: jobID, AggregateRevision: job.JobRevision}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		after, err := store.LoadAltMountJob(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		return after
	}
	makeDue := func() {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET auto_retry_due_at=now()-interval '1 second',auto_retry_requested_at=auto_retry_requested_at-interval '1 hour' WHERE id=$1`, jobID.String()); err != nil {
			t.Fatal(err)
		}
	}

	for attempt := 0; attempt < altMountBusyRetries; attempt++ {
		planned := reconcile()
		if planned.Workflow.State == workflows.StateNeedsAttention {
			t.Fatalf("attempt %d: failure reached owner attention", attempt)
		}
		if planned.AutoRetryDueAt == nil || planned.AutoRetryCount != attempt {
			t.Fatalf("attempt %d: due=%v count=%d, want a planned retry", attempt, planned.AutoRetryDueAt, planned.AutoRetryCount)
		}
		if wait := time.Until(*planned.AutoRetryDueAt); wait < altMountAutoRetryPause(attempt)-time.Minute {
			t.Fatalf("attempt %d: retry due in %v, want about %v", attempt, wait, altMountAutoRetryPause(attempt))
		}
		// Not yet due: reading the job again sends nothing to AltMount.
		reconcile()
		if got := fake.retryCount(); got != attempt {
			t.Fatalf("attempt %d: retries sent before due=%d", attempt, got)
		}
		makeDue()
		requested := reconcile()
		if got := fake.retryCount(); got != attempt+1 || requested.AutoRetryCount != attempt+1 {
			t.Fatalf("attempt %d: retries sent=%d count=%d, want %d", attempt, got, requested.AutoRetryCount, attempt+1)
		}
	}

	// The retries are used up: the release is excluded and the round goes on.
	reconcile()
	got, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != workflows.StateWaiting {
		t.Fatalf("after the last retry state=%q, want waiting for the next release", got.State)
	}
	var kind string
	var searches int
	if err := store.Pool.QueryRow(ctx, `SELECT failure_kind FROM workflow_release_exclusions WHERE workflow_id=$1`, workflow.ID.String()).Scan(&kind); err != nil || kind != postgres.ImportFailedCode {
		t.Fatalf("exclusion kind=%q err=%v", kind, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflow.ID.String()).Scan(&searches); err != nil || searches == 0 {
		t.Fatalf("search_requested events=%d err=%v, want the next release to be looked for", searches, err)
	}
	if fake.retryCount() != altMountBusyRetries {
		t.Fatalf("retries sent=%d, want %d", fake.retryCount(), altMountBusyRetries)
	}
}

// seedQueuedAltMountJob records one submitted release whose AltMount job is
// queued under queueID, in an open round with room for another release.
func seedQueuedAltMountJob(t *testing.T, store *postgres.Store, workflowID, indexerID, altMountID uuid.UUID, queueID int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	runID, observationID, candidateID, jobID, roundID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted','\x01','complete',1,now(),now())`, []any{runID.String(), workflowID.String()}},
		{`INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test','auto-retry','Klaus.2019.1080p.WEB-DL','\x01','[]','[]','test','[]',now())`, []any{observationID.String(), indexerID.String()}},
		{`INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,'auto-retry','Klaus.2019.1080p.WEB-DL','native:auto-retry',now())`, []any{candidateID.String(), workflowID.String(), runID.String(), observationID.String(), indexerID.String()}},
		{`INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,1,'eligible',10,'[]','policy',now())`, []any{workflowID.String(), candidateID.String()}},
		{`INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,opened_at)
			SELECT $1,$2,1,'open',3,0,profile.policy_version,profile.policy,now() FROM workflows workflow JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id WHERE workflow.id=$2
			ON CONFLICT DO NOTHING`, []any{roundID.String(), workflowID.String()}},
		{`INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,nzb_package_sha256,round_id,round_slot,external_queue_id) VALUES($1,$2,$3,$4,1,'auto-retry.nzb','queued',now(),1,$5,(SELECT id FROM acquisition_rounds WHERE workflow_id=$2 AND state='open'),1,$6)`, []any{jobID.String(), workflowID.String(), candidateID.String(), altMountID.String(), make([]byte, 32), queueID}},
		{`INSERT INTO workflow_attempted_releases(workflow_id,candidate_id,first_job_id,created_at) VALUES($1,$2,$3,now())`, []any{workflowID.String(), candidateID.String(), jobID.String()}},
		{`INSERT INTO workflow_submitted_packages(workflow_id,package_sha256,first_job_id,created_at) VALUES($1,$2,$3,now())`, []any{workflowID.String(), make([]byte, 32), jobID.String()}},
		{`UPDATE workflows SET state='queued',selected_release_id=$2,grab_attempt_id=$3,revision=revision+1,updated_at=now() WHERE id=$1`, []any{workflowID.String(), candidateID.String(), jobID.String()}},
	}
	for _, statement := range statements {
		if _, err := store.Pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed %q: %v", statement.sql[:40], err)
		}
	}
	return jobID
}

// TestDeletedAltMountQueueItemMovesOnInsteadOfRetryingForever covers a queue
// item that disappears from AltMount (deleted by hand) while the job is still
// queued and has no stored failure: the reconcile used to retry forever.
func TestDeletedAltMountQueueItemMovesOnInsteadOfRetryingForever(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	const queueID = 4343
	fake := &fakeFailingAltMount{message: "unused"}
	// The fake only knows another queue item, so this job's item answers 404.
	server := httptest.NewServer(fake.handler(queueID + 1))
	defer server.Close()

	indexer := configureMovieSearchIndexer(t, store, "http://indexer.invalid")
	workflow := createMovieSearchWorkflow(t, store)
	config, _ := json.Marshal(map[string]any{"base_url": server.URL, "allow_http": true, "authentication": "anonymous_admin"})
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$2 WHERE id=$1`, workflow.DownloadClientID.String(), config); err != nil {
		t.Fatal(err)
	}
	jobID := seedQueuedAltMountJob(t, store, workflow.ID, indexer.ID, workflow.DownloadClientID, queueID)
	service := &AltMountReconcileService{Store: store, CheckMount: func(string) error { return nil }}
	job, err := store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: jobID, AggregateRevision: job.JobRevision}); err != nil {
		t.Fatalf("reconcile of a deleted queue item returned %v, want it settled", err)
	}
	got, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != workflows.StateWaiting {
		t.Fatalf("state=%q, want waiting for the next release", got.State)
	}
	var kind string
	if err := store.Pool.QueryRow(ctx, `SELECT failure_kind FROM workflow_release_exclusions WHERE workflow_id=$1`, workflow.ID.String()).Scan(&kind); err != nil || kind != postgres.ImportFailedCode {
		t.Fatalf("exclusion kind=%q err=%v", kind, err)
	}
}
