package releases

import (
	"testing"

	"uuid"
)

func TestReleaseGroup(t *testing.T) {
	t.Parallel()
	for title, want := range map[string]string{
		"Show.S01E01.1080p.WEB-DL-NTb":            "ntb",
		"Show.S01E01.1080p.WEB.h264-SALT.mkv":     "salt",
		"Show S01E01 1080p WEB-DL DDP2.0-MeGusta": "megusta",
		"Rugrats.S09E14.German.FS.dTV.XviD":       "",
		"Show.S01E01.1080p.WEB-DL-":               "",
		"Show.S01E01-GRP[rarbg]":                  "",
	} {
		if got := ReleaseGroup(title); got != want {
			t.Errorf("ReleaseGroup(%q)=%q want %q", title, got, want)
		}
	}
}

func TestDanishRankingTriesGroupsThatUsuallyHaveDanishFirst(t *testing.T) {
	t.Parallel()
	release := func(title string, score int) (Observation, Evaluation) {
		id := uuid.NewV7()
		return Observation{ID: id, Title: title}, Evaluation{ObservationID: id, Verdict: VerdictEligible, Score: score}
	}
	lowObs, lowEval := release("Show.S01E01.2160p.WEB-DL-LOW", 30)
	unknownObs, unknownEval := release("Show.S01E01.1080p.WEB-DL-NEW", 20)
	goodObs, goodEval := release("Show.S01E01.720p.WEB-DL-GOOD", 10)
	taggedObs, taggedEval := release("Show.S01E01.DANiSH.720p.WEB-DL-LOW", 5)
	options := RankOptions{DanishGroupShare: map[string]float64{"low": 0.05, "good": 0.9}}
	ranked := RankForDanishSubtitlesWith(
		[]Observation{lowObs, unknownObs, goodObs, taggedObs},
		[]Evaluation{lowEval, unknownEval, goodEval, taggedEval}, options)
	want := []Observation{taggedObs, goodObs, unknownObs, lowObs}
	for index, observation := range want {
		if ranked[index].ObservationID != observation.ID {
			t.Fatalf("position %d: got %s want %s", index, ranked[index].ObservationID, observation.Title)
		}
	}
}
