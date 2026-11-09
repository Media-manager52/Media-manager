package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/postgres"
)

// TestOwnerConnectsPlexFromTheWebsite walks the website's setup path: create a
// Plex connection, save its token encrypted, check it against Plex, list its
// libraries, edit it, and read the setup checklist. The token is never
// returned, and a new integration cannot borrow another one's sealed secret.
func TestOwnerConnectsPlexFromTheWebsite(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	secrets, err := integrationsecrets.New(store, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(reference string) (string, error) { return secrets.Resolve(ctx, reference) }
	const token = "plex-owner-token-0123456789"
	plexServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Plex-Token") != token || request.URL.Path != "/library/sections" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(writer, `<MediaContainer><Directory key="7" title="Film 4K" type="movie"><Location path="/media/Film 4K"/></Directory><Directory key="8" title="Serier" type="show"><Location path="/media/Serier"/></Directory></MediaContainer>`)
	}))
	defer plexServer.Close()
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", IntegrationSecrets: secrets, ResolveSecret: resolve}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	call := func(method, path string, body any, target any) (int, string) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		}
		request, err := http.NewRequest(method, testServer.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
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
		if target != nil {
			envelope := struct {
				Data any `json:"data"`
			}{Data: target}
			_ = json.Unmarshal(raw, &envelope)
		}
		return response.StatusCode, string(raw)
	}

	var created postgres.Integration
	if status, body := call("POST", "/api/v1/settings/integrations", map[string]any{"kind": "plex", "name": "Plex", "enabled": true, "is_default": true, "config": map[string]any{"base_url": plexServer.URL, "allow_http": true}}, &created); status != http.StatusCreated {
		t.Fatalf("create plex status=%d body=%s", status, body)
	}
	var check connectionCheck
	if status, _ := call("POST", "/api/v1/settings/integrations/"+created.ID.String()+"/check", nil, &check); status != 200 || check.OK || !strings.Contains(check.Detail, "token") {
		t.Fatalf("check without a token: status=%d check=%+v", status, check)
	}
	if status, body := call("PUT", "/api/v1/settings/integrations/"+created.ID.String()+"/secret", map[string]any{"value": "has space"}, nil); status != http.StatusBadRequest {
		t.Fatalf("invalid secret status=%d body=%s", status, body)
	}
	var saved postgres.Integration
	status, body := call("PUT", "/api/v1/settings/integrations/"+created.ID.String()+"/secret", map[string]any{"value": token}, &saved)
	if status != 200 || saved.SecretRef != integrationsecrets.Reference(created.ID) || strings.Contains(body, token) {
		t.Fatalf("save secret status=%d ref=%q body=%s", status, saved.SecretRef, body)
	}
	if _, listing := call("GET", "/api/v1/settings/integrations", nil, nil); strings.Contains(listing, token) {
		t.Fatal("the integration listing returned the token")
	}
	var stored []byte
	if err := store.Pool.QueryRow(ctx, `SELECT ciphertext FROM integration_secrets WHERE integration_id=$1`, created.ID.String()).Scan(&stored); err != nil || bytes.Contains(stored, []byte(token)) {
		t.Fatalf("stored secret err=%v plaintext=%v", err, bytes.Contains(stored, []byte(token)))
	}
	if status, _ := call("POST", "/api/v1/settings/integrations/"+created.ID.String()+"/check", nil, &check); status != 200 || !check.OK {
		t.Fatalf("check with the token: status=%d check=%+v", status, check)
	}
	statuses, err := store.ListSourceStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, status := range statuses {
		recorded = recorded || status.ID == created.ID && status.LastCheckOK != nil && *status.LastCheckOK && status.LastCheckAt != nil
	}
	if !recorded {
		t.Fatalf("the latest connection test was not kept: %+v", statuses)
	}
	var sections []struct {
		Key       string   `json:"key"`
		Title     string   `json:"title"`
		Locations []string `json:"locations"`
	}
	if status, body := call("GET", "/api/v1/settings/integrations/"+created.ID.String()+"/plex-libraries", nil, &sections); status != 200 || len(sections) != 2 || sections[0].Key != "7" || sections[0].Locations[0] != "/media/Film 4K" {
		t.Fatalf("plex libraries status=%d body=%s", status, body)
	}
	if status, body := call("POST", "/api/v1/settings/integrations", map[string]any{"kind": "seerr", "name": "Borrower", "secret_ref": integrationsecrets.Reference(created.ID), "config": map[string]any{"base_url": "http://seerr:5055", "allow_http": true}}, nil); status != 422 {
		t.Fatalf("borrowing a sealed secret: status=%d body=%s", status, body)
	}
	var edited postgres.Integration
	if status, body := call("PATCH", "/api/v1/settings/integrations/"+created.ID.String()+"/connection", map[string]any{"name": "Plex hjemme"}, &edited); status != 200 || edited.Name != "Plex hjemme" {
		t.Fatalf("edit connection status=%d body=%s", status, body)
	}
	if status, _ := call("PATCH", "/api/v1/settings/integrations/"+created.ID.String()+"/connection", map[string]any{"base_url": "ftp://plex"}, nil); status != http.StatusBadRequest {
		t.Fatalf("invalid base_url status=%d", status)
	}
	if status, _ := call("PATCH", "/api/v1/settings/integrations/"+created.ID.String()+"/connection", map[string]any{"category": "x"}, nil); status != http.StatusBadRequest {
		t.Fatalf("AltMount field on Plex status=%d", status)
	}
	var setup setupStatus
	if status, body := call("GET", "/api/v1/setup/status", nil, &setup); status != 200 || !setup.SecretStorageEnabled || setup.Complete {
		t.Fatalf("setup status=%d body=%s", status, body)
	}
	steps := map[string]SetupStep{}
	for _, step := range setup.Steps {
		steps[step.ID] = step
	}
	if !steps["plex"].Done || steps["plex"].Detail != "Plex hjemme" || steps["indexer"].Done || !steps["seerr"].Optional || steps["seerr"].Done {
		t.Fatalf("setup steps=%+v", setup.Steps)
	}
}

// TestSecretRouteNeedsAnEncryptionKey proves secrets are refused, not stored
// in plaintext, when no key is configured.
func TestSecretRouteNeedsAnEncryptionKey(t *testing.T) {
	store := openHTTPTestStore(t)
	created, err := store.CreateIntegration(context.Background(), postgres.Integration{Kind: "plex", Name: "Plex", Config: []byte(`{"base_url":"http://plex:32400","allow_http":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("PUT", "/api/v1/settings/integrations/"+created.ID.String()+"/secret", strings.NewReader(`{"value":"token"}`))
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "secret_storage_unavailable") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
