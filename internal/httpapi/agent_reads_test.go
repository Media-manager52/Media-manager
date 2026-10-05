package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestIndexerSettingsPatchIsBoundedAuditedAndIndexerOnly(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	uncommissioned, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Patch Indexer", SecretRef: "PATCH_KEY", Config: []byte(`{"base_url":"https://indexer.invalid/api","query_limit":1000}`)})
	if err != nil {
		t.Fatal(err)
	}
	plex, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Patch Plex", SecretRef: "PATCH_PLEX", Config: []byte(`{"base_url":"http://plex.invalid:32400"}`)})
	if err != nil {
		t.Fatal(err)
	}
	patch := func(id uuid.UUID, body string) (int, errorEnvelope, postgres.Integration) {
		request := httptest.NewRequest("PATCH", "/api/v1/settings/integrations/"+id.String(), strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		var failure errorEnvelope
		var success struct {
			Data postgres.Integration `json:"data"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &failure)
		_ = json.Unmarshal(response.Body.Bytes(), &success)
		return response.Code, failure, success.Data
	}
	for _, spec := range []struct {
		body   string
		status int
		code   string
	}{
		{`{}`, 400, "empty_patch"},
		{`{"query_limit":0}`, 400, "invalid_query_limit"},
		{`{"grab_limit":5001}`, 400, "invalid_grab_limit"},
		{`{"minimum_interval_ms":1000}`, 400, "invalid_minimum_interval"},
		{`{"name":"   "}`, 400, "invalid_name"},
		{`{"enabled":true}`, 409, "commission_required"},
		{`{"unknown":1}`, 400, "invalid_request"},
	} {
		if status, failure, _ := patch(uncommissioned.ID, spec.body); status != spec.status || failure.Error.Code != spec.code {
			t.Fatalf("%s: %d %+v", spec.body, status, failure)
		}
	}
	if status, failure, _ := patch(plex.ID, `{"enabled":false}`); status != 409 || failure.Error.Code != "not_an_indexer" || failure.Error.Hint == "" {
		t.Fatal("non-indexer integration was editable", status, failure)
	}
	status, _, updated := patch(uncommissioned.ID, `{"name":" Renamed Indexer ","query_limit":2500,"grab_limit":40,"minimum_interval_ms":4000}`)
	var config struct {
		QueryLimit int    `json:"query_limit"`
		GrabLimit  int    `json:"grab_limit"`
		Interval   int64  `json:"minimum_interval_ms"`
		Source     string `json:"budget_source"`
		BaseURL    string `json:"base_url"`
	}
	if status != 200 || json.Unmarshal(updated.Config, &config) != nil || updated.Name != "Renamed Indexer" || config.QueryLimit != 2500 || config.GrabLimit != 40 || config.Interval != 4000 || config.Source != "owner_stated" || config.BaseURL == "" || updated.Enabled {
		t.Fatalf("patch not applied exactly: %d %+v %+v", status, updated, config)
	}
	var audits int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='integration.settings_changed' AND aggregate_id=$1 AND detail->'before'->>'query_limit'='1000' AND detail->'after'->>'query_limit'='2500'`, uncommissioned.ID.String()).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("settings change not audited", audits, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=true,config=jsonb_set(config,'{mode_verified}','true') WHERE id=$1`, uncommissioned.ID.String()); err != nil {
		t.Fatal(err)
	}
	if status, _, disabled := patch(uncommissioned.ID, `{"enabled":false}`); status != 200 || disabled.Enabled {
		t.Fatal("disable failed", status)
	}
	if status, _, enabled := patch(uncommissioned.ID, `{"enabled":true}`); status != 200 || !enabled.Enabled {
		t.Fatal("commissioned indexer could not be re-enabled", status)
	}
}
