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

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
	"github.com/media-manager/media-manager/internal/plexaccess"
	"github.com/media-manager/media-manager/internal/postgres"
)

type fakePlexAccessCosmos struct {
	containers map[string]cosmos.Container
	list       []cosmos.ContainerSummary
	updated    []cosmos.Mount
}

func (fake *fakePlexAccessCosmos) Containers(context.Context) ([]cosmos.ContainerSummary, error) {
	return fake.list, nil
}

func (fake *fakePlexAccessCosmos) Container(_ context.Context, name string) (cosmos.Container, error) {
	return fake.containers[name], nil
}

func (fake *fakePlexAccessCosmos) UpdateMounts(_ context.Context, _ string, mounts []cosmos.Mount, _, _ int64) error {
	fake.updated = mounts
	return nil
}

// TestFixPlexAccessGivesPlexAltMountThroughCosmos runs "Ret Plex": the
// pasted Cosmos token is used once and never stored or audited, a preview
// changes nothing, the fix sends Plex's folders with AltMount's added, and
// several Plex containers are offered to choose from.
func TestFixPlexAccessGivesPlexAltMountThroughCosmos(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx := context.Background()
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$1, enabled=true WHERE kind='altmount'`, `{"base_url":"http://altmount:8080","allow_http":true,"local_read_only_root":"/mnt/altmount"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "TEST_PLEX_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	self := cosmos.Container{ID: "aaa111", Mounts: []cosmos.MountPoint{{Type: "bind", Source: "/srv/mm/usenet", Destination: "/mnt", Propagation: "rslave"}}}
	var plex cosmos.Container
	if err := json.Unmarshal([]byte(`{"Id":"bbb","State":{"Running":true},"HostConfig":{"Binds":["/srv/config/plex:/config"]}}`), &plex); err != nil {
		t.Fatal(err)
	}
	fake := &fakePlexAccessCosmos{
		containers: map[string]cosmos.Container{"aaa111": self, "plex": plex, "plex-test": plex},
		list:       []cosmos.ContainerSummary{{ID: "aaa111", Names: []string{"/mm-api"}}, {ID: "bbb", Names: []string{"/plex"}, Image: "lscr.io/linuxserver/plex"}},
	}
	const pastedKey = "pasted-cosmos-token-0123456789"
	var opened []string
	access := &plexaccess.Service{
		OpenCosmos: func(_ context.Context, host, key string) (plexaccess.Cosmos, error) {
			if key != pastedKey {
				return nil, plexaccess.ErrCosmosUnauthorized
			}
			opened = append(opened, host)
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
	post := func(body map[string]any) (int, map[string]any, string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		request, _ := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/setup/plex-access", bytes.NewReader(raw))
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

	if status, _, text := post(map[string]any{"cosmos_host": "media.example.test"}); status != http.StatusBadRequest {
		t.Fatalf("missing token: %d %s", status, text)
	}
	if status, _, text := post(map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": "wrong"}); status != http.StatusBadRequest || !strings.Contains(text, "cosmos_unauthorized") {
		t.Fatalf("wrong token: %d %s", status, text)
	}
	status, plan, text := post(map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey})
	if status != http.StatusOK || plan["plex_container"] != "plex" || plan["applied"] != false || plan["restart"] != true || !strings.Contains(text, `"kind":"add_folder","purpose":"altmount","path":"/mnt/altmount"`) || fake.updated != nil {
		t.Fatalf("preview: %d %s", status, text)
	}
	status, plan, text = post(map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey, "apply": true})
	if status != http.StatusOK || plan["applied"] != true {
		t.Fatalf("apply: %d %s", status, text)
	}
	sent, _ := json.Marshal(fake.updated)
	if string(sent) != `[{"Source":"/srv/config/plex","Target":"/config","Type":"bind"},{"BindOptions":{"Propagation":"rslave"},"ReadOnly":true,"Source":"/srv/mm/usenet","Target":"/mnt","Type":"bind"}]` {
		t.Fatalf("mounts sent: %s", sent)
	}
	var detail string
	if err := store.Pool.QueryRow(ctx, `SELECT detail::text FROM audit_events WHERE action='plex.access_fixed'`).Scan(&detail); err != nil || !strings.Contains(detail, "add_folder altmount /mnt/altmount") || strings.Contains(detail, pastedKey) {
		t.Fatalf("audit %q %v", detail, err)
	}
	var stored int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE detail::text LIKE '%'||$1||'%'`, pastedKey).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("the token was stored: %d %v", stored, err)
	}
	if len(opened) != 2 || opened[0] != "media.example.test" {
		t.Fatalf("opened = %v", opened)
	}

	// Two Plex containers and an address that names neither: choose.
	fake.list = append(fake.list, cosmos.ContainerSummary{ID: "ccc", Names: []string{"/plex-test"}, Image: "plexinc/pms-docker"})
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$1 WHERE kind='plex'`, `{"base_url":"https://plex.example.test:32400"}`); err != nil {
		t.Fatal(err)
	}
	status, plan, text = post(map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey})
	if status != http.StatusOK || !strings.Contains(text, `"problem":"choose_plex"`) || !strings.Contains(text, `"candidates":["plex","plex-test"]`) {
		t.Fatalf("choose: %d %s", status, text)
	}
	status, plan, text = post(map[string]any{"cosmos_host": "media.example.test", "cosmos_api_key": pastedKey, "plex_container": "plex-test"})
	if status != http.StatusOK || plan["plex_container"] != "plex-test" {
		t.Fatalf("chosen: %d %s", status, text)
	}
}
