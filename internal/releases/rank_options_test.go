package releases

import (
	"testing"

	"uuid"
)

func TestRankWithPrefersReliableIndexerOnlyOnEqualScore(t *testing.T) {
	t.Parallel()
	weak, strong := uuid.MustParse("00000000-0000-7000-8000-0000000000a1"), uuid.MustParse("00000000-0000-7000-8000-0000000000b2")
	a := Observation{ID: uuid.MustParse("00000000-0000-7000-8000-000000000001"), IndexerID: weak, NativeGUID: "a"}
	b := Observation{ID: uuid.MustParse("00000000-0000-7000-8000-000000000002"), IndexerID: strong, NativeGUID: "b"}
	c := Observation{ID: uuid.MustParse("00000000-0000-7000-8000-000000000003"), IndexerID: weak, NativeGUID: "c"}
	observations := []Observation{a, b, c}
	evaluations := []Evaluation{
		{ObservationID: a.ID, Verdict: VerdictEligible, Score: 10},
		{ObservationID: b.ID, Verdict: VerdictEligible, Score: 10},
		{ObservationID: c.ID, Verdict: VerdictEligible, Score: 11},
	}
	if got := Rank(observations, evaluations); got[0].ObservationID != c.ID || got[1].ObservationID != a.ID {
		t.Fatalf("without options the stable indexer order must hold: %v", got)
	}
	options := RankOptions{IndexerReliability: map[uuid.UUID]float64{weak: 0.02, strong: 0.86}}
	got := RankWith(observations, evaluations, options)
	if got[0].ObservationID != c.ID {
		t.Fatal("reliability must never outrank a higher policy score")
	}
	if got[1].ObservationID != b.ID || got[2].ObservationID != a.ID {
		t.Fatalf("equal scores must try the more reliable indexer first: %v", got)
	}
	// An indexer without history ranks at the default share.
	options = RankOptions{IndexerReliability: map[uuid.UUID]float64{weak: 0.02}}
	if got := RankWith(observations, evaluations, options); got[1].ObservationID != b.ID {
		t.Fatalf("unknown indexer must outrank one known to be worse than the default: %v", got)
	}
}

func TestRankWithTriesProviderMarkedArchivesLastWithinVerdict(t *testing.T) {
	t.Parallel()
	indexer := uuid.MustParse("00000000-0000-7000-8000-0000000000a1")
	marked := Observation{ID: uuid.MustParse("00000000-0000-7000-8000-000000000001"), IndexerID: indexer, NativeGUID: "a", Fields: []RawField{{Name: "Password", Value: " 2 "}}}
	clean := Observation{ID: uuid.MustParse("00000000-0000-7000-8000-000000000002"), IndexerID: indexer, NativeGUID: "b", Fields: []RawField{{Name: "password", Value: "0"}}}
	held := Observation{ID: uuid.MustParse("00000000-0000-7000-8000-000000000003"), IndexerID: indexer, NativeGUID: "c"}
	got := Rank([]Observation{marked, clean, held}, []Evaluation{
		{ObservationID: marked.ID, Verdict: VerdictEligible, Score: 50},
		{ObservationID: clean.ID, Verdict: VerdictEligible, Score: 5},
		{ObservationID: held.ID, Verdict: VerdictHeld, Score: 90},
	})
	if got[0].ObservationID != clean.ID || got[1].ObservationID != marked.ID || got[2].ObservationID != held.ID {
		t.Fatalf("marked archive must come after clean eligible releases but keep its verdict: %v", got)
	}
	for value, want := range map[string]bool{"-1": false, "0": false, "1": false, "2": true, "10": true, "": false} {
		if ProviderMarksUnusable([]RawField{{Name: "password", Value: value}}) != want {
			t.Fatalf("password=%q: want %v", value, want)
		}
	}
}
