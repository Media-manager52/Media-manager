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

func TestInferTitleFactsRecordsEpisodeNumberForSingleEpisode(t *testing.T) {
	observation := InferTitleFacts(Observation{Title: "Rivalerne.S01E04.DANiSH.1080p.WEB-DL.H.264.AAC2.0-GROUP"}, time.Unix(1, 0))
	want := map[string]string{"season_number": "1", "episode_number": "4", "release_scope": "episode"}
	for _, fact := range observation.Facts {
		if value, exists := want[fact.Name]; exists && len(fact.Values) == 1 && fact.Values[0] == value {
			delete(want, fact.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("single episode facts missing: %v", want)
	}
}

func TestInferTitleFactsDoesNotRecordEpisodeNumberForRange(t *testing.T) {
	observation := InferTitleFacts(Observation{Title: "Example.Show.S01E01-E10.2160p.WEB-DL"}, time.Unix(1, 0))
	for _, fact := range observation.Facts {
		if fact.Name == "episode_number" {
			t.Fatalf("ranged release exposed an episode number: %+v", fact)
		}
		if fact.Name == "release_scope" && len(fact.Values) == 1 && fact.Values[0] != "season_pack" {
			t.Fatalf("range scope=%q, want season_pack", fact.Values[0])
		}
	}
}

func TestTitleMatchesCanonicalPrefixAndYear(t *testing.T) {
	if !TitleMatches("Blade.Runner.2049.2017.2160p", "Blade Runner 2049") || !TitleContainsYear("Blade.Runner.2049.2017.2160p", 2017) {
		t.Fatal("canonical title and year were not recognized")
	}
	if TitleMatches("Blade Runner 1982 2160p", "Blade Runner 2049") {
		t.Fatal("different title was accepted")
	}
}

func TestInferTitleFactsRecordsBoundedAudioCodecEvidence(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Movie.2026.2160p.Remux.TrueHD.7.1.Atmos":        "truehd_atmos",
		"Movie.2026.2160p.BluRay.DTS-HD.MA.7.1":          "dts_hd_ma",
		"Movie.2026.2160p.BluRay.DTS-HD.5.1":             "dts_hd",
		"Movie.2026.2160p.WEB-DL.DDP5.1.Atmos":           "eac3_atmos",
		"Movie.2026.1080p.WEB-DL.E-AC-3.5.1":             "eac3",
		"Movie.2026.1080p.BluRay.FLAC.2.0":               "flac",
		"Movie.2026.1080p.WEB-DL.AAC2.0":                 "aac",
		"Movie.2026.1080p.WEB-DL.Without.Audio.Metadata": "",
	}
	for title, want := range tests {
		observation := InferTitleFacts(Observation{Title: title}, time.Unix(1, 0))
		got := ""
		for _, fact := range observation.Facts {
			if fact.Name == "audio_codec" && len(fact.Values) == 1 {
				got = fact.Values[0]
			}
		}
		if got != want {
			t.Errorf("%q audio_codec=%q, want %q", title, got, want)
		}
	}
}

func TestInferTitleFactsCanonicalizesUHD(t *testing.T) {
	t.Parallel()
	observation := InferTitleFacts(Observation{Title: "Movie.2026.UHD.WEB-DL"}, time.Unix(1, 0))
	for _, fact := range observation.Facts {
		if fact.Name == "resolution" && fact.State == FactInferred && len(fact.Values) == 1 && fact.Values[0] == "2160p" {
			return
		}
	}
	t.Fatal("UHD was not inferred as canonical 2160p")
}

func TestInferTitleFactsRecordsOnlyExplicitDanishSubtitleEvidence(t *testing.T) {
	t.Parallel()
	positive := []string{
		"Dexter.S02.Retail.DKsubs.1080p.WEB-DL",
		"NCIS.S19.DANiSH.SUBS.1080p.WEB-DL",
		"Movie.2026.da-subtitle.2160p.WEB-DL",
		"Movie.2026.dansk_undertekster.2160p.WEB-DL",
	}
	for _, title := range positive {
		observation := InferTitleFacts(Observation{Title: title}, time.Unix(1, 0))
		if !hasInferredFact(observation.Facts, "subtitle_languages", "da") {
			t.Errorf("%q did not produce inferred canonical Danish subtitle evidence", title)
		}
	}

	unknown := []string{
		"Movie.2026.DANISH.AUDIO.2160p.WEB-DL",
		"Movie.2026.MULTISUBS.2160p.WEB-DL-DANiSHBiTS",
		"Movie.2026.NORDIC.2160p.WEB-DL",
		"Movie.2026.SUBPACK.1080p.WEB-DL",
		"Movie.2026.NLSubs.1080p.WEB-DL",
	}
	for _, title := range unknown {
		observation := InferTitleFacts(Observation{Title: title}, time.Unix(1, 0))
		if hasInferredFact(observation.Facts, "subtitle_languages", "da") {
			t.Errorf("%q was incorrectly treated as Danish subtitle evidence", title)
		}
	}
}

func hasInferredFact(facts []Fact, name, value string) bool {
	for _, fact := range facts {
		if fact.Name == name && fact.State == FactInferred && len(fact.Values) == 1 && fact.Values[0] == value {
			return true
		}
	}
	return false
}
