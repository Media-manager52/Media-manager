package httpapi

import (
	"encoding"
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

// The OpenAPI document and the API index are generated from apiOperations,
// the same table that registers the routes, so they cannot drift from the
// live server. Request schemas come from the Go types handlers decode into.

const apiConventions = `All /api/v1 routes except the Seerr webhook need "Authorization: Bearer <OWNER_TOKEN>".
Success bodies are {"data": ..., "meta": ...}; errors are {"error": {"code": "snake_case", "message": "..."}} with a 4xx/5xx status.
Request bodies are JSON (Content-Type: application/json) and unknown fields are rejected with 400.
Every operation has x-mm-effect: "read" (no change), "write" (changes Media Manager state or queues local work),
"provider" (contacts or queues requests to indexers, Seerr, Plex or metadata services and can spend provider budget)
or "destructive" (removes or replaces data; ask the owner first). x-mm-auth "owner_browser" routes can only be used
from the Media Manager settings page. Workflow actions are guarded by state and revision. GET /api/v1/workflows/{id} lists available_actions (route, effect,
available or the reason why not, and a ready body). A 409 means the action does not apply right now; error.hint says what
to do instead. Background work is paced: a 202 means queued, not finished.`

var effectMeaning = map[apiEffect]string{
	effectRead:        "No state change and no provider contact. Always safe.",
	effectWrite:       "Changes Media Manager state or queues local work. Audited.",
	effectProvider:    "Sends or queues requests to indexers, Seerr, Plex or metadata services; can spend provider budget.",
	effectDestructive: "Removes, replaces or purges data and may be irreversible. Ask the owner first.",
}

func (server *Server) apiIndex(writer http.ResponseWriter, _ *http.Request) {
	counts := map[apiEffect]int{}
	byTag := map[string]int{}
	for _, operation := range server.apiOperations() {
		counts[operation.Effect]++
		byTag[operation.Tag]++
	}
	effects := map[string]any{}
	for effect, meaning := range effectMeaning {
		effects[string(effect)] = map[string]any{"meaning": meaning, "operations": counts[effect]}
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"name":        "Media Manager API",
		"version":     server.config.Version,
		"commit":      server.config.Commit,
		"openapi":     "/api/v1/openapi.json",
		"guide":       "docs/operations/ai-agents.md",
		"conventions": strings.Split(apiConventions, "\n"),
		"effects":     effects,
		"tags":        byTag,
		"start_here": []string{
			"GET /api/v1/system/info",
			"GET /api/v1/system/health",
			"GET /api/v1/dashboard",
			"GET /api/v1/requests",
			"GET /api/v1/workflows",
			"GET /api/v1/settings/integrations",
		},
	}})
}

func (server *Server) openAPI(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, server.openAPIDocument())
}

var apiTags = [][2]string{
	{"system", "Health, version, background tasks, backups and this API's own description."},
	{"overview", "Dashboard, activity timeline, calendar and wanted list."},
	{"media", "Catalogue movies and series: add, inspect, refresh, archive, recycle."},
	{"workflows", "One acquisition unit (movie, season or episode): state, search and recovery actions."},
	{"requests", "Seerr requests and their Media Manager lifecycle."},
	{"releases", "Observed release candidates, verification results and the blocklist."},
	{"library", "Published files, collections and the recycle bin."},
	{"adoption", "Importing files that already exist on disk."},
	{"discovery", "Metadata search for new titles, request sources and import lists."},
	{"settings", "Integrations, indexer credentials and budgets, library roots, quality profiles, notifications."},
	{"commissioning", "Browser-only indexer commissioning v2 runs."},
}

var pathParameter = regexp.MustCompile(`\{([A-Za-z]+)\}`)

func (server *Server) openAPIDocument() map[string]any {
	generator := schemaGenerator{components: map[string]any{}}
	paths := map[string]map[string]any{}
	for _, operation := range server.apiOperations() {
		item := paths[operation.Path]
		if item == nil {
			item = map[string]any{}
			paths[operation.Path] = item
		}
		item[strings.ToLower(operation.Method)] = generator.operation(operation)
	}
	generator.components["Envelope"] = map[string]any{"type": "object", "properties": map[string]any{"data": map[string]any{}, "meta": map[string]any{}}, "required": []string{"data"}}
	generator.components["Error"] = map[string]any{"type": "object", "required": []string{"error"}, "properties": map[string]any{"error": map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": map[string]any{"type": "string", "description": "Stable snake_case reason."}, "message": map[string]any{"type": "string"}, "hint": map[string]any{"type": "string", "description": "The next step, when the server knows one."}}}}}
	tags := []map[string]any{}
	for _, tag := range apiTags {
		tags = append(tags, map[string]any{"name": tag[0], "description": tag[1]})
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Media Manager API",
			"version":     firstNonEmpty(server.config.Version, "dev"),
			"description": apiConventions,
		},
		"servers":  []map[string]any{{"url": "/"}},
		"tags":     tags,
		"security": []map[string]any{{"ownerToken": []string{}}},
		"paths":    paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"ownerToken":   map[string]any{"type": "http", "scheme": "bearer", "description": "OWNER_TOKEN from the server environment."},
				"seerrWebhook": map[string]any{"type": "http", "scheme": "bearer", "description": "SEERR_WEBHOOK_SECRET; used by Seerr only."},
			},
			"schemas": generator.components,
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// operationID is the handler's method name (stable, readable, unique). The
// two request-decision routes share a closure factory and get explicit names.
func operationID(operation apiOperation) string {
	switch operation.Path {
	case "/api/v1/requests/{id}/approve":
		return "approveRequest"
	case "/api/v1/requests/{id}/decline":
		return "declineRequest"
	}
	name := runtime.FuncForPC(reflect.ValueOf(operation.Handler).Pointer()).Name()
	name = strings.TrimSuffix(name, "-fm")
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	return name
}

type schemaGenerator struct{ components map[string]any }

func (generator schemaGenerator) operation(operation apiOperation) map[string]any {
	result := map[string]any{
		"operationId": operationID(operation),
		"summary":     operation.Summary,
		"tags":        []string{operation.Tag},
		"x-mm-effect": string(operation.Effect),
		"x-mm-auth":   string(operation.Auth),
	}
	description := effectMeaning[operation.Effect]
	if operation.Description != "" {
		description = operation.Description + "\n\n" + description
	}
	result["description"] = description
	switch operation.Auth {
	case authNone:
		result["security"] = []map[string]any{}
	case authSeerrWebhook:
		result["security"] = []map[string]any{{"seerrWebhook": []string{}}}
	}
	parameters := []map[string]any{}
	for _, match := range pathParameter.FindAllStringSubmatch(operation.Path, -1) {
		parameters = append(parameters, map[string]any{"name": match[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
	}
	for _, param := range operation.Query {
		schema := map[string]any{"type": param.Type}
		if param.Format != "" {
			schema["format"] = param.Format
		}
		parameters = append(parameters, map[string]any{"name": param.Name, "in": "query", "required": param.Required, "description": param.Description, "schema": schema})
	}
	if len(parameters) > 0 {
		result["parameters"] = parameters
	}
	switch body := operation.Body.(type) {
	case nil:
	case rawBody:
		result["requestBody"] = map[string]any{"required": true, "description": body.Description, "content": map[string]any{body.ContentType: map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}
	default:
		schema := generator.schema(reflect.TypeOf(body))
		if len(operation.Required) > 0 {
			schema = map[string]any{"allOf": []any{schema}, "required": operation.Required}
		}
		result["requestBody"] = map[string]any{"required": !operation.OptionalBody, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	envelope := map[string]any{"$ref": "#/components/schemas/Envelope"}
	if operation.Response != nil {
		envelope = map[string]any{"allOf": []any{envelope, map[string]any{"type": "object", "properties": map[string]any{"data": generator.schema(reflect.TypeOf(operation.Response))}}}}
	}
	success := map[string]any{"description": "Success. 202 means the work was queued; 204 means an idempotent no-op.", "content": map[string]any{"application/json": map[string]any{"schema": envelope}}}
	if strings.HasSuffix(operation.Path, "/download") {
		success["content"] = map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}
	}
	result["responses"] = map[string]any{
		"2XX":     success,
		"default": map[string]any{"description": "Error with a stable code.", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}},
	}
	return result
}

var (
	timeType          = reflect.TypeOf(time.Time{})
	rawMessageType    = reflect.TypeOf(json.RawMessage{})
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

func (generator schemaGenerator) schema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == rawMessageType:
		return map[string]any{"description": "Arbitrary JSON."}
	case t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType):
		schema := map[string]any{"type": "string"}
		if t.Name() == "UUID" {
			schema["format"] = "uuid"
		}
		return schema
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": generator.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": generator.schema(t.Elem())}
	case reflect.Struct:
		if t.Name() == "" {
			return generator.object(t)
		}
		name := componentName(t)
		if _, exists := generator.components[name]; !exists {
			generator.components[name] = map[string]any{} // placeholder breaks recursion
			generator.components[name] = generator.object(t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

func (generator schemaGenerator) object(t reflect.Type) map[string]any {
	properties := map[string]any{}
	var embedded []any
	for index := 0; index < t.NumField(); index++ {
		field := t.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if field.Anonymous && name == "" {
			embedded = append(embedded, generator.schema(field.Type))
			continue
		}
		if name == "" {
			name = field.Name
		}
		properties[name] = generator.schema(field.Type)
	}
	object := map[string]any{"type": "object", "properties": properties}
	if len(embedded) > 0 {
		return map[string]any{"allOf": append(embedded, object)}
	}
	return object
}

func componentName(t reflect.Type) string {
	name := t.Name()
	pkg := t.PkgPath()
	if index := strings.LastIndex(pkg, "/"); index >= 0 {
		pkg = pkg[index+1:]
	}
	if pkg != "" && pkg != "httpapi" {
		name = pkg + "_" + name
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// sortedOperationIDs is used by tests and the index to list operations stably.
func sortedOperationIDs(operations []apiOperation) []string {
	ids := make([]string, 0, len(operations))
	for _, operation := range operations {
		ids = append(ids, operationID(operation))
	}
	sort.Strings(ids)
	return ids
}
