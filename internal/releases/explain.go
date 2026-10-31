package releases

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// ExplanationCategory is the owner-facing dimension a reason belongs to. It is
// deliberately separate from RuleKind: a required 1080p rule and a preferred
// 1080p rule both concern resolution, but only the rule kind says whether the
// requirement is hard.
type ExplanationCategory string

const (
	CategoryEligibility     ExplanationCategory = "eligibility"
	CategoryQuality         ExplanationCategory = "quality"
	CategoryResolutionCodec ExplanationCategory = "resolution_codec"
	CategoryDynamicRange    ExplanationCategory = "dynamic_range"
	CategoryAudioCodec      ExplanationCategory = "audio_codec"
	CategoryDanishLanguage  ExplanationCategory = "danish_language"
	CategoryIdentity        ExplanationCategory = "identity"
	CategoryUpgrade         ExplanationCategory = "upgrade"
	CategoryExclusion       ExplanationCategory = "exclusion"
	CategoryOther           ExplanationCategory = "other"
)

// DecisionExplanation is a deterministic presentation of an existing decision.
// It never re-evaluates the release, never changes the verdict or score, and
// never adds evidence the provider or the bounded verifier did not supply.
type DecisionExplanation struct {
	Version       int            `json:"version"`
	Verdict       Verdict        `json:"verdict"`
	Score         int            `json:"score"`
	Reasons       []Explanation  `json:"reasons"`
	EvidenceScope string         `json:"evidence_scope"`
	Evidence      []EvidenceView `json:"evidence"`
}

// EvidenceView describes one stored fact from the currently linked observation.
// The decision row itself does not retain the exact observation snapshot that
// produced a historical decision, so callers must not present this as
// decision-time provenance.
type EvidenceView struct {
	Fact       string              `json:"fact"`
	Category   ExplanationCategory `json:"category"`
	State      FactState           `json:"state"`
	Values     []string            `json:"values"`
	ObservedAt *time.Time          `json:"observed_at,omitempty"`
	EvidenceAt []int               `json:"evidence_at,omitempty"`
}

const (
	CurrentLinkedObservationEvidenceScope = "current_linked_observation"
	DryRunReplayEvidenceScope             = "dry_run_replay"
)

// ExplainDecision converts persisted reasons and stored facts into a stable,
// categorized explanation. Missing or malformed legacy reasons stay absent
// instead of being guessed.
func ExplainDecision(verdict Verdict, score int, reasons []Explanation, facts []Fact) DecisionExplanation {
	return ExplainDecisionForScope(verdict, score, reasons, facts, CurrentLinkedObservationEvidenceScope)
}

// ExplainDecisionForScope is ExplainDecision with an explicit provenance
// scope. The dry-run uses its own scope so a replay is never presented as the
// historical decision-time observation.
func ExplainDecisionForScope(verdict Verdict, score int, reasons []Explanation, facts []Fact, scope string) DecisionExplanation {
	if strings.TrimSpace(scope) == "" {
		scope = CurrentLinkedObservationEvidenceScope
	}
	result := DecisionExplanation{
		Version:       1,
		Verdict:       verdict,
		Score:         score,
		Reasons:       make([]Explanation, 0, len(reasons)),
		EvidenceScope: scope,
		Evidence:      make([]EvidenceView, 0, len(facts)),
	}
	for _, reason := range reasons {
		copied := reason
		copied.Values = append([]string(nil), reason.Values...)
		if copied.Category == "" {
			copied.Category = categoryForReason(copied)
		}
		result.Reasons = append(result.Reasons, copied)
	}
	for _, fact := range facts {
		if strings.TrimSpace(fact.Name) == "" {
			continue
		}
		view := EvidenceView{
			Fact:       fact.Name,
			Category:   categoryForFact(fact),
			State:      fact.State,
			Values:     append([]string(nil), fact.Values...),
			EvidenceAt: append([]int(nil), fact.EvidenceAt...),
		}
		if !fact.ObservedAt.IsZero() {
			observedAt := fact.ObservedAt.UTC()
			view.ObservedAt = &observedAt
		}
		if len(view.Values) == 0 {
			view.Values = []string{}
		}
		result.Evidence = append(result.Evidence, view)
	}
	sort.SliceStable(result.Evidence, func(i, j int) bool {
		left, right := result.Evidence[i], result.Evidence[j]
		if left.Category != right.Category {
			return categoryRank(left.Category) < categoryRank(right.Category)
		}
		if left.Fact != right.Fact {
			return left.Fact < right.Fact
		}
		leftValues, rightValues := strings.Join(left.Values, "\x00"), strings.Join(right.Values, "\x00")
		if leftValues != rightValues {
			return leftValues < rightValues
		}
		return left.State < right.State
	})
	return result
}

// ParseDecisionReasons accepts the JSON stored in candidate_decisions.reasons.
// Malformed or legacy shapes return no reasons; the original JSON remains
// available to the caller as raw evidence.
func ParseDecisionReasons(raw json.RawMessage) []Explanation {
	if len(raw) == 0 {
		return nil
	}
	var reasons []Explanation
	if err := json.Unmarshal(raw, &reasons); err != nil {
		return nil
	}
	for index := range reasons {
		reasons[index].Values = append([]string(nil), reasons[index].Values...)
	}
	return reasons
}

// ParseObservationFacts accepts the JSON stored in release_observations.
// normalized_facts. An empty legacy object is a valid "no facts" shape and
// must not become invented evidence.
func ParseObservationFacts(raw json.RawMessage) []Fact {
	if len(raw) == 0 {
		return nil
	}
	var facts []Fact
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil
	}
	for index := range facts {
		facts[index].Values = append([]string(nil), facts[index].Values...)
		facts[index].EvidenceAt = append([]int(nil), facts[index].EvidenceAt...)
	}
	return facts
}

// CategoryForRule exposes the same deterministic mapping the evaluator uses,
// so callers that persist an explanation do not need a second taxonomy.
func CategoryForRule(rule Rule) ExplanationCategory {
	return categoryForRule(rule)
}

// CategoryForConstraint names the category of a non-rule acquisition
// constraint. Unknown names stay in the generic category.
func CategoryForConstraint(rule string) ExplanationCategory {
	switch normalize(rule) {
	case "quality profile":
		return CategoryQuality
	case "release blocklist", "workflow release verification", "workflow subtitle verification", "altmount repair":
		return CategoryExclusion
	case "upgrade improvement":
		return CategoryUpgrade
	case "verified media identity", "requested title", "requested movie year", "series identity evidence", "native release identity", "native danish series catalogue binding":
		return CategoryIdentity
	default:
		return CategoryOther
	}
}

func categoryForReason(explanation Explanation) ExplanationCategory {
	if explanation.Category != "" {
		return explanation.Category
	}
	if category := CategoryForConstraint(explanation.Rule); category != CategoryOther {
		return category
	}
	if category := categoryForFact(Fact{Name: explanation.Fact, Values: explanation.Values, State: explanation.EvidenceState}); category != CategoryOther {
		return category
	}
	// Legacy reasons may have no values. The rule name is still stored
	// evidence, so use its Danish token instead of calling a language rule
	// "other"; this never adds a value the rule did not already contain.
	ruleWords := append(append([]string(nil), explanation.Values...), strings.Fields(explanation.Rule)...)
	if containsDanish(ruleWords) {
		return CategoryDanishLanguage
	}
	return CategoryOther
}

func categoryForRule(rule Rule) ExplanationCategory {
	return categoryForFact(Fact{Name: rule.Fact, Values: rule.AllowedValues})
}

func categoryForFact(fact Fact) ExplanationCategory {
	switch normalize(fact.Name) {
	case "resolution", "video_codec", "source":
		return CategoryResolutionCodec
	case "dynamic_range":
		return CategoryDynamicRange
	case "audio_codec":
		return CategoryAudioCodec
	case "audio_languages", "audio_language_hints", "audio_language_evidence", "subtitle_languages", "subtitle_or_audio_languages":
		if containsDanish(fact.Values) {
			return CategoryDanishLanguage
		}
		return CategoryOther
	case "season_number", "episode_number", "release_scope":
		return CategoryEligibility
	case "imdb_id", "tmdb_id", "tvdb_id", "media_id", "oldboys_catalogue_binding":
		return CategoryIdentity
	case "current_release", "quality_score":
		return CategoryUpgrade
	case "release_title", "native_release":
		return CategoryIdentity
	default:
		return CategoryOther
	}
}

func containsDanish(values []string) bool {
	for _, value := range values {
		switch normalize(value) {
		case "da", "dan", "danish", "dansk":
			return true
		}
	}
	return false
}

func categoryRank(category ExplanationCategory) int {
	switch category {
	case CategoryEligibility:
		return 0
	case CategoryQuality:
		return 1
	case CategoryResolutionCodec:
		return 2
	case CategoryDynamicRange:
		return 3
	case CategoryAudioCodec:
		return 4
	case CategoryDanishLanguage:
		return 5
	case CategoryIdentity:
		return 6
	case CategoryUpgrade:
		return 7
	case CategoryExclusion:
		return 8
	case CategoryOther:
		return 9
	default:
		return 10
	}
}
