package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestAgentReadEndpointsReportUsageQueueSearchesAndCandidates(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	ctx := context.Background()
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Agent Usage Indexer", SecretRef: "AGENT_USAGE_KEY", Config: []byte(`{"base_url":"https://indexer.invalid/api","query_limit":40,"grab_limit":7,"budget_window_seconds":86400,"budget_source":"owner_stated"}`)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for range 3 {
		if retry, err := store.ReserveIndexerRequest(ctx, indexer.ID.String(), "query", 40, 24*time.Hour, now); err != nil || !retry.IsZero() {
			t.Fatal("reservation failed", err)
		}
	}
	get := func(path string, target any) int {
		request, err := http.NewRequest("GET", testServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		envelope := struct {
			Data any `json:"data"`
		}{Data: target}
		_ = json.NewDecoder(response.Body).Decode(&envelope)
		return response.StatusCode
	}
	var usages []postgres.IndexerUsage
	if get("/api/v1/indexers", &usages) != 200 {
		t.Fatal("indexer usage unavailable")
	}
	found := false
	for _, usage := range usages {
		if usage.ID != indexer.ID {
			continue
		}
		found = true
		if usage.Budget.QueryLimit != 40 || usage.Budget.GrabLimit != 7 || usage.Budget.BackgroundQueryLimit != 8 || usage.Budget.Source != "owner_stated" || usage.Usage.QueriesUsed != 3 || usage.Usage.QueriesRemaining != 37 || usage.Usage.BackgroundQueriesRemaining != 5 || usage.Usage.GrabsRemaining != 7 || usage.Enabled || usage.SearchOutcomes24h == nil {
			t.Fatalf("indexer usage wrong: %+v", usage)
		}
	}
	if !found {
		t.Fatal("created indexer missing from usage")
	}
	var queue postgres.QueueSnapshot
	if get("/api/v1/system/queue", &queue) != 200 || queue.ByType == nil || queue.CheckedAt.IsZero() {
		t.Fatal("queue snapshot unavailable", queue)
	}
	media := createMediaThroughAPI(t, testServer.URL, map[string]any{
		"kind": "movie", "title": "Agent Reads Movie", "year": 2024, "search_immediately": false,
		"identities": []map[string]any{{"namespace": "tmdb", "value": "810078", "state": "verified", "source": "test_metadata"}, {"namespace": "imdb", "value": "tt0810078", "state": "verified", "source": "test_metadata"}},
		"metadata":   map[string]any{"overview": "Agent read fixture.", "original_language": "da", "genres": []string{"Drama"}, "countries": []string{"DK"}, "networks": []any{}, "aliases": []any{}, "sources": []any{}, "ratings": []any{}, "events": []any{}, "observed_at": "2026-09-01T00:00:00Z"},
	}, 1)
	workflowID := getMediaDetailThroughAPI(t, testServer.URL, media.ID).Workflows[0].ID
	var runs []postgres.SearchRunView
	if get("/api/v1/workflows/"+workflowID.String()+"/searches", &runs) != 200 || runs == nil || len(runs) != 0 {
		t.Fatal("new workflow search history wrong", runs)
	}
	var candidates []postgres.WorkflowCandidateView
	if get("/api/v1/workflows/"+workflowID.String()+"/candidates?verdict=eligible", &candidates) != 200 || candidates == nil {
		t.Fatal("candidate list unavailable")
	}
	if get("/api/v1/workflows/"+workflowID.String()+"/candidates?verdict=maybe", &candidates) != http.StatusBadRequest {
		t.Fatal("invalid verdict accepted")
	}
	missing := uuid.MustParse("01a10934-0000-7000-8000-000000000000").String()
	if get("/api/v1/workflows/"+missing+"/searches", &runs) != 404 || get("/api/v1/workflows/"+missing+"/candidates", &candidates) != 404 {
		t.Fatal("unknown workflow must be 404")
	}
}
