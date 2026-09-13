package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	draftCalls     atomic.Int32
	draftReadCalls atomic.Int32
	executeResult  commissioningv2.LiveExecuteResult
	executeErr     error
	draftErr       error
	draftReadErr   error
	activateCalls  atomic.Int32
	activateErr    error
	activateDraft  postgres.IndexerCapabilityProfile
	run            postgres.IndexerCommissioningRun
	draft          postgres.IndexerCapabilityProfile
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

func (service *countingCommissioningV2) SealSearchPlan(context.Context, uuid.UUID, uuid.UUID, uint64, []int, []int, bool, bool) (postgres.IndexerCommissioningRun, error) {
	service.authorizeCalls.Add(1)
	return service.run, nil
}

func (service *countingCommissioningV2) AuthorizeSearches(context.Context, uuid.UUID, uuid.UUID, uint64) (postgres.IndexerCommissioningRun, error) {
	service.authorizeCalls.Add(1)
	return service.run, nil
}

func (service *countingCommissioningV2) ExecuteStep(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (commissioningv2.LiveExecuteResult, error) {
	service.executeCalls.Add(1)
	if service.executeErr != nil {
		return commissioningv2.LiveExecuteResult{}, service.executeErr
	}
	return service.executeResult, nil
}

func (service *countingCommissioningV2) FinalizeDraft(context.Context, uuid.UUID, uuid.UUID, uint64) (postgres.IndexerCommissioningRun, error) {
	service.draftCalls.Add(1)
	if service.draftErr != nil {
		return postgres.IndexerCommissioningRun{}, service.draftErr
	}
	return service.run, nil
}

func (service *countingCommissioningV2) Draft(context.Context, uuid.UUID, uuid.UUID) (postgres.IndexerCapabilityProfile, error) {
	service.draftReadCalls.Add(1)
	if service.draftReadErr != nil {
		return postgres.IndexerCapabilityProfile{}, service.draftReadErr
	}
	return service.draft, nil
}

func (service *countingCommissioningV2) ActivateProfile(context.Context, uuid.UUID, uuid.UUID, uint64) (postgres.IndexerCapabilityProfile, error) {
	service.activateCalls.Add(1)
	if service.activateErr != nil {
		return postgres.IndexerCapabilityProfile{}, service.activateErr
	}
	return service.activateDraft, nil
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

func TestCommissioningV2OwnerAPISearchRoutes(t *testing.T) {
	service := &countingCommissioningV2{}
	testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
	base := testServer.URL + "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/22222222-2222-4222-8222-222222222222"
	for _, testCase := range []struct{ path, body string }{
		{"/categories", `{"expected_revision":1,"movie_category_ids":[2000],"series_category_ids":[5000],"include_movie_fallback":true,"include_complete_season_fallback":true}`},
		{"/authorize-searches", `{"expected_revision":1}`},
	} {
		request, err := http.NewRequest(http.MethodPost, base+testCase.path, strings.NewReader(testCase.body))
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
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d, want 200", testCase.path, response.StatusCode)
		}
	}
	if service.authorizeCalls.Load() != 2 {
		t.Fatalf("search route service calls=%d, want 2", service.authorizeCalls.Load())
	}
}

func TestCommissioningV2OwnerAPIDraftRoutes(t *testing.T) {
	runID := uuid.NewV7()
	profileID := uuid.NewV7()
	run := postgres.IndexerCommissioningRun{ID: runID, GeneratedProfileID: &profileID, State: "completed"}
	service := &countingCommissioningV2{run: run}
	testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
	base := testServer.URL + "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/" + runID.String() + "/draft"

	request, err := http.NewRequest(http.MethodPost, base, strings.NewReader(`{"expected_revision":1}`))
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
	if response.StatusCode != http.StatusOK || service.draftCalls.Load() != 1 {
		t.Fatalf("finalize status=%d calls=%d", response.StatusCode, service.draftCalls.Load())
	}

	readRequest, err := http.NewRequest(http.MethodGet, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	readRequest.Header.Set("Authorization", "Bearer "+testOwnerToken)
	readResponse, err := http.DefaultClient.Do(readRequest)
	if err != nil {
		t.Fatal(err)
	}
	readResponse.Body.Close()
	if readResponse.StatusCode != http.StatusOK || service.draftReadCalls.Load() != 1 {
		t.Fatalf("read status=%d calls=%d", readResponse.StatusCode, service.draftReadCalls.Load())
	}

	unauthorized, err := http.NewRequest(http.MethodGet, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResponse, err := http.DefaultClient.Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized read status=%d", unauthorizedResponse.StatusCode)
	}

	unavailable := &countingCommissioningV2{draftErr: postgres.ErrIndexerCommissioningDraftUnavailable}
	unavailableServer := commissioningV2TestServer(t, unavailable, commissionV2TestOrigin)
	unavailableRequest, err := http.NewRequest(http.MethodPost, unavailableServer.URL+"/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/"+runID.String()+"/draft", strings.NewReader(`{"expected_revision":1}`))
	if err != nil {
		t.Fatal(err)
	}
	unavailableRequest.Header.Set("Authorization", "Bearer "+testOwnerToken)
	unavailableRequest.Header.Set("Origin", commissionV2TestOrigin)
	unavailableRequest.Header.Set("Content-Type", "application/json")
	unavailableResponse, err := http.DefaultClient.Do(unavailableRequest)
	if err != nil {
		t.Fatal(err)
	}
	unavailableResponse.Body.Close()
	if unavailableResponse.StatusCode != http.StatusConflict {
		t.Fatalf("unavailable status=%d", unavailableResponse.StatusCode)
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

func activationTestProfile() postgres.IndexerCapabilityProfile {
	activatedAt := time.Unix(1_700_000_000, 0).UTC()
	return postgres.IndexerCapabilityProfile{
		ID:                uuid.NewV7(),
		IndexerInstanceID: uuid.NewV7(),
		Revision:          7,
		SchemaVersion:     1,
		LifecycleState:    "active",
		ActivatedAt:       &activatedAt,
	}
}

func TestCommissioningV2OwnerAPIActivationGatesMutations(t *testing.T) {
	service := &countingCommissioningV2{activateDraft: activationTestProfile()}
	testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
	runID := uuid.NewV7()
	base := testServer.URL + "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/" + runID.String() + "/activate"

	cases := []struct {
		name        string
		authorize   bool
		origin      string
		contentType string
		setSite     string
		body        string
		wantStatus  int
	}{
		{name: "unauthorized", origin: commissionV2TestOrigin, contentType: "application/json", body: `{"expected_revision":7}`, wantStatus: http.StatusUnauthorized},
		{name: "missing origin", authorize: true, contentType: "application/json", body: `{"expected_revision":7}`, wantStatus: http.StatusForbidden},
		{name: "wrong origin", authorize: true, origin: "https://evil.test", contentType: "application/json", body: `{"expected_revision":7}`, wantStatus: http.StatusForbidden},
		{name: "cross-site fetch metadata", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", setSite: "cross-site", body: `{"expected_revision":7}`, wantStatus: http.StatusForbidden},
		{name: "wrong media type", authorize: true, origin: commissionV2TestOrigin, contentType: "text/plain", body: `{"expected_revision":7}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing revision", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "zero revision", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", body: `{"expected_revision":0}`, wantStatus: http.StatusBadRequest},
		{name: "unknown field", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", body: `{"expected_revision":7,"unexpected":true}`, wantStatus: http.StatusBadRequest},
		{name: "allowed", authorize: true, origin: commissionV2TestOrigin, contentType: "application/json", body: `{"expected_revision":7}`, wantStatus: http.StatusOK},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before := service.activateCalls.Load()
			request, err := http.NewRequest(http.MethodPost, base, strings.NewReader(testCase.body))
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
			called := service.activateCalls.Load() > before
			if testCase.wantStatus == http.StatusOK {
				if !called {
					t.Fatal("allowed activation did not reach the service")
				}
				if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-store" {
					t.Fatalf("activation response Cache-Control=%q, want no-store", cacheControl)
				}
			} else if called {
				t.Fatal("rejected activation reached the service")
			}
		})
	}
}

func TestCommissioningV2OwnerAPIActivationClosedErrors(t *testing.T) {
	runID := uuid.NewV7()
	base := "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/" + runID.String() + "/activate"
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: postgres.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "category review required", err: postgres.ErrIndexerCategoryReviewRequired, wantStatus: http.StatusConflict, wantCode: "category_review_required"},
		{name: "endpoint changed", err: postgres.ErrIndexerEndpointConfigurationChanged, wantStatus: http.StatusConflict, wantCode: "configuration_changed"},
		{name: "contract mismatch", err: postgres.ErrIndexerProviderContractMismatch, wantStatus: http.StatusConflict, wantCode: "contract_mismatch"},
		{name: "concurrent modification", err: postgres.ErrConcurrentModification, wantStatus: http.StatusConflict, wantCode: "state_conflict"},
		{name: "invalid profile", err: postgres.ErrInvalidIndexerCapabilityProfile, wantStatus: http.StatusUnprocessableEntity, wantCode: "invalid_profile"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &countingCommissioningV2{activateErr: testCase.err}
			testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
			request, err := http.NewRequest(http.MethodPost, testServer.URL+base, strings.NewReader(`{"expected_revision":7}`))
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
			defer response.Body.Close()
			if response.StatusCode != testCase.wantStatus {
				t.Fatalf("status=%d, want %d", response.StatusCode, testCase.wantStatus)
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != testCase.wantCode {
				t.Fatalf("error code=%q, want %q", envelope.Error.Code, testCase.wantCode)
			}
			if service.activateCalls.Load() != 1 {
				t.Fatalf("activate calls=%d, want 1", service.activateCalls.Load())
			}
		})
	}
}

func TestCommissioningV2OwnerAPIActivationIsIdempotent(t *testing.T) {
	profile := activationTestProfile()
	service := &countingCommissioningV2{activateDraft: profile}
	testServer := commissioningV2TestServer(t, service, commissionV2TestOrigin)
	runID := uuid.NewV7()
	url := testServer.URL + "/api/v1/settings/integrations/11111111-1111-4111-8111-111111111111/commissioning-v2/runs/" + runID.String() + "/activate"

	for attempt := 0; attempt < 2; attempt++ {
		request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"expected_revision":7}`))
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
		var envelope struct {
			Data postgres.IndexerCapabilityProfile `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d status=%d", attempt, response.StatusCode)
		}
		if envelope.Data.Revision != profile.Revision || envelope.Data.LifecycleState != "active" {
			t.Fatalf("attempt %d returned profile=%#v", attempt, envelope.Data)
		}
	}
	if service.activateCalls.Load() != 2 {
		t.Fatalf("activate calls=%d, want 2 idempotent requests", service.activateCalls.Load())
	}
}
