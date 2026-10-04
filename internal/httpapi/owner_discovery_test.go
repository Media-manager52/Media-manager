package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestOwnerDiscoveryRouteRequiresOwner(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		owner  bool
		status int
	}{
		{"/api/v1/workflows/11111111-1111-4111-8111-111111111111/discover", false, http.StatusUnauthorized},
		{"/api/v1/workflows/invalid/discover", true, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, tc.path, nil)
		if tc.owner {
			request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
		}
	}
}

func TestOwnerDiscoveryUnknownWorkflow(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, openHTTPTestStore(t))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/11111111-1111-4111-8111-111111111111/discover", nil)
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
