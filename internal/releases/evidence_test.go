package releases

import (
	"testing"
	"time"
	"uuid"
)

func TestRequiredUnknownIsHeldAndPreferenceIsExplainable(t *testing.T) {
	observation := Observation{ID: uuid.NewV7(), Facts: []Fact{{Name: "resolution", Values: []string{"2160p"}, State: FactObserved}}}
	rules := []Rule{
		{Name: "English audio", Fact: "language", AllowedValues: []string{"en"}, Kind: RuleRequired, Unknown: UnknownHold},
		{Name: "Prefer UHD", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RulePreferred, Weight: 100},
	}
	evaluation, err := Evaluate(observation, rules)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Verdict != VerdictHeld || evaluation.Score != 100 {
		t.Fatalf("unexpected evaluation: %#v", evaluation)
	}
}

func TestRankIsDeterministic(t *testing.T) {
	older := Observation{ID: uuid.NewV7(), NativeGUID: "b", ObservedAt: time.Unix(1, 0)}
	newer := Observation{ID: uuid.NewV7(), NativeGUID: "a", ObservedAt: time.Unix(2, 0)}
	ranked := Rank([]Observation{older, newer}, []Evaluation{{ObservationID: older.ID, Verdict: VerdictEligible, Score: 1}, {ObservationID: newer.ID, Verdict: VerdictEligible, Score: 1}})
	if ranked[0].ObservationID != newer.ID {
		t.Fatal("expected newest equally-scored candidate first")
	}
}

func TestPreferredSubtitleUnknownIsNeutralAndPositiveEvidenceScores(t *testing.T) {
	t.Parallel()
	rule := Rule{Name: "Preferred subtitle da", Fact: "subtitle_languages", AllowedValues: []string{"da"}, Kind: RulePreferred, Weight: 250, Unknown: UnknownAllow}
	unknown, err := Evaluate(Observation{ID: uuid.NewV7()}, []Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	positive, err := Evaluate(Observation{ID: uuid.NewV7(), Facts: []Fact{{Name: "subtitle_languages", Values: []string{"da"}, State: FactObserved}}}, []Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Verdict != VerdictEligible || unknown.Score != 0 || positive.Score != 250 {
		t.Fatalf("unknown=%+v positive=%+v", unknown, positive)
	}
}
