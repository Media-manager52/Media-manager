package releases

import (
	"testing"
	"time"
)

func TestSeasonPackDoesNotBecomeEpisode(t *testing.T) {
	observation := InferTitleFacts(Observation{Title: "Show.S01.2160p.WEB-DL.HEVC"}, time.Now())
	found := false
	for _, fact := range observation.Facts {
		if fact.Name == "release_scope" && fact.Values[0] == "season_pack" {
			found = true
		}
	}
	if !found {
		t.Fatal("season pack scope was not inferred")
	}
}

func TestInferTitleFactsRecordsCanonicalSeason(t *testing.T) {
	observation := InferTitleFacts(Observation{Title: "Example.Show.S002.2160p.WEB-DL"}, time.Now())
	for _, fact := range observation.Facts {
		if fact.Name == "season_number" && len(fact.Values) == 1 && fact.Values[0] == "2" {
			return
		}
	}
	t.Fatal("expected canonical season_number=2")
}

func TestInferRangedEpisodeTitleAsSeasonPack(t *testing.T) {
	observation := InferTitleFacts(Observation{Title: "Example.Show.S01E01-E10.2160p.WEB-DL"}, time.Now())
	want := map[string]string{"season_number": "1", "release_scope": "season_pack"}
	for _, fact := range observation.Facts {
		if value, exists := want[fact.Name]; exists && len(fact.Values) == 1 && fact.Values[0] == value {
			delete(want, fact.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("ranged pack facts missing: %v", want)
	}
}

func TestEpisodeEncodingTokenDoesNotBecomeSeasonPack(t *testing.T) {
	observation := InferTitleFacts(Observation{Title: "Example.Show.S01E01-10bit.1080p.WEB-DL"}, time.Now())
	for _, fact := range observation.Facts {
		if fact.Name == "release_scope" && len(fact.Values) == 1 {
			if fact.Values[0] != "episode" {
				t.Fatalf("scope=%q, want episode", fact.Values[0])
			}
			return
		}
	}
	t.Fatal("episode scope was not inferred")
}

func TestTitleMatchesCanonicalPrefixAndYear(t *testing.T) {
	if !TitleMatches("Blade.Runner.2049.2017.2160p", "Blade Runner 2049") || !TitleContainsYear("Blade.Runner.2049.2017.2160p", 2017) {
		t.Fatal("canonical title and year were not recognized")
	}
	if TitleMatches("Blade Runner 1982 2160p", "Blade Runner 2049") {
		t.Fatal("different title was accepted")
	}
}
