package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/postgres"
)

func TestOldBoysCredentialsRequireOwnerEvenWhenUnavailable(t *testing.T) {
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32)}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT"} {
		request := httptest.NewRequest(method, "/api/v1/settings/oldboys/credentials", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected %s route: %d", method, response.Code)
		}
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if method == "GET" && (response.Code != 200 || !strings.Contains(response.Body.String(), `"available":false`)) {
			t.Fatal("missing server key status incorrect")
		}
		if method == "PUT" && response.Code != 503 {
			t.Fatal("missing server key allowed write")
		}
	}
}

func TestOldBoysCredentialOwnerRoutesWithEncryptedDatabase(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	service, err := oldboyscredentials.New(store, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32), OldBoysCredentials: service}, store)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/v1/settings/oldboys/credentials", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	input := oldboyscredentials.Credentials{APIToken: "secret-api-fixture", PassKey: "secret-pid-fixture", RSSKey: "secret-rid-fixture"}
	body, _ := json.Marshal(input)
	for _, headers := range []map[string]string{{"Content-Type": "text/plain"}, {"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}} {
		request := httptest.NewRequest("PUT", "/api/v1/settings/oldboys/credentials", strings.NewReader(string(body)))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnsupportedMediaType && response.Code != http.StatusForbidden {
			t.Fatal("unsafe browser request accepted")
		}
	}
	response := call("PUT", string(body))
	if response.Code != 200 {
		t.Fatal("save failed", response.Body.String())
	}
	for _, method := range []string{"GET", "PUT"} {
		response = call(method, string(body))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("status or caching incorrect")
		}
		for _, secret := range []string{input.APIToken, input.PassKey, input.RSSKey} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("response exposed a credential")
			}
		}
	}
	loaded, err := service.Load(ctx)
	if err != nil || loaded != input {
		t.Fatal("database roundtrip failed", err)
	}
	var auditCount int
	var safeAudit bool
	if err := store.Pool.QueryRow(ctx, `SELECT count(*),bool_and(detail='{"credentials_saved":true}'::jsonb) FROM audit_events WHERE action='oldboys.credentials_saved'`).Scan(&auditCount, &safeAudit); err != nil || auditCount != 2 || !safeAudit {
		t.Fatal("credential writes need an atomic audit without secret values", err)
	}
	sealed, err := store.LoadOldBoysCredentials(ctx)
	if err != nil || strings.Contains(string(sealed), input.APIToken) {
		t.Fatal("database stored plaintext")
	}
	for _, bad := range []string{`{"api_token":"secret-api-fixture"}`, `{"api_token":"secret-api-fixture","pass_key":"pid","rss_key":"rid","unexpected":"secret-api-fixture"}`, `{"api_token":"` + strings.Repeat("x", 17000) + `"}`, `{"api_token":"invalid space","pass_key":"pid","rss_key":"rid"}`} {
		response = call("PUT", bad)
		if response.Code != 400 || strings.Contains(response.Body.String(), input.APIToken) {
			t.Fatal("invalid request handling exposed secret or allowed write")
		}
	}
	loaded, err = service.Load(ctx)
	if err != nil || loaded != input {
		t.Fatal("invalid request changed saved credentials")
	}
}

func TestOldBoysCommissionRequiresOwnerAndSanitizesProviderErrors(t *testing.T) {
	calls := 0
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32), CommissionOldBoys: func(context.Context) (postgres.Integration, error) {
		calls++
		return postgres.Integration{}, errors.New("provider URL with secret-pid-fixture")
	}}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		owner, cross bool
		status       int
	}{{false, false, 401}, {true, true, 403}, {true, false, 502}} {
		request := httptest.NewRequest("POST", "/api/v1/settings/oldboys/commission", nil)
		if spec.owner {
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
		}
		if spec.cross {
			request.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != spec.status || strings.Contains(response.Body.String(), "secret-pid-fixture") {
			t.Fatal("commission owner/origin/error boundary failed")
		}
	}
	if calls != 1 {
		t.Fatal("unprotected commission sent requests")
	}
}
