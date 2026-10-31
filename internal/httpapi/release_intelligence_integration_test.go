package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// riDo sends one request to the test server, with or without the owner token.
func riDo(t *testing.T, method, target, body string, authorized bool) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorized {
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

func riErrorCode(t *testing.T, payload []byte) string {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("error body %q: %v", payload, err)
	}
	return envelope.Error.Code
}

func riProfile(t *testing.T, payload []byte) postgres.QualityProfile {
	t.Helper()
	var envelope struct {
		Data postgres.QualityProfile `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("profile body %q: %v", payload, err)
	}
	return envelope.Data
}

func riPolicyDocument(t *testing.T, policy json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(policy, &document); err != nil {
		t.Fatalf("policy %s: %v", policy, err)
	}
	return document
}

func riRules(t *testing.T, policy json.RawMessage) []releases.Rule {
	t.Helper()
	rules, err := releases.ParsePolicyRules(policy)
	if err != nil {
		t.Fatalf("policy rules %s: %v", policy, err)
	}
	return rules
}

func TestQualityProfilePresetAPIPreservesHardRulesAndGuardsVersion(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	base := testServer.URL

	// The preset catalogue is soft-only over the wire as well as in the package.
	status, payload := riDo(t, http.MethodGet, base+"/api/v1/settings/quality-profile-presets", "", true)
	if status != http.StatusOK {
		t.Fatalf("list presets status=%d body=%s", status, payload)
	}
	var presets struct {
		Data []releases.PresetDefinition `json:"data"`
	}
	if err := json.Unmarshal(payload, &presets); err != nil {
		t.Fatal(err)
	}
	if len(presets.Data) != 4 {
		t.Fatalf("presets=%d, want 4", len(presets.Data))
	}
	for _, preset := range presets.Data {
		for _, rule := range preset.Rules {
			if rule.Kind != releases.RulePreferred {
				t.Fatalf("preset %s exposes a non-soft rule %+v", preset.Preset, rule)
			}
		}
	}
	if status, _ := riDo(t, http.MethodGet, base+"/api/v1/settings/quality-profile-presets", "", false); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated preset list status=%d, want 401", status)
	}

	seed := postgres.QualityProfile{
		MediaKind: catalog.KindMovie,
		Name:      "Preset API profile",
		Policy: json.RawMessage(`{
			"rules": [
				{"name":"Required resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","weight":0,"unknown":"hold"},
				{"name":"Preferred source","fact":"source","allowed_values":["webdl"],"kind":"preferred","weight":100,"unknown":"allow"},
				{"name":"Preferred HDR","fact":"dynamic_range","allowed_values":["hdr"],"kind":"preferred","weight":50,"unknown":"allow"}
			],
			"required_subtitle_languages": ["da"],
			"preferred_subtitle_languages": []
		}`),
		UpgradesEnabled: true,
		Default:         false,
		Enabled:         true,
	}
	body, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	status, payload = riDo(t, http.MethodPost, base+"/api/v1/settings/quality-profiles", string(body), true)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create profile status=%d body=%s", status, payload)
	}
	created := riProfile(t, payload)
	presetURL := base + "/api/v1/settings/quality-profiles/" + created.ID.String() + "/preset"
	call := func(version uint64, preset string) (int, []byte) {
		t.Helper()
		request, err := json.Marshal(map[string]any{"preset": preset, "policy_version": version})
		if err != nil {
			t.Fatal(err)
		}
		return riDo(t, http.MethodPost, presetURL, string(request), true)
	}

	t.Run("requires the owner token", func(t *testing.T) {
		if status, _ := riDo(t, http.MethodPost, presetURL, `{"preset":"balanced","policy_version":1}`, false); status != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", status)
		}
	})
	t.Run("rejects malformed input without writing", func(t *testing.T) {
		for name, request := range map[string]string{
			"missing version": `{"preset":"balanced"}`,
			"not json":        `{`,
		} {
			status, payload := riDo(t, http.MethodPost, presetURL, request, true)
			if status != http.StatusBadRequest || riErrorCode(t, payload) != "invalid_request" {
				t.Fatalf("%s: status=%d body=%s, want 400 invalid_request", name, status, payload)
			}
		}
		if status, payload := riDo(t, http.MethodPost, base+"/api/v1/settings/quality-profiles/not-a-uuid/preset", `{"preset":"balanced","policy_version":1}`, true); status != http.StatusBadRequest || riErrorCode(t, payload) != "invalid_id" {
			t.Fatalf("invalid id: status=%d body=%s", status, payload)
		}
		if status, payload := riDo(t, http.MethodPost, base+"/api/v1/settings/quality-profiles/00000000-0000-4000-8000-000000000000/preset", `{"preset":"balanced","policy_version":1}`, true); status != http.StatusNotFound {
			t.Fatalf("unknown profile: status=%d body=%s, want 404", status, payload)
		}
	})
	t.Run("rejects an unknown preset", func(t *testing.T) {
		status, payload := call(created.PolicyVersion, "bogus")
		if status != http.StatusUnprocessableEntity || riErrorCode(t, payload) != "invalid_quality_preset" {
			t.Fatalf("status=%d body=%s, want 422 invalid_quality_preset", status, payload)
		}
	})

	var applied postgres.QualityProfile
	t.Run("applies only soft rules and bumps the version", func(t *testing.T) {
		status, payload := call(created.PolicyVersion, "storage_efficient")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, payload)
		}
		applied = riProfile(t, payload)
		if applied.PolicyVersion != created.PolicyVersion+1 || applied.Name != created.Name || applied.Default != created.Default || applied.Enabled != created.Enabled || applied.UpgradesEnabled != created.UpgradesEnabled {
			t.Fatalf("profile metadata changed: before=%+v after=%+v", created, applied)
		}
		var envelope struct {
			Meta map[string]string `json:"meta"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Meta["workflow_policy"] == "" {
			t.Fatalf("response must state that existing rounds keep their policy: meta=%v err=%v", envelope.Meta, err)
		}

		// Everything outside "rules" (language requirements, routing) is preserved.
		before, after := riPolicyDocument(t, created.Policy), riPolicyDocument(t, applied.Policy)
		for key, value := range before {
			if key == "rules" {
				continue
			}
			if !bytes.Equal(compactJSON(t, value), compactJSON(t, after[key])) {
				t.Fatalf("policy key %q changed: before=%s after=%s", key, value, after[key])
			}
		}

		rules := riRules(t, applied.Policy)
		var requiredResolution, hdr, oldSource, presetResolution bool
		for _, rule := range rules {
			switch {
			case rule.Name == "Required resolution" && rule.Kind == releases.RuleRequired:
				requiredResolution = true
			case rule.Name == "Preferred HDR" && rule.Kind == releases.RulePreferred:
				hdr = true // storage_efficient does not manage dynamic_range
			case rule.Name == "Preferred source":
				oldSource = true
			case strings.HasPrefix(rule.Name, "Storage efficient resolution"):
				presetResolution = true
			}
			if rule.Kind == releases.RuleRequired && rule.Name != "Required resolution" {
				t.Fatalf("a preset introduced a required rule: %+v", rule)
			}
		}
		if !requiredResolution || !hdr || oldSource || !presetResolution {
			t.Fatalf("required=%v hdr=%v oldSource=%v presetResolution=%v rules=%+v", requiredResolution, hdr, oldSource, presetResolution, rules)
		}
	})
	t.Run("a stale version is refused and does not change the policy", func(t *testing.T) {
		status, payload := call(created.PolicyVersion, "maximum_quality")
		if status != http.StatusConflict || riErrorCode(t, payload) != "quality_profile_changed" {
			t.Fatalf("status=%d body=%s, want 409 quality_profile_changed", status, payload)
		}
		status, payload = riDo(t, http.MethodGet, base+"/api/v1/settings/quality-profiles", "", true)
		if status != http.StatusOK {
			t.Fatalf("list profiles status=%d", status)
		}
		var listed struct {
			Data []postgres.QualityProfile `json:"data"`
		}
		if err := json.Unmarshal(payload, &listed); err != nil {
			t.Fatal(err)
		}
		for _, profile := range listed.Data {
			if profile.ID != created.ID {
				continue
			}
			if profile.PolicyVersion != applied.PolicyVersion || !reflect.DeepEqual(compactJSON(t, profile.Policy), compactJSON(t, applied.Policy)) {
				t.Fatalf("a refused stale write changed the profile: %+v", profile)
			}
			return
		}
		t.Fatal("profile disappeared")
	})
	t.Run("the current version can apply the next preset", func(t *testing.T) {
		status, payload := call(applied.PolicyVersion, "compatibility_first")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, payload)
		}
		if next := riProfile(t, payload); next.PolicyVersion != applied.PolicyVersion+1 {
			t.Fatalf("version=%d, want %d", next.PolicyVersion, applied.PolicyVersion+1)
		}
	})
}

func compactJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if len(raw) == 0 {
		return nil
	}
	if err := json.Compact(&buffer, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	// Re-marshal through a generic value so key order never matters.
	var value any
	if err := json.Unmarshal(buffer.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestWorkflowDryRunAPIIsAuthenticatedAndReadOnly(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	base := testServer.URL

	movie := createMediaThroughAPI(t, base, map[string]any{
		"kind":               "movie",
		"title":              "Dry Run API Movie",
		"year":               2024,
		"search_immediately": false,
		"identities": []map[string]any{
			{"namespace": "tmdb", "value": "810101", "state": "verified", "source": "test_metadata"},
			{"namespace": "imdb", "value": "tt0810101", "state": "verified", "source": "test_metadata"},
		},
		"metadata": map[string]any{
			"overview": "Dry run API.", "original_language": "da", "genres": []string{"Drama"}, "countries": []string{"DK"},
			"networks": []any{}, "aliases": []any{}, "sources": []any{}, "ratings": []any{}, "events": []any{}, "observed_at": "2026-09-01T00:00:00Z",
		},
	}, 1)
	workflowID := getMediaDetailThroughAPI(t, base, movie.ID).Workflows[0].ID
	dryRunURL := base + "/api/v1/workflows/" + workflowID.String() + "/candidates/dry-run"

	if status, _ := riDo(t, http.MethodGet, dryRunURL, "", false); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dry run status=%d, want 401", status)
	}
	if status, payload := riDo(t, http.MethodGet, base+"/api/v1/workflows/not-a-uuid/candidates/dry-run", "", true); status != http.StatusBadRequest || riErrorCode(t, payload) != "invalid_id" {
		t.Fatalf("invalid id status=%d body=%s", status, payload)
	}
	if status, payload := riDo(t, http.MethodGet, base+"/api/v1/workflows/00000000-0000-4000-8000-000000000000/candidates/dry-run", "", true); status != http.StatusNotFound || riErrorCode(t, payload) != "not_found" {
		t.Fatalf("unknown workflow status=%d body=%s", status, payload)
	}
	// The route is a read: no other verb is accepted on it.
	if status, _ := riDo(t, http.MethodPost, dryRunURL, `{}`, true); status != http.StatusMethodNotAllowed && status != http.StatusNotFound {
		t.Fatalf("POST on the dry-run route status=%d, want 405/404", status)
	}

	snapshot := func() string {
		t.Helper()
		var text string
		if err := store.Pool.QueryRow(context.Background(), `SELECT concat_ws('|',
			(SELECT revision FROM workflows WHERE id=$1),(SELECT state FROM workflows WHERE id=$1),
			(SELECT count(*) FROM search_runs),(SELECT count(*) FROM release_candidates),
			(SELECT count(*) FROM candidate_decisions),(SELECT count(*) FROM altmount_jobs),
			(SELECT count(*) FROM workflow_release_exclusions),(SELECT count(*) FROM workflow_attempted_releases))`, workflowID.String()).Scan(&text); err != nil {
			t.Fatal(err)
		}
		return text
	}
	before := snapshot()
	status, payload := riDo(t, http.MethodGet, dryRunURL, "", true)
	if status != http.StatusOK {
		t.Fatalf("dry run status=%d body=%s", status, payload)
	}
	if after := snapshot(); after != before {
		t.Fatalf("the dry run changed durable state:\nbefore=%s\nafter =%s", before, after)
	}

	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	// Empty collections must serialize as [] (never null) so clients can iterate.
	for _, key := range []string{"candidates", "ranked_candidate_ids", "limitations"} {
		if value := strings.TrimSpace(string(raw.Data[key])); value == "" || value == "null" {
			t.Fatalf("%s=%q, want an array", key, value)
		}
	}
	if string(raw.Data["candidates"]) != "[]" || string(raw.Data["ranked_candidate_ids"]) != "[]" {
		t.Fatalf("a workflow without stored candidates must replay none: %s %s", raw.Data["candidates"], raw.Data["ranked_candidate_ids"])
	}
	if _, present := raw.Data["best_eligible_candidate_id"]; present {
		t.Fatalf("best_eligible_candidate_id must be omitted when nothing is eligible: %s", raw.Data["best_eligible_candidate_id"])
	}
	var limitations []string
	if err := json.Unmarshal(raw.Data["limitations"], &limitations); err != nil || len(limitations) == 0 {
		t.Fatalf("a replay must state its limitations: %s err=%v", raw.Data["limitations"], err)
	}
	var source string
	if err := json.Unmarshal(raw.Data["policy_source"], &source); err != nil || source == "" {
		t.Fatalf("policy_source=%s err=%v", raw.Data["policy_source"], err)
	}
	if string(raw.Data["workflow_id"]) != `"`+workflowID.String()+`"` {
		t.Fatalf("workflow_id=%s, want %s", raw.Data["workflow_id"], workflowID)
	}
}

// schemaFingerprint hashes the full contents of every base table in the test
// schema. Two equal fingerprints prove that nothing between them inserted,
// updated or deleted a row anywhere, including tables this test never names
// (outbox, events, audit trails).
func schemaFingerprint(t *testing.T, store *postgres.Store) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := store.Pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(name) {
			t.Fatalf("unexpected table name %q", name)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) < 20 {
		t.Fatalf("only %d tables found; the fingerprint would be vacuous", len(tables))
	}
	result := make(map[string]string, len(tables))
	for _, name := range tables {
		var digest string
		if err := store.Pool.QueryRow(ctx, `SELECT count(*)::text||':'||COALESCE(md5(string_agg(x::text,'|' ORDER BY x::text)),'') FROM "`+name+`" x`).Scan(&digest); err != nil {
			t.Fatalf("fingerprint %s: %v", name, err)
		}
		result[name] = digest
	}
	return result
}

func diffFingerprints(before, after map[string]string) []string {
	var changed []string
	for name, digest := range before {
		if after[name] != digest {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

type storedDryRunCandidate struct {
	key, title, facts string
	observedAgo       time.Duration
	id                uuid.UUID
}

func riFacts(entries ...string) string {
	parts := make([]string, 0, len(entries)/2)
	for index := 0; index+1 < len(entries); index += 2 {
		parts = append(parts, `{"name":"`+entries[index]+`","values":["`+entries[index+1]+`"],"state":"observed","evidence_at":[],"observed_at":"2026-01-02T03:04:05Z"}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func seedStoredCandidate(t *testing.T, store *postgres.Store, workflowID, indexerID uuid.UUID, item *storedDryRunCandidate, now time.Time) {
	t.Helper()
	ctx := context.Background()
	runID, observationID := uuid.NewV7(), uuid.NewV7()
	item.id = uuid.NewV7()
	digest := sha256.Sum256([]byte(item.key))
	at := now.Add(-item.observedAgo)
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,$4,$4)`, runID.String(), workflowID.String(), digest[:], at); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'e2e',$3,$4,$5,'[]',$6,'e2e','[]',$7)`, observationID.String(), indexerID.String(), item.key, item.title, digest[:], item.facts, at); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at,last_observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, item.id.String(), workflowID.String(), runID.String(), observationID.String(), indexerID.String(), item.key, item.title, "native:"+item.key, at); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowDryRunAPIReplaysStoredCandidatesEndToEnd(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	base := testServer.URL
	ctx := context.Background()

	// A default movie profile with a known policy: one required gate and three
	// soft preferences, so every expected score below is derived, not guessed.
	profileBody, err := json.Marshal(postgres.QualityProfile{
		MediaKind: catalog.KindMovie,
		Name:      "Dry run E2E",
		Policy: json.RawMessage(`{"rules":[
			{"name":"Required resolution","fact":"resolution","allowed_values":["1080p","2160p"],"kind":"required","weight":0,"unknown":"hold"},
			{"name":"Prefer 2160p","fact":"resolution","allowed_values":["2160p"],"kind":"preferred","weight":100,"unknown":"allow"},
			{"name":"Prefer 1080p","fact":"resolution","allowed_values":["1080p"],"kind":"preferred","weight":50,"unknown":"allow"},
			{"name":"Prefer WEB-DL","fact":"source","allowed_values":["webdl"],"kind":"preferred","weight":10,"unknown":"allow"}
		]}`),
		UpgradesEnabled: true,
		Default:         true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, payload := riDo(t, http.MethodPost, base+"/api/v1/settings/quality-profiles", string(profileBody), true)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create profile status=%d body=%s", status, payload)
	}
	profile := riProfile(t, payload)

	movie := createMediaThroughAPI(t, base, map[string]any{
		"kind": "movie", "title": "Dry Run API Movie", "year": 2024, "search_immediately": false,
		"identities": []map[string]any{
			{"namespace": "tmdb", "value": "810201", "state": "verified", "source": "test_metadata"},
			{"namespace": "imdb", "value": "tt0810201", "state": "verified", "source": "test_metadata"},
		},
		"metadata": map[string]any{
			"overview": "Dry run end to end.", "original_language": "en", "genres": []string{"Drama"}, "countries": []string{"DK"},
			"networks": []any{}, "aliases": []any{}, "sources": []any{}, "ratings": []any{}, "events": []any{}, "observed_at": "2026-09-01T00:00:00Z",
		},
	}, 1)
	workflowID := getMediaDetailThroughAPI(t, base, movie.ID).Workflows[0].ID
	var workflowProfile string
	if err := store.Pool.QueryRow(ctx, `SELECT quality_profile_id::text FROM workflows WHERE id=$1`, workflowID.String()).Scan(&workflowProfile); err != nil || workflowProfile != profile.ID.String() {
		t.Fatalf("workflow uses profile %q (err=%v), want the E2E profile %s", workflowProfile, err, profile.ID)
	}

	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Dry Run Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "Dry Run AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true,"local_read_only_root":"/mnt/altmount"}`)})
	if err != nil {
		t.Fatal(err)
	}

	// Observation order (newest first) is deliberately not the ranking order.
	now := time.Now().UTC().Truncate(time.Second)
	candidates := map[string]*storedDryRunCandidate{
		"rejected":   {key: "f-rejected", title: "Dry.Run.API.Movie.2024.720p.HDTV-REJECTED", facts: riFacts("resolution", "720p", "source", "hdtv"), observedAgo: 1 * time.Minute},
		"plain":      {key: "d-plain", title: "Dry.Run.API.Movie.2024.1080p.BluRay-PLAIN", facts: riFacts("resolution", "1080p", "source", "bluray"), observedAgo: 2 * time.Minute},
		"wrongtitle": {key: "h-wrongtitle", title: "Different.Movie.2024.2160p.BluRay-WRONGTITLE", facts: riFacts("resolution", "2160p", "source", "bluray"), observedAgo: 3 * time.Minute},
		"attempted":  {key: "a-attempted", title: "Dry.Run.API.Movie.2024.2160p.WEB-DL-ATTEMPTED", facts: riFacts("resolution", "2160p", "source", "webdl"), observedAgo: 4 * time.Minute},
		"excluded":   {key: "g-excluded", title: "Dry.Run.API.Movie.2024.2160p.WEB-DL-EXCLUDED", facts: riFacts("resolution", "2160p", "source", "webdl"), observedAgo: 5 * time.Minute},
		"held":       {key: "e-held", title: "Dry.Run.API.Movie.2024.HDTV-HELD", facts: riFacts("source", "hdtv"), observedAgo: 6 * time.Minute},
		"selected":   {key: "c-selected", title: "Dry.Run.API.Movie.2024.1080p.WEB-DL-SELECTED", facts: riFacts("resolution", "1080p", "source", "webdl"), observedAgo: 7 * time.Minute},
		"best":       {key: "b-best", title: "Dry.Run.API.Movie.2024.2160p.BluRay-BEST", facts: riFacts("resolution", "2160p", "source", "bluray"), observedAgo: 8 * time.Minute},
	}
	for _, item := range candidates {
		seedStoredCandidate(t, store, workflowID, indexer.ID, item, now)
	}

	// Attempted: a real AltMount job and its first-attempt marker.
	var roundID string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1 ORDER BY round_ordinal LIMIT 1`, workflowID.String()).Scan(&roundID); err != nil {
		t.Fatalf("a new workflow must have an acquisition round: %v", err)
	}
	jobID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot,acquisition_outcome) VALUES($1,$2,$3,$4,1,'attempted.nzb','needs_attention',now(),1,$5,1,'output_empty')`, jobID.String(), workflowID.String(), candidates["attempted"].id.String(), altMount.ID.String(), roundID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_attempted_releases(workflow_id,candidate_id,first_job_id,created_at) VALUES($1,$2,$3,now())`, workflowID.String(), candidates["attempted"].id.String(), jobID.String()); err != nil {
		t.Fatal(err)
	}
	// Excluded: the real failure path (the workflow must be on the selected candidate first).
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, workflowID.String(), candidates["excluded"].id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,1,'eligible',110,'[]','policy',now())`, workflowID.String(), candidates["excluded"].id.String()); err != nil {
		t.Fatal(err)
	}
	retrieval, err := store.LoadRetrievalContext(ctx, workflowID, candidates["excluded"].id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordReleaseRetrievalFailure(ctx, retrieval, "newznab_http_403", "access denied", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Selected: the owner-visible current selection is a different candidate.
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, workflowID.String(), candidates["selected"].id.String()); err != nil {
		t.Fatal(err)
	}

	dryRunURL := base + "/api/v1/workflows/" + workflowID.String() + "/candidates/dry-run"
	fetch := func() (acquisition.DryRunResult, []byte) {
		t.Helper()
		status, payload := riDo(t, http.MethodGet, dryRunURL, "", true)
		if status != http.StatusOK {
			t.Fatalf("dry run status=%d body=%s", status, payload)
		}
		var envelope struct {
			Data acquisition.DryRunResult `json:"data"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data, payload
	}

	// Self-check: the fingerprint must actually notice a write, or "no writes" proves nothing.
	probe := schemaFingerprint(t, store)
	if _, err := store.Pool.Exec(ctx, `UPDATE release_candidates SET observation_count=observation_count+1 WHERE id=$1`, candidates["plain"].id.String()); err != nil {
		t.Fatal(err)
	}
	if changed := diffFingerprints(probe, schemaFingerprint(t, store)); !reflect.DeepEqual(changed, []string{"release_candidates"}) {
		t.Fatalf("fingerprint self-check changed tables=%v, want exactly release_candidates", changed)
	}

	before := schemaFingerprint(t, store)
	result, firstPayload := fetch()
	second, _ := fetch()
	if changed := diffFingerprints(before, schemaFingerprint(t, store)); len(changed) != 0 {
		t.Fatalf("the dry run wrote to the database; changed tables: %v", changed)
	}

	// Identity of the replay.
	if result.WorkflowID != workflowID || result.PolicySource != "open_round" || result.ProfilePolicyVersion != profile.PolicyVersion || result.PolicyVersion != profile.PolicyVersion || result.Truncated || result.GeneratedAt.IsZero() {
		t.Fatalf("replay header=%+v profile version=%d", result, profile.PolicyVersion)
	}
	if len(result.Limitations) < 3 {
		t.Fatalf("limitations=%v, want the informational-only caveats", result.Limitations)
	}
	byID := map[uuid.UUID]acquisition.DryRunCandidate{}
	for _, candidate := range result.Candidates {
		byID[candidate.CandidateID] = candidate
	}
	if len(result.Candidates) != len(candidates) || len(result.RankedCandidateIDs) != len(candidates) {
		t.Fatalf("replayed=%d ranked=%d, want %d", len(result.Candidates), len(result.RankedCandidateIDs), len(candidates))
	}

	// Ranking: verdict group first (eligible, held, rejected), then score
	// descending. This is not the observation order the rows were stored in.
	wantRanking := []string{"attempted", "best", "selected", "plain", "held", "excluded", "wrongtitle", "rejected"}
	var gotRanking []string
	titleToKey := map[uuid.UUID]string{}
	for key, item := range candidates {
		titleToKey[item.id] = key
	}
	for _, id := range result.RankedCandidateIDs {
		gotRanking = append(gotRanking, titleToKey[id])
	}
	if !reflect.DeepEqual(gotRanking, wantRanking) {
		t.Fatalf("ranking=%v, want %v", gotRanking, wantRanking)
	}
	wantVerdicts := map[string]struct {
		verdict releases.Verdict
		score   int
	}{
		"attempted": {releases.VerdictEligible, 110}, "best": {releases.VerdictEligible, 100}, "selected": {releases.VerdictEligible, 60}, "plain": {releases.VerdictEligible, 50},
		"held": {releases.VerdictHeld, 0}, "excluded": {releases.VerdictRejected, 110}, "wrongtitle": {releases.VerdictRejected, 100}, "rejected": {releases.VerdictRejected, 0},
	}
	for key, want := range wantVerdicts {
		got := byID[candidates[key].id]
		if got.Evaluation.Verdict != want.verdict || got.Evaluation.Score != want.score {
			t.Fatalf("%s: verdict=%s score=%d, want %s %d (reasons=%+v)", key, got.Evaluation.Verdict, got.Evaluation.Score, want.verdict, want.score, got.Evaluation.Explanations)
		}
		if got.Explanation.Verdict != got.Evaluation.Verdict || got.Explanation.Score != got.Evaluation.Score || got.Explanation.EvidenceScope != releases.DryRunReplayEvidenceScope {
			t.Fatalf("%s: explanation=%+v does not mirror the evaluation or declare replay provenance", key, got.Explanation)
		}
		if got.Title != candidates[key].title {
			t.Fatalf("%s: title=%q", key, got.Title)
		}
	}

	// Gates come from stored state, and a gated release is never proposed.
	for key, want := range map[string][3]bool{
		"attempted": {true, false, false}, "excluded": {false, true, false}, "selected": {false, false, true},
		"best": {}, "plain": {}, "held": {}, "wrongtitle": {}, "rejected": {},
	} {
		got := byID[candidates[key].id]
		if got.Attempted != want[0] || got.Excluded != want[1] || got.Selected != want[2] {
			t.Fatalf("%s gates attempted=%v excluded=%v selected=%v, want %v", key, got.Attempted, got.Excluded, got.Selected, want)
		}
	}
	if result.BestEligibleCandidateID == nil || *result.BestEligibleCandidateID != candidates["best"].id {
		t.Fatalf("best eligible=%v, want %s: the higher-scoring attempted and selected releases must be skipped", result.BestEligibleCandidateID, candidates["best"].id)
	}

	// Held and rejected releases explain themselves from the stored facts.
	reasonFor := func(key, rule string) releases.Explanation {
		t.Helper()
		for _, reason := range byID[candidates[key].id].Explanation.Reasons {
			if reason.Rule == rule {
				return reason
			}
		}
		t.Fatalf("%s has no %q reason: %+v", key, rule, byID[candidates[key].id].Explanation.Reasons)
		return releases.Explanation{}
	}
	held := reasonFor("held", "Required resolution")
	if held.Decision != releases.DecisionUnknown || held.RuleKind != releases.RuleRequired || held.Category != releases.CategoryResolutionCodec || held.EvidenceState != releases.FactUnknown {
		t.Fatalf("held reason=%+v: missing evidence must stay unknown and hold the release", held)
	}
	rejected := reasonFor("rejected", "Required resolution")
	if rejected.Decision != releases.DecisionFail || rejected.RuleKind != releases.RuleRequired || len(rejected.Values) != 1 || rejected.Values[0] != "720p" {
		t.Fatalf("rejected reason=%+v", rejected)
	}
	wrongTitle := reasonFor("wrongtitle", "requested title")
	if wrongTitle.Decision != releases.DecisionFail || wrongTitle.Category != releases.CategoryIdentity {
		t.Fatalf("wrong-title reason=%+v", wrongTitle)
	}
	exclusion := reasonFor("excluded", "workflow release verification")
	if exclusion.Decision != releases.DecisionFail || exclusion.Category != releases.CategoryExclusion {
		t.Fatalf("exclusion reason=%+v", exclusion)
	}
	if soft := reasonFor("best", "Prefer 2160p"); soft.Score != 100 || soft.RuleKind != releases.RulePreferred {
		t.Fatalf("soft reason=%+v", soft)
	}

	// Deterministic: a second replay of unchanged data ranks identically.
	if !reflect.DeepEqual(second.RankedCandidateIDs, result.RankedCandidateIDs) || second.BestEligibleCandidateID == nil || *second.BestEligibleCandidateID != *result.BestEligibleCandidateID {
		t.Fatalf("a second replay differs: %v vs %v", second.RankedCandidateIDs, result.RankedCandidateIDs)
	}
	// No secret-bearing locator leaks through the replay payload.
	for _, forbidden := range []string{"native:", "download_locator", "SECRET", "INDEXER_KEY"} {
		if strings.Contains(string(firstPayload), forbidden) {
			t.Fatalf("dry-run payload exposes %q", forbidden)
		}
	}
}

// A corrupt stored fact set must not produce a partial or guessed replay.
func TestWorkflowDryRunAPIFailsClosedOnMalformedStoredFacts(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	base := testServer.URL
	ctx := context.Background()

	movie := createMediaThroughAPI(t, base, map[string]any{
		"kind": "movie", "title": "Dry Run Corrupt Movie", "year": 2024, "search_immediately": false,
		"identities": []map[string]any{{"namespace": "tmdb", "value": "810301", "state": "verified", "source": "test_metadata"}},
		"metadata": map[string]any{
			"overview": "Corrupt facts.", "original_language": "en", "genres": []string{"Drama"}, "countries": []string{"DK"},
			"networks": []any{}, "aliases": []any{}, "sources": []any{}, "ratings": []any{}, "events": []any{}, "observed_at": "2026-09-01T00:00:00Z",
		},
	}, 1)
	workflowID := getMediaDetailThroughAPI(t, base, movie.ID).Workflows[0].ID
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Corrupt Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	good := &storedDryRunCandidate{key: "good", title: "Dry.Run.Corrupt.Movie.2024.1080p.WEB-DL-GOOD", facts: riFacts("resolution", "1080p"), observedAgo: time.Minute}
	corrupt := &storedDryRunCandidate{key: "corrupt", title: "Dry.Run.Corrupt.Movie.2024.2160p.WEB-DL-CORRUPT", facts: `{"resolution":"2160p"}`, observedAgo: 2 * time.Minute}
	seedStoredCandidate(t, store, workflowID, indexer.ID, good, now)
	seedStoredCandidate(t, store, workflowID, indexer.ID, corrupt, now)

	before := schemaFingerprint(t, store)
	status, payload := riDo(t, http.MethodGet, base+"/api/v1/workflows/"+workflowID.String()+"/candidates/dry-run", "", true)
	if status != http.StatusInternalServerError || riErrorCode(t, payload) != "dry_run_failed" {
		t.Fatalf("status=%d body=%s, want 500 dry_run_failed", status, payload)
	}
	for _, leaked := range []string{corrupt.id.String(), "2160p", "invalid stored facts", "json:"} {
		if strings.Contains(string(payload), leaked) {
			t.Fatalf("the error body leaks internal detail %q: %s", leaked, payload)
		}
	}
	if changed := diffFingerprints(before, schemaFingerprint(t, store)); len(changed) != 0 {
		t.Fatalf("a failed dry run wrote to the database; changed tables: %v", changed)
	}

	// The tolerant owner read model still lists both candidates, so the owner can
	// find the corrupt one even though the replay refuses to guess.
	status, payload = riDo(t, http.MethodGet, base+"/api/v1/workflows/"+workflowID.String()+"/candidates", "", true)
	if status != http.StatusOK || !strings.Contains(string(payload), "CORRUPT") || !strings.Contains(string(payload), "GOOD") {
		t.Fatalf("candidates list status=%d body=%s", status, payload)
	}
}
