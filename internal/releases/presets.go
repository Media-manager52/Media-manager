package releases

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Preset is an opt-in soft-scoring profile. Presets never contain required
// rules: hard eligibility (scope, identity, required languages, explicit
// resolution requirements) remains owned by the existing profile policy and
// the acquisition pipeline.
type Preset string

const (
	PresetBalanced         Preset = "balanced"
	PresetMaximumQuality   Preset = "maximum_quality"
	PresetStorageEfficient Preset = "storage_efficient"
	PresetCompatibility    Preset = "compatibility_first"
)

type PresetDefinition struct {
	Preset       Preset   `json:"preset"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ManagedFacts []string `json:"managed_facts"`
	Rules        []Rule   `json:"rules"`
}

var ErrUnknownPreset = errors.New("unknown quality preset")

// Presets returns the supported definitions in a stable owner-facing order.
// Callers receive independent slices and must not rely on shared mutable state.
func Presets() []PresetDefinition {
	definitions := []PresetDefinition{
		{
			Preset:       PresetBalanced,
			Name:         "Balanced",
			Description:  "Prefers higher resolution and better audio without imposing a minimum or rejecting a safe fallback.",
			ManagedFacts: []string{"resolution", "video_codec", "source", "audio_codec", "dynamic_range"},
			Rules: concatRules(
				resolutionRules("Balanced", []resolutionWeight{{"2160p", 50000}, {"1080p", 40000}, {"720p", 30000}, {"576p", 20000}, {"480p", 10000}}),
				videoCodecRules("Balanced", []codecWeight{{"hevc", 30}, {"av1", 25}, {"avc", 20}}),
				sourceRules("Balanced", []sourceWeight{{"remux", 110}, {"webdl", 100}, {"bluray", 90}, {"webrip", 80}, {"hdtv", 70}}),
				audioRules("Balanced", standardAudioWeights()),
				dynamicRangeRules("Balanced", []dynamicRangeWeight{{"dolby_vision", 200}, {"hdr10plus", 150}, {"hdr", 100}}),
			),
		},
		{
			Preset:       PresetMaximumQuality,
			Name:         "Maximum quality",
			Description:  "Strongly prefers 2160p, lossless or object-based audio, lossless sources and HDR/Dolby Vision when the release declares it.",
			ManagedFacts: []string{"resolution", "video_codec", "source", "audio_codec", "dynamic_range"},
			Rules: concatRules(
				resolutionRules("Maximum quality", []resolutionWeight{{"2160p", 90000}, {"1080p", 60000}, {"720p", 30000}, {"576p", 10000}, {"480p", 1000}}),
				videoCodecRules("Maximum quality", []codecWeight{{"av1", 6000}, {"hevc", 5500}, {"avc", 3000}}),
				sourceRules("Maximum quality", []sourceWeight{{"remux", 20000}, {"bluray", 15000}, {"webdl", 10000}, {"webrip", 5000}, {"hdtv", 1000}}),
				audioRules("Maximum quality", maximumAudioWeights()),
				dynamicRangeRules("Maximum quality", []dynamicRangeWeight{{"dolby_vision", 5000}, {"hdr10plus", 4500}, {"hdr", 4000}}),
			),
		},
		{
			Preset:       PresetStorageEfficient,
			Name:         "Storage efficient",
			Description:  "Prefers 1080p or 720p, efficient codecs and compact audio while leaving HDR preferences untouched.",
			ManagedFacts: []string{"resolution", "video_codec", "source", "audio_codec"},
			Rules: concatRules(
				resolutionRules("Storage efficient", []resolutionWeight{{"1080p", 50000}, {"720p", 45000}, {"576p", 30000}, {"480p", 20000}, {"2160p", 10000}}),
				videoCodecRules("Storage efficient", []codecWeight{{"hevc", 6000}, {"av1", 5000}, {"avc", 2000}}),
				sourceRules("Storage efficient", []sourceWeight{{"webdl", 3000}, {"webrip", 2500}, {"hdtv", 2000}, {"bluray", 1000}, {"remux", 500}}),
				audioRules("Storage efficient", []audioWeight{{"aac", 5000}, {"opus", 4500}, {"eac3", 3500}, {"ac3", 3000}, {"mp3", 2000}, {"dts", 1000}, {"eac3_atmos", 600}, {"truehd", 500}, {"flac", 400}, {"dts_hd_ma", 300}, {"dts_hd", 250}, {"dts_x", 200}, {"truehd_atmos", 150}, {"lpcm", 100}}),
			),
		},
		{
			Preset:       PresetCompatibility,
			Name:         "Compatibility first",
			Description:  "Prefers widely supported H.264/AAC/AC-3 and 1080p for players that do not handle newer codecs reliably.",
			ManagedFacts: []string{"resolution", "video_codec", "source", "audio_codec"},
			Rules: concatRules(
				resolutionRules("Compatibility first", []resolutionWeight{{"1080p", 50000}, {"720p", 45000}, {"2160p", 20000}, {"576p", 15000}, {"480p", 10000}}),
				videoCodecRules("Compatibility first", []codecWeight{{"avc", 8000}, {"hevc", 4000}, {"av1", 1000}}),
				sourceRules("Compatibility first", []sourceWeight{{"webdl", 4000}, {"hdtv", 3000}, {"webrip", 2500}, {"bluray", 2000}, {"remux", 1000}}),
				audioRules("Compatibility first", []audioWeight{{"aac", 6000}, {"ac3", 5000}, {"eac3", 4000}, {"mp3", 3000}, {"opus", 2500}, {"dts", 1500}, {"flac", 1000}, {"lpcm", 800}, {"eac3_atmos", 800}, {"truehd", 500}, {"truehd_atmos", 400}, {"dts_hd_ma", 400}, {"dts_hd", 300}, {"dts_x", 300}}),
			),
		},
	}
	for index := range definitions {
		definitions[index].Rules = cloneRules(definitions[index].Rules)
		definitions[index].ManagedFacts = append([]string(nil), definitions[index].ManagedFacts...)
	}
	return definitions
}

func concatRules(groups ...[]Rule) []Rule {
	var result []Rule
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

func PresetFor(value string) (PresetDefinition, bool) {
	normalized := Preset(strings.ToLower(strings.TrimSpace(value)))
	for _, definition := range Presets() {
		if definition.Preset == normalized {
			return definition, true
		}
	}
	return PresetDefinition{}, false
}

// ApplyPreset replaces only the soft rules for the preset's managed facts.
// Required rules and preferences outside that scope, including language
// preferences, are preserved verbatim. The input slice is never mutated.
func ApplyPreset(rules []Rule, preset Preset) ([]Rule, error) {
	definition, ok := PresetFor(string(preset))
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownPreset, preset)
	}
	managed := make(map[string]bool, len(definition.ManagedFacts))
	for _, fact := range definition.ManagedFacts {
		managed[normalize(fact)] = true
	}
	result := make([]Rule, 0, len(rules)+len(definition.Rules))
	for _, rule := range rules {
		copied := rule
		copied.AllowedValues = append([]string(nil), rule.AllowedValues...)
		if copied.Kind == RulePreferred && managed[normalize(copied.Fact)] {
			continue
		}
		result = append(result, copied)
	}
	result = append(result, cloneRules(definition.Rules)...)
	if err := ValidateRules(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ParsePolicyRules accepts both the current {"rules":[...]} policy object and
// the legacy bare rule array.
func ParsePolicyRules(raw json.RawMessage) ([]Rule, error) {
	var wrapped struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Rules != nil {
		return wrapped.Rules, nil
	}
	var rules []Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// WithPolicyRules replaces the rules member without discarding the rest of the
// profile policy. A legacy bare array is converted to the current object form.
func WithPolicyRules(policy json.RawMessage, rules []Rule) (json.RawMessage, error) {
	encoded, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(policy, &document); err == nil && document != nil {
		document["rules"] = encoded
		return json.Marshal(document)
	}
	return json.Marshal(map[string]json.RawMessage{"rules": encoded})
}

type resolutionWeight struct {
	value  string
	weight int
}
type codecWeight struct {
	value  string
	weight int
}
type sourceWeight struct {
	value  string
	weight int
}
type audioWeight struct {
	value  string
	weight int
}
type dynamicRangeWeight struct {
	value  string
	weight int
}

func resolutionRules(prefix string, values []resolutionWeight) []Rule {
	result := make([]Rule, 0, len(values))
	for _, item := range values {
		result = append(result, softRule(prefix+" resolution "+item.value, "resolution", item.value, item.weight))
	}
	return result
}

func videoCodecRules(prefix string, values []codecWeight) []Rule {
	result := make([]Rule, 0, len(values))
	for _, item := range values {
		result = append(result, softRule(prefix+" video codec "+item.value, "video_codec", item.value, item.weight))
	}
	return result
}

func sourceRules(prefix string, values []sourceWeight) []Rule {
	result := make([]Rule, 0, len(values))
	for _, item := range values {
		result = append(result, softRule(prefix+" source "+item.value, "source", item.value, item.weight))
	}
	return result
}

func audioRules(prefix string, values []audioWeight) []Rule {
	result := make([]Rule, 0, len(values))
	for _, item := range values {
		result = append(result, softRule(prefix+" audio "+item.value, "audio_codec", item.value, item.weight))
	}
	return result
}

func dynamicRangeRules(prefix string, values []dynamicRangeWeight) []Rule {
	result := make([]Rule, 0, len(values))
	for _, item := range values {
		result = append(result, softRule(prefix+" dynamic range "+item.value, "dynamic_range", item.value, item.weight))
	}
	return result
}

func softRule(name, fact, value string, weight int) Rule {
	return Rule{Name: name, Fact: fact, AllowedValues: []string{value}, Kind: RulePreferred, Weight: weight, Unknown: UnknownAllow}
}

func standardAudioWeights() []audioWeight {
	return []audioWeight{{"truehd_atmos", 5300}, {"dts_x", 5100}, {"truehd", 5000}, {"dts_hd_ma", 4800}, {"dts_hd", 4700}, {"flac", 4600}, {"lpcm", 4400}, {"eac3_atmos", 3300}, {"eac3", 3000}, {"dts", 2800}, {"ac3", 2000}, {"opus", 1800}, {"aac", 1500}, {"mp3", 1000}}
}

func maximumAudioWeights() []audioWeight {
	return []audioWeight{{"truehd_atmos", 6000}, {"dts_x", 5800}, {"truehd", 5600}, {"dts_hd_ma", 5400}, {"flac", 5200}, {"lpcm", 5000}, {"dts_hd", 4800}, {"eac3_atmos", 4200}, {"eac3", 4000}, {"dts", 3600}, {"ac3", 3000}, {"opus", 2500}, {"aac", 2000}, {"mp3", 1000}}
}

func cloneRules(rules []Rule) []Rule {
	result := make([]Rule, len(rules))
	for index, rule := range rules {
		result[index] = rule
		result[index].AllowedValues = append([]string(nil), rule.AllowedValues...)
	}
	return result
}
