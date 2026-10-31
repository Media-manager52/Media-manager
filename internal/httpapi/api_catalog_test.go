package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

func catalogServer(t *testing.T) *Server {
	t.Helper()
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32), Version: "test"}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestAPICatalogIsCompleteAndConsistent(t *testing.T) {
	server := catalogServer(t)
	operations := server.apiOperations()
	tags := map[string]bool{}
	for _, tag := range apiTags {
		tags[tag[0]] = true
	}
	seenRoute, seenID := map[string]bool{}, map[string]bool{}
	for _, operation := range operations {
		route := operation.Method + " " + operation.Path
		id := operationID(operation)
		if seenRoute[route] || seenID[id] || strings.Contains(id, "func") || id == "" {
			t.Fatalf("duplicate or unnamed operation %s (%s)", route, id)
		}
		seenRoute[route], seenID[id] = true, true
		if operation.Handler == nil || operation.Summary == "" || !tags[operation.Tag] || effectMeaning[operation.Effect] == "" {
			t.Fatalf("incomplete catalog entry %s", route)
		}
		if operation.Method == "GET" && operation.Effect != effectRead && operation.Effect != effectProvider {
			t.Fatalf("GET %s must not be a write", operation.Path)
		}
		if strings.HasPrefix(operation.Path, "/api/v1") != (operation.Auth != authNone) {
			t.Fatalf("%s auth mode %s does not match its surface", route, operation.Auth)
		}
	}
}

func TestReleaseIntelligenceAPIRoutesHaveSafeEffects(t *testing.T) {
	server := catalogServer(t)
	operations := map[string]apiOperation{}
	for _, operation := range server.apiOperations() {
		operations[operation.Method+" "+operation.Path] = operation
	}
	dryRun, ok := operations["GET /api/v1/workflows/{id}/candidates/dry-run"]
	if !ok || dryRun.Effect != effectRead {
		t.Fatalf("dry-run route effect=%v ok=%v, want read", dryRun.Effect, ok)
	}
	presets, ok := operations["GET /api/v1/settings/quality-profile-presets"]
	if !ok || presets.Effect != effectRead {
		t.Fatalf("preset list route effect=%v ok=%v, want read", presets.Effect, ok)
	}
	apply, ok := operations["POST /api/v1/settings/quality-profiles/{id}/preset"]
	if !ok || apply.Effect != effectWrite {
		t.Fatalf("preset apply route effect=%v ok=%v, want write", apply.Effect, ok)
	}
	required := map[string]bool{}
	for _, name := range apply.Required {
		required[name] = true
	}
	if !required["preset"] || !required["policy_version"] {
		t.Fatalf("preset apply required fields=%v, want preset and policy_version", apply.Required)
	}
}

// The catalog must declare every JSON body and query parameter its handlers
// actually read, so the published schema cannot silently fall behind the code.
func TestAPICatalogDeclaresWhatHandlersRead(t *testing.T) {
	bodies, queries := handlerInputs(t)
	if len(bodies) < 25 || len(queries) < 15 || !bodies["activateCommissioningV2Profile"] || len(queries["listCalendar"]) != 3 {
		t.Fatalf("source scan found too little (%d bodies, %d query readers); the scanner is broken", len(bodies), len(queries))
	}
	server := catalogServer(t)
	declared := map[string]apiOperation{}
	for _, operation := range server.apiOperations() {
		declared[operationID(operation)] = operation
	}
	for handler := range bodies {
		operation, ok := declared[handler]
		if !ok {
			continue // helpers and Servarr compatibility handlers
		}
		if operation.Body == nil {
			t.Errorf("%s decodes a JSON body but the catalog declares none", handler)
		}
	}
	for handler, names := range queries {
		operation, ok := declared[handler]
		if !ok {
			continue
		}
		for _, name := range names {
			found := false
			for _, param := range operation.Query {
				found = found || param.Name == name
			}
			if !found {
				t.Errorf("%s reads query parameter %q that the catalog does not declare", handler, name)
			}
		}
	}
	for id, operation := range declared {
		if _, decodes := bodies[id]; operation.Body != nil && !decodes {
			if _, raw := operation.Body.(rawBody); !raw {
				t.Errorf("%s declares a JSON body its handler never decodes", id)
			}
		}
	}
}

func handlerInputs(t *testing.T) (map[string]bool, map[string][]string) {
	t.Helper()
	bodies, queries := map[string]bool{}, map[string][]string{}
	// Package helpers that decode a body (for example decodeExpectedRevision)
	// count as a body read for every method that calls them.
	decoders := map[string]bool{"decodeJSON": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, file)
	}
	for _, file := range parsed {
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Body != nil {
				ast.Inspect(function.Body, func(node ast.Node) bool {
					if call, ok := node.(*ast.CallExpr); ok {
						if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "decodeJSON" {
							decoders[function.Name.Name] = true
						}
					}
					return true
				})
			}
		}
	}
	for _, file := range parsed {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := call.Fun.(type) {
				case *ast.Ident:
					if decoders[callee.Name] {
						bodies[function.Name.Name] = true
					}
				case *ast.SelectorExpr:
					if callee.Sel.Name == "Get" && len(call.Args) == 1 && strings.Contains(types(callee.X), "Query()") {
						if literal, ok := call.Args[0].(*ast.BasicLit); ok {
							value, _ := strconv.Unquote(literal.Value)
							queries[function.Name.Name] = append(queries[function.Name.Name], value)
						}
					}
				}
				return true
			})
		}
	}
	return bodies, queries
}

func types(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Query" {
		return "Query()"
	}
	return ""
}

func TestOpenAPIDocumentAndIndexAreOwnerOnlyAndComplete(t *testing.T) {
	server := catalogServer(t)
	for _, path := range []string{"/api/v1", "/api/v1/openapi.json"} {
		request := httptest.NewRequest("GET", path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s readable without the owner token: %d", path, response.Code)
		}
	}
	request := httptest.NewRequest("GET", "/api/v1/openapi.json", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var document struct {
		OpenAPI    string                               `json:"openapi"`
		Paths      map[string]map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &document) != nil || document.OpenAPI != "3.1.0" {
		t.Fatal("OpenAPI document unavailable", response.Code)
	}
	count := 0
	for path, item := range document.Paths {
		for method, operation := range item {
			count++
			if operation["operationId"] == "" || operation["x-mm-effect"] == nil || operation["responses"] == nil {
				t.Fatalf("%s %s incomplete in OpenAPI", method, path)
			}
		}
	}
	if count != len(server.apiOperations()) {
		t.Fatalf("OpenAPI has %d operations, routes %d", count, len(server.apiOperations()))
	}
	search := document.Paths["/api/v1/workflows/{id}/search"]["post"]
	if search["x-mm-effect"] != "provider" || search["operationId"] != "searchWorkflow" {
		t.Fatal("workflow search not described as a provider action", search)
	}
	budget, _ := json.Marshal(document.Paths["/api/v1/settings/oldboys/budget"]["put"])
	if !strings.Contains(string(budget), `"query_limit"`) || !strings.Contains(string(budget), `SetOldBoysBudgetInput`) {
		t.Fatal("budget request schema missing", string(budget))
	}
	created, _ := json.Marshal(document.Components.Schemas["CreateMediaInput"])
	if !strings.Contains(string(created), `"seasons"`) || !strings.Contains(string(created), `Catalog_MediaMetadata`) {
		t.Fatal("nested request schema not generated", string(created))
	}
	request = httptest.NewRequest("GET", "/api/v1", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"/api/v1/openapi.json"`) || !strings.Contains(response.Body.String(), `"destructive"`) {
		t.Fatal("API index incomplete", response.Body.String())
	}
}

// docs/reference/openapi.json is the committed copy for agents that read the
// repository instead of a running server. Regenerate it with:
//
//	MM_WRITE_OPENAPI=1 go test ./internal/httpapi -run TestCommittedOpenAPIIsCurrent
func TestCommittedOpenAPIIsCurrent(t *testing.T) {
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32), Version: "repository"}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := json.MarshalIndent(server.openAPIDocument(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	generated = append(generated, '\n')
	path := filepath.Join("..", "..", "docs", "reference", "openapi.json")
	if os.Getenv("MM_WRITE_OPENAPI") == "1" {
		if err := os.WriteFile(path, generated, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(path)
	if err != nil || string(committed) != string(generated) {
		t.Fatal("docs/reference/openapi.json is stale; run: MM_WRITE_OPENAPI=1 go test ./internal/httpapi -run TestCommittedOpenAPIIsCurrent")
	}
}
