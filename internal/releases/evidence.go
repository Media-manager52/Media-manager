package releases

import (
	"errors"
	"sort"
	"strings"
	"time"
	"uuid"
)

// RawField is intentionally a list element rather than a map entry. Newznab
// providers repeat attributes and Media Manager must retain their original
// order instead of silently discarding evidence.
type RawField struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Value     string `json:"value"`
	Ordinal   int    `json:"ordinal"`
}

type FactState string

const (
	FactObserved    FactState = "observed"
	FactInferred    FactState = "inferred"
	FactUnknown     FactState = "unknown"
	FactConflicting FactState = "conflicting"
)

// NoSubtitleLanguageObserved is the single value of an observed
// subtitle_languages fact when bounded output inspection verified the
// release and found no subtitle language at all. Evaluate treats an empty
// value list as unknown, so the verified empty set needs an explicit value
// that can never satisfy a language rule.
const NoSubtitleLanguageObserved = "none"

type Fact struct {
	Name       string    `json:"name"`
	Values     []string  `json:"values"`
	State      FactState `json:"state"`
	EvidenceAt []int     `json:"evidence_at"`
	ObservedAt time.Time `json:"observed_at"`
}

type Observation struct {
	ID           uuid.UUID  `json:"id"`
	IndexerID    uuid.UUID  `json:"indexer_id"`
	Operation    string     `json:"operation"`
	NativeGUID   string     `json:"native_guid"`
	Title        string     `json:"title"`
	DownloadURL  string     `json:"-"`
	Fields       []RawField `json:"fields"`
	Facts        []Fact     `json:"facts"`
	BodyHash     string     `json:"body_hash"`
	ObservedAt   time.Time  `json:"observed_at"`
	ParseVersion string     `json:"parse_version"`
	Problems     []string   `json:"problems,omitempty"`
}

type RuleKind string

const (
	RuleRequired  RuleKind = "required"
	RulePreferred RuleKind = "preferred"
)

type UnknownPolicy string

const (
	UnknownReject UnknownPolicy = "reject"
	UnknownHold   UnknownPolicy = "hold"
	UnknownAllow  UnknownPolicy = "allow"
)

type Rule struct {
	Name          string        `json:"name"`
	Fact          string        `json:"fact"`
	AllowedValues []string      `json:"allowed_values"`
	Kind          RuleKind      `json:"kind"`
	Weight        int           `json:"weight"`
	Unknown       UnknownPolicy `json:"unknown"`
}

type RuleDecision string

const (
	DecisionPass    RuleDecision = "pass"
	DecisionFail    RuleDecision = "fail"
	DecisionUnknown RuleDecision = "unknown"
)

type Explanation struct {
	Rule     string       `json:"rule"`
	Fact     string       `json:"fact"`
	Decision RuleDecision `json:"decision"`
	Values   []string     `json:"values,omitempty"`
	Score    int          `json:"score"`
	Reason   string       `json:"reason"`
}

type Verdict string

const (
	VerdictEligible Verdict = "eligible"
	VerdictHeld     Verdict = "held"
	VerdictRejected Verdict = "rejected"
)

type Evaluation struct {
	ObservationID uuid.UUID     `json:"observation_id"`
	Verdict       Verdict       `json:"verdict"`
	Score         int           `json:"score"`
	Explanations  []Explanation `json:"explanations"`
}

var ErrInvalidRule = errors.New("invalid release rule")

func ValidateRules(rules []Rule) error {
	if len(rules) == 0 {
		return ErrInvalidRule
	}
	for _, rule := range rules {
		if strings.TrimSpace(rule.Name) == "" || strings.TrimSpace(rule.Fact) == "" || len(rule.AllowedValues) == 0 || (rule.Kind != RuleRequired && rule.Kind != RulePreferred) {
			return ErrInvalidRule
		}
		for _, value := range rule.AllowedValues {
			if strings.TrimSpace(value) == "" {
				return ErrInvalidRule
			}
		}
		if rule.Unknown != "" && rule.Unknown != UnknownReject && rule.Unknown != UnknownHold && rule.Unknown != UnknownAllow {
			return ErrInvalidRule
		}
	}
	return nil
}

func Evaluate(observation Observation, rules []Rule) (Evaluation, error) {
	if err := ValidateRules(rules); err != nil {
		return Evaluation{}, err
	}
	facts := make(map[string]Fact, len(observation.Facts)+1)
	for _, fact := range observation.Facts {
		facts[normalize(fact.Name)] = fact
	}
	if combined, ok := subtitleOrAudioLanguages(facts); ok {
		facts[SubtitleOrAudioLanguagesFact] = combined
	}
	if evidence, ok := audioLanguageEvidence(facts); ok {
		facts[AudioLanguageEvidenceFact] = evidence
	}
	result := Evaluation{ObservationID: observation.ID, Verdict: VerdictEligible}
	for _, rule := range rules {
		unknown := rule.Unknown
		if unknown == "" {
			if rule.Kind == RuleRequired {
				unknown = UnknownHold
			} else {
				unknown = UnknownAllow
			}
		}
		if unknown != UnknownReject && unknown != UnknownHold && unknown != UnknownAllow {
			return Evaluation{}, ErrInvalidRule
		}
		fact, exists := facts[normalize(rule.Fact)]
		if !exists || fact.State == FactUnknown || len(fact.Values) == 0 {
			explanation := Explanation{Rule: rule.Name, Fact: rule.Fact, Decision: DecisionUnknown, Reason: "the provider supplied no conclusive evidence"}
			result.Explanations = append(result.Explanations, explanation)
			if rule.Kind == RuleRequired {
				switch unknown {
				case UnknownReject:
					result.Verdict = VerdictRejected
				case UnknownHold:
					if result.Verdict != VerdictRejected {
						result.Verdict = VerdictHeld
					}
				}
			}
			continue
		}
		if fact.State == FactConflicting {
			result.Explanations = append(result.Explanations, Explanation{Rule: rule.Name, Fact: rule.Fact, Decision: DecisionUnknown, Values: append([]string(nil), fact.Values...), Reason: "the evidence conflicts"})
			if rule.Kind == RuleRequired && result.Verdict != VerdictRejected {
				result.Verdict = VerdictHeld
			}
			continue
		}
		matched := intersectsFold(fact.Values, rule.AllowedValues)
		if matched {
			score := 0
			if rule.Kind == RulePreferred {
				score = rule.Weight
				result.Score += score
			}
			result.Explanations = append(result.Explanations, Explanation{Rule: rule.Name, Fact: rule.Fact, Decision: DecisionPass, Values: append([]string(nil), fact.Values...), Score: score, Reason: "observed evidence satisfies the rule"})
			continue
		}
		result.Explanations = append(result.Explanations, Explanation{Rule: rule.Name, Fact: rule.Fact, Decision: DecisionFail, Values: append([]string(nil), fact.Values...), Reason: "observed evidence does not satisfy the rule"})
		if rule.Kind == RuleRequired {
			result.Verdict = VerdictRejected
		}
	}
	return result, nil
}

func Rank(observations []Observation, evaluations []Evaluation) []Evaluation {
	byID := make(map[uuid.UUID]Observation, len(observations))
	for _, observation := range observations {
		byID[observation.ID] = observation
	}
	result := append([]Evaluation(nil), evaluations...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Verdict != result[j].Verdict {
			return verdictOrder(result[i].Verdict) < verdictOrder(result[j].Verdict)
		}
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		left, right := byID[result[i].ObservationID], byID[result[j].ObservationID]
		if left.IndexerID != right.IndexerID {
			return left.IndexerID.String() < right.IndexerID.String()
		}
		return left.NativeGUID < right.NativeGUID
	})
	return result
}

func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func intersectsFold(values, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		set[normalize(value)] = struct{}{}
	}
	for _, value := range values {
		if _, ok := set[normalize(value)]; ok {
			return true
		}
	}
	return false
}

func verdictOrder(verdict Verdict) int {
	switch verdict {
	case VerdictEligible:
		return 0
	case VerdictHeld:
		return 1
	default:
		return 2
	}
}

// SubtitleOrAudioLanguagesFact is derived during evaluation from the
// subtitle_languages and audio_languages facts. A required language is
// satisfied by either a subtitle or an audio track in that language: a Danish
// dub needs no Danish subtitles. The fact exists only when at least one of the
// two is observed or inferred, so unknown evidence stays unknown.
const SubtitleOrAudioLanguagesFact = "subtitle_or_audio_languages"

func subtitleOrAudioLanguages(facts map[string]Fact) (Fact, bool) {
	return unionFacts(SubtitleOrAudioLanguagesFact, facts, "subtitle_languages", "audio_languages")
}

// AudioLanguageEvidenceFact is derived from explicit audio_languages and the
// weaker audio_language_hints (a bare DANISH tag). Profiles that require an
// audio language evaluate it.
const AudioLanguageEvidenceFact = "audio_language_evidence"

// audioLanguageEvidence prefers audio languages a provider observed; only
// without them do title-inferred languages and hints count.
func audioLanguageEvidence(facts map[string]Fact) (Fact, bool) {
	if observed, exists := facts["audio_languages"]; exists && observed.State == FactObserved && len(observed.Values) > 0 {
		observed.Name = AudioLanguageEvidenceFact
		return observed, true
	}
	return unionFacts(AudioLanguageEvidenceFact, facts, "audio_languages", "audio_language_hints")
}

func unionFacts(name string, facts map[string]Fact, sources ...string) (Fact, bool) {
	combined := Fact{Name: name, State: FactInferred}
	known := false
	seen := map[string]bool{}
	for _, source := range sources {
		fact, exists := facts[source]
		if !exists || (fact.State != FactObserved && fact.State != FactInferred) || len(fact.Values) == 0 {
			continue
		}
		known = true
		if fact.State == FactObserved {
			combined.State = FactObserved
		}
		if fact.ObservedAt.After(combined.ObservedAt) {
			combined.ObservedAt = fact.ObservedAt
		}
		for _, value := range fact.Values {
			if !seen[normalize(value)] {
				seen[normalize(value)] = true
				combined.Values = append(combined.Values, value)
			}
		}
	}
	return combined, known
}
