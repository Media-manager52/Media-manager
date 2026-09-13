package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

const commissionV2TestOrigin = "https://owner.media-manager.test"

type countingCommissioningV2 struct {
	createCalls    atomic.Int32
	runCalls       atomic.Int32
	authorizeCalls atomic.Int32
	executeCalls   atomic.Int32
	executeResult  commissioningv2.LiveExecuteResult
	executeErr     error
	run            postgres.IndexerCommissioningRun
}

func (service *countingCommissioningV2) CreateRun(context.Context, uuid.UUID, commissioningv2.FixtureDocument) (postgres.IndexerCommissioningRun, error) {
	service.createCalls.Add(1)
	return service.run, nil
}

func (service *countingCommissioningV2) Run(context.Context, uuid.UUID) (postgres.IndexerCommissioningRun, error) {
	service.runCalls.Add(1)
	return service.run, nil
}

func (service *countingCommissioningV2) AuthorizeCAPS(context.Context, uuid.UUID, uuid.UUID, uint64) (postgres.IndexerCommissioningRun, error) {
	service.authorizeCalls.Add(1)
	return service.run, nil
}

func (service *countingCommissioningV2) ExecuteCAPS(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (commissioningv2.LiveExecuteResult, error) {
	service.executeCalls.Add(1)
	if service.executeErr != nil {
		return commissioningv2.LiveExecuteResult{}, service.executeErr
	}
	return service.executeResult, nil
}

func commissioningV2TestServer(t *testing.T, service CommissioningV2Service, trustedOrigin string) *httptest.Server {
	t.Helper()
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", CommissioningV2: service, CommissioningV2Origin: trustedOrigin}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func TestCommissioningV2OwnerAPIGatesMutations(t *testing.T) {
	service := &countingCommissioningV2{}
	testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
	base := testServer.URL + "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs"
	validBody := `{}`

	cases := []struct {
		name        string
		authorize   bool
		origin      string
		contentType string
		setSite     string
		wantStatus  int
	}{
		{name: "unauthorized", origin: commissionV2TestOrigin, contentType: "application/json", wantStatus: http.StatusUnauthorized},
		{name: "missing origin", authorize: true, contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "wrong origin", authorize: true, origin: "https://evil.test", contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "cross-site fetch metadata", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", setSite: "cross-site", wantStatus: http.StatusForbidden},
		{name: "wrong media type", authorize: true, origin: commissionV2TestOrigin, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		{name: "allowed", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", wantStatus: http.StatusCreated},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before := service.createCalls.Load()
			request, err := http.NewRequest(http.MethodPost, base, strings.NewReader(validBody))
			if err != nil {
				t.Fatal(err)
			}
			if testCase.authorize {
				request.Header.Set("Authorization", "Bearer "+testOwnerToken)
			}
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if testCase.contentType != "" {
				request.Header.Set("Content-Type", testCase.contentType)
			}
			if testCase.setSite != "" {
				request.Header.Set("Sec-Fetch-Site", testCase.setSite)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != testCase.wantStatus {
				t.Fatalf("status=%d, want %d", response.StatusCode, testCase.wantStatus)
			}
			called := service.createCalls.Load() > before
			if testCase.wantStatus == http.StatusCreated {
				if !called {
					t.Fatal("allowed mutation did not reach the service")
				}
			} else if called {
				t.Fatal("rejected mutation reached the service")
			}
		})
	}
}

func TestCommissioningV2OwnerAPIMissingOriginDisablesMutations(t *testing.T) {
	service := &countingCommissioningV2{}
	testServer := commissioningV2TestServer(t, service, "")
	request, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	request.Header.Set("Origin", commissionV2TestOrigin)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	if service.createCalls.Load() != 0 {
		t.Fatal("unconfigured mutation reached the service")
	}
}

func TestCommissioningV2OwnerAPIExecuteReturnsClosedErrors(t *testing.T) {
	runID := uuid.NewV7()
	stepID := uuid.NewV7()
	service := &countingCommissioningV2{executeErr: commissioningv2.ErrLiveExecutorNotAuthorized}
	testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
	url := testServer.URL + "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/" + runID.String() + "/steps/" + stepID.String() + "/execute"
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	request.Header.Set("Origin", commissionV2TestOrigin)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusConflict)
	}
	if service.executeCalls.Load() != 1 {
		t.Fatalf("execute calls=%d, want 1", service.executeCalls.Load())
	}
}
