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

func TestBestAvailablePreferencesRankResolutionBeforeAudio(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{Name: "Prefer 2160p", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: RulePreferred, Weight: 50000, Unknown: UnknownAllow},
		{Name: "Prefer 1080p", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: RulePreferred, Weight: 40000, Unknown: UnknownAllow},
		{Name: "Prefer TrueHD Atmos", Fact: "audio_codec", AllowedValues: []string{"truehd_atmos"}, Kind: RulePreferred, Weight: 5300, Unknown: UnknownAllow},
		{Name: "Prefer AAC", Fact: "audio_codec", AllowedValues: []string{"aac"}, Kind: RulePreferred, Weight: 1500, Unknown: UnknownAllow},
	}
	now := time.Unix(1, 0)
	uhd := InferTitleFacts(Observation{ID: uuid.NewV7(), Title: "Movie.2160p.WEB-DL.AAC2.0"}, now)
	hd := InferTitleFacts(Observation{ID: uuid.NewV7(), Title: "Movie.1080p.Remux.TrueHD.7.1.Atmos"}, now)
	unknown := InferTitleFacts(Observation{ID: uuid.NewV7(), Title: "Movie.Without.Quality.Evidence"}, now)
	evaluations := make([]Evaluation, 0, 3)
	for _, observation := range []Observation{uhd, hd, unknown} {
		evaluation, err := Evaluate(observation, rules)
		if err != nil {
			t.Fatal(err)
		}
		if evaluation.Verdict != VerdictEligible {
			t.Fatalf("best-available evidence was not neutral/eligible: %+v", evaluation)
		}
		evaluations = append(evaluations, evaluation)
	}
	ranked := Rank([]Observation{uhd, hd, unknown}, evaluations)
	if len(ranked) != 3 || ranked[0].ObservationID != uhd.ID || ranked[1].ObservationID != hd.ID || ranked[2].ObservationID != unknown.ID {
		t.Fatalf("unexpected best-available rank order: %+v", ranked)
	}
}

// A required language is satisfied by subtitles or audio. Explicit Danish-audio
// release names pass; a release listing only Swedish subtitles still fails; a
// bare DANISH tag stays unknown and therefore allowed but unscored.
func TestRequiredLanguageAcceptsSubtitleOrAudio(t *testing.T) {
	rules := []Rule{
		{Name: "Required subtitle or audio da", Fact: SubtitleOrAudioLanguagesFact, AllowedValues: []string{"da"}, Kind: RuleRequired, Unknown: UnknownAllow},
		{Name: "Prefer verified required subtitle or audio da", Fact: SubtitleOrAudioLanguagesFact, AllowedValues: []string{"da"}, Kind: RulePreferred, Weight: 100000, Unknown: UnknownAllow},
	}
	now := time.Now()
	for _, tc := range []struct {
		title   string
		facts   []Fact
		verdict Verdict
		score   int
	}{
		{title: "Bluey.S03E01.DKaudio.1080p.WEB.h264-GRP", verdict: VerdictEligible, score: 100000},
		{title: "Paw.Patrol.S01E01.DANISH.DUBBED.720p.WEB-DL", verdict: VerdictEligible, score: 100000},
		{title: "Movie.2020.1080p.BluRay.DKsubs.x264", verdict: VerdictEligible, score: 100000},
		{title: "Movie.2020.DANISH.1080p.WEB.H264", verdict: VerdictEligible, score: 0},
		{title: "Movie.2020.1080p.WEB.H264", facts: []Fact{{Name: "subtitle_languages", Values: []string{"sv"}, State: FactObserved}}, verdict: VerdictRejected},
		{title: "Movie.2020.DKaudio.1080p.WEB.H264", facts: []Fact{{Name: "subtitle_languages", Values: []string{"sv"}, State: FactObserved}}, verdict: VerdictEligible, score: 100000},
	} {
		observation := InferTitleFacts(Observation{ID: uuid.NewV7(), Title: tc.title, Facts: tc.facts}, now)
		evaluation, err := Evaluate(observation, rules)
		if err != nil {
			t.Fatal(err)
		}
		if evaluation.Verdict != tc.verdict || evaluation.Score != tc.score {
			t.Fatalf("%s: verdict=%s score=%d, want %s %d", tc.title, evaluation.Verdict, evaluation.Score, tc.verdict, tc.score)
		}
	}
}
