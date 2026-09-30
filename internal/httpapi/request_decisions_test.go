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

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/postgres"
)

func TestRequestDecisionRoutesRequireOwnerAndSeerr(t *testing.T) {
	server, err := New(Config{OwnerToken: testOwnerToken}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	response, err := http.Post(testServer.URL+"/api/v1/plex/reconfirm-timed-out", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Plex re-check without owner status=%d, want 401", response.StatusCode)
	}
	for _, path := range []string{"/api/v1/requests/1001/approve", "/api/v1/requests/1001/decline"} {
		response, err := http.Post(testServer.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without owner status=%d, want 401", path, response.StatusCode)
		}
		request, _ := http.NewRequest(http.MethodPost, testServer.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s without Seerr status=%d, want 503", path, response.StatusCode)
		}
	}
}

// TestOwnerRequestDecisionReachesSeerrAndQueuesIntake proves a pending request
// is decided in Seerr, audited, and handed to normal intake; a request that is
// not pending, or a decision Seerr does not confirm, changes nothing locally.
func TestOwnerRequestDecisionReachesSeerrAndQueuesIntake(t *testing.T) {
	store := openHTTPTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	instance, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "seerr", Name: "Decision Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr.invalid:5055"}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(requestID, state string) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_request_snapshots(seerr_instance_id,request_id,state,identities,payload_sha256,revision,reconciled_at,title,media_kind) VALUES($1,$2,$3,'[]','\x00',1,now(),'Decision Movie','movie')`, instance.ID.String(), requestID, state); err != nil {
			t.Fatal(err)
		}
	}
	seed("1001", "1")
	seed("1002", "2")
	seed("1003", "1")

	var calls atomic.Int32
	seerrServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		switch request.URL.Path {
		case "/api/v1/request/1001/approve":
			_, _ = writer.Write([]byte(`{"id":1001,"status":2}`))
		case "/api/v1/request/1003/decline":
			// Seerr answers, but not with a declined status.
			_, _ = writer.Write([]byte(`{"id":1003,"status":1}`))
		default:
			t.Errorf("unexpected Seerr call %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(seerrServer.Close)
	client, err := seerr.New(seerr.Config{BaseURL: seerrServer.URL, APIKey: "secret", AllowHTTP: true}, seerrServer.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{OwnerToken: testOwnerToken, SeerrInstanceID: instance.ID.String(), MetadataProvider: client}, store)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(server.Handler())
	t.Cleanup(api.Close)
	post := func(path string) (int, string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, api.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body strings.Builder
		buffer := make([]byte, 4096)
		for {
			count, readErr := response.Body.Read(buffer)
			body.Write(buffer[:count])
			if readErr != nil {
				break
			}
		}
		return response.StatusCode, body.String()
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var value int
		if err := store.Pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	if status, body := post("/api/v1/requests/1001/approve"); status != http.StatusAccepted || !strings.Contains(body, `"approved"`) {
		t.Fatalf("approve status=%d body=%s", status, body)
	}
	if got := count(`SELECT count(*) FROM audit_events WHERE action='seerr.request_approved_by_owner' AND detail->>'request_id'='1001'`); got != 1 {
		t.Fatalf("approval audits=%d, want 1", got)
	}
	if got := count(`SELECT count(*) FROM seerr_deliveries delivery JOIN outbox_events event ON event.aggregate_id=delivery.id AND event.event_type='seerr.reconcile' WHERE delivery.request_id='1001' AND delivery.event_id LIKE 'owner-decision:%'`); got != 1 {
		t.Fatalf("queued intake reconciles=%d, want 1", got)
	}

	if status, _ := post("/api/v1/requests/1002/approve"); status != http.StatusConflict {
		t.Fatalf("already approved status=%d, want 409", status)
	}
	if status, _ := post("/api/v1/requests/9999/approve"); status != http.StatusNotFound {
		t.Fatalf("unknown request status=%d, want 404", status)
	}
	if status, _ := post("/api/v1/requests/1003/decline"); status != http.StatusBadGateway {
		t.Fatalf("unconfirmed decline status=%d, want 502", status)
	}
	if got := count(`SELECT count(*) FROM audit_events WHERE detail->>'request_id' IN ('1002','1003','9999')`); got != 0 {
		t.Fatalf("audits for refused decisions=%d, want 0", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("Seerr calls=%d, want 2 (not-pending and unknown requests never reach Seerr)", got)
	}
}
