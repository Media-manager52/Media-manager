package acquisition

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/platform"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

func completeNonSearchEvents(t *testing.T, store *postgres.Store, workflowID uuid.UUID) {
	t.Helper()
	if _, err := store.Pool.Exec(context.Background(), `UPDATE outbox_events SET completed_at=now(),lease_owner=NULL,lease_until=NULL WHERE aggregate_id=$1 AND event_type<>'workflow.search_requested' AND completed_at IS NULL`, workflowID.String()); err != nil {
		t.Fatal(err)
	}
}

func ensureSearchOutboxEvent(t *testing.T, store *postgres.Store, workflow workflows.Workflow) {
	t.Helper()
	var pending int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflow.ID.String()).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending == 0 {
		payload, _ := json.Marshal(map[string]string{"workflow_id": workflow.ID.String(), "reason": "review worker test"})
		if _, err := store.Pool.Exec(context.Background(), `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,now(),now())`, uuid.NewV7().String(), workflow.ID.String(), int64(workflow.Revision), string(payload)); err != nil {
			t.Fatal(err)
		}
	}
}

func testSearchWorker(store *postgres.Store, service *SearchService) *platform.Worker {
	return &platform.Worker{
		Store:  store,
		Owner:  "review-worker",
		Lease:  time.Minute,
		Limits: map[string]time.Duration{"workflow.search_requested": time.Minute},
		Handlers: map[string]platform.Handler{
			"workflow.search_requested": service.Handle,
			"workflow.transitioned":     func(context.Context, postgres.OutboxEvent) error { return nil },
		},
	}
}

func makeSearchEventsDue(t *testing.T, store *postgres.Store, workflowID uuid.UUID) {
	t.Helper()
	if _, err := store.Pool.Exec(context.Background(), `UPDATE outbox_events SET available_at=now() WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflowID.String()); err != nil {
		t.Fatal(err)
	}
}

func pendingOutboxEvent(t *testing.T, store *postgres.Store, workflowID uuid.UUID) postgres.OutboxEvent {
	t.Helper()
	var id, payload []byte
	var revision int64
	var eventType string
	if err := store.Pool.QueryRow(context.Background(), `SELECT id::text,aggregate_revision,event_type,payload FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL ORDER BY occurred_at DESC,id DESC LIMIT 1`, workflowID.String()).Scan(&id, &revision, &eventType, &payload); err != nil {
		t.Fatal(err)
	}
	eventID, _ := uuid.Parse(string(id))
	return postgres.OutboxEvent{ID: eventID, AggregateID: workflowID, AggregateRevision: uint64(revision), Type: eventType, Payload: payload}
}

func TestPartialRetryProductionOutboxCrashAndContinuation(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.720p.WEB-DL", "1280x720")
	})
	defer aServer.Close()
	bServer, bCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.1080p.WEB-DL", "1920x1080")
	})
	defer bServer.Close()
	configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))
	completeNonSearchEvents(t, store, workflow.ID)
	ensureSearchOutboxEvent(t, store, workflow)

	service := newTwoProviderSearchService(store)
	worker := testSearchWorker(store, service)
	if err := worker.RunBatch(ctx); err != nil {
		t.Fatalf("worker initial run: %v", err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("initial worker calls A=%d B=%d, want 1/0", len(aCaptured()), len(bCaptured()))
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 1 {
		t.Fatalf("initial worker state=%d, want 1", got)
	}
	initialEvent := pendingOutboxEvent(t, store, workflow.ID)
	if initialEvent.AggregateRevision == workflow.Revision {
		t.Fatal("new continuation event did not advance workflow revision")
	}
	var initialPayload struct {
		PartialRetry bool `json:"partial_retry"`
	}
	if err := json.Unmarshal(initialEvent.Payload, &initialPayload); err != nil || !initialPayload.PartialRetry {
		t.Fatalf("initial continuation marker=%s err=%v", initialEvent.Payload, err)
	}
	completeNonSearchEvents(t, store, workflow.ID)
	makeSearchEventsDue(t, store, workflow.ID)

	// Simulate a committed continuation whose worker dies before CompleteOutbox.
	claimed, err := store.ClaimOutbox(ctx, "review-crash", 1, time.Minute, false)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim continuation: claims=%d err=%v", len(claimed), err)
	}
	if claimed[0].AggregateID != workflow.ID || claimed[0].AggregateRevision != initialEvent.AggregateRevision {
		t.Fatalf("wrong event claimed: %+v", claimed[0])
	}
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, claimed[0]); err != nil {
		t.Fatalf("continuation before simulated crash: %v", err)
	}
	completeNonSearchEvents(t, store, workflow.ID)
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET lease_until=now()-interval '1 second' WHERE id=$1`, claimed[0].ID.String()); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.ClaimOutbox(ctx, "review-crash-recovery", 1, time.Minute, false)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != claimed[0].ID {
		t.Fatalf("re-claim old event: claims=%v err=%v", reclaimed, err)
	}
	if err := service.Handle(ctx, reclaimed[0]); err != nil {
		t.Fatalf("stale re-delivery: %v", err)
	}
	if err := store.CompleteOutbox(ctx, reclaimed[0].ID, "review-crash-recovery"); err != nil {
		t.Fatalf("complete stale event: %v", err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("stale re-delivery repeated provider calls A=%d B=%d", len(aCaptured()), len(bCaptured()))
	}
	makeSearchEventsDue(t, store, workflow.ID)

	// Exercise DeferOutbox and later claim of the still-valid continuation.
	next := pendingOutboxEvent(t, store, workflow.ID)
	var nextPayload struct {
		PartialRetry bool `json:"partial_retry"`
	}
	if err := json.Unmarshal(next.Payload, &nextPayload); err != nil || !nextPayload.PartialRetry {
		t.Fatalf("continuation after crash lost partial marker: %s err=%v", next.Payload, err)
	}
	claimed, err = store.ClaimOutbox(ctx, "review-defer", 1, time.Minute, false)
	if err != nil || len(claimed) != 1 || claimed[0].ID != next.ID {
		t.Fatalf("claim for defer: claims=%v next=%s err=%v", claimed, next.ID, err)
	}
	retryAt := time.Now().UTC().Add(time.Hour)
	if err := store.DeferOutbox(ctx, claimed[0].ID, "review-defer", "indexer_pacing_deferred", retryAt); err != nil {
		t.Fatal(err)
	}
	var available time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT available_at FROM outbox_events WHERE id=$1`, claimed[0].ID.String()).Scan(&available); err != nil || available.Sub(retryAt).Abs() > time.Second {
		t.Fatalf("deferred event available=%s want=%s err=%v", available, retryAt, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=now()-interval '1 second' WHERE id=$1`, claimed[0].ID.String()); err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, b.ID)
	worker = testSearchWorker(store, newTwoProviderSearchService(store))
	if err := worker.RunBatch(ctx); err != nil {
		t.Fatalf("worker final run: %v", err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 1 {
		t.Fatalf("final worker calls A=%d B=%d, want 1/1", len(aCaptured()), len(bCaptured()))
	}
	final, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || final.State != workflows.StateCandidateSelected || final.SelectedRelease == nil {
		t.Fatalf("final workflow=%+v err=%v", final, err)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("final state=%d, want 0", got)
	}
}

// TestOwnerSearchSupersedesOlderPartialContinuation covers the race where an
// owner asks for a fresh full search while an older automatic continuation is
// still queued. The continuation must not reuse its completed providers and
// overwrite the owner's event; the owner search must query the completed
// provider again and select a newly available release.
func TestOwnerSearchSupersedesOlderPartialContinuation(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	eligible := false
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string {
		if !eligible {
			return emptySearchXML()
		}
		return movieSearchXMLWithResolution("Klaus.2019.1080p.WEB-DL", "1920x1080")
	})
	defer aServer.Close()
	bServer, bCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.720p.WEB-DL", "1280x720")
	})
	defer bServer.Close()
	a := configureMovieSearchIndexerNamed(t, store, aServer.URL, "Owner Search A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Owner Search B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))
	completeNonSearchEvents(t, store, workflow.ID)
	ensureSearchOutboxEvent(t, store, workflow)

	worker := testSearchWorker(store, newTwoProviderSearchService(store))
	if err := worker.RunBatch(ctx); err != nil {
		t.Fatalf("initial partial search: %v", err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("initial calls A=%d B=%d, want 1/0", len(aCaptured()), len(bCaptured()))
	}
	completeNonSearchEvents(t, store, workflow.ID)
	old := pendingOutboxEvent(t, store, workflow.ID)
	var oldPayload struct {
		PartialRetry bool `json:"partial_retry"`
	}
	if err := json.Unmarshal(old.Payload, &oldPayload); err != nil || !oldPayload.PartialRetry {
		t.Fatalf("old event is not a continuation: %s err=%v", old.Payload, err)
	}

	eligible = true
	if err := store.RequestWorkflowSearch(ctx, workflow.ID, "", time.Now().UTC()); err != nil {
		t.Fatalf("owner search request: %v", err)
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateSearching {
		t.Fatalf("owner search state=%+v err=%v", current, err)
	}
	var oldID, ownerID string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL AND payload->>'partial_retry'='true'`, workflow.ID.String()).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL AND COALESCE(payload->>'partial_retry','false')<>'true'`, workflow.ID.String()).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	// Deliver the old continuation first. Delay the owner event only by
	// availability so the worker claims the older event deterministically.
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=now() WHERE id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=now()+interval '1 hour' WHERE id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox(ctx, "owner-review", 1, time.Minute, false)
	if err != nil || len(claimed) != 1 || claimed[0].ID.String() != oldID {
		t.Fatalf("claimed=%v err=%v old=%s", claimed, err, oldID)
	}
	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, claimed[0]); err != nil {
		t.Fatalf("stale continuation: %v", err)
	}
	if err := store.CompleteOutbox(ctx, claimed[0].ID, "owner-review"); err != nil {
		t.Fatal(err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("stale continuation queried A=%d B=%d, want 1/0", len(aCaptured()), len(bCaptured()))
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=now() WHERE id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, a.ID)
	service = newTwoProviderSearchService(store)
	worker = testSearchWorker(store, service)
	if err := worker.RunBatch(ctx); err != nil {
		t.Fatalf("owner search worker: %v", err)
	}
	if len(aCaptured()) != 2 || len(bCaptured()) != 0 {
		t.Fatalf("owner search calls A=%d B=%d, want A queried again and B deferred", len(aCaptured()), len(bCaptured()))
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil {
		t.Fatalf("owner search did not select A: %+v err=%v", selected, err)
	}
	var retrievals int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested' AND completed_at IS NULL`, workflow.ID.String()).Scan(&retrievals); err != nil || retrievals != 1 {
		t.Fatalf("retrieval events=%d err=%v, want exactly one", retrievals, err)
	}
}
