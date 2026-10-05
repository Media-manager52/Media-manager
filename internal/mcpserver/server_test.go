package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/media-manager/media-manager/internal/httpapi"
	"github.com/media-manager/media-manager/internal/postgres"
)

const testToken = "mcp-test-owner-token-0123456789abcdef"

type recorded struct{ Method, Path, Query, Body, Auth string }

func fakeAPI(t *testing.T) (*httptest.Server, func() []recorded) {
	t.Helper()
	api, err := httpapi.New(httpapi.Config{OwnerToken: testToken, Version: "test"}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []recorded
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/openapi.json" {
			api.Handler().ServeHTTP(writer, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		calls = append(calls, recorded{request.Method, request.URL.Path, request.URL.RawQuery, string(body), request.Header.Get("Authorization")})
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/acquisition-rounds") {
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"error":{"code":"no_release_ready","message":"no newly observed eligible release is ready for owner approval","hint":"Wait for paced discovery"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":{"ok":true}}`))
	}))
	t.Cleanup(server.Close)
	return server, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), calls...)
	}
}

// session runs the MCP server over the given lines and returns each reply.
func session(t *testing.T, baseURL string, lines ...string) ([]map[string]any, string) {
	t.Helper()
	var output bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &output, Config{BaseURL: baseURL, Token: testToken, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	var replies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var reply map[string]any
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatalf("non-JSON output line %q", line)
		}
		replies = append(replies, reply)
	}
	return replies, output.String()
}

func call(id int, tool string, arguments any) string {
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": arguments}})
	return string(encoded)
}

func toolPayload(t *testing.T, reply map[string]any) (map[string]any, bool) {
	t.Helper()
	result, _ := reply["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tool reply has no content: %v", reply)
	}
	text := content[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool text is not JSON: %s", text)
	}
	isError, _ := result["isError"].(bool)
	return payload, isError
}

func TestMCPHandshakeToolsAndProtocolErrors(t *testing.T) {
	api, _ := fakeAPI(t)
	replies, raw := session(t, api.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`not json`,
		`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
	)
	if len(replies) != 6 {
		t.Fatalf("expected 6 replies (notification unanswered), got %d: %s", len(replies), raw)
	}
	initialized := replies[0]["result"].(map[string]any)
	if initialized["protocolVersion"] != "2025-03-26" || initialized["instructions"] == "" || initialized["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatal("initialize result wrong", initialized)
	}
	tools := replies[1]["result"].(map[string]any)["tools"].([]any)
	names := []string{}
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "overview,list_operations,describe_operation,call_operation" {
		t.Fatal("unexpected tools", names)
	}
	if replies[3]["error"].(map[string]any)["code"].(float64) != -32601 || replies[4]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatal("protocol errors wrong", replies[3], replies[4])
	}
	if replies[5]["result"].(map[string]any)["protocolVersion"] != supportedProtocolVersions[0] {
		t.Fatal("unsupported version not negotiated down to the latest supported")
	}
}

func TestMCPToolsUseTheLiveOperationsAndKeepGuards(t *testing.T) {
	api, calls := fakeAPI(t)
	workflow := "01a10934-98a8-7dbf-895d-402abfc33c04"
	replies, raw := session(t, api.URL,
		call(1, "list_operations", map[string]any{"effect": "destructive"}),
		call(2, "describe_operation", map[string]any{"operation_id": "setOldBoysBudget"}),
		call(3, "call_operation", map[string]any{"operation_id": "searchWorkflow", "path_params": map[string]string{"id": workflow}}),
		call(4, "call_operation", map[string]any{"operation_id": "confirmRealFilePurge", "path_params": map[string]string{"id": workflow}, "body": map[string]string{"confirmation": "x"}}),
		call(5, "call_operation", map[string]any{"operation_id": "confirmRealFilePurge", "path_params": map[string]string{"id": workflow}, "body": map[string]string{"confirmation": "x"}, "confirm": true}),
		call(6, "call_operation", map[string]any{"operation_id": "getWorkflow"}),
		call(7, "call_operation", map[string]any{"operation_id": "createCommissioningV2Run", "path_params": map[string]string{"id": workflow}}),
		call(8, "call_operation", map[string]any{"operation_id": "downloadBackup", "path_params": map[string]string{"id": workflow}}),
		call(9, "call_operation", map[string]any{"operation_id": "openAcquisitionRound", "path_params": map[string]string{"id": workflow}}),
		call(10, "call_operation", map[string]any{"operation_id": "listWorkflows", "query": map[string]any{"active": false, "limit": 5}}),
		call(11, "call_operation", map[string]any{"operation_id": "listWorkflows", "body": map[string]any{"x": 1}}),
		call(12, "call_operation", map[string]any{"operation_id": "noSuchOperation"}),
		call(13, "list_operations", map[string]any{"unexpected": true}),
		call(14, "overview", map[string]any{}),
	)
	if strings.Contains(raw, testToken) {
		t.Fatal("owner token leaked into MCP output")
	}
	destructive, _ := toolPayload(t, replies[0])
	if destructive["count"].(float64) != 4 {
		t.Fatal("destructive operations miscounted", destructive)
	}
	described, _ := toolPayload(t, replies[1])
	if described["method"] != "PUT" || described["schemas"].(map[string]any)["SetOldBoysBudgetInput"] == nil {
		t.Fatal("describe_operation not self-contained", described)
	}
	searched, isError := toolPayload(t, replies[2])
	if isError || searched["status"].(float64) != 200 {
		t.Fatal("search call failed", searched)
	}
	for index, reason := range map[int]string{3: "destructive", 5: "missing path_params", 6: "settings page", 7: "binary", 10: "no request body", 11: "unknown operation_id", 12: "invalid arguments"} {
		payload, isError := toolPayload(t, replies[index])
		if !isError || !strings.Contains(payload["error"].(string), reason) {
			t.Fatalf("reply %d should refuse with %q: %v", index+1, reason, payload)
		}
	}
	purged, isError := toolPayload(t, replies[4])
	if isError || purged["status"].(float64) != 200 {
		t.Fatal("confirmed destructive call not sent", purged)
	}
	refused, isError := toolPayload(t, replies[8])
	body := refused["body"].(map[string]any)["error"].(map[string]any)
	if !isError || refused["status"].(float64) != 409 || body["hint"] == "" || body["code"] != "no_release_ready" {
		t.Fatal("409 not passed through with its hint", refused)
	}
	overview, _ := toolPayload(t, replies[13])
	for _, key := range []string{"info", "health", "queue", "indexers"} {
		if overview[key] == nil {
			t.Fatal("overview missing", key)
		}
	}
	got := calls()
	sent := map[string]recorded{}
	for _, call := range got {
		if call.Auth != "Bearer "+testToken {
			t.Fatal("call without the owner token", call)
		}
		sent[call.Method+" "+call.Path] = call
	}
	if _, ok := sent["POST /api/v1/workflows/"+workflow+"/search"]; !ok {
		t.Fatal("search not sent", got)
	}
	if purge := sent["POST /api/v1/recycle/"+workflow+"/confirm-purge"]; purge.Body != `{"confirmation":"x"}` {
		t.Fatal("confirmed purge body wrong", purge)
	}
	if list := sent["GET /api/v1/workflows"]; list.Query != "active=false&limit=5" {
		t.Fatal("query not encoded", list)
	}
	purges := 0
	for _, call := range got {
		if strings.HasSuffix(call.Path, "/confirm-purge") {
			purges++
		}
	}
	if purges != 1 {
		t.Fatal("unconfirmed destructive call reached the API")
	}
}

func TestMCPRequiresToken(t *testing.T) {
	if err := Run(context.Background(), strings.NewReader(""), io.Discard, Config{}); err == nil {
		t.Fatal("MCP server started without an owner token")
	}
}
