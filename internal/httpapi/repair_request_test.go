package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

// TestReplacementRequestNeedsOwnerAndASafePath proves an owner replacement
// request is refused without the owner token and rejects a path that is not a
// plain relative path inside the AltMount mount before touching the database.
func TestReplacementRequestNeedsOwnerAndASafePath(t *testing.T) {
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32)}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	send := func(body string, owner bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/v1/repairs", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if owner {
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("o", 32))
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := send(`{"altmount_file_path":"media-manager/a/Movie.mkv"}`, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("without the owner token: %d", response.Code)
	}
	for _, path := range []string{``, `/`, `media-manager//Movie.mkv`, `media-manager\\Movie.mkv`, `c:/Movie.mkv`} {
		if response := send(`{"altmount_file_path":"`+path+`"}`, true); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("path %q: %d %s, want 422", path, response.Code, response.Body.String())
		}
	}
	if response := send(`{not json`, true); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: %d", response.Code)
	}
}
