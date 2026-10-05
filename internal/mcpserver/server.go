// Package mcpserver exposes the Media Manager owner API to AI clients as a
// Model Context Protocol server over stdio (newline-delimited JSON-RPC 2.0).
//
// Tools are generated from the live OpenAPI document, so they always match
// the running API and inherit every server-side guard: the MCP server is only
// an HTTP client with the owner token. Destructive operations need an explicit
// confirm flag and browser-only operations are refused.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const maxResultBytes = 120_000

const instructions = `Media Manager acquires Danish-friendly movies and TV through indexers, AltMount and Plex.
Start with the overview tool. Find routes with list_operations, read one with describe_operation, then call it with call_operation.
Every operation has an effect: read (safe), write (changes state), provider (contacts indexers/Seerr/Plex and spends budget; do not loop it),
destructive (ask the owner first, then pass confirm: true). For a workflow, read GET /api/v1/workflows/{id} and use its available_actions:
call only actions with available: true and send their body as given. A 409 response carries error.code and error.hint; follow the hint
instead of retrying. 202 means queued, not finished. Never weaken safeguards (Danish subtitles, retained-output caps, exclusions).`

// Config locates the API. Token is the owner bearer token.
type Config struct {
	BaseURL string
	Token   string
	Version string
	Client  *http.Client
}

type operation struct {
	ID          string
	Method      string
	Path        string
	Tag         string
	Effect      string
	Auth        string
	Summary     string
	Raw         map[string]any
	HasBody     bool
	BinaryBody  bool
	BinaryReply bool
}

type server struct {
	config     Config
	mu         sync.Mutex
	operations map[string]operation
	document   map[string]any
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Run serves MCP on input/output until input closes.
func Run(ctx context.Context, input io.Reader, output io.Writer, config Config) error {
	if strings.TrimSpace(config.Token) == "" {
		return errors.New("OWNER_TOKEN is required for the MCP server")
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://127.0.0.1:8080"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	s := &server{config: config}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message request
		if err := json.Unmarshal(line, &message); err != nil || message.JSONRPC != "2.0" || message.Method == "" {
			if err := encoder.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "invalid JSON-RPC message"}}); err != nil {
				return err
			}
			continue
		}
		result, failure := s.handle(ctx, message)
		if len(message.ID) == 0 {
			continue // notification: never answered
		}
		reply := response{JSONRPC: "2.0", ID: message.ID, Result: result, Error: failure}
		if failure == nil && result == nil {
			reply.Result = map[string]any{}
		}
		if err := encoder.Encode(reply); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *server) handle(ctx context.Context, message request) (any, *rpcError) {
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(message.Params, &params)
		version := supportedProtocolVersions[0]
		for _, supported := range supportedProtocolVersions {
			if params.ProtocolVersion == supported {
				version = supported
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "media-manager", "title": "Media Manager", "version": firstNonEmpty(s.config.Version, "dev")},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": toolDefinitions()}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil || params.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "tools/call needs a tool name"}
		}
		if len(params.Arguments) == 0 || string(params.Arguments) == "null" {
			params.Arguments = json.RawMessage("{}")
		}
		return s.callTool(ctx, params.Name, params.Arguments)
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + message.Method}
}

func toolDefinitions() []map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	readOnly := map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
	return []map[string]any{
		{"name": "overview", "title": "Media Manager overview", "description": "Version, health, background queue and indexer budgets in one read. Start here.", "inputSchema": object(map[string]any{}), "annotations": readOnly},
		{"name": "list_operations", "title": "List API operations", "description": "List API operations with operation_id, method, path, effect and summary. Filter by tag (workflows, requests, media, settings, system, ...), effect (read, write, provider, destructive) or free text.", "inputSchema": object(map[string]any{
			"tag":    map[string]any{"type": "string"},
			"effect": map[string]any{"type": "string", "enum": []string{"read", "write", "provider", "destructive"}},
			"query":  map[string]any{"type": "string", "description": "Case-insensitive text matched against id, path and summary."},
		}), "annotations": readOnly},
		{"name": "describe_operation", "title": "Describe an API operation", "description": "Full description of one operation: parameters, request body schema, response schema, effect and any confirmation phrase.", "inputSchema": object(map[string]any{"operation_id": map[string]any{"type": "string"}}, "operation_id"), "annotations": readOnly},
		{"name": "call_operation", "title": "Call an API operation", "description": "Call one API operation by operation_id. Effects: read is safe; write changes state; provider contacts indexers, Seerr or Plex and spends budget; destructive needs the owner's approval and confirm: true. Returns the HTTP status and JSON body; on 409 follow error.hint.", "inputSchema": object(map[string]any{
			"operation_id": map[string]any{"type": "string"},
			"path_params":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Values for {placeholders} in the path, e.g. {\"id\": \"<uuid>\"}."},
			"query":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": []string{"string", "number", "boolean"}}},
			"body":         map[string]any{"description": "JSON request body, exactly as the operation's schema (or available_actions[].body) describes."},
			"confirm":      map[string]any{"type": "boolean", "description": "Required true for destructive operations, after the owner approved."},
		}, "operation_id"), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true}},
	}
}

func toolResult(value any, isError bool) map[string]any {
	text, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		text = []byte(fmt.Sprint(value))
	}
	if len(text) > maxResultBytes {
		text = append(text[:maxResultBytes], []byte("\n… truncated; use limit/offset or a narrower query.")...)
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": isError}
}

func toolError(message string) map[string]any {
	return toolResult(map[string]string{"error": message}, true)
}

func (s *server) callTool(ctx context.Context, name string, arguments json.RawMessage) (any, *rpcError) {
	switch name {
	case "overview":
		return s.overview(ctx), nil
	case "list_operations":
		var args struct{ Tag, Effect, Query string }
		if err := strictDecode(arguments, &args); err != nil {
			return toolError(err.Error()), nil
		}
		operations, err := s.loadOperations(ctx, false)
		if err != nil {
			return toolError(err.Error()), nil
		}
		query := strings.ToLower(args.Query)
		var result []map[string]string
		for _, op := range sortedOperations(operations) {
			if (args.Tag != "" && op.Tag != args.Tag) || (args.Effect != "" && op.Effect != args.Effect) {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(op.ID+" "+op.Path+" "+op.Summary), query) {
				continue
			}
			result = append(result, map[string]string{"operation_id": op.ID, "method": op.Method, "path": op.Path, "effect": op.Effect, "tag": op.Tag, "summary": op.Summary})
		}
		return toolResult(map[string]any{"count": len(result), "operations": result}, false), nil
	case "describe_operation":
		var args struct {
			OperationID string `json:"operation_id"`
		}
		if err := strictDecode(arguments, &args); err != nil {
			return toolError(err.Error()), nil
		}
		op, err := s.operation(ctx, args.OperationID)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return toolResult(map[string]any{"operation_id": op.ID, "method": op.Method, "path": op.Path, "effect": op.Effect, "auth": op.Auth, "operation": op.Raw, "schemas": s.referencedSchemas(op.Raw)}, false), nil
	case "call_operation":
		var args struct {
			OperationID string            `json:"operation_id"`
			PathParams  map[string]string `json:"path_params"`
			Query       map[string]any    `json:"query"`
			Body        json.RawMessage   `json:"body"`
			Confirm     bool              `json:"confirm"`
		}
		if err := strictDecode(arguments, &args); err != nil {
			return toolError(err.Error()), nil
		}
		op, err := s.operation(ctx, args.OperationID)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return s.call(ctx, op, args.PathParams, args.Query, args.Body, args.Confirm), nil
	}
	return nil, &rpcError{Code: -32602, Message: "unknown tool: " + name}
}

func strictDecode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)\}`)

func (s *server) call(ctx context.Context, op operation, pathParams map[string]string, query map[string]any, body json.RawMessage, confirm bool) map[string]any {
	switch {
	case op.Auth == "owner_browser":
		return toolError(op.ID + " can only be used from the Media Manager settings page.")
	case op.Auth == "seerr_webhook":
		return toolError(op.ID + " is called by Seerr, not by agents.")
	case op.BinaryBody || op.BinaryReply:
		return toolError(op.ID + " transfers a binary file and is not available through MCP.")
	case op.Effect == "destructive" && !confirm:
		return toolError(op.ID + " is destructive. Ask the owner first; after they approve, repeat the call with confirm: true. " + firstNonEmpty(fmt.Sprint(op.Raw["description"]), ""))
	}
	path := op.Path
	missing := []string{}
	path = placeholder.ReplaceAllStringFunc(path, func(token string) string {
		name := token[1 : len(token)-1]
		value, ok := pathParams[name]
		if !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, name)
			return token
		}
		return url.PathEscape(value)
	})
	if len(missing) > 0 {
		return toolError("missing path_params: " + strings.Join(missing, ", "))
	}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, fmt.Sprint(value))
	}
	target := s.config.BaseURL + path
	if len(values) > 0 {
		target += "?" + values.Encode()
	}
	hasBody := len(body) > 0 && string(body) != "null"
	if hasBody && !op.HasBody {
		return toolError(op.ID + " takes no request body.")
	}
	var reader io.Reader
	if hasBody {
		reader = bytes.NewReader(body)
	}
	status, decoded, err := s.http(ctx, op.Method, target, reader)
	if err != nil {
		return toolError(err.Error())
	}
	return toolResult(map[string]any{"status": status, "body": decoded}, status >= 400)
}

func (s *server) http(ctx context.Context, method, target string, body io.Reader) (int, any, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+s.config.Token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "media-manager-mcp/1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.config.Client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("Media Manager API unreachable: %v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return response.StatusCode, nil, err
	}
	var decoded any
	if len(raw) > 0 && json.Unmarshal(raw, &decoded) != nil {
		decoded = string(raw)
	}
	return response.StatusCode, decoded, nil
}

func (s *server) overview(ctx context.Context) map[string]any {
	result := map[string]any{}
	for key, path := range map[string]string{"info": "/api/v1/system/info", "health": "/api/v1/system/health", "queue": "/api/v1/system/queue", "indexers": "/api/v1/indexers"} {
		status, body, err := s.http(ctx, http.MethodGet, s.config.BaseURL+path, nil)
		if err != nil || status != http.StatusOK {
			result[key] = map[string]any{"status": status, "error": fmt.Sprint(err), "body": body}
			continue
		}
		data := body
		if envelope, ok := body.(map[string]any); ok {
			data = envelope["data"]
		}
		if key == "indexers" {
			data = compactIndexers(data)
		}
		result[key] = data
	}
	return toolResult(result, false)
}

func compactIndexers(data any) any {
	items, ok := data.([]any)
	if !ok {
		return data
	}
	compact := []map[string]any{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		budget, _ := item["budget"].(map[string]any)
		usage, _ := item["usage"].(map[string]any)
		pacing, _ := item["pacing"].(map[string]any)
		compact = append(compact, map[string]any{
			"id": item["id"], "name": item["name"], "kind": item["kind"], "enabled": item["enabled"],
			"queries":      fmt.Sprintf("%v/%v", usage["queries_used"], budget["query_limit"]),
			"grabs":        fmt.Sprintf("%v/%v", usage["grabs_used"], budget["grab_limit"]),
			"last_outcome": pacing["last_outcome"], "consecutive_failures": pacing["consecutive_failures"],
		})
	}
	return compact
}

func (s *server) operation(ctx context.Context, id string) (operation, error) {
	operations, err := s.loadOperations(ctx, false)
	if err != nil {
		return operation{}, err
	}
	if op, ok := operations[id]; ok {
		return op, nil
	}
	// The server may have been upgraded since the document was loaded.
	if operations, err = s.loadOperations(ctx, true); err != nil {
		return operation{}, err
	}
	if op, ok := operations[id]; ok {
		return op, nil
	}
	return operation{}, fmt.Errorf("unknown operation_id %q; use list_operations", id)
}

func (s *server) loadOperations(ctx context.Context, refresh bool) (map[string]operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.operations != nil && !refresh {
		return s.operations, nil
	}
	status, body, err := s.http(ctx, http.MethodGet, s.config.BaseURL+"/api/v1/openapi.json", nil)
	if err != nil {
		return nil, err
	}
	document, ok := body.(map[string]any)
	if status != http.StatusOK || !ok {
		return nil, fmt.Errorf("could not load the OpenAPI document (HTTP %d)", status)
	}
	operations := map[string]operation{}
	paths, _ := document["paths"].(map[string]any)
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for method, rawOp := range item {
			op, _ := rawOp.(map[string]any)
			id, _ := op["operationId"].(string)
			if id == "" {
				continue
			}
			parsed := operation{ID: id, Method: strings.ToUpper(method), Path: path, Raw: op, Summary: fmt.Sprint(op["summary"]), Effect: fmt.Sprint(op["x-mm-effect"]), Auth: fmt.Sprint(op["x-mm-auth"])}
			if tags, _ := op["tags"].([]any); len(tags) > 0 {
				parsed.Tag = fmt.Sprint(tags[0])
			}
			if requestBody, ok := op["requestBody"].(map[string]any); ok {
				parsed.HasBody = true
				content, _ := requestBody["content"].(map[string]any)
				_, parsed.BinaryBody = content["application/octet-stream"]
			}
			if responses, ok := op["responses"].(map[string]any); ok {
				success, _ := responses["2XX"].(map[string]any)
				content, _ := success["content"].(map[string]any)
				_, parsed.BinaryReply = content["application/octet-stream"]
			}
			operations[id] = parsed
		}
	}
	s.operations, s.document = operations, document
	return operations, nil
}

// referencedSchemas returns every component schema an operation refers to,
// transitively, so describe_operation is self-contained.
func (s *server) referencedSchemas(value any) map[string]any {
	s.mu.Lock()
	document := s.document
	s.mu.Unlock()
	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	result := map[string]any{}
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if ref, ok := typed["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if _, seen := result[name]; !seen && schemas[name] != nil {
					result[name] = schemas[name]
					walk(schemas[name])
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return result
}

func sortedOperations(operations map[string]operation) []operation {
	result := make([]operation, 0, len(operations))
	for _, op := range operations {
		result = append(result, op)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Method < result[j].Method
	})
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}
