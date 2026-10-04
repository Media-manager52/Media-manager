package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestRetainedMovieRouteOwnerAndInputGuards(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner    bool
		id, body string
		status   int
	}{
		{false, "11111111-1111-4111-8111-111111111111", `{}`, http.StatusUnauthorized},
		{true, "invalid", `{}`, http.StatusBadRequest},
		{true, "11111111-1111-4111-8111-111111111111", `{"job_id":"invalid"}`, http.StatusBadRequest},
		{true, "11111111-1111-4111-8111-111111111111", `{"job_id":"11111111-1111-4111-8111-111111111111","path":"/outside"}`, http.StatusBadRequest},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+tc.id+"/reinspect-retained-movie", strings.NewReader(tc.body))
		if tc.owner {
			r.Header.Set("Authorization", "Bearer "+testOwnerToken)
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d want %d body %s", w.Code, tc.status, w.Body.String())
		}
	}
}
