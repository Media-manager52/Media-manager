package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// TestQualityProfileUpdateAPIRefusesStaleEditsWithoutLosingRequiredRules proves
// PATCH /settings/quality-profiles/{id} is optimistic: the body's policy_version
// is the version the editor read, a stale one is a 409, and a refused edit never
// removes or replaces the newer policy (in particular its required rules).
func TestQualityProfileUpdateAPIRefusesStaleEditsWithoutLosingRequiredRules(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	base := testServer.URL

	policy := func(rules ...releases.Rule) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{"rules": rules, "required_subtitle_languages": []string{"da"}, "preferred_subtitle_languages": []string{}})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	requiredResolution := releases.Rule{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}
	requiredAudio := releases.Rule{Name: "Required audio codec", Fact: "audio_codec", AllowedValues: []string{"eac3"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}

	seed, err := json.Marshal(postgres.QualityProfile{MediaKind: catalog.KindMovie, Name: "Concurrency API profile", Policy: policy(requiredResolution), UpgradesEnabled: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	status, payload := riDo(t, http.MethodPost, base+"/api/v1/settings/quality-profiles", string(seed), true)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create profile status=%d body=%s", status, payload)
	}
	created := riProfile(t, payload)
	url := base + "/api/v1/settings/quality-profiles/" + created.ID.String()

	patch := func(version any, name string, rules ...releases.Rule) (int, []byte) {
		t.Helper()
		body := map[string]any{"media_kind": created.MediaKind, "name": name, "policy": json.RawMessage(policy(rules...)), "upgrades_enabled": true, "is_default": false, "enabled": true}
		if version != nil {
			body["policy_version"] = version
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return riDo(t, http.MethodPatch, url, string(encoded), true)
	}
	current := func() postgres.QualityProfile {
		t.Helper()
		status, payload := riDo(t, http.MethodGet, base+"/api/v1/settings/quality-profiles", "", true)
		if status != http.StatusOK {
			t.Fatalf("list status=%d", status)
		}
		var listed struct {
			Data []postgres.QualityProfile `json:"data"`
		}
		if err := json.Unmarshal(payload, &listed); err != nil {
			t.Fatal(err)
		}
		for _, profile := range listed.Data {
			if profile.ID == created.ID {
				return profile
			}
		}
		t.Fatal("profile not listed")
		return postgres.QualityProfile{}
	}

	t.Run("requires the owner token", func(t *testing.T) {
		if status, _ := riDo(t, http.MethodPatch, url, `{"policy_version":1}`, false); status != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", status)
		}
	})

	t.Run("an edit without the version it replaces is refused without writing", func(t *testing.T) {
		before := current()
		for name, version := range map[string]any{"omitted": nil, "zero": 0, "not a number": "1"} {
			status, payload := patch(version, "Unversioned rename", requiredResolution, requiredAudio)
			if status != http.StatusBadRequest || riErrorCode(t, payload) != "invalid_request" {
				t.Fatalf("%s: status=%d body=%s, want 400 invalid_request", name, status, payload)
			}
		}
		if after := current(); !reflect.DeepEqual(after, before) {
			t.Fatalf("a refused edit changed the profile: %+v", after)
		}
	})

	t.Run("malformed and unknown targets keep their own errors", func(t *testing.T) {
		if status, payload := riDo(t, http.MethodPatch, base+"/api/v1/settings/quality-profiles/not-a-uuid", `{"policy_version":1}`, true); status != http.StatusBadRequest || riErrorCode(t, payload) != "invalid_id" {
			t.Fatalf("invalid id: status=%d body=%s", status, payload)
		}
		unknown := base + "/api/v1/settings/quality-profiles/00000000-0000-4000-8000-000000000000"
		body := `{"media_kind":"movie","name":"x","policy_version":1,"policy":{"rules":[{"name":"r","fact":"resolution","allowed_values":["1080p"],"kind":"required"}]},"enabled":true}`
		if status, payload := riDo(t, http.MethodPatch, unknown, body, true); status != http.StatusNotFound {
			t.Fatalf("unknown profile: status=%d body=%s, want 404", status, payload)
		}
	})

	// Two editors open the profile at the same version.
	read := created.PolicyVersion

	t.Run("the first editor saves at the current version", func(t *testing.T) {
		status, payload := patch(read, "Concurrency API profile", requiredResolution, requiredAudio)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, payload)
		}
		if saved := riProfile(t, payload); saved.PolicyVersion != read+1 {
			t.Fatalf("version=%d, want %d", saved.PolicyVersion, read+1)
		}
	})

	t.Run("the second editor, still on the old version, is refused and the required rules survive", func(t *testing.T) {
		newer := current()
		status, payload := patch(read, "Stale rename", requiredResolution)
		if status != http.StatusConflict || riErrorCode(t, payload) != "quality_profile_changed" {
			t.Fatalf("status=%d body=%s, want 409 quality_profile_changed", status, payload)
		}
		after := current()
		if !reflect.DeepEqual(after, newer) {
			t.Fatalf("the stale edit changed the profile:\n got  %+v\n want %+v", after, newer)
		}
		rules := riRules(t, after.Policy)
		if len(rules) != 2 || rules[1].Fact != "audio_codec" || rules[1].Kind != releases.RuleRequired {
			t.Fatalf("the newer required audio-codec rule did not survive: %+v", rules)
		}
	})

	t.Run("an edit opened before a preset was applied is refused and the preset survives", func(t *testing.T) {
		opened := current()
		presetBody, err := json.Marshal(map[string]any{"preset": "balanced", "policy_version": opened.PolicyVersion})
		if err != nil {
			t.Fatal(err)
		}
		if status, payload := riDo(t, http.MethodPost, url+"/preset", string(presetBody), true); status != http.StatusOK {
			t.Fatalf("preset status=%d body=%s", status, payload)
		}
		applied := current()
		status, payload := patch(opened.PolicyVersion, "Edited against the old policy", requiredResolution, requiredAudio)
		if status != http.StatusConflict || riErrorCode(t, payload) != "quality_profile_changed" {
			t.Fatalf("status=%d body=%s, want 409 quality_profile_changed", status, payload)
		}
		if after := current(); !reflect.DeepEqual(after, applied) {
			t.Fatalf("the stale edit replaced the preset result: %+v", after)
		}
	})

	t.Run("a version from the future is refused as well", func(t *testing.T) {
		before := current()
		if status, payload := patch(before.PolicyVersion+3, "Future", requiredResolution); status != http.StatusConflict || riErrorCode(t, payload) != "quality_profile_changed" {
			t.Fatalf("status=%d body=%s, want 409", status, payload)
		}
		if after := current(); !reflect.DeepEqual(after, before) {
			t.Fatalf("the refused edit changed the profile: %+v", after)
		}
	})

	t.Run("reloading and repeating the edit succeeds, as a client that round-trips the listed profile does", func(t *testing.T) {
		reloaded := current()
		reloaded.Name = "Renamed after reload"
		// The listed object, sent back unchanged apart from the edit, carries the
		// version it was read at: this is how the UI and any scripted client work.
		encoded, err := json.Marshal(reloaded)
		if err != nil {
			t.Fatal(err)
		}
		status, payload := riDo(t, http.MethodPatch, url, string(encoded), true)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, payload)
		}
		saved := riProfile(t, payload)
		if saved.PolicyVersion != reloaded.PolicyVersion+1 || saved.Name != "Renamed after reload" {
			t.Fatalf("saved=%+v", saved)
		}
		if !reflect.DeepEqual(riRules(t, saved.Policy), riRules(t, reloaded.Policy)) {
			t.Fatalf("a round-tripped edit changed the rules: %+v", riRules(t, saved.Policy))
		}
	})
}
