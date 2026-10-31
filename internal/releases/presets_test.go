package releases

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestPresetsContainOnlySoftRules(t *testing.T) {
	definitions := Presets()
	if len(definitions) != 4 {
		t.Fatalf("presets=%d, want 4", len(definitions))
	}
	seenPresets := map[Preset]bool{}
	for _, definition := range definitions {
		if definition.Preset == "" || definition.Name == "" || definition.Description == "" {
			t.Fatalf("preset metadata is incomplete: %+v", definition)
		}
		if seenPresets[definition.Preset] {
			t.Fatalf("duplicate preset %q", definition.Preset)
		}
		seenPresets[definition.Preset] = true
		if len(definition.Rules) == 0 {
			t.Fatalf("preset %q has no rules", definition.Preset)
		}
		if len(definition.ManagedFacts) == 0 {
			t.Fatalf("preset %q has no managed facts", definition.Preset)
		}
		if err := ValidateRules(definition.Rules); err != nil {
			t.Fatalf("preset %q is invalid: %v", definition.Preset, err)
		}
		names := map[string]bool{}
		for _, rule := range definition.Rules {
			if rule.Kind != RulePreferred {
				t.Fatalf("preset %q contains non-preferred rule %q", definition.Preset, rule.Name)
			}
			if rule.Unknown != UnknownAllow {
				t.Fatalf("preset %q rule %q has unknown policy %q", definition.Preset, rule.Name, rule.Unknown)
			}
			if rule.Weight <= 0 {
				t.Fatalf("preset %q rule %q has non-positive weight %d", definition.Preset, rule.Name, rule.Weight)
			}
			if names[rule.Name] {
				t.Fatalf("preset %q duplicates rule name %q", definition.Preset, rule.Name)
			}
			names[rule.Name] = true
		}
	}
}

func TestApplyPresetPreservesHardRulesAndLanguagePreferences(t *testing.T) {
	existing := []Rule{
		{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Best available resolution: 2160p", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RulePreferred, Weight: 50000, Unknown: UnknownAllow},
		{Name: "Preferred subtitle da", Fact: "subtitle_languages", AllowedValues: []string{"da"}, Kind: RulePreferred, Weight: 250, Unknown: UnknownAllow},
		{Name: "Preferred source webdl", Fact: "source", AllowedValues: []string{"webdl"}, Kind: RulePreferred, Weight: 100},
		{Name: "Required audio da", Fact: "audio_languages", AllowedValues: []string{"da"}, Kind: RuleRequired, Unknown: UnknownHold},
	}
	original := append([]Rule(nil), existing...)

	applied, err := ApplyPreset(existing, PresetBalanced)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRules(applied); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(existing, original) {
		t.Fatal("ApplyPreset mutated its input")
	}
	required := 0
	language := 0
	balancedResolution := false
	for _, rule := range applied {
		if rule.Kind == RuleRequired {
			required++
		}
		if rule.Name == "Preferred subtitle da" {
			language++
		}
		if rule.Name == "Best available resolution: 2160p" || rule.Name == "Preferred source webdl" {
			t.Fatalf("managed preference was not replaced: %+v", rule)
		}
		if rule.Name == "Balanced resolution 2160p" && rule.Kind == RulePreferred {
			balancedResolution = true
		}
	}
	if required != 2 {
		t.Fatalf("required rules=%d, want 2", required)
	}
	if language != 1 {
		t.Fatalf("language preference=%d, want 1", language)
	}
	if !balancedResolution {
		t.Fatal("balanced preset rules were not appended")
	}
	// Required 1080p must remain first; preset soft rules follow the preserved
	// rules so the owner can still read the hard gate before the preferences.
	if applied[0].Name != "Required resolution" || applied[0].Kind != RuleRequired {
		t.Fatalf("first rule=%+v", applied[0])
	}
}

func TestApplyPresetRejectsUnknownPreset(t *testing.T) {
	if _, err := ApplyPreset(nil, Preset("invented")); !errors.Is(err, ErrUnknownPreset) {
		t.Fatalf("error=%v, want ErrUnknownPreset", err)
	}
}

func TestApplyPresetEmptyProfileIsValid(t *testing.T) {
	applied, err := ApplyPreset(nil, PresetMaximumQuality)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRules(applied); err != nil {
		t.Fatal(err)
	}
	if len(applied) != len(Presets()[1].Rules) {
		t.Fatalf("rules=%d", len(applied))
	}
}

func TestStorageEfficientLeavesHDRPreference(t *testing.T) {
	existing := []Rule{{Name: "Prefer HDR", Fact: "dynamic_range", AllowedValues: []string{"hdr"}, Kind: RulePreferred, Weight: 100, Unknown: UnknownAllow}}
	applied, err := ApplyPreset(existing, PresetStorageEfficient)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range applied {
		if rule.Name == "Prefer HDR" {
			found = true
		}
	}
	if !found {
		t.Fatal("storage-efficient preset must not silently remove an unmanaged HDR preference")
	}
	balanced, err := ApplyPreset(existing, PresetBalanced)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range balanced {
		if rule.Name == "Prefer HDR" {
			t.Fatal("balanced preset should replace managed dynamic-range preferences")
		}
	}
}

func TestParsePolicyRulesAndWithPolicyRules(t *testing.T) {
	policy := json.RawMessage(`{"rules":[{"name":"R","fact":"resolution","allowed_values":["1080p"],"kind":"required"}],"required_subtitle_languages":["da"],"upgrades_enabled":true}`)
	rules, err := ParsePolicyRules(policy)
	if err != nil || len(rules) != 1 || rules[0].Name != "R" {
		t.Fatalf("rules=%+v err=%v", rules, err)
	}
	updated, err := WithPolicyRules(policy, []Rule{{Name: "P", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RulePreferred, Weight: 10, Unknown: UnknownAllow}})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rules", "required_subtitle_languages", "upgrades_enabled"} {
		if _, exists := document[key]; !exists {
			t.Fatalf("policy key %q was discarded", key)
		}
	}
	bare, err := ParsePolicyRules(json.RawMessage(`[{"name":"B","fact":"source","allowed_values":["webdl"],"kind":"preferred","weight":1}]`))
	if err != nil || len(bare) != 1 || bare[0].Name != "B" {
		t.Fatalf("bare rules=%+v err=%v", bare, err)
	}
	converted, err := WithPolicyRules(json.RawMessage(`[]`), bare)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePolicyRules(converted); err != nil {
		t.Fatalf("converted policy is invalid: %v", err)
	}
	if _, err := ParsePolicyRules(json.RawMessage(`not-json`)); err == nil {
		t.Fatal("malformed policy was accepted")
	}
}

func TestPresetForIsCaseInsensitiveAndDeterministic(t *testing.T) {
	definition, ok := PresetFor("  MAXIMUM_QUALITY ")
	if !ok || definition.Preset != PresetMaximumQuality {
		t.Fatalf("definition=%+v ok=%v", definition, ok)
	}
	first := Presets()
	second := Presets()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Presets is not deterministic")
	}
	first[0].Rules[0].AllowedValues[0] = "mutated"
	if second[0].Rules[0].AllowedValues[0] == "mutated" {
		t.Fatal("Presets returned shared mutable rules")
	}
}

// presetEditorFixturePath is the contract between the Go preset engine and the
// TypeScript profile editor. The editor decides, from stored rules alone,
// whether a profile "has" a preset ladder and which rules are hard gates; a
// hand-copied ladder in a UI test cannot notice the engine changing. This file
// holds the real ApplyPreset output for every preset over a profile that has
// required resolution and audio-codec rules, and both the Go test below and the
// editor's unit and browser tests read it.
const presetEditorFixturePath = "../../tests/browser/fixtures/quality-preset-applications.json"

type presetEditorFixture struct {
	Note    string            `json:"note"`
	Base    []Rule            `json:"base_rules"`
	Applied map[string][]Rule `json:"applied_rules"`
}

func buildPresetEditorFixture(t *testing.T) []byte {
	t.Helper()
	base := []Rule{
		{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Required audio codec", Fact: "audio_codec", AllowedValues: []string{"eac3", "ac3"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Required subtitle or audio da", Fact: SubtitleOrAudioLanguagesFact, AllowedValues: []string{"da"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Preferred subtitle da", Fact: "subtitle_languages", AllowedValues: []string{"da"}, Kind: RulePreferred, Weight: 250, Unknown: UnknownAllow},
		{Name: "Preferred source", Fact: "source", AllowedValues: []string{"webdl"}, Kind: RulePreferred, Weight: 100, Unknown: UnknownAllow},
	}
	fixture := presetEditorFixture{
		Note:    "Generated by TestQualityPresetEditorFixtureIsCurrent. Regenerate with: MM_WRITE_PRESET_FIXTURE=1 go test ./internal/releases -run TestQualityPresetEditorFixtureIsCurrent",
		Base:    base,
		Applied: map[string][]Rule{},
	}
	for _, definition := range Presets() {
		applied, err := ApplyPreset(base, definition.Preset)
		if err != nil {
			t.Fatalf("apply %s: %v", definition.Preset, err)
		}
		fixture.Applied[string(definition.Preset)] = applied
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

func TestQualityPresetEditorFixtureIsCurrent(t *testing.T) {
	want := buildPresetEditorFixture(t)
	if os.Getenv("MM_WRITE_PRESET_FIXTURE") == "1" {
		if err := os.WriteFile(presetEditorFixturePath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(presetEditorFixturePath)
	if err != nil {
		t.Fatalf("read preset editor fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("tests/browser/fixtures/quality-preset-applications.json is stale; run: MM_WRITE_PRESET_FIXTURE=1 go test ./internal/releases -run TestQualityPresetEditorFixtureIsCurrent")
	}
}

// Every preset must keep every required rule, and must not add one, over a
// profile that carries required resolution and audio-codec gates. The editor's
// "unrelated edit changes nothing" guarantee rests on this.
func TestEveryPresetKeepsRequiredResolutionAndAudioGates(t *testing.T) {
	var fixture presetEditorFixture
	if err := json.Unmarshal(buildPresetEditorFixture(t), &fixture); err != nil {
		t.Fatal(err)
	}
	var baseRequired []Rule
	for _, rule := range fixture.Base {
		if rule.Kind == RuleRequired {
			baseRequired = append(baseRequired, rule)
		}
	}
	if len(baseRequired) != 3 {
		t.Fatalf("fixture base has %d required rules, want 3", len(baseRequired))
	}
	for preset, applied := range fixture.Applied {
		var required []Rule
		for _, rule := range applied {
			if rule.Kind == RuleRequired {
				required = append(required, rule)
			}
		}
		if !reflect.DeepEqual(required, baseRequired) {
			t.Fatalf("preset %s changed the required rules: got %+v want %+v", preset, required, baseRequired)
		}
	}
}
