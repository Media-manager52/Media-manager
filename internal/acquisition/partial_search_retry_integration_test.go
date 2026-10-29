package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

func newTwoProviderSearchService(store *postgres.Store) *SearchService {
	return &SearchService{
		Store:         store,
		Scheduler:     indexers.NewScheduler(2, store),
		ResolveSecret: func(string) (string, error) { return "test-api-key", nil },
		Transport:     http.DefaultTransport,
	}
}

func movieSearchXMLWithResolution(title, resolution string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>%s</title><guid>%s-guid</guid><newznab:attr name="resolution" value="%s"/></item></channel></rss>`, title, title, resolution)
}

func deferIndexerBudget(t *testing.T, store *postgres.Store, indexerID uuid.UUID, until time.Time) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.Pool.Exec(context.Background(), `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,last_outcome_at,revision,updated_at)
		VALUES($1,3000,$2,NULL,0,'success',$3,1,$3)
		ON CONFLICT(indexer_instance_id) DO UPDATE SET next_allowed_at=EXCLUDED.next_allowed_at,cooldown_until=NULL,last_outcome='success',last_outcome_at=EXCLUDED.last_outcome_at,updated_at=EXCLUDED.updated_at,revision=indexer_budget_state.revision+1`,
		indexerID.String(), until.UTC(), now); err != nil {
		t.Fatal(err)
	}
}

func clearIndexerBudget(t *testing.T, store *postgres.Store, indexerID uuid.UUID) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.Pool.Exec(context.Background(), `UPDATE indexer_budget_state SET next_allowed_at=$2,cooldown_until=NULL,last_outcome='success',last_outcome_at=$3,updated_at=$3,revision=revision+1 WHERE indexer_instance_id=$1`, indexerID.String(), now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
}

func countSearchPartialRetries(t *testing.T, store *postgres.Store, workflowID uuid.UUID) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM search_partial_retries WHERE workflow_id=$1`, workflowID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func pendingSearchEvent(t *testing.T, store *postgres.Store, workflowID uuid.UUID) postgres.OutboxEvent {
	t.Helper()
	var revision int64
	var payload []byte
	if err := store.Pool.QueryRow(context.Background(), `SELECT aggregate_revision,payload FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL ORDER BY occurred_at DESC LIMIT 1`, workflowID.String()).Scan(&revision, &payload); err != nil {
		t.Fatal(err)
	}
	return postgres.OutboxEvent{AggregateID: workflowID, AggregateRevision: uint64(revision), Payload: payload, Type: "workflow.search_requested"}
}

func pendingRecoveryEvent(t *testing.T, store *postgres.Store, workflowID uuid.UUID) postgres.OutboxEvent {
	t.Helper()
	var revision int64
	var payload []byte
	if err := store.Pool.QueryRow(context.Background(), `SELECT aggregate_revision,payload FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.discovery_rescan_requested' AND completed_at IS NULL ORDER BY occurred_at DESC LIMIT 1`, workflowID.String()).Scan(&revision, &payload); err != nil {
		t.Fatal(err)
	}
	return postgres.OutboxEvent{AggregateID: workflowID, AggregateRevision: uint64(revision), Payload: payload, Type: "workflow.discovery_rescan_requested"}
}

func TestPartialSearchRetryCallsOnlyDeferredProvider(t *testing.T) {
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

	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first partial search: %v", err)
	}
	if got := len(aCaptured()); got != 1 {
		t.Fatalf("provider A requests=%d, want 1", got)
	}
	if got := len(bCaptured()); got != 0 {
		t.Fatalf("deferred provider B requests=%d, want 0", got)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 1 {
		t.Fatalf("partial retry states=%d, want 1", got)
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || (current.State != workflows.StateHeld && current.State != workflows.StateWaiting) {
		t.Fatalf("workflow after partial search=%+v err=%v", current, err)
	}
	var completed []byte
	if err := store.Pool.QueryRow(ctx, `SELECT completed_indexers FROM search_partial_retries WHERE workflow_id=$1`, workflow.ID.String()).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if len(completed) == 0 || string(completed) == "{}" {
		t.Fatalf("partial state has no successful provider: %s", completed)
	}
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("duplicate stale event: %v", err)
	}
	if got := len(aCaptured()); got != 1 {
		t.Fatalf("duplicate stale event queried provider A: %d", got)
	}
	if got := len(bCaptured()); got != 0 {
		t.Fatalf("duplicate stale event queried provider B: %d", got)
	}

	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("retry partial search: %v", err)
	}
	if got := len(aCaptured()); got != 1 {
		t.Fatalf("successful provider A was queried again; requests=%d, want 1", got)
	}
	if got := len(bCaptured()); got != 1 {
		t.Fatalf("deferred provider B requests=%d, want 1", got)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil {
		t.Fatalf("selected workflow=%+v err=%v", selected, err)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("completed retry chain left %d states", got)
	}
	var title string
	if err := store.Pool.QueryRow(ctx, `SELECT c.title FROM release_candidates c WHERE c.id=$1`, selected.SelectedRelease.String()).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Klaus.2019.1080p.WEB-DL" {
		t.Fatalf("selected title=%q, want provider B 1080p release", title)
	}
}

func TestPartialSearchRetrySkipsSuccessfulEmptyProvider(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string { return emptySearchXML() })
	defer aServer.Close()
	bServer, bCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.1080p.WEB-DL", "1920x1080")
	})
	defer bServer.Close()
	configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))

	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first partial search: %v", err)
	}
	var completed []byte
	if err := store.Pool.QueryRow(ctx, `SELECT completed_indexers FROM search_partial_retries WHERE workflow_id=$1`, workflow.ID.String()).Scan(&completed); err != nil {
		t.Fatalf("empty success state: %v", err)
	}
	if string(completed) == "{}" {
		t.Fatalf("empty provider A success was not recorded: %s", completed)
	}
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("retry partial search: %v", err)
	}
	if got := len(aCaptured()); got != 1 {
		t.Fatalf("empty successful provider A requests=%d, want 1", got)
	}
	if got := len(bCaptured()); got != 1 {
		t.Fatalf("provider B requests=%d, want 1", got)
	}
}

func TestPartialSearchRetryInvalidatesWhenCandidateObservationMoves(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.720p.WEB-DL", "1280x720")
	})
	defer aServer.Close()
	bServer, _ := newMovieSearchServer(t, func(url.Values) string { return emptySearchXML() })
	defer bServer.Close()
	a := configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))

	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first partial search: %v", err)
	}
	var candidateText, oldObservationText string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text,observation_id::text FROM release_candidates WHERE workflow_id=$1 ORDER BY created_at LIMIT 1`, workflow.ID.String()).Scan(&candidateText, &oldObservationText); err != nil {
		t.Fatal(err)
	}
	candidateID, _ := uuid.Parse(candidateText)
	newObservationID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at)
		SELECT $1,indexer_instance_id,operation_key,native_guid,title,body_sha256,'[]','[]',parse_version,'[]',observed_at FROM release_observations WHERE id=$2`, newObservationID.String(), oldObservationText); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE release_candidates SET observation_id=$2 WHERE id=$1`, candidateID.String(), newObservationID.String()); err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, a.ID)
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("retry after moved evidence: %v", err)
	}
	if got := len(aCaptured()); got != 2 {
		t.Fatalf("invalidated retry queried provider A %d times, want full rerun = 2; states=%d", got, countSearchPartialRetries(t, store, workflow.ID))
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("invalid state was not closed by the full search; states=%d", got)
	}
}

func TestPartialSearchRetryDoesNotInheritCompletionAfterMetadataChange(t *testing.T) {
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
	a := configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))

	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first partial search: %v", err)
	}
	if _, err := store.RefreshMediaMetadata(ctx, workflow.MediaID, &catalog.MediaMetadata{Overview: "changed metadata invalidates retry intent"}, nil, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, a.ID)
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("retry after metadata change: %v", err)
	}
	if got := len(aCaptured()); got != 2 {
		t.Fatalf("metadata change reused provider A: requests=%d, want full rerun 2", got)
	}
	if got := len(bCaptured()); got != 1 {
		t.Fatalf("provider B requests=%d, want 1", got)
	}
}

func TestSearchIntentFingerprintCoversRetryInvalidationInputs(t *testing.T) {
	base := postgres.SearchContext{
		Workflow:             workflows.Workflow{ID: uuid.NewV7()},
		Variant:              "default",
		Media:                catalog.MediaItem{ID: uuid.NewV7(), Kind: catalog.KindMovie, Title: "Intent"},
		PolicyVersion:        1,
		Policy:               []byte(`{"rules":[]}`),
		ProfilePolicyVersion: 1,
		SearchRoundID:        uuid.NewV7(),
		CompleteSearches:     2,
	}
	planned := []plannedSearchIndexer{{
		Integration:    postgres.Integration{ID: uuid.NewV7(), Kind: "newznab", Enabled: true, Config: []byte(`{"base_url":"https://indexer.invalid"}`)},
		Config:         indexerConfig{BaseURL: "https://indexer.invalid"},
		Request:        newznab.SearchRequest{Mode: "movie", Params: []newznab.Param{{Name: "q", Value: "Intent"}}},
		ProfileID:      uuid.NewV7(),
		ProfileVersion: 1,
	}}
	fingerprint := func(search postgres.SearchContext, planned []plannedSearchIndexer) []byte {
		t.Helper()
		digest, err := searchIntentFingerprint(search, "targeted", planned)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	initial := fingerprint(base, planned)
	if string(initial) != string(fingerprint(base, planned)) {
		t.Fatal("intent fingerprint is not stable for unchanged inputs")
	}
	recoveryFingerprint, err := searchIntentFingerprint(base, "recovery", planned)
	if err != nil {
		t.Fatal(err)
	}
	if string(recoveryFingerprint) == string(initial) {
		t.Fatal("recovery mode did not invalidate targeted intent")
	}
	mutations := []struct {
		name string
		fn   func(*postgres.SearchContext, *[]plannedSearchIndexer)
	}{
		{"policy", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) { search.PolicyVersion++ }},
		{"metadata", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) { search.Media.Revision++ }},
		{"round", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) { search.SearchRoundID = uuid.NewV7() }},
		{"variant", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) { search.Variant = "season:01" }},
		{"episode scope", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) { search.Variant = "episode:01" }},
		{"media kind", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) {
			search.Media.Kind = catalog.KindSeries
		}},
		{"episode mapping", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) {
			search.EpisodeNumbering = packs.EpisodeNumbering{Year: 2026, Dates: map[string]uint16{"2026-01-01": 1}}
		}},
		{"query rotation", func(search *postgres.SearchContext, _ *[]plannedSearchIndexer) { search.CompleteSearches++ }},
		{"provider plan", func(_ *postgres.SearchContext, planned *[]plannedSearchIndexer) {
			(*planned)[0].Request.Params[0].Value = "Changed"
		}},
		{"capability profile", func(_ *postgres.SearchContext, planned *[]plannedSearchIndexer) {
			(*planned)[0].ProfileVersion++
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changedSearch := base
			changedPlanned := append([]plannedSearchIndexer(nil), planned...)
			mutation.fn(&changedSearch, &changedPlanned)
			if string(fingerprint(changedSearch, changedPlanned)) == string(initial) {
				t.Fatalf("%s change did not invalidate fingerprint", mutation.name)
			}
		})
	}
}

func TestPartialRecoveryReusesCompletedProviderAndKeepsIdentity(t *testing.T) {
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
	configureMovieSearchIndexerNamed(t, store, aServer.URL, "Recovery A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Recovery B", true)
	workflow := createMovieSearchWorkflow(t, store)
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != workflows.StateWaiting {
		current, err = store.TransitionWorkflow(ctx, current.ID, current.Revision, workflows.StateWaiting, "test recovery", "test", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}
	var roundID string
	if err := store.Pool.QueryRow(ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test recovery' WHERE workflow_id=$1 AND state='open' RETURNING id::text`, workflow.ID.String()).Scan(&roundID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,next_rescan_at,complete_no_new_scans,pending_indexer_ids,created_at,updated_at)
		VALUES($1,$2,'waiting',4,now(),0,NULL,now(),now())`, workflow.ID.String(), roundID); err != nil {
		t.Fatal(err)
	}
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))
	service := newTwoProviderSearchService(store)
	if err := service.HandleDiscovery(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: 4}); err != nil {
		t.Fatalf("first recovery scan: %v", err)
	}
	if got := len(aCaptured()); got != 1 {
		t.Fatalf("recovery A requests=%d, want 1", got)
	}
	if got := len(bCaptured()); got != 0 {
		t.Fatalf("deferred recovery B requests=%d, want 0", got)
	}
	var generation int64
	if err := store.Pool.QueryRow(ctx, `SELECT rescan_generation FROM workflow_retry_discovery WHERE workflow_id=$1`, workflow.ID.String()).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != 5 {
		t.Fatalf("recovery generation=%d, want 5", generation)
	}
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.HandleDiscovery(ctx, pendingRecoveryEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("retry recovery scan: %v", err)
	}
	if got := len(aCaptured()); got != 1 {
		t.Fatalf("recovery reused A was queried again: %d", got)
	}
	if got := len(bCaptured()); got != 1 {
		t.Fatalf("recovery B requests=%d, want 1", got)
	}
	var discoveryState string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflow_retry_discovery WHERE workflow_id=$1`, workflow.ID.String()).Scan(&discoveryState); err != nil {
		t.Fatal(err)
	}
	if discoveryState != "ready" {
		t.Fatalf("recovery discovery state=%q, want ready", discoveryState)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("completed recovery left %d partial states", got)
	}
}

func TestPartialSearchRetryExpiredStateFallsBackToFullSearch(t *testing.T) {
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
	a := configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))
	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first partial search: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE search_partial_retries SET created_at=now()-interval '8 days',expires_at=now()-interval '1 day' WHERE workflow_id=$1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, a.ID)
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("retry after state expiry: %v", err)
	}
	if got := len(aCaptured()); got != 2 {
		t.Fatalf("expired state queried provider A %d times, want full rerun 2", got)
	}
	if got := len(bCaptured()); got != 1 {
		t.Fatalf("expired state queried provider B %d times, want 1", got)
	}
}

func TestNewSearchEventDoesNotReusePartialCompletion(t *testing.T) {
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
	a := configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))
	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first partial search: %v", err)
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, a.ID)
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	// No partial_retry marker: this models an owner/new search event, not the
	// automatic continuation written by the incomplete-run scheduler.
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: current.Revision}); err != nil {
		t.Fatalf("new search event: %v", err)
	}
	if got := len(aCaptured()); got != 2 {
		t.Fatalf("new search reused provider A: calls=%d, want full search 2", got)
	}
	if got := len(bCaptured()); got != 1 {
		t.Fatalf("new search provider B calls=%d, want 1", got)
	}
}

// TestPartialSearchRetryCarriedEligibleEvidenceWins proves that a successful
// provider's eligible candidate is held for the continuation ranking rather
// than selected before the deferred provider has completed.
func TestPartialSearchRetryCarriedEligibleEvidenceWins(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.1080p.WEB-DL", "1920x1080")
	})
	defer aServer.Close()
	bServer, bCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.720p.WEB-DL", "1280x720")
	})
	defer bServer.Close()
	configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))

	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("initial partial search: %v", err)
	}
	initial, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.State == workflows.StateCandidateSelected {
		t.Fatal("partial search selected an eligible candidate before the deferred provider completed")
	}
	var candidateText, observationText string
	if err := store.Pool.QueryRow(ctx, `SELECT candidate.id::text,candidate.observation_id::text
		FROM release_candidates candidate WHERE candidate.workflow_id=$1 AND candidate.indexer_instance_id IN
		(SELECT id FROM integration_instances WHERE name='Provider A')`, workflow.ID.String()).Scan(&candidateText, &observationText); err != nil {
		t.Fatalf("load carried candidate: %v", err)
	}
	var beforeCount int64
	var beforeLast time.Time
	var beforeRun string
	if err := store.Pool.QueryRow(ctx, `SELECT observation_count,last_observed_at,search_run_id::text FROM release_candidates WHERE id=$1`, candidateText).Scan(&beforeCount, &beforeLast, &beforeRun); err != nil {
		t.Fatal(err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("initial calls A=%d B=%d", len(aCaptured()), len(bCaptured()))
	}
	clearIndexerBudget(t, store, b.ID)
	service = newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("continuation: %v", err)
	}
	if len(aCaptured()) != 1 {
		t.Fatalf("carried provider A was called again: %d", len(aCaptured()))
	}
	if len(bCaptured()) != 1 {
		t.Fatalf("deferred provider B calls=%d, want 1", len(bCaptured()))
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil || selected.SelectedRelease.String() != candidateText {
		t.Fatalf("selected workflow=%+v want carried candidate %s err=%v", selected, candidateText, err)
	}
	var afterCount int64
	var afterLast time.Time
	var afterRun string
	if err := store.Pool.QueryRow(ctx, `SELECT observation_count,last_observed_at,search_run_id::text FROM release_candidates WHERE id=$1`, candidateText).Scan(&afterCount, &afterLast, &afterRun); err != nil {
		t.Fatal(err)
	}
	if afterCount != beforeCount || !afterLast.Equal(beforeLast) || afterRun != beforeRun {
		t.Fatalf("reused candidate mutated freshness/provenance: count %d/%d last %s/%s run %s/%s", beforeCount, afterCount, beforeLast, afterLast, beforeRun, afterRun)
	}
	var decision string
	if err := store.Pool.QueryRow(ctx, `SELECT verdict FROM candidate_decisions WHERE workflow_id=$1 AND candidate_id=$2 ORDER BY decided_at DESC LIMIT 1`, workflow.ID.String(), candidateText).Scan(&decision); err != nil || decision != string(releases.VerdictEligible) {
		t.Fatalf("carried candidate decision=%q err=%v, want eligible", decision, err)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("completed retry left %d states", got)
	}
}

func TestPartialSearchRetrySurvivesSuccessiveDeferrals(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.1080p.WEB-DL", "1920x1080")
	})
	defer aServer.Close()
	bServer, bCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.720p.WEB-DL", "1280x720")
	})
	defer bServer.Close()
	configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))

	var previousRevision uint64
	expectedStates := []int{1, 1}
	for attempt := 0; attempt < 2; attempt++ {
		service := newTwoProviderSearchService(store)
		if attempt == 0 {
			if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
				t.Fatalf("attempt %d: %v", attempt+1, err)
			}
		} else if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
		current, err := store.GetWorkflow(ctx, workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == workflows.StateCandidateSelected {
			t.Fatalf("attempt %d selected before deferred coverage completed", attempt+1)
		}
		if previousRevision != 0 && current.Revision <= previousRevision {
			t.Fatalf("attempt %d revision %d did not advance from %d", attempt+1, current.Revision, previousRevision)
		}
		previousRevision = current.Revision
		if got := countSearchPartialRetries(t, store, workflow.ID); got != expectedStates[attempt] {
			t.Fatalf("attempt %d states=%d, want %d", attempt+1, got, expectedStates[attempt])
		}
		var completed map[string]json.RawMessage
		if err := store.Pool.QueryRow(ctx, `SELECT completed_indexers FROM search_partial_retries WHERE workflow_id=$1`, workflow.ID.String()).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if len(completed) != 1 {
			t.Fatalf("attempt %d completed providers=%v, want only A", attempt+1, completed)
		}
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("provider calls across deferrals A=%d B=%d, want 1/0", len(aCaptured()), len(bCaptured()))
	}

	clearIndexerBudget(t, store, b.ID)
	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("final continuation: %v", err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 1 {
		t.Fatalf("final calls A=%d B=%d, want 1/1", len(aCaptured()), len(bCaptured()))
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateCandidateSelected || current.SelectedRelease == nil {
		t.Fatalf("final workflow=%+v err=%v", current, err)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("final states=%d, want 0", got)
	}
	var candidateCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM release_candidates WHERE workflow_id=$1 AND native_key LIKE 'Klaus.2019.%'`, workflow.ID.String()).Scan(&candidateCount); err != nil || candidateCount != 2 {
		t.Fatalf("final candidates=%d err=%v, want A and B evidence", candidateCount, err)
	}
}

func TestPartialSearchRetrySurvivesSuccessiveEmptyDeferrals(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	aServer, aCaptured := newMovieSearchServer(t, func(url.Values) string { return emptySearchXML() })
	defer aServer.Close()
	bServer, bCaptured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchXMLWithResolution("Klaus.2019.1080p.WEB-DL", "1920x1080")
	})
	defer bServer.Close()
	configureMovieSearchIndexerNamed(t, store, aServer.URL, "Provider A", true)
	b := configureMovieSearchIndexerNamed(t, store, bServer.URL, "Provider B", true)
	workflow := createMovieSearchWorkflow(t, store)
	deferIndexerBudget(t, store, b.ID, time.Now().UTC().Add(time.Hour))
	for attempt := 0; attempt < 2; attempt++ {
		service := newTwoProviderSearchService(store)
		if attempt == 0 {
			if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
				t.Fatalf("attempt %d: %v", attempt+1, err)
			}
		} else if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
		if got := countSearchPartialRetries(t, store, workflow.ID); got != 1 {
			t.Fatalf("attempt %d states=%d, want 1", attempt+1, got)
		}
		var completed map[string][]json.RawMessage
		if err := store.Pool.QueryRow(ctx, `SELECT completed_indexers FROM search_partial_retries WHERE workflow_id=$1`, workflow.ID.String()).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if len(completed) != 1 || len(completed[aCapturedIndexerID(t, store, "Provider A")]) != 0 {
			t.Fatalf("attempt %d empty A evidence=%v", attempt+1, completed)
		}
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 0 {
		t.Fatalf("deferred empty calls A=%d B=%d, want 1/0", len(aCaptured()), len(bCaptured()))
	}
	clearIndexerBudget(t, store, b.ID)
	service := newTwoProviderSearchService(store)
	if err := service.Handle(ctx, pendingSearchEvent(t, store, workflow.ID)); err != nil {
		t.Fatalf("final continuation: %v", err)
	}
	if len(aCaptured()) != 1 || len(bCaptured()) != 1 {
		t.Fatalf("final calls A=%d B=%d, want 1/1", len(aCaptured()), len(bCaptured()))
	}
	current, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || current.State != workflows.StateCandidateSelected || current.SelectedRelease == nil {
		t.Fatalf("final workflow=%+v err=%v", current, err)
	}
	if got := countSearchPartialRetries(t, store, workflow.ID); got != 0 {
		t.Fatalf("final states=%d, want 0", got)
	}
}

func aCapturedIndexerID(t *testing.T, store *postgres.Store, name string) string {
	t.Helper()
	var id string
	if err := store.Pool.QueryRow(context.Background(), `SELECT id::text FROM integration_instances WHERE name=$1`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
