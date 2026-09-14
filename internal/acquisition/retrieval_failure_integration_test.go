package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestRetrievalFailureBeforeAltMountCreatesNoJob drives the real
// RetrievalService against a fake Newznab endpoint that rejects NZB retrieval.
// The first bounded request is retryable and the second records a durable,
// workflow-local release exclusion. Neither attempt may create an AltMount job,
// consume an acquisition slot, or queue publication work.
func TestRetrievalFailureBeforeAltMountCreatesNoJob(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		switch query.Get("t") {
		case "get":
			return "" // HTTP 500 for every bounded NZB retrieval
		case "movie":
			return movieSearchReleaseXML("Klaus 2019 1080p WEB-DL")
		default:
			return emptySearchXML()
		}
	})
	defer server.Close()

	indexer := configureMovieSearchIndexer(t, store, server.URL, false)
	workflow := createMovieSearchWorkflow(t, store)
	if err := movieSearchService(store, server).Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("search: %v", err)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.SelectedRelease == nil || selected.State != workflows.StateCandidateSelected {
		t.Fatalf("selected workflow=%+v err=%v", selected, err)
	}
	// The just-completed search paced the provider. Clear the durable floor so
	// this test exercises the retrieval failure, not scheduler deferral.
	clearPacing := func() {
		if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET next_allowed_at=now()-interval '1 minute',cooldown_until=NULL,consecutive_failure=0 WHERE indexer_instance_id=$1`, indexer.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	clearPacing()
	retrievalService := func() *RetrievalService {
		return &RetrievalService{
			Store:         store,
			Scheduler:     indexers.NewScheduler(2, store),
			ResolveSecret: func(string) (string, error) { return "test-api-key", nil },
			Transport:     server.Client().Transport,
		}
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflow.ID.String(), "candidate_id": selected.SelectedRelease.String()})
	event := func(attempts int) postgres.OutboxEvent {
		return postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: selected.Revision, Type: "release.retrieve_requested", Payload: payload, Attempts: attempts}
	}

	if err := retrievalService().Handle(ctx, event(1)); err == nil {
		t.Fatal("first retrieval failure did not surface a retryable error")
	}
	assertNoRetrievalAttempt(t, store, workflow.ID)
	// The failed request backed the indexer off; simulate the paced window
	// elapsing so the second bounded request can be made.
	clearPacing()
	if err := retrievalService().Handle(ctx, event(2)); err != nil {
		t.Fatalf("second retrieval failure: %v", err)
	}
	assertNoRetrievalAttempt(t, store, workflow.ID)

	failed, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || failed.State != workflows.StateNeedsAttention {
		t.Fatalf("workflow after retrieval failure=%+v err=%v", failed, err)
	}
	var kind string
	var verificationID *string
	if err := store.Pool.QueryRow(ctx, `SELECT failure_kind,verification_id::text FROM workflow_release_exclusions WHERE workflow_id=$1`, workflow.ID.String()).Scan(&kind, &verificationID); err != nil {
		t.Fatal(err)
	}
	if kind != "release_retrieval" || verificationID != nil {
		t.Fatalf("retrieval exclusion kind=%q verification=%v", kind, verificationID)
	}
	getRequests := 0
	for _, request := range captured() {
		if request.Get("t") == "get" {
			getRequests++
		}
	}
	if getRequests != 2 {
		t.Fatalf("NZB retrieval requests=%d, want the bounded two", getRequests)
	}
}

func retrievalTestNZBIdentity(seed byte) [sha256.Size]byte {
	var identity [sha256.Size]byte
	identity[0] = seed
	return identity
}

func assertNoRetrievalAttempt(t *testing.T, store *postgres.Store, workflowID uuid.UUID) {
	t.Helper()
	var jobs, attempts, packages, publications int
	if err := store.Pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1),
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1),
		(SELECT count(*) FROM workflow_submitted_packages WHERE workflow_id=$1),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type IN ('library.publish_requested','plex.scan_requested'))`, workflowID.String()).Scan(&jobs, &attempts, &packages, &publications); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || attempts != 0 || packages != 0 || publications != 0 {
		t.Fatalf("retrieval failure created jobs=%d attempts=%d packages=%d publications=%d", jobs, attempts, packages, publications)
	}
}

// TestActiveSubmissionIsNotRetransmitted proves the retrieval service attaches
// to an already-created AltMount job after a simulated restart rather than
// submitting the same NZB a second time.
func TestActiveSubmissionIsNotRetransmitted(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		switch query.Get("t") {
		case "get":
			return `<?xml version="1.0"?><nzb xmlns="http://www.newznab.com/DTD/2010/feeds/attributes/"><file poster="x" date="0" subject="x"><segment bytes="1" number="1">x@y</segment></file></nzb>`
		case "movie":
			return movieSearchReleaseXML("Klaus 2019 1080p WEB-DL")
		default:
			return emptySearchXML()
		}
	})
	defer server.Close()

	configureMovieSearchIndexer(t, store, server.URL, false)
	workflow := createMovieSearchWorkflow(t, store)
	if err := movieSearchService(store, server).Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("search: %v", err)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.SelectedRelease == nil {
		t.Fatalf("selected workflow=%+v err=%v", selected, err)
	}
	// Create the durable job without contacting AltMount by using the store
	// boundary directly, exactly as the upload handler does after retrieval.
	retrieval, err := store.LoadRetrievalContext(ctx, workflow.ID, *selected.SelectedRelease)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.BeginAltMountSubmission(ctx, retrieval, retrievalTestNZBIdentity(70), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("submission intent=%+v err=%v", intent, err)
	}
	// A restart leaves the workflow submitting; the retrieval handler must
	// reconcile the same job, never submit a second NZB.
	restarted := &RetrievalService{
		Store:         store,
		Scheduler:     indexers.NewScheduler(2, store),
		ResolveSecret: func(string) (string, error) { return "test-api-key", nil },
		Transport:     server.Client().Transport,
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflow.ID.String(), "candidate_id": selected.SelectedRelease.String()})
	if err := restarted.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: selected.Revision, Type: "release.retrieve_requested", Payload: payload, Attempts: 1}); err != nil {
		t.Fatalf("restart recovery: %v", err)
	}
	var jobs, getRequests int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	for _, request := range captured() {
		if request.Get("t") == "get" {
			getRequests++
		}
	}
	if jobs != 1 || getRequests != 0 {
		t.Fatalf("restart retransmitted NZB jobs=%d retrievals=%d, want 1/0", jobs, getRequests)
	}
	recovered, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || recovered.State != workflows.StateTransportUnknown {
		t.Fatalf("recovered workflow=%+v err=%v", recovered, err)
	}
}
