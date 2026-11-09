package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestErrorLogAPIRedactsCredentialsAndRequiresOwner(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	if _, err := store.Pool.Exec(context.Background(), `INSERT INTO backup_records(id,state,format,last_error,created_at) VALUES ('11111111-1111-4111-8111-111111111190','failed','postgres_custom_aes256gcm_v1','GET https://user:hunter2@example.test/nzb?apikey=hunter2 failed',$1)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	get := func(path, token string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}

	if status, _ := get("/api/v1/errors", ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want %d", status, http.StatusUnauthorized)
	}
	status, body := get("/api/v1/errors?area=backup", testOwnerToken)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if strings.Contains(string(body), "hunter2") {
		t.Fatalf("credential reached the page: %s", body)
	}
	var envelope struct {
		Data postgres.ErrorLog `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Entries) != 1 || envelope.Data.Entries[0].Area != "backup" {
		t.Fatalf("entries=%+v", envelope.Data.Entries)
	}
	for path, code := range map[string]string{
		"/api/v1/errors?area=nope":                                    "invalid_area",
		"/api/v1/errors?limit=1001":                                   "invalid_limit",
		"/api/v1/errors?q=" + strings.Repeat("a", errorLogMaxQuery+1): "invalid_query",
	} {
		status, body := get(path, testOwnerToken)
		if status != http.StatusUnprocessableEntity || !strings.Contains(string(body), code) {
			t.Fatalf("%s status=%d body=%s", path, status, body)
		}
	}
}
