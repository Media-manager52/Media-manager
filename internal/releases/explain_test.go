package releases

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"
)

func TestEvaluateExplanationIsAdditiveAndKeepsSelectionScores(t *testing.T) {
	rules := []Rule{
		{Name: "Required audio en", Fact: "audio_languages", AllowedValues: []string{"en"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Prefer 2160p", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RulePreferred, Weight: 100, Unknown: UnknownAllow},
		{Name: "Prefer 1080p", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: RulePreferred, Weight: 50, Unknown: UnknownAllow},
		{Name: "Prefer UHD", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RulePreferred, Weight: 7, Unknown: UnknownAllow},
	}
	observation := Observation{ID: uuid.NewV7(), Facts: []Fact{
		{Name: "audio_languages", Values: []string{"en"}, State: FactObserved},
		{Name: "resolution", Values: []string{"1080p"}, State: FactObserved},
	}}

	evaluation, err := Evaluate(observation, rules)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Verdict != VerdictEligible || evaluation.Score != 50 {
		t.Fatalf("verdict=%s score=%d, want eligible 50", evaluation.Verdict, evaluation.Score)
	}
	if len(evaluation.Explanations) != 4 {
		t.Fatalf("explanations=%d, want 4", len(evaluation.Explanations))
	}
	total := 0
	for _, explanation := range evaluation.Explanations {
		total += explanation.Score
		if explanation.Category == "" {
			t.Fatalf("reason %q has no category", explanation.Rule)
		}
		if explanation.RuleKind == "" {
			t.Fatalf("reason %q has no rule kind", explanation.Rule)
		}
	}
	if total != evaluation.Score {
		t.Fatalf("explanation scores=%d, evaluation score=%d", total, evaluation.Score)
	}
	preserved := ExplainDecision(VerdictEligible, 50, []Explanation{{Rule: "Prefer 1080p", Fact: "resolution", Decision: DecisionPass, Values: []string{"1080p"}, Score: 50, Reason: "observed evidence satisfies the rule"}}, nil)
	if preserved.Verdict != VerdictEligible || preserved.Score != 50 {
		t.Fatalf("explanation verdict=%s score=%d, want unchanged eligible 50", preserved.Verdict, preserved.Score)
	}
	required := evaluation.Explanations[0]
	if required.RuleKind != RuleRequired || required.Category != CategoryAudioCodec && required.Category != CategoryOther {
		t.Fatalf("required explanation=%+v", required)
	}
	if required.EvidenceState != FactObserved {
		t.Fatalf("required evidence state=%q, want observed", required.EvidenceState)
	}
	matched := evaluation.Explanations[2]
	if matched.Rule != "Prefer 1080p" || matched.Score != 50 || matched.EvidenceState != FactObserved || matched.Category != CategoryResolutionCodec {
		t.Fatalf("matched explanation=%+v", matched)
	}
}

func TestEvaluateUnknownEvidenceStaysUnknown(t *testing.T) {
	rules := []Rule{
		{Name: "Require 2160p", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Prefer HDR", Fact: "dynamic_range", AllowedValues: []string{"hdr"}, Kind: RulePreferred, Weight: 300, Unknown: UnknownAllow},
	}
	evaluation, err := Evaluate(Observation{ID: uuid.NewV7()}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Verdict != VerdictHeld || evaluation.Score != 0 {
		t.Fatalf("verdict=%s score=%d, want held 0", evaluation.Verdict, evaluation.Score)
	}
	for _, explanation := range evaluation.Explanations {
		if explanation.Decision != DecisionUnknown {
			t.Fatalf("decision=%q, want unknown", explanation.Decision)
		}
		if explanation.EvidenceState != FactUnknown {
			t.Fatalf("evidence state=%q, want unknown", explanation.EvidenceState)
		}
		if len(explanation.Values) != 0 {
			t.Fatalf("unknown evidence invented values: %v", explanation.Values)
		}
	}
}

func TestEvaluateConflictingEvidenceStaysConflicting(t *testing.T) {
	rules := []Rule{{Name: "Require 2160p", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RuleRequired, Unknown: UnknownHold}}
	evaluation, err := Evaluate(Observation{ID: uuid.NewV7(), Facts: []Fact{
		{Name: "resolution", Values: []string{"2160p", "1080p"}, State: FactConflicting},
	}}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Verdict != VerdictHeld || len(evaluation.Explanations) != 1 {
		t.Fatalf("verdict=%s explanations=%d, want held 1", evaluation.Verdict, len(evaluation.Explanations))
	}
	explanation := evaluation.Explanations[0]
	if explanation.Decision != DecisionUnknown || explanation.EvidenceState != FactConflicting {
		t.Fatalf("explanation=%+v", explanation)
	}
	if explanation.Category != CategoryResolutionCodec || explanation.RuleKind != RuleRequired {
		t.Fatalf("explanation metadata=%+v", explanation)
	}
}

func TestExplainDecisionPreservesOrderAndCategories(t *testing.T) {
	observedAt := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	reasons := []Explanation{
		{Rule: "requested title", Fact: "release_title", Decision: DecisionFail, Values: []string{"Wrong.Movie.2020"}, Reason: "identity"},
		{Rule: "quality profile", Decision: DecisionUnknown, Reason: "no rules"},
		{Rule: "upgrade improvement", Fact: "quality_score", Decision: DecisionFail, Reason: "not an improvement"},
		{Rule: "release blocklist", Fact: "native_release", Decision: DecisionFail, Reason: "blocked"},
		{Rule: "Prefer 2160p", Fact: "resolution", Decision: DecisionFail, Values: []string{"1080p"}, RuleKind: RulePreferred, Category: CategoryResolutionCodec, Reason: "observed evidence does not satisfy the rule"},
	}
	facts := []Fact{
		{Name: "subtitle_languages", Values: []string{"none"}, State: FactObserved, ObservedAt: observedAt},
		{Name: "resolution", Values: []string{"1080p"}, State: FactInferred, ObservedAt: observedAt},
		{Name: "audio_language_hints", Values: []string{"da"}, State: FactInferred},
		{Name: "dynamic_range", Values: []string{"hdr", "dolby_vision"}, State: FactConflicting, EvidenceAt: []int{2, 3}},
	}
	explanation := ExplainDecision(VerdictRejected, 0, reasons, facts)
	if explanation.Version != 1 || explanation.Verdict != VerdictRejected || explanation.Score != 0 {
		t.Fatalf("summary=%+v", explanation)
	}
	if explanation.EvidenceScope != CurrentLinkedObservationEvidenceScope {
		t.Fatalf("scope=%q", explanation.EvidenceScope)
	}
	if len(explanation.Reasons) != len(reasons) {
		t.Fatalf("reasons=%d, want %d", len(explanation.Reasons), len(reasons))
	}
	wantCategories := []ExplanationCategory{CategoryIdentity, CategoryQuality, CategoryUpgrade, CategoryExclusion, CategoryResolutionCodec}
	for index, want := range wantCategories {
		if explanation.Reasons[index].Category != want {
			t.Fatalf("reason %d category=%q, want %q", index, explanation.Reasons[index].Category, want)
		}
	}
	if explanation.Reasons[0].Rule != "requested title" {
		t.Fatal("reason order changed")
	}
	if len(explanation.Evidence) != 4 {
		t.Fatalf("evidence=%d, want 4", len(explanation.Evidence))
	}
	// Evidence is stable: resolution and dynamic range before language facts.
	if explanation.Evidence[0].Category != CategoryResolutionCodec || explanation.Evidence[0].Fact != "resolution" {
		t.Fatalf("first evidence=%+v", explanation.Evidence[0])
	}
	if explanation.Evidence[1].Category != CategoryDynamicRange || explanation.Evidence[1].State != FactConflicting {
		t.Fatalf("second evidence=%+v", explanation.Evidence[1])
	}
	for _, evidence := range explanation.Evidence {
		if evidence.Fact == "subtitle_languages" {
			if evidence.Category != CategoryOther || evidence.State != FactObserved {
				t.Fatalf("subtitle evidence=%+v", evidence)
			}
			if evidence.ObservedAt == nil || !evidence.ObservedAt.Equal(observedAt) {
				t.Fatalf("observed time=%v", evidence.ObservedAt)
			}
		}
		if evidence.Fact == "audio_language_hints" {
			if evidence.Category != CategoryDanishLanguage {
				t.Fatalf("danish hint category=%q", evidence.Category)
			}
			if evidence.Values[0] != "da" {
				t.Fatalf("danish hint values=%v", evidence.Values)
			}
		}
	}
	// The presenter must not mutate the caller's reasons or share its slices.
	if reasons[0].Category != "" {
		t.Fatal("presenter mutated the original reason")
	}
	explanation.Reasons[0].Values[0] = "changed"
	if reasons[0].Values[0] != "Wrong.Movie.2020" {
		t.Fatal("presenter shared a values slice with the caller")
	}
}

func TestExplainDecisionForScopeKeepsReplayDistinct(t *testing.T) {
	persisted := ExplainDecision(VerdictHeld, 0, nil, nil)
	if persisted.EvidenceScope != CurrentLinkedObservationEvidenceScope {
		t.Fatalf("persisted scope=%q", persisted.EvidenceScope)
	}
	replay := ExplainDecisionForScope(VerdictHeld, 0, nil, nil, DryRunReplayEvidenceScope)
	if replay.EvidenceScope != DryRunReplayEvidenceScope {
		t.Fatalf("replay scope=%q", replay.EvidenceScope)
	}
	fallback := ExplainDecisionForScope(VerdictHeld, 0, nil, nil, "  ")
	if fallback.EvidenceScope != CurrentLinkedObservationEvidenceScope {
		t.Fatalf("fallback scope=%q", fallback.EvidenceScope)
	}
}

func TestExplainDecisionWithoutRuleKindDoesNotGuessHardness(t *testing.T) {
	explanation := ExplainDecision(VerdictEligible, 0, []Explanation{{Rule: "legacy reviewer reason", Fact: "resolution", Decision: DecisionPass}}, nil)
	if len(explanation.Reasons) != 1 {
		t.Fatal("reason missing")
	}
	if explanation.Reasons[0].RuleKind != "" {
		t.Fatalf("legacy rule kind=%q, want unrecorded", explanation.Reasons[0].RuleKind)
	}
	if explanation.Reasons[0].Category != CategoryResolutionCodec {
		t.Fatalf("category=%q", explanation.Reasons[0].Category)
	}
}

func TestExplainDecisionLegacyDanishReasonUsesStoredRuleName(t *testing.T) {
	explanation := ExplainDecision(VerdictHeld, 0, []Explanation{{Rule: "Preferred subtitle da", Fact: "subtitle_languages", Decision: DecisionUnknown}}, nil)
	if len(explanation.Reasons) != 1 || explanation.Reasons[0].Category != CategoryDanishLanguage {
		t.Fatalf("legacy Danish reason=%+v", explanation.Reasons)
	}
	if explanation.Reasons[0].RuleKind != "" {
		t.Fatal("legacy rule kind was invented")
	}
}

func TestParseDecisionReasonsAndFactsTolerateLegacyShapes(t *testing.T) {
	if reasons := ParseDecisionReasons(nil); reasons != nil {
		t.Fatalf("nil reasons=%v", reasons)
	}
	if reasons := ParseDecisionReasons(json.RawMessage(`{"legacy":true}`)); reasons != nil {
		t.Fatalf("object reasons=%v", reasons)
	}
	if reasons := ParseDecisionReasons(json.RawMessage(`not-json`)); reasons != nil {
		t.Fatalf("invalid reasons=%v", reasons)
	}
	reasons := ParseDecisionReasons(json.RawMessage(`[{"rule":"r","fact":"resolution","decision":"pass","values":["1080p"],"score":0,"reason":"ok"}]`))
	if len(reasons) != 1 || reasons[0].Values[0] != "1080p" {
		t.Fatalf("reasons=%+v", reasons)
	}
	if facts := ParseObservationFacts(json.RawMessage(`{}`)); facts != nil {
		t.Fatalf("legacy empty object facts=%v", facts)
	}
	if facts := ParseObservationFacts(json.RawMessage(`[]`)); facts == nil || len(facts) != 0 {
		t.Fatalf("empty facts=%v", facts)
	}
	facts := ParseObservationFacts(json.RawMessage(`[{"name":"resolution","values":["2160p"],"state":"observed","observed_at":"2026-10-30T12:00:00Z"}]`))
	if len(facts) != 1 || facts[0].ObservedAt.IsZero() || facts[0].Values[0] != "2160p" {
		t.Fatalf("facts=%+v", facts)
	}
}

func TestExplainDecisionDoesNotInventDanishEvidence(t *testing.T) {
	facts := []Fact{
		{Name: "audio_language_hints", Values: []string{"da"}, State: FactInferred},
		{Name: "subtitle_languages", Values: []string{NoSubtitleLanguageObserved}, State: FactObserved},
	}
	explanation := ExplainDecision(VerdictHeld, 0, []Explanation{
		{Rule: "Required audio da", Fact: AudioLanguageEvidenceFact, Decision: DecisionUnknown, RuleKind: RuleRequired, Category: CategoryDanishLanguage, EvidenceState: FactUnknown, Reason: "the provider supplied no conclusive evidence"},
	}, facts)
	if len(explanation.Evidence) != 2 {
		t.Fatalf("evidence=%d, want 2", len(explanation.Evidence))
	}
	for _, evidence := range explanation.Evidence {
		if evidence.Fact == "audio_language_hints" {
			if evidence.State != FactInferred || evidence.Category != CategoryDanishLanguage {
				t.Fatalf("hint evidence=%+v", evidence)
			}
		}
		if evidence.Fact == "subtitle_languages" {
			if evidence.Category != CategoryOther || evidence.Values[0] != NoSubtitleLanguageObserved {
				t.Fatalf("subtitle evidence=%+v", evidence)
			}
		}
	}
	if explanation.Reasons[0].EvidenceState != FactUnknown {
		t.Fatal("unknown reason evidence was upgraded")
	}
}
