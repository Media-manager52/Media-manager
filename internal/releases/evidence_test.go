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

func TestRankUsesStableProviderAndNativeIdentity(t *testing.T) {
	firstIndexer := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	secondIndexer := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	stable := Observation{ID: uuid.NewV7(), IndexerID: firstIndexer, NativeGUID: "z", ObservedAt: time.Unix(1, 0)}
	lastResponse := Observation{ID: uuid.NewV7(), IndexerID: secondIndexer, NativeGUID: "a", ObservedAt: time.Unix(2, 0)}
	ranked := Rank([]Observation{stable, lastResponse}, []Evaluation{{ObservationID: stable.ID, Verdict: VerdictEligible, Score: 1}, {ObservationID: lastResponse.ID, Verdict: VerdictEligible, Score: 1}})
	if ranked[0].ObservationID != stable.ID {
		t.Fatal("equally scored candidates were biased by response completion time")
	}
	sameIndexerLater := Observation{ID: uuid.NewV7(), IndexerID: firstIndexer, NativeGUID: "b", ObservedAt: time.Unix(3, 0)}
	ranked = Rank([]Observation{stable, sameIndexerLater}, []Evaluation{{ObservationID: stable.ID, Verdict: VerdictEligible, Score: 1}, {ObservationID: sameIndexerLater.ID, Verdict: VerdictEligible, Score: 1}})
	if ranked[0].ObservationID != sameIndexerLater.ID {
		t.Fatal("same-provider tie was not resolved by durable native identity")
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
