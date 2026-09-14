package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestRequestsRoutesRequireOwner(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	for _, path := range []string{"/api/v1/requests", "/api/v1/requests/1001"} {
		response, err := http.Get(testServer.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", path, response.StatusCode)
		}
	}
}

func TestRequestDetailWithoutSeerrConfigurationIsNotFound(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/requests/1001", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unconfigured request detail status=%d, want 404", response.StatusCode)
	}
}
