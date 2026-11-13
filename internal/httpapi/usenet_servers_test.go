package httpapi

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

	"github.com/media-manager/media-manager/internal/postgres"
)

// fakeUsenetAltMount is AltMount's provider API as our AltMount build serves
// it: a session login, the server list inside GET /api/config, and the
// /api/providers routes. It keeps every request body so the test can prove
// what reached AltMount.
type fakeUsenetAltMount struct {
	mu        sync.Mutex
	providers []map[string]any
	bodies    []string
	nextID    int
	// Live status for GET /api/fuse/status, /api/system/pool/metrics and
	// /api/files/active-streams; a nil value answers 500.
	drive, pool, streams any
}

func (fake *fakeUsenetAltMount) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(request.Body)
		if request.URL.Path == "/api/auth/login" {
			var login map[string]string
			_ = json.Unmarshal(raw, &login)
			if login["username"] != "mm-user" || login["password"] != "mm-pass" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "JWT", Value: "session", Path: "/"})
			_, _ = io.WriteString(writer, `{"success":true,"data":{"user":{"id":"u1","is_admin":true}}}`)
			return
		}
		if cookie, err := request.Cookie("JWT"); err != nil || cookie.Value != "session" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		fake.bodies = append(fake.bodies, request.Method+" "+request.URL.Path+" "+string(raw))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		reply := func(data any) {
			encoded, _ := json.Marshal(map[string]any{"success": true, "data": data})
			_, _ = writer.Write(encoded)
		}
		find := func(id string) int {
			for index, provider := range fake.providers {
				if provider["id"] == id {
					return index
				}
			}
			return -1
		}
		public := func(provider map[string]any) map[string]any {
			copy := map[string]any{}
			for key, value := range provider {
				if key != "password" {
					copy[key] = value
				}
			}
			copy["password_set"] = provider["password"] != ""
			return copy
		}
		path := request.URL.Path
		switch {
		case request.Method == http.MethodPost && path == "/api/user/logout":
			reply(nil)
		case request.Method == http.MethodGet && path == "/api/system/stats":
			reply(map[string]any{"system": map[string]any{"version": "v0.4.0-mm.3"}})
		case request.Method == http.MethodGet && path == "/api/config":
			list := []map[string]any{}
			for _, provider := range fake.providers {
				list = append(list, public(provider))
			}
			reply(map[string]any{"api_key": "altmount-own-api-key", "providers": list})
		case request.Method == http.MethodGet && (path == "/api/fuse/status" || path == "/api/system/pool/metrics" || path == "/api/files/active-streams"):
			data := map[string]any{"/api/fuse/status": fake.drive, "/api/system/pool/metrics": fake.pool, "/api/files/active-streams": fake.streams}[path]
			if data == nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			reply(data)
		case request.Method == http.MethodPost && path == "/api/providers/test":
			if body["password"] == "right-pass" {
				reply(map[string]any{"success": true, "rtt_ms": 23})
				return
			}
			reply(map[string]any{"success": false, "error_message": "481 authentication failed for " + body["password"].(string)})
		case request.Method == http.MethodPost && path == "/api/providers":
			if port, _ := body["port"].(float64); port == 119 && body["host"] == "refuse.example.test" {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(writer, `{"success":false,"error":{"code":"VALIDATION_ERROR","message":"Configuration validation failed","details":"provider host is blocked"}}`)
				return
			}
			fake.nextID++
			provider := map[string]any{"id": "provider_" + string(rune('0'+fake.nextID))}
			for key, value := range body {
				provider[key] = value
			}
			fake.providers = append(fake.providers, provider)
			reply(public(provider))
		case request.Method == http.MethodPut && path == "/api/providers/reorder":
			ids, _ := body["provider_ids"].([]any)
			ordered := []map[string]any{}
			for _, id := range ids {
				ordered = append(ordered, fake.providers[find(id.(string))])
			}
			fake.providers = ordered
			reply(nil)
		case request.Method == http.MethodPut && strings.HasPrefix(path, "/api/providers/"):
			index := find(strings.TrimPrefix(path, "/api/providers/"))
			if index < 0 {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			for key, value := range body {
				fake.providers[index][key] = value
			}
			reply(public(fake.providers[index]))
		case request.Method == http.MethodDelete && strings.HasPrefix(path, "/api/providers/"):
			index := find(strings.TrimPrefix(path, "/api/providers/"))
			if index < 0 {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			fake.providers = append(fake.providers[:index], fake.providers[index+1:]...)
			reply(nil)
		default:
			t.Errorf("unexpected AltMount request %s %s", request.Method, path)
			writer.WriteHeader(http.StatusNotFound)
		}
	})
}

// TestOwnerManagesUsenetServersThroughAltMount walks the Usenet servers page:
// with no AltMount connection it says what to do; then the owner tests a
// login, adds two servers, changes one without retyping its password,
// reorders them, and is stopped from removing or turning off the last
// active server. Passwords reach AltMount and nothing else: no response,
// audit row or error repeats them, and AltMount's own API key is never shown.
func TestOwnerManagesUsenetServersThroughAltMount(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	fake := &fakeUsenetAltMount{}
	altMount := httptest.NewServer(fake.handler(t))
	defer altMount.Close()
	resolve := func(reference string) (string, error) {
		return `{"username":"mm-user","password":"mm-pass"}`, nil
	}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", ResolveSecret: resolve}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	responses := []string{}
	call := func(method, path string, body any, target any) (int, string) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		}
		request, _ := http.NewRequest(method, testServer.URL+path, reader)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		responses = append(responses, string(raw))
		if target != nil {
			_ = json.Unmarshal(raw, &struct {
				Data any `json:"data"`
			}{Data: target})
		}
		return response.StatusCode, string(raw)
	}
	const base = "/api/v1/settings/usenet-servers"

	// The test store comes with an AltMount connection; turned off, the page
	// says where to add one.
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=false WHERE kind='altmount'`); err != nil {
		t.Fatal(err)
	}
	if status, body := call("GET", base, nil, nil); status != http.StatusConflict || !strings.Contains(body, "altmount_not_configured") {
		t.Fatalf("without AltMount: %d %s", status, body)
	}
	var connection postgres.Integration
	if err := store.Pool.QueryRow(ctx, `UPDATE integration_instances SET enabled=true, config=$1 WHERE kind='altmount' RETURNING id`, `{"base_url":"`+altMount.URL+`","allow_http":true}`).Scan(&connection.ID); err != nil {
		t.Fatal(err)
	}

	var test usenetServerTestResult
	if status, body := call("POST", base+"/test", map[string]any{"host": "news.example.test", "port": 563, "username": "me", "password": "wrong-pass", "tls": true}, &test); status != 200 || test.OK || !strings.Contains(test.Detail, "481") || strings.Contains(body, "wrong-pass") {
		t.Fatalf("failed test login: %d %s", status, body)
	}
	if status, body := call("POST", base+"/test", map[string]any{"host": "news.example.test", "port": 563, "username": "me", "password": "right-pass", "tls": true}, &test); status != 200 || !test.OK || test.RTTMs != 23 {
		t.Fatalf("good test login: %d %s", status, body)
	}

	if status, body := call("POST", base, map[string]any{"host": "http://news.example.test", "port": 563, "username": "me", "password": "p"}, nil); status != http.StatusBadRequest || !strings.Contains(body, "invalid_usenet_server") {
		t.Fatalf("a URL as host: %d %s", status, body)
	}
	if status, body := call("POST", base, map[string]any{"host": "news.example.test", "port": 563, "username": "me"}, nil); status != http.StatusBadRequest || !strings.Contains(body, "password is required") {
		t.Fatalf("no password: %d %s", status, body)
	}
	if status, body := call("POST", base, map[string]any{"host": "refuse.example.test", "port": 119, "username": "me", "password": "refused-pass"}, nil); status != http.StatusUnprocessableEntity || !strings.Contains(body, "provider host is blocked") || strings.Contains(body, "refused-pass") {
		t.Fatalf("AltMount refusal: %d %s", status, body)
	}

	var primary, backup map[string]any
	if status, body := call("POST", base, map[string]any{"name": "Primær", "host": " news.example.test ", "port": 563, "username": "me", "password": "primary-pass", "tls": true}, &primary); status != http.StatusCreated || primary["password_set"] != true || primary["host"] != "news.example.test" || primary["enabled"] != true || primary["max_connections"] != float64(10) {
		t.Fatalf("create primary: %d %s", status, body)
	}
	if status, body := call("POST", base, map[string]any{"host": "backup.example.test", "port": 563, "username": "me2", "password": "backup-pass", "tls": true, "is_backup_provider": true, "max_connections": 5}, &backup); status != http.StatusCreated {
		t.Fatalf("create backup: %d %s", status, body)
	}
	primaryID, backupID := primary["id"].(string), backup["id"].(string)

	var updated map[string]any
	if status, body := call("PATCH", base+"/"+primaryID, map[string]any{"max_connections": 30, "password": ""}, &updated); status != 200 || updated["max_connections"] != float64(30) || updated["password_set"] != true {
		t.Fatalf("change without password: %d %s", status, body)
	}
	if fake.providers[0]["password"] != "primary-pass" {
		t.Fatal("an empty password replaced the stored one")
	}
	if status, body := call("PATCH", base+"/"+primaryID, map[string]any{"password": "new-primary-pass"}, nil); status != 200 {
		t.Fatalf("change password: %d %s", status, body)
	}
	if fake.providers[0]["password"] != "new-primary-pass" {
		t.Fatal("the new password did not reach AltMount")
	}

	if status, body := call("PUT", base+"/order", map[string]any{"ids": []string{backupID, primaryID}}, nil); status != 200 {
		t.Fatalf("reorder: %d %s", status, body)
	}
	var view usenetServersView
	if status, body := call("GET", base, nil, &view); status != 200 || len(view.Servers) != 2 || view.Servers[0].ID != backupID || view.AltMount.ID != connection.ID || strings.Contains(body, "altmount-own-api-key") {
		t.Fatalf("list: %d %s", status, body)
	}

	if status, body := call("PATCH", base+"/"+backupID, map[string]any{"enabled": false}, nil); status != 200 {
		t.Fatalf("turn off backup: %d %s", status, body)
	}
	if status, body := call("PATCH", base+"/"+primaryID, map[string]any{"enabled": false}, nil); status != http.StatusConflict || !strings.Contains(body, "usenet_server_last_active") {
		t.Fatalf("turn off last active: %d %s", status, body)
	}
	if status, body := call("DELETE", base+"/"+primaryID, nil, nil); status != http.StatusConflict || !strings.Contains(body, "usenet_server_last_active") {
		t.Fatalf("delete last active: %d %s", status, body)
	}
	if status, body := call("DELETE", base+"/"+backupID, nil, nil); status != 200 {
		t.Fatalf("delete disabled backup: %d %s", status, body)
	}
	if status, _ := call("DELETE", base+"/provider_9", nil, nil); status != http.StatusNotFound {
		t.Fatalf("delete unknown: %d", status)
	}
	if len(fake.providers) != 1 || fake.providers[0]["id"] != primaryID || fake.providers[0]["enabled"] != true {
		t.Fatalf("AltMount servers: %+v", fake.providers)
	}

	rows, err := store.Pool.Query(ctx, `SELECT action, detail::text FROM audit_events WHERE aggregate_type='integration' AND aggregate_id=$1 AND action LIKE 'usenet_server.%' ORDER BY occurred_at, id`, connection.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	for rows.Next() {
		var action, detail string
		if err := rows.Scan(&action, &detail); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		for _, secret := range []string{"primary-pass", "new-primary-pass", "backup-pass"} {
			if strings.Contains(detail, secret) {
				t.Fatalf("a password reached the audit trail: %s %s", action, detail)
			}
		}
		if action == postgres.UsenetServerChanged && strings.Contains(detail, `"password"`) && !strings.Contains(detail, `"fields": ["password"]`) {
			t.Fatalf("unexpected change detail: %s", detail)
		}
	}
	rows.Close()
	want := []string{"usenet_server.created", "usenet_server.created", "usenet_server.changed", "usenet_server.changed", "usenet_server.reordered", "usenet_server.changed", "usenet_server.deleted"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions=%v want %v", actions, want)
	}
	for _, response := range responses {
		for _, secret := range []string{"primary-pass", "new-primary-pass", "backup-pass", "right-pass", "mm-pass"} {
			if strings.Contains(response, secret) {
				t.Fatalf("a password came back in a response: %s", response)
			}
		}
	}
}
