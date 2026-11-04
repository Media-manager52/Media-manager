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
	"time"

	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/postgres"
)

// fakeSeerr keeps webhook settings like Seerr: the payload template is
// returned decoded and stored quoted, and the test endpoint really posts to
// the webhook address and fails when the receiver does not answer 2xx.
type fakeSeerr struct {
	mu       sync.Mutex
	settings map[string]any
	tests    int
	saves    int
}

func (fake *fakeSeerr) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("X-Api-Key") != "seerr-key-0123456789" {
		writer.WriteHeader(http.StatusForbidden)
		return
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	switch {
	case request.Method == "GET" && request.URL.Path == "/api/v1/settings/notifications/webhook":
		_ = json.NewEncoder(writer).Encode(fake.settings)
	case request.Method == "POST" && strings.HasPrefix(request.URL.Path, "/api/v1/settings/notifications/webhook"):
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		options := body["options"].(map[string]any)
		var template string
		if json.Unmarshal([]byte(options["jsonPayload"].(string)), &template) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(request.URL.Path, "/test") {
			fake.tests++
			payload := strings.ReplaceAll(template, "{{notification_type}}", "TEST_NOTIFICATION")
			delivery, _ := http.NewRequest("POST", options["webhookUrl"].(string), strings.NewReader(payload))
			delivery.Header.Set("Authorization", options["authHeader"].(string))
			response, err := http.DefaultClient.Do(delivery)
			if err != nil || response.StatusCode >= 300 {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		fake.saves++
		options["jsonPayload"] = template
		fake.settings = body
		writer.WriteHeader(http.StatusOK)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// TestOwnerPointsSeerrAtMediaManager proves the webhook is saved in Seerr only
// after Seerr's test notification reached this API with the right secret,
// that the owner's other Seerr settings are kept, and that the secret is never
// returned.
func TestOwnerPointsSeerrAtMediaManager(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	secrets, err := integrationsecrets.New(store, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSeerr{settings: map[string]any{"enabled": false, "embedPoster": true, "types": float64(256), "options": map[string]any{"webhookUrl": "", "jsonPayload": "{}", "authHeader": "", "customHeaders": []any{map[string]any{"key": "X-Owner", "value": "kept"}}, "supportVariables": false}}}
	seerrServer := httptest.NewServer(fake)
	defer seerrServer.Close()
	seerrIntegration, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: []byte(`{"base_url":"` + seerrServer.URL + `","allow_http":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Save(ctx, seerrIntegration.ID, "seerr-key-0123456789", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	webhookSecret := secrets.Derive("seerr-webhook:" + seerrIntegration.ID.String())
	resolve := func(reference string) (string, error) { return secrets.Resolve(ctx, reference) }
	newServer := func(running bool) *httptest.Server {
		config := Config{OwnerToken: testOwnerToken, Version: "test", IntegrationSecrets: secrets, ResolveSecret: resolve}
		if running {
			config.SeerrEnabled, config.SeerrInstanceID, config.SeerrWebhookSecret = true, seerrIntegration.ID.String(), webhookSecret
		}
		server, err := New(config, store)
		if err != nil {
			t.Fatal(err)
		}
		return httptest.NewServer(server.Handler())
	}
	call := func(base, method, path string, body any, target any) (int, string) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		}
		request, _ := http.NewRequest(method, base+path, reader)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if target != nil {
			_ = json.Unmarshal(raw, &struct {
				Data any `json:"data"`
			}{Data: target})
		}
		return response.StatusCode, string(raw)
	}
	path := "/api/v1/settings/integrations/" + seerrIntegration.ID.String() + "/seerr-webhook"

	stopped := newServer(false)
	defer stopped.Close()
	if status, body := call(stopped.URL, "POST", path, map[string]string{"webhook_url": stopped.URL + "/api/v1/integrations/seerr/webhook"}, nil); status != http.StatusConflict || !strings.Contains(body, "intake_not_running") {
		t.Fatalf("setup before intake runs: status=%d body=%s", status, body)
	}

	running := newServer(true)
	defer running.Close()
	if status, _ := call(running.URL, "POST", path, map[string]string{"webhook_url": seerrServer.URL + "/nowhere"}, nil); status != http.StatusBadGateway || fake.saves != 0 {
		t.Fatalf("a wrong address was saved: status=%d saves=%d", status, fake.saves)
	}
	var result seerrWebhookStatus
	status, body := call(running.URL, "POST", path, map[string]string{"webhook_url": running.URL + "/api/v1/integrations/seerr/webhook"}, &result)
	if status != 200 || result.Webhook == nil || !result.Webhook.Managed || fake.saves != 1 || fake.tests != 2 || strings.Contains(body, webhookSecret) {
		t.Fatalf("configure: status=%d result=%+v saves=%d tests=%d body=%s", status, result, fake.saves, fake.tests, body)
	}
	options := fake.settings["options"].(map[string]any)
	if fake.settings["embedPoster"] != true || int64(fake.settings["types"].(float64))&256 == 0 || len(options["customHeaders"].([]any)) != 1 || options["authHeader"] != "Bearer "+webhookSecret {
		t.Fatalf("Seerr settings not merged: %+v", fake.settings)
	}
	var read seerrWebhookStatus
	if status, body := call(running.URL, "GET", path, nil, &read); status != 200 || !read.IntakeRunning || read.Webhook == nil || !read.Webhook.Managed || strings.Contains(body, webhookSecret) {
		t.Fatalf("read back: status=%d body=%s", status, body)
	}
	if status, body := call(stopped.URL, "GET", path, nil, &read); status != 200 || read.IntakeRunning || read.Webhook == nil || read.Webhook.Managed {
		t.Fatalf("a stopped intake reported a managed webhook: status=%d body=%s", status, body)
	}
}
