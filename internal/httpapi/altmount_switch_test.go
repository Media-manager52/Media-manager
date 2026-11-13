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

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
	"github.com/media-manager/media-manager/internal/altmountswitch"
	"github.com/media-manager/media-manager/internal/plexaccess"
)

// fakeSwitchCosmos is a Cosmos holding one AltMount container whose image
// decides which version the fake AltMount reports.
type fakeSwitchCosmos struct {
	mu      sync.Mutex
	image   string
	updates []string
}

func (fake *fakeSwitchCosmos) Containers(context.Context) ([]cosmos.ContainerSummary, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return []cosmos.ContainerSummary{{ID: "aaa111", Names: []string{"/mm-api"}}, {ID: "bbb", Names: []string{"/altmount"}, Image: fake.image}}, nil
}

func (fake *fakeSwitchCosmos) Container(_ context.Context, name string) (cosmos.Container, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var container cosmos.Container
	err := json.Unmarshal([]byte(`{"Id":"bbb","Name":"/`+name+`","Config":{"Image":"`+fake.image+`"},"State":{"Running":true,"Status":"running"}}`), &container)
	return container, err
}

func (fake *fakeSwitchCosmos) UpdateMounts(context.Context, string, []cosmos.Mount, int64, int64) error {
	return nil
}

func (fake *fakeSwitchCosmos) UpdateImage(_ context.Context, name, image string, _ []cosmos.Mount, _, _ int64) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.updates = append(fake.updates, name+"="+image)
	fake.image = image
	return nil
}

func (fake *fakeSwitchCosmos) version() string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.image == altmountswitch.Image {
		return "v0.4.0-mm.3"
	}
	if strings.HasSuffix(fake.image, ":v0.5.0") {
		return "v0.5.0"
	}
	return "v0.4.0"
}

// TestSwitchAltMountGivesANormalAltMountMediaManagersBuild runs "Skift til
// Media Managers AltMount": the status names the build, a preview changes
// nothing, the switch only swaps the image, a newer AltMount is refused, and
// the pasted Cosmos token is never stored.
func TestSwitchAltMountGivesANormalAltMountMediaManagersBuild(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	fake := &fakeSwitchCosmos{image: "ghcr.io/kipsilabs/altmount:latest"}
	altMount := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reply := func(data any) {
			_ = json.NewEncoder(writer).Encode(map[string]any{"success": true, "data": data})
		}
		switch request.URL.Path {
		case "/api/auth/config":
			reply(map[string]any{"login_required": false})
		case "/api/user":
			reply(map[string]any{"id": "admin", "is_admin": true})
		case "/api/system/stats":
			reply(map[string]any{"system": map[string]any{"version": fake.version()}})
		case "/api/fuse/status":
			reply(map[string]any{"status": "running", "healthy": true, "path": "/mnt/altmount"})
		case "/api/config":
			reply(map[string]any{"providers": []any{}})
		case "/api/system/pool/metrics":
			reply(map[string]any{"providers": []any{}})
		case "/api/files/active-streams":
			reply(nil)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer altMount.Close()
	host := strings.TrimPrefix(altMount.URL, "http://")
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$1, enabled=true, is_default=true WHERE kind='altmount'`, `{"base_url":"http://`+host+`","allow_http":true,"authentication":"anonymous_admin","local_read_only_root":"/mnt/altmount"}`); err != nil {
		t.Fatal(err)
	}
	const pastedKey = "pasted-cosmos-token-0123456789"
	access := &plexaccess.Service{
		OpenCosmos: func(_ context.Context, _, key string) (plexaccess.Cosmos, error) {
			if key != pastedKey {
				return nil, plexaccess.ErrCosmosUnauthorized
			}
			return fake, nil
		},
		Self: func() (string, error) { return "aaa111", nil },
	}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", PlexAccess: access}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	call := func(method, path string, body map[string]any) (int, map[string]any, string) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		}
		request, _ := http.NewRequest(method, testServer.URL+path, reader)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		text, _ := io.ReadAll(response.Body)
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(text, &envelope)
		return response.StatusCode, envelope.Data, string(text)
	}

	status, data, text := call(http.MethodGet, "/api/v1/system/altmount", nil)
	if status != http.StatusOK || data["version"] != "v0.4.0" || data["build"] != "standard" || data["can_switch"] != true {
		t.Fatalf("status: %d %s", status, text)
	}
	if status, _, text := call(http.MethodPost, "/api/v1/system/altmount/switch", map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": "wrong"}); status != http.StatusBadRequest || !strings.Contains(text, "cosmos_unauthorized") {
		t.Fatalf("wrong token: %d %s", status, text)
	}
	status, plan, text := call(http.MethodPost, "/api/v1/system/altmount/switch", map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey})
	if status != http.StatusOK || plan["container"] != "altmount" || plan["current_version"] != "v0.4.0" || plan["new_image"] != altmountswitch.Image || plan["applied"] != false || len(fake.updates) != 0 {
		t.Fatalf("preview: %d %s", status, text)
	}
	status, plan, text = call(http.MethodPost, "/api/v1/system/altmount/switch", map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey, "apply": true})
	if status != http.StatusOK || plan["applied"] != true || plan["new_version"] != "v0.4.0-mm.3" || len(fake.updates) != 1 || fake.updates[0] != "altmount="+altmountswitch.Image {
		t.Fatalf("apply: %d %s %v", status, text, fake.updates)
	}
	var detail string
	if err := store.Pool.QueryRow(ctx, `SELECT detail::text FROM audit_events WHERE action='altmount.image_switched'`).Scan(&detail); err != nil || !strings.Contains(detail, "kipsilabs/altmount:latest") || strings.Contains(detail, pastedKey) {
		t.Fatalf("audit %q %v", detail, err)
	}
	status, data, text = call(http.MethodGet, "/api/v1/system/altmount", nil)
	if status != http.StatusOK || data["build"] != "media_manager" || data["can_switch"] != false {
		t.Fatalf("status after: %d %s", status, text)
	}
	status, plan, text = call(http.MethodPost, "/api/v1/system/altmount/switch", map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey, "apply": true})
	if status != http.StatusOK || plan["already_media_manager"] != true || len(fake.updates) != 1 {
		t.Fatalf("again: %d %s", status, text)
	}

	fake.image = "ghcr.io/kipsilabs/altmount:v0.5.0"
	if status, _, text := call(http.MethodPost, "/api/v1/system/altmount/switch", map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey, "apply": true}); status != http.StatusConflict || !strings.Contains(text, "altmount_version_newer") || len(fake.updates) != 1 {
		t.Fatalf("newer: %d %s", status, text)
	}
	var stored int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE detail::text LIKE '%'||$1||'%'`, pastedKey).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("the token was stored: %d %v", stored, err)
	}
}
