package subtitles

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/media-manager/media-manager/internal/languages"
)

type Policy struct {
	Required  []string `json:"required_subtitle_languages"`
	Preferred []string `json:"preferred_subtitle_languages"`
}

// CanonicalLanguage returns only language values that are reliable enough to
// use as policy evidence. The original value belongs in the evidence record.
func CanonicalLanguage(value string) (string, bool) {
	return languages.Canonical(value)
}

func ParsePolicy(body json.RawMessage) (Policy, error) {
	if len(body) == 0 || !json.Valid(body) {
		return Policy{}, errors.New("quality profile policy is invalid")
	}
	var wrapper struct {
		Required  []string `json:"required_subtitle_languages"`
		Preferred []string `json:"preferred_subtitle_languages"`
	}
	if len(body) > 0 && body[0] == '[' {
		return Policy{}, nil
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return Policy{}, errors.New("quality profile policy is invalid")
	}
	required, err := canonicalLanguages(wrapper.Required)
	if err != nil {
		return Policy{}, err
	}
	preferred, err := canonicalLanguages(wrapper.Preferred)
	if err != nil {
		return Policy{}, err
	}
	return Policy{Required: required, Preferred: preferred}, nil
}

// CanonicalizePolicy preserves the release-rule document while ensuring that
// language lists are canonical before they reach PostgreSQL.
func CanonicalizePolicy(body json.RawMessage) (json.RawMessage, Policy, error) {
	policy, err := ParsePolicy(body)
	if err != nil {
		return nil, Policy{}, err
	}
	var document map[string]json.RawMessage
	if len(body) > 0 && body[0] == '[' {
		document = map[string]json.RawMessage{"rules": append(json.RawMessage(nil), body...)}
	} else if err := json.Unmarshal(body, &document); err != nil {
		return nil, Policy{}, errors.New("quality profile policy is invalid")
	}
	required, _ := json.Marshal(policy.Required)
	preferred, _ := json.Marshal(policy.Preferred)
	document["required_subtitle_languages"] = required
	document["preferred_subtitle_languages"] = preferred
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, Policy{}, err
	}
	return canonical, policy, nil
}

func canonicalLanguages(values []string) ([]string, error) {
	if len(values) > 32 {
		return nil, errors.New("quality profile has too many subtitle languages")
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, raw := range values {
		canonical, ok := CanonicalLanguage(raw)
		if !ok {
			return nil, errors.New("quality profile has an unreliable subtitle language tag")
		}
		if !seen[canonical] {
			seen[canonical] = true
			result = append(result, canonical)
		}
	}
	sort.Strings(result)
	return result, nil
}
