package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestAccessRetryRouteRequiresOwnerAndExactSelection(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/workflows/11111111-1111-4111-8111-111111111111/retry-access-rejected-release"
	for _, tc := range []struct {
		name, path, body string
		owner            bool
		want             int
	}{
		{"unauthenticated", path, `{}`, false, http.StatusUnauthorized},
		{"invalid workflow", "/api/v1/workflows/invalid/retry-access-rejected-release", `{}`, true, http.StatusBadRequest},
		{"missing selection", path, `{}`, true, http.StatusBadRequest},
		{"missing revision", path, `{"candidate_id":"22222222-2222-4222-8222-222222222222"}`, true, http.StatusBadRequest},
		{"invalid candidate", path, `{"candidate_id":"invalid","revision":3}`, true, http.StatusBadRequest},
		{"unknown field", path, `{"candidate_id":"22222222-2222-4222-8222-222222222222","revision":3,"reset_everything":true}`, true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			if tc.owner {
				request.Header.Set("Authorization", "Bearer "+testOwnerToken)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

func TestAccessRetryRouteUnknownWorkflow(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken}, store)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/11111111-1111-4111-8111-111111111111/retry-access-rejected-release", strings.NewReader(`{"candidate_id":"22222222-2222-4222-8222-222222222222","revision":3}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
