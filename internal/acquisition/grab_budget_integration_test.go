package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func configureNamedSearchIndexer(t *testing.T, store *postgres.Store, name, baseURL string) postgres.Integration {
	t.Helper()
	ctx := context.Background()
	config, err := json.Marshal(map[string]any{"base_url": baseURL, "allow_http": true, "movie_category_ids": []int{2000}, "series_category_ids": []int{5000}, "category_selection_review_required": false})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: name, Enabled: true, Config: config, SecretRef: "MOVIE_SEARCH_TEST_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{Movie: newznab.SearchDialect{Mode: "movie", Params: []string{"q", "year"}}, Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "season"}}}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "caps-body", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return indexer
}

// TestGrabBudgetExhaustionMovesSelectionToCrossPost proves a selection whose
// indexer has spent its rolling grab budget moves to the identical release on
// an indexer with budget left, without any request to the exhausted indexer,
// instead of idling until that indexer's window resets.
func TestGrabBudgetExhaustionMovesSelectionToCrossPost(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	release := func(url.Values) string { return movieSearchReleaseXML("Klaus.2019.1080p.WEB-DL") }
	first, firstRequests := newMovieSearchServer(t, release)
	defer first.Close()
	second, secondRequests := newMovieSearchServer(t, release)
	defer second.Close()
	indexerA := configureNamedSearchIndexer(t, store, "Budget A", first.URL)
	indexerB := configureNamedSearchIndexer(t, store, "Budget B", second.URL)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, first)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatal(err)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	var selectedIndexer string
	if err := store.Pool.QueryRow(ctx, `SELECT indexer_instance_id::text FROM release_candidates WHERE id=$1`, selected.SelectedRelease.String()).Scan(&selectedIndexer); err != nil {
		t.Fatal(err)
	}
	exhausted, spare, exhaustedRequests := indexerA, indexerB, firstRequests
	if selectedIndexer == indexerB.ID.String() {
		exhausted, spare, exhaustedRequests = indexerB, indexerA, secondRequests
	}
	// The selected indexer allows one grab per window and has already used it.
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=config||'{"grab_limit":1}'::jsonb WHERE id=$1`, exhausted.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'grab',now())`, uuid.NewV7().String(), exhausted.ID.String()); err != nil {
		t.Fatal(err)
	}
	requestsBefore := len(exhaustedRequests())
	var payload []byte
	var revision int64
	if err := store.Pool.QueryRow(ctx, `SELECT payload,aggregate_revision FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested' AND completed_at IS NULL`, workflow.ID.String()).Scan(&payload, &revision); err != nil {
		t.Fatal(err)
	}
	retrieval := &RetrievalService{Store: store, Scheduler: service.Scheduler, ResolveSecret: service.ResolveSecret, Transport: service.Transport}
	retrieveEvent := postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: uint64(revision), Payload: payload}
	err = retrieval.Handle(ctx, retrieveEvent)
	var paced indexers.CooldownError
	if errors.As(err, &paced) {
		// The search just queried this indexer; wait out its minimum interval
		// exactly as the worker's DeferOutbox would.
		time.Sleep(time.Until(paced.RetryAt()) + 50*time.Millisecond)
		err = retrieval.Handle(ctx, retrieveEvent)
	}
	if err != nil {
		t.Fatalf("retrieval with exhausted grab budget returned %v, want a cross-post switch", err)
	}
	if got := len(exhaustedRequests()); got != requestsBefore {
		t.Fatalf("exhausted indexer received %d new requests", got-requestsBefore)
	}
	moved, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || moved.State != workflows.StateCandidateSelected || moved.SelectedRelease == nil || moved.Revision != selected.Revision+1 {
		t.Fatalf("moved=%+v err=%v", moved, err)
	}
	var movedIndexer, movedTitle string
	if err := store.Pool.QueryRow(ctx, `SELECT indexer_instance_id::text,title FROM release_candidates WHERE id=$1`, moved.SelectedRelease.String()).Scan(&movedIndexer, &movedTitle); err != nil {
		t.Fatal(err)
	}
	if movedIndexer != spare.ID.String() || movedTitle != "Klaus.2019.1080p.WEB-DL" {
		t.Fatalf("moved to indexer=%s title=%q, want %s", movedIndexer, movedTitle, spare.ID)
	}
	var queued int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested' AND completed_at IS NULL AND aggregate_revision=$2 AND payload->>'candidate_id'=$3`, workflow.ID.String(), int64(moved.Revision), moved.SelectedRelease.String()).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued retrievals for the cross-post=%d err=%v", queued, err)
	}

	// With every copy's indexer exhausted, nothing moves and the deferral stays.
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=config||'{"grab_limit":1}'::jsonb WHERE id=$1`, spare.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'grab',now())`, uuid.NewV7().String(), spare.ID.String()); err != nil {
		t.Fatal(err)
	}
	current, err := store.LoadRetrievalContext(ctx, workflow.ID, *moved.SelectedRelease)
	if err != nil {
		t.Fatal(err)
	}
	if switched, err := store.SelectCrossPostWithGrabBudget(ctx, current, time.Now().UTC()); err != nil || switched {
		t.Fatalf("switch with no budget anywhere switched=%v err=%v", switched, err)
	}
	_ = secondRequests
}
