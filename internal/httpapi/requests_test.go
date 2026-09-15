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

func TestSplitSeerrRequestIDOnlyAcceptsInstanceQualifiedKeys(t *testing.T) {
	t.Parallel()
	const instance = "01a05529-6fd8-7704-b0ec-a6ef3309acb7"
	tests := []struct {
		name         string
		value        string
		wantOK       bool
		wantInstance string
		wantRequest  string
	}{
		{name: "qualified", value: instance + ":1001", wantOK: true, wantInstance: instance, wantRequest: "1001"},
		{name: "bare request id", value: "1001"},
		{name: "empty", value: ""},
		{name: "empty instance", value: ":1001"},
		{name: "empty request", value: instance + ":"},
		{name: "non-uuid instance", value: "not-a-uuid:1001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance, request, ok := splitSeerrRequestID(test.value)
			if ok != test.wantOK || instance != test.wantInstance || request != test.wantRequest {
				t.Fatalf("splitSeerrRequestID(%q)=(%q,%q,%v), want (%q,%q,%v)", test.value, instance, request, ok, test.wantInstance, test.wantRequest, test.wantOK)
			}
		})
	}
}

func TestArchiveMediaRouteRequiresOwner(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	response, err := http.Post(testServer.URL+"/api/v1/media/01a05529-6fd8-7704-b0ec-a6ef3309acb7/archive", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("archive status=%d, want 401", response.StatusCode)
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
