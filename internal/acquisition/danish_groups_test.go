package acquisition

import (
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
	"uuid"
)

func danishGroupEntry(title string, facts ...releases.Fact) postgres.EvaluatedObservation {
	id := uuid.NewV7()
	return postgres.EvaluatedObservation{
		Observation: releases.Observation{ID: id, Title: title, Facts: facts},
		Evaluation:  releases.Evaluation{ObservationID: id, Verdict: releases.VerdictEligible, Score: 10},
	}
}

func TestHoldUnlikelyDanishGroupsHoldsOnlyProvenBadGroups(t *testing.T) {
	t.Parallel()
	records := postgres.DanishGroupRecords{
		Subtitles: map[string]postgres.DanishGroupRecord{
			"megusta": {Verified: 10, Missing: 330},
			"edge":    {Verified: 1, Missing: 19},  // 5 %: held
			"over":    {Verified: 2, Missing: 18},  // 10 %: tried
			"young":   {Verified: 0, Missing: 19},  // too little record
			"kyr":     {Verified: 377, Missing: 0}, // always Danish
		},
		Audio: map[string]postgres.DanishGroupRecord{"kyr": {Verified: 0, Missing: 40}},
	}
	daSubs := subtitles.Policy{Required: []string{"da"}}
	entries := []postgres.EvaluatedObservation{
		danishGroupEntry("Show.S01E01.1080p.WEB-DL-MeGusta"),
		danishGroupEntry("Show.S01E01.1080p.WEB-DL-edge.mkv"),
		danishGroupEntry("Show.S01E01.1080p.WEB-DL-over"),
		danishGroupEntry("Show.S01E01.1080p.WEB-DL-young"),
		danishGroupEntry("Show.S01E01.1080p.WEB-DL-KYR"),
		danishGroupEntry("Show.S01E01.NORDiC.1080p.WEB-DL-MeGusta"),
		danishGroupEntry("Show.S01E01.1080p.WEB-DL-MeGusta", releases.Fact{Name: "subtitle_languages", State: releases.FactObserved, Values: []string{"da"}}),
		danishGroupEntry("Show S01E01 1080p"),
	}
	rejected := danishGroupEntry("Show.S01E02.1080p.WEB-DL-MeGusta")
	rejected.Evaluation.Verdict = releases.VerdictRejected
	entries = append(entries, rejected)
	holdUnlikelyDanishGroups(entries, daSubs, records)
	want := []releases.Verdict{releases.VerdictHeld, releases.VerdictHeld, releases.VerdictEligible, releases.VerdictEligible, releases.VerdictEligible, releases.VerdictEligible, releases.VerdictEligible, releases.VerdictEligible, releases.VerdictRejected}
	for index, entry := range entries {
		if entry.Evaluation.Verdict != want[index] {
			t.Fatalf("%s: verdict=%s want %s", entry.Observation.Title, entry.Evaluation.Verdict, want[index])
		}
	}
	last := entries[0].Evaluation.Explanations[len(entries[0].Evaluation.Explanations)-1]
	if last.Rule != DanishGroupRuleName || last.Decision != releases.DecisionFail || last.Reason != "release group megusta had the required Danish in 10 of 340 measured downloads" {
		t.Fatalf("explanation=%+v", last)
	}

	// Danish-audio profiles use the audio record.
	audio := []postgres.EvaluatedObservation{danishGroupEntry("Show.S01E01.1080p.WEB-DL-KYR")}
	holdUnlikelyDanishGroups(audio, subtitles.Policy{RequiredAudio: []string{"da"}}, records)
	if audio[0].Evaluation.Verdict != releases.VerdictHeld {
		t.Fatal("a group without Danish audio must be held for a Danish-audio profile")
	}
	// No Danish requirement, or a title originally in Danish: nothing held.
	for _, policy := range []subtitles.Policy{{}, subtitles.Policy{Required: []string{"da"}}.WithOriginalLanguage("da")} {
		entry := []postgres.EvaluatedObservation{danishGroupEntry("Show.S01E01.1080p.WEB-DL-MeGusta")}
		holdUnlikelyDanishGroups(entry, policy, records)
		if entry[0].Evaluation.Verdict != releases.VerdictEligible {
			t.Fatalf("policy %+v held a release", policy)
		}
	}
}
