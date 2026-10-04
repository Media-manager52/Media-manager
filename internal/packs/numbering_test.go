package packs

import (
	"testing"
	"uuid"
)

func annualTestEpisodes() []ExpectedEpisode {
	return []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:79591", Season: 23, Episode: 1}, ProviderEpisodeID: "tmdb:1", AirDate: "2026-04-15"}, {Key: EpisodeKey{SeriesID: "tvdb:79591", Season: 23, Episode: 2}, ProviderEpisodeID: "tmdb:2", AirDate: "2026-06-10"}}
}

func TestAnnualNumberingMatchesFrozenEpisodes(t *testing.T) {
	n := AnnualEpisodeNumbering(23, annualTestEpisodes())
	for _, c := range []struct {
		name      string
		episode   uint16
		broadcast bool
	}{
		{"Operation.X.S2026E01.DANiSH.mkv", 1, true}, {"Operation.X.2026.06.10.Flaaet.Af.Skat.mkv", 2, true},
		{"Operation.X.S23E01.DANiSH.mkv", 0, false}, {"Operation.X.S23E01.2026.04.15.mkv", 0, false}, {"Operation.X.S2025E01.mkv", 0, true}, {"Operation.X.S2026E03.mkv", 0, true},
		{"Operation.X.2026.06.11.mkv", 0, true}, {"Operation.X.2026.02.30.mkv", 0, true},
		{"Operation.X.S2026E01-E02.mkv", 0, true}, {"Operation.X.S2026E01-02.mkv", 0, true}, {"Operation.X.2026.06.10.E01.mkv", 0, true},
		{"Operation.X.S2026E01E02.mkv", 0, true}, {"Operation.X.S2026E01-S2026E02.mkv", 0, true},
		{"Operation.X.S2026E01.S23E02.mkv", 0, true}, {"Operation.X.S2026E01.2026.06.10.mkv", 0, true},
		{"Operation.X.XS2026E01.mkv", 0, true}, {"Operation.X.S2026E0100.mkv", 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, b := n.BroadcastEpisode(c.name)
			if e != c.episode || b != c.broadcast {
				t.Fatalf("episode=%d broadcast=%v, want %d/%v", e, b, c.episode, c.broadcast)
			}
		})
	}
}
func TestAnnualNumberingRejectsIncompleteOrSplitSnapshots(t *testing.T) {
	for _, mutate := range []func([]ExpectedEpisode) []ExpectedEpisode{
		func(e []ExpectedEpisode) []ExpectedEpisode { e[1].AirDate = ""; return e },
		func(e []ExpectedEpisode) []ExpectedEpisode { e[1].AirDate = "2025-06-10"; return e },
		func(e []ExpectedEpisode) []ExpectedEpisode { e[1].Key.Episode = 3; return e },
		func(e []ExpectedEpisode) []ExpectedEpisode { return e[1:] },
		func(e []ExpectedEpisode) []ExpectedEpisode { e[1].Key.Season = 22; return e },
	} {
		if n := AnnualEpisodeNumbering(23, mutate(annualTestEpisodes())); n.Year != 0 {
			t.Fatalf("ambiguous snapshot accepted: %+v", n)
		}
	}
	e := annualTestEpisodes()
	e[1].AirDate = e[0].AirDate
	n := AnnualEpisodeNumbering(23, e)
	if got, _ := n.BroadcastEpisode("Operation.X.2026.04.15.mkv"); got != 0 {
		t.Fatal("duplicate air date was mapped")
	}
}
func TestNumberedInventoryPreservesExactCoverageAndPaths(t *testing.T) {
	expected := annualTestEpisodes()
	n := AnnualEpisodeNumbering(23, expected)
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Operation.X.S2026E01.DANiSH.mkv", Size: 100}, {ID: uuid.NewV7(), RelativePath: "Operation.X.2026.06.10.DANiSH.mkv", Size: 100}}
	report := EvaluateNumbered("tvdb:79591", 23, expected, assets, n)
	if !report.Publishable() {
		t.Fatalf("annual inventory rejected: %+v", report)
	}
	if assets[0].RelativePath != "Operation.X.S2026E01.DANiSH.mkv" {
		t.Fatal("source renamed")
	}
	assets = append(assets, ObservedAsset{ID: uuid.NewV7(), RelativePath: "Operation.X.S23E02.mkv", Size: 100})
	if r := EvaluateNumbered("tvdb:79591", 23, expected, assets, n); r.Publishable() {
		t.Fatal("duplicate episode published")
	}
	assets = assets[:2]
	assets[1].RelativePath = "Operation.X.S2025E02.mkv"
	if r := EvaluateNumbered("tvdb:79591", 23, expected, assets, n); r.Publishable() {
		t.Fatal("wrong-year inventory published")
	}
	if r := EvaluateNumbered("tvdb:79591", 23, expected, assets, EpisodeNumbering{}); r.Publishable() {
		t.Fatal("year inferred without mapping")
	}
}

func TestAnnualSeasonPackLabelsKeepExactInventoryGate(t *testing.T) {
	n := AnnualEpisodeNumbering(23, annualTestEpisodes())
	for _, c := range []struct {
		name             string
		match, broadcast bool
	}{
		{"Operation.X.S2026.DANiSH.1080p.WEB-DL", true, true},
		{"Operation.X.S2025.DANiSH.1080p.WEB-DL", false, true},
		{"Operation.X.S2026E01.DANiSH", false, false}, {"Operation.X.S2026-E01.DANiSH", false, true},
		{"Operation.X.S2026.S2025.DANiSH", false, true},
		{"Operation.X.XS2026.DANiSH", false, true},
	} {
		matched, broadcast := n.BroadcastSeasonPack(c.name)
		if matched != c.match || broadcast != c.broadcast {
			t.Fatalf("%s: %v/%v", c.name, matched, broadcast)
		}
	}
	expected := annualTestEpisodes()
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Operation.X.S2026.mkv", Size: 100}}
	if r := EvaluateNumbered("tvdb:79591", 23, expected, assets, n); r.Publishable() {
		t.Fatal("bare year pack file passed without episode coverage")
	}
}
