package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/media-manager/media-manager/internal/adapters/plextv"
	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/plexlogin"
)

type httpFakePlexAccount struct {
	approved  atomic.Bool
	serverURL string
}

func (fake *httpFakePlexAccount) CreatePIN(context.Context) (plextv.PIN, error) {
	return plextv.PIN{ID: 9, Code: "pin-code"}, nil
}

func (fake *httpFakePlexAccount) CheckPIN(context.Context, int64) (plextv.PIN, error) {
	if !fake.approved.Load() {
		return plextv.PIN{ID: 9, Code: "pin-code"}, nil
	}
	return plextv.PIN{ID: 9, Code: "pin-code", AuthToken: "account-token"}, nil
}

func (fake *httpFakePlexAccount) AuthURL(code string) string {
	return "https://app.plex.tv/auth#?code=" + code
}

func (fake *httpFakePlexAccount) Servers(context.Context, string) ([]plextv.Server, error) {
	return []plextv.Server{{Name: "Hjemme", ClientIdentifier: "machine-1", Owned: true, AccessToken: plexLoginServerToken, Connections: []plextv.Connection{{URI: fake.serverURL, Local: true}}}}, nil
}

const plexLoginServerToken = "plex-server-token-from-login"

// TestOwnerLogsInWithPlex walks "Log ind med Plex": start, wait for approval,
// pick the found server, and get a working Plex connection whose token was
// never sent to the browser.
func TestOwnerLogsInWithPlex(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	secrets, err := integrationsecrets.New(store, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	plexServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Plex-Token") != plexLoginServerToken {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/identity":
			_, _ = io.WriteString(writer, `{"MediaContainer":{"machineIdentifier":"machine-1"}}`)
		case "/library/sections":
			_, _ = io.WriteString(writer, `<MediaContainer><Directory key="1" title="Film" type="movie"><Location path="/media/Film"/></Directory></MediaContainer>`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer plexServer.Close()
	before, err := store.ListIntegrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hadDefault := false
	for _, item := range before {
		hadDefault = hadDefault || (item.Kind == "plex" && item.Default && item.Enabled)
	}
	account := &httpFakePlexAccount{serverURL: plexServer.URL}
	login := &plexlogin.Service{Account: account, Candidates: []string{}, Identity: func(ctx context.Context, uri, token string) (string, error) {
		return plextv.Identity(ctx, uri, token, nil)
	}}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", IntegrationSecrets: secrets, ResolveSecret: func(reference string) (string, error) { return secrets.Resolve(ctx, reference) }, PlexLogin: login}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	var bodies strings.Builder
	call := func(method, path string, body any, target any) int {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		}
		request, _ := http.NewRequest(method, testServer.URL+path, reader)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		bodies.Write(raw)
		if target != nil {
			_ = json.Unmarshal(raw, &struct {
				Data any `json:"data"`
			}{Data: target})
		}
		return response.StatusCode
	}

	var started plexlogin.Started
	if status := call(http.MethodPost, "/api/v1/setup/plex/login", nil, &started); status != http.StatusCreated || started.ID == "" || !strings.Contains(started.AuthURL, "pin-code") {
		t.Fatalf("start = %d %+v", status, started)
	}
	var polled plexlogin.Status
	if status := call(http.MethodGet, "/api/v1/setup/plex/login/"+started.ID, nil, &polled); status != http.StatusOK || polled.State != "waiting" {
		t.Fatalf("poll before approval = %d %+v", status, polled)
	}
	if status := call(http.MethodPost, "/api/v1/setup/plex/login/"+started.ID+"/connect", map[string]string{"server_id": "machine-1"}, nil); status != http.StatusConflict {
		t.Fatalf("connect before approval = %d, want 409", status)
	}
	account.approved.Store(true)
	if status := call(http.MethodGet, "/api/v1/setup/plex/login/"+started.ID, nil, &polled); status != http.StatusOK || polled.State != "approved" || len(polled.Servers) != 1 || polled.Servers[0].Recommended != plexServer.URL {
		t.Fatalf("poll after approval = %d %+v", status, polled)
	}
	var connected plexLoginConnected
	if status := call(http.MethodPost, "/api/v1/setup/plex/login/"+started.ID+"/connect", map[string]string{"server_id": "machine-1"}, &connected); status != http.StatusOK {
		t.Fatalf("connect = %d: %s", status, bodies.String())
	}
	if !connected.Created || !connected.Check.OK || connected.Integration.Kind != "plex" || !connected.Integration.Enabled || connected.Integration.Default == hadDefault || connected.Integration.Name != "Hjemme" {
		t.Fatalf("connected = %+v", connected)
	}
	if plexBaseURL(connected.Integration) != plexServer.URL || !strings.HasPrefix(connected.Integration.SecretRef, "sealed:") {
		t.Fatalf("saved connection = %+v", connected.Integration)
	}
	if strings.Contains(bodies.String(), plexLoginServerToken) || strings.Contains(bodies.String(), "account-token") {
		t.Fatal("a Plex token reached the browser")
	}
	if status := call(http.MethodPost, "/api/v1/setup/plex/login/"+started.ID+"/connect", map[string]string{"server_id": "machine-1"}, nil); status != http.StatusNotFound {
		t.Fatalf("a used login connected again: %d", status)
	}

	// Logging in again refreshes the same connection instead of adding one.
	account.approved.Store(false)
	call(http.MethodPost, "/api/v1/setup/plex/login", nil, &started)
	account.approved.Store(true)
	call(http.MethodGet, "/api/v1/setup/plex/login/"+started.ID, nil, &polled)
	var again plexLoginConnected
	if status := call(http.MethodPost, "/api/v1/setup/plex/login/"+started.ID+"/connect", map[string]string{"server_id": "machine-1"}, &again); status != http.StatusOK || again.Created || again.Integration.ID != connected.Integration.ID || !again.Check.OK {
		t.Fatalf("second login = %d %+v", status, again)
	}
}
