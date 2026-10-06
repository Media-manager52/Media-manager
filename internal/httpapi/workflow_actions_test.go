package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func actionByName(actions []workflowAction, name string) (workflowAction, bool) {
	for _, action := range actions {
		if action.Action == name {
			return action, true
		}
	}
	return workflowAction{}, false
}

func TestWorkflowActionsFollowTheReadModel(t *testing.T) {
	id := uuid.MustParse("01a10934-98a8-7dbf-895d-402abfc33c04")
	candidate := uuid.MustParse("01a0efe4-23da-7b2a-8f7c-68e38db97dd9")
	finished := time.Now()
	for _, spec := range []struct {
		name      string
		item      postgres.WorkflowSummary
		available []string
	}{
		{"held searchable", postgres.WorkflowSummary{State: workflows.StateHeld, SearchAllowed: true, RoundState: "open"}, []string{"search", "cancel"}},
		{"exhausted, discovery waiting", postgres.WorkflowSummary{State: workflows.StateWaiting, RoundState: "exhausted", RetryDiscoveryState: "waiting", RetainedOutputLimit: 8, RetainedOutputCount: 2, SearchBlockedReason: "The round is exhausted"}, []string{"discover", "open_round_from_known", "cancel"}},
		{"exhausted, new release ready", postgres.WorkflowSummary{State: workflows.StateWaiting, RoundState: "exhausted", RetryDiscoveryState: "ready", NewReleaseReady: true, OpenNextRoundAllowed: true, RetainedOutputLimit: 8}, []string{"open_next_round", "open_round_from_known", "cancel"}},
		{"retained limit reached", postgres.WorkflowSummary{State: workflows.StateWaiting, RoundState: "exhausted", RetryDiscoveryState: "waiting", RetainedOutputLimit: 8, RetainedOutputCount: 8}, []string{"discover", "cancel"}},
		{"retained cap reached", postgres.WorkflowSummary{State: workflows.StateNeedsAttention, AttentionCode: "retained_failed_output_limit", SearchAllowed: true, RoundState: "open", RetainedOutputLimit: 8, RetainedOutputCount: 8}, []string{"cancel"}},
		{"AltMount attention", postgres.WorkflowSummary{State: workflows.StateNeedsAttention, AttentionCode: "altmount_job_attention", SearchBlockedReason: "Recover the current AltMount job"}, []string{"reconcile_altmount", "cancel"}},
		{"AltMount attention with active job", postgres.WorkflowSummary{State: workflows.StateNeedsAttention, AttentionCode: "altmount_job_attention", ActiveAltMountJob: true}, []string{"reconcile_altmount"}},
		{"Plex timeout", postgres.WorkflowSummary{State: workflows.StateNeedsAttention, AttentionCode: "plex_timeout"}, []string{"reconfirm_plex", "cancel"}},
		{"access rejected", postgres.WorkflowSummary{State: workflows.StateNeedsAttention, Revision: 14, AccessRejectedCandidateID: &candidate, RoundState: "open"}, []string{"cancel", "retry_access_rejected_release"}},
		{"published", postgres.WorkflowSummary{State: workflows.StatePlexConfirmed, TerminalAt: &finished}, nil},
		{"downloading", postgres.WorkflowSummary{State: workflows.StateQueued, ActiveAltMountJob: true}, nil},
	} {
		spec.item.ID = id
		actions := workflowActions(spec.item)
		var got []string
		for _, action := range actions {
			if !strings.HasPrefix(action.Path, "/api/v1/workflows/"+id.String()+"/") {
				t.Fatalf("%s: %s has path %s", spec.name, action.Action, action.Path)
			}
			if action.Available {
				got = append(got, action.Action)
			} else if action.Reason == "" {
				t.Fatalf("%s: unavailable %s gives no reason", spec.name, action.Action)
			}
		}
		if strings.Join(got, ",") != strings.Join(spec.available, ",") {
			t.Fatalf("%s: available %v, want %v", spec.name, got, spec.available)
		}
	}
	retry, _ := actionByName(workflowActions(postgres.WorkflowSummary{ID: id, State: workflows.StateNeedsAttention, Revision: 14, AccessRejectedCandidateID: &candidate}), "retry_access_rejected_release")
	if retry.Body["candidate_id"] != candidate.String() || retry.Body["revision"] != uint64(14) {
		t.Fatal("access retry body not ready to send", retry.Body)
	}
	adopted, _ := actionByName(workflowActions(postgres.WorkflowSummary{ID: id, State: workflows.StateMonitored, SearchAllowed: true, AdoptedReplacementConfirmationRequired: true}), "search")
	if adopted.Confirmation != postgres.AdoptedReplacementConfirmation || adopted.Body != nil {
		t.Fatal("adopted replacement must require an owner confirmation, never pre-fill it", adopted)
	}
}

func TestWorkflowActionsMatchTheRouteCatalog(t *testing.T) {
	server := catalogServer(t)
	catalog := map[string]apiOperation{}
	for _, operation := range server.apiOperations() {
		catalog[operationID(operation)] = operation
	}
	id := uuid.MustParse("01a10934-98a8-7dbf-895d-402abfc33c04")
	candidate := id
	for _, action := range workflowActions(postgres.WorkflowSummary{ID: id, AccessRejectedCandidateID: &candidate}) {
		operation, ok := catalog[action.OperationID]
		if !ok {
			t.Fatalf("%s names unknown operation %s", action.Action, action.OperationID)
		}
		if operation.Method != action.Method || strings.Replace(action.Path, id.String(), "{id}", 1) != operation.Path || operation.Effect != action.Effect {
			t.Fatalf("%s disagrees with the catalog: %s %s %s", action.Action, operation.Method, operation.Path, operation.Effect)
		}
	}
}

func TestWriteConflictKeepsStableCodesAndHints(t *testing.T) {
	for _, spec := range []struct {
		err        error
		code, hint string
	}{
		{postgres.ActionRefusal{Code: "round_exhausted", Message: "the acquisition round is exhausted", Hint: "open the next round"}, "round_exhausted", "open the next round"},
		{errors.Join(errors.New("wrapped"), postgres.ErrWorkflowAltMountJobActive), "altmount_job_active", "Wait for the active AltMount job to settle, then retry."},
		{errors.New("something else"), "search_failed", workflowActionsHint},
	} {
		response := httptest.NewRecorder()
		writeConflict(response, spec.err, "search_failed", spec.err.Error())
		var body errorEnvelope
		if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error.Code != spec.code || body.Error.Hint != spec.hint || body.Error.Message == "" {
			t.Fatalf("conflict mapping for %v: %d %s", spec.err, response.Code, response.Body.String())
		}
	}
}

func TestWorkflowDetailListsActionsAndRefusalsCarryHints(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	media := createMediaThroughAPI(t, testServer.URL, map[string]any{
		"kind": "movie", "title": "Agent Actions Movie", "year": 2024, "search_immediately": false,
		"identities": []map[string]any{{"namespace": "tmdb", "value": "810077", "state": "verified", "source": "test_metadata"}, {"namespace": "imdb", "value": "tt0810077", "state": "verified", "source": "test_metadata"}},
		"metadata":   map[string]any{"overview": "Agent action fixture.", "original_language": "da", "genres": []string{"Drama"}, "countries": []string{"DK"}, "networks": []any{}, "aliases": []any{}, "sources": []any{}, "ratings": []any{}, "events": []any{}, "observed_at": "2026-09-01T00:00:00Z"},
	}, 1)
	workflowID := getMediaDetailThroughAPI(t, testServer.URL, media.ID).Workflows[0].ID
	call := func(method, path string) (int, []byte) {
		request, err := http.NewRequest(method, testServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body json.RawMessage
		_ = json.NewDecoder(response.Body).Decode(&body)
		return response.StatusCode, body
	}
	read := func() workflowDetail {
		status, body := call("GET", "/api/v1/workflows/"+workflowID.String())
		var envelope struct {
			Data workflowDetail `json:"data"`
		}
		if status != 200 || json.Unmarshal(body, &envelope) != nil {
			t.Fatal("workflow detail unavailable", status, string(body))
		}
		return envelope.Data
	}
	detail := read()
	if detail.RootID == "" || detail.QualityProfileID == "" || detail.ID != workflowID || detail.Revision == 0 || len(detail.AvailableActions) < 7 {
		t.Fatalf("workflow detail incomplete: %+v", detail)
	}
	if search, _ := actionByName(detail.AvailableActions, "search"); !search.Available {
		t.Fatal("new monitored workflow should be searchable", search)
	}
	if status, body := call("POST", "/api/v1/workflows/"+workflowID.String()+"/cancel"); status != 200 {
		t.Fatal("cancel failed", status, string(body))
	}
	detail = read()
	for _, action := range detail.AvailableActions {
		if action.Available {
			t.Fatalf("cancelled workflow still offers %s", action.Action)
		}
	}
	status, body := call("POST", "/api/v1/workflows/"+workflowID.String()+"/search")
	var refusal errorEnvelope
	if status != http.StatusConflict || json.Unmarshal(body, &refusal) != nil || refusal.Error.Code != "workflow_not_searchable" || refusal.Error.Hint == "" {
		t.Fatal("refused search lacks a stable code and hint", status, string(body))
	}
	status, body = call("GET", "/api/v1/workflows?active=false&limit=5")
	if status != 200 || !strings.Contains(string(body), `"available_actions"`) || !strings.Contains(string(body), `"active_altmount_job"`) {
		t.Fatal("workflow list lacks actions", status)
	}
}
