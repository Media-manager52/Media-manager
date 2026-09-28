package releases

import (
	"reflect"
	"testing"
	"uuid"
)

func TestDanishRankingUsesCluesWithoutChangingEvidenceOrEligibility(t *testing.T) {
	observations := []Observation{
		{ID: uuid.NewV7(), Title: "Movie.2026.NLsubs"},
		{ID: uuid.NewV7(), Title: "Movie.2026.NORDiC"},
		{ID: uuid.NewV7(), Title: "Movie.2026", Facts: []Fact{{Name: "subtitle_languages", State: FactObserved, Values: []string{"da"}}}},
		{ID: uuid.NewV7(), Title: "Wrong.Movie.2026.DANiSH"},
	}
	evaluations := []Evaluation{
		{ObservationID: observations[0].ID, Verdict: VerdictEligible, Score: 999999},
		{ObservationID: observations[1].ID, Verdict: VerdictEligible, Score: 1000},
		{ObservationID: observations[2].ID, Verdict: VerdictEligible, Score: 1},
		{ObservationID: observations[3].ID, Verdict: VerdictRejected, Score: 9999999},
	}
	ranked := RankForDanishSubtitles(observations, evaluations)
	for i, want := range []int{2, 1, 0, 3} {
		if !reflect.DeepEqual(ranked[i], evaluations[want]) {
			t.Fatalf("rank[%d]=%+v, want %+v", i, ranked[i], evaluations[want])
		}
	}
	if len(observations[1].Facts) != 0 {
		t.Fatal("Nordic clue became a subtitle fact")
	}
	for _, title := range []string{"Nordicland.2026", "Movie.Danishness", "Movie.2026.MULTiSUBS", "Movie.2026.NLsubs"} {
		if danishSubtitleHint.MatchString(title) {
			t.Fatalf("false clue: %s", title)
		}
	}
	for _, title := range []string{"Movie.NORDiC.2026", "Movie.DANiSH.2026", "Movie.DKsubs.2026", "Movie-Dansk-2026"} {
		if !danishSubtitleHint.MatchString(title) {
			t.Fatalf("missing clue: %s", title)
		}
	}
}
