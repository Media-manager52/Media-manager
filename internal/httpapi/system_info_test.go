package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

// TestSystemInfoReportsBuildIdentity pins the build/runtime identity contract
// the acceptance controller depends on: GET /api/v1/system/info must report the
// compiled-in version and commit alongside the authoritative PostgreSQL schema
// version, without dropping any pre-existing fields.
func TestSystemInfoReportsBuildIdentity(t *testing.T) {
	const (
		version = "0.1.0"
		commit  = "a66c9d474493a7186667127431a989a89c82f9ca-dirty-test-identity"
	)
	store := &postgres.Store{}
	if err := store.ConfigureAcquisitionLimits(postgres.AcquisitionLimits{AttemptsPerRound: 4, RetainedFailedOutputs: 8}); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: version, Commit: commit}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/system/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("system info status=%d, want %d", response.StatusCode, http.StatusOK)
	}

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if got := envelope.Data["version"]; got != version {
		t.Fatalf("version=%v, want %q", got, version)
	}
	if got := envelope.Data["commit"]; got != commit {
		t.Fatalf("commit=%v, want %q", got, commit)
	}
	if got := envelope.Data["schema_version"]; got != float64(postgres.CurrentSchemaVersion) {
		t.Fatalf("schema_version=%v, want %d", got, postgres.CurrentSchemaVersion)
	}
	limits, ok := envelope.Data["acquisition_limits"].(map[string]any)
	if !ok || limits["attempts_per_round"] != float64(4) || limits["retained_failed_outputs"] != float64(8) {
		t.Fatalf("acquisition limits: %v", limits)
	}
	// The additive change must preserve every existing system-info field.
	for _, field := range []string{"owner_only", "browser_auth", "api_auth", "seerr_webhook_enabled", "metadata_providers", "altmount_source_policy", "publication_type", "upgrade_replacement_gate", "recycle_retention_days", "public_edge_owner", "machine_api_keys", "api_prefix", "started_at", "checked_at"} {
		if _, ok := envelope.Data[field]; !ok {
			t.Fatalf("system info dropped existing field %q", field)
		}
	}
}
