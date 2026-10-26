package acquisition

import (
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"testing"
	"time"
	"uuid"
)

func TestOldBoysCatalogueBindingIsInferredScopedAndPreservesUnknownIDs(t *testing.T) {
	search := numberingSearch()
	search.Media.ID = uuid.NewV7()
	search.Media.Metadata = &catalog.MediaMetadata{OriginalLanguage: "da", OriginalTitle: "Operation X"}
	for _, spec := range []struct {
		title, version, language, id string
		want                         bool
	}{
		{"Operation.X.S2026E02.DANiSH.1080p.WEB", "oldboys-v1", "da", "", true},
		{"Operation.X.2026.06.10.DANiSH.1080p.WEB", "oldboys-v1", "da", "", true},
		{"Operation.X.2010.S23E02.DANiSH.1080p.WEB", "oldboys-v1", "da", "", false},
		{"Operation.X.S2025E02.DANiSH.1080p.WEB", "oldboys-v1", "da", "", false},
		{"Operation.X.S2026E02.DANiSH.1080p.WEB", "newznab-v1", "da", "", false},
		{"Operation.X.S2026E02.DANiSH.1080p.WEB", "oldboys-v1", "en", "", false},
		{"Operation.X.Unrelated.Spinoff.S2026E02.DANiSH.1080p.WEB", "oldboys-v1", "da", "", false},
		{"Operation.X.S2026E02.NORDiC.1080p.WEB", "oldboys-v1", "da", "", true},
		{"Operation.X.S2026E02.DANiSH.1080p.WEB", "oldboys-v1", "da", "tt9999999", false},
	} {
		t.Run(spec.title+spec.version+spec.language+spec.id, func(t *testing.T) {
			search.Media.Metadata.OriginalLanguage = spec.language
			fact := releases.Fact{Name: "imdb_id", State: releases.FactUnknown}
			if spec.id != "" {
				fact.State = releases.FactObserved
				fact.Values = []string{spec.id}
			}
			observation := releases.Observation{Title: spec.title, ParseVersion: spec.version, Facts: []releases.Fact{fact}}
			observation = bindOldBoysCatalogue(releases.InferTitleFacts(observation, time.Now().UTC()), search, time.Now().UTC())
			observation = applyEpisodeNumbering(observation, search, time.Now().UTC())
			rules, _ := workflowScopeRules(search.Variant)
			evaluation, err := releases.Evaluate(observation, rules)
			if err != nil {
				t.Fatal(err)
			}
			evaluation = constrainMediaMatch(observation, evaluation, search.Media)
			if (evaluation.Verdict == releases.VerdictEligible) != spec.want {
				t.Fatalf("evaluation=%+v", evaluation)
			}
			if spec.id == "" {
				for _, f := range observation.Facts {
					if f.Name == "imdb_id" && f.State != releases.FactUnknown {
						t.Fatal("provider ID fabricated")
					}
				}
			}
		})
	}
}
func TestOldBoysSearchVisitsTitleCalendarAndOrdinalPagesWithoutAdditionalCalls(t *testing.T) {
	search := numberingSearch()
	search.Media.Metadata = &catalog.MediaMetadata{OriginalLanguage: "da", OriginalTitle: "Operation X"}
	for phase := uint64(0); phase < 24; phase++ {
		search.CompleteSearches = phase
		got := oldBoysSearchRequest(search)
		want := []string{"Operation X DANiSH", "Operation X 2026 DANiSH", "Operation X S23E02 DANiSH"}[phase%3]
		if got.Name != want || got.Page != 1+int((phase/3)%4) || got.Category != 2 {
			t.Fatalf("phase %d: %+v", phase, got)
		}
	}
	search.EpisodeNumbering.Year = 0
	for phase := uint64(0); phase < 16; phase++ {
		search.CompleteSearches = phase
		got := oldBoysSearchRequest(search)
		want := []string{"Operation X DANiSH", "Operation X S23E02 DANiSH"}[phase%2]
		if got.Name != want || got.Page != 1+int((phase/2)%4) {
			t.Fatalf("nonannual phase %d: %+v", phase, got)
		}
	}
}

func TestOldBoysCollectionAdmissionUsesDeclaredMembershipAndPreservesFacts(t *testing.T) {
	search := numberingSearch()
	search.Media.ID = uuid.NewV7()
	seasons := uint16(23)
	search.Media.Metadata = &catalog.MediaMetadata{OriginalLanguage: "da", OriginalTitle: "Operation X", SeasonCount: &seasons}
	for _, spec := range []struct {
		variant, title, version string
		want                    releases.Verdict
	}{
		{"season:16:episode:06", "Operation.X.S01-S16.DANISH.720p.AAC2.0-PlexLover", "oldboys-v1", releases.VerdictEligible},
		{"season:16", "Operation.X.S01-S16.DANISH.720p.AAC2.0-PlexLover", "oldboys-v1", releases.VerdictEligible},
		{"season:17", "Operation.X.S01-S16.DANISH.720p.AAC2.0-PlexLover", "oldboys-v1", releases.VerdictRejected},
		{"season:16", "Operation.X.S01-S99.DANISH.720p", "oldboys-v1", releases.VerdictHeld},
		{"season:16", "Operation.X.S16-S01.DANISH.720p", "oldboys-v1", releases.VerdictHeld},
		{"season:16", "Operation.X.Spinoff.S01-S16.DANISH.720p", "oldboys-v1", releases.VerdictRejected},
		{"season:16", "Operation.X.S01-S16.DANISH.720p", "newznab-v1", releases.VerdictRejected},
	} {
		t.Run(spec.variant+spec.title+spec.version, func(t *testing.T) {
			search.Variant = spec.variant
			o := releases.InferTitleFacts(releases.Observation{Title: spec.title, ParseVersion: spec.version}, time.Now().UTC())
			o = bindOldBoysCatalogue(o, search, time.Now().UTC())
			scope, _ := workflowScopeRules(search.Variant)
			o, rules := oldBoysRangeScope(o, search, scope, []releases.Rule{{Name: "quality", Fact: "resolution", AllowedValues: []string{"720p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}})
			result, err := releases.Evaluate(o, rules)
			if err != nil {
				t.Fatal(err)
			}
			result = constrainMediaMatch(o, result, search.Media)
			if result.Verdict != spec.want {
				t.Fatalf("got %+v", result)
			}
			for _, fact := range o.Facts {
				if fact.Name == "season_number" && len(fact.Values) == 1 && fact.Values[0] != "1" && spec.title == "Operation.X.S01-S16.DANISH.720p.AAC2.0-PlexLover" {
					t.Fatal("first declared season was rewritten")
				}
				if fact.Name == "episode_number" {
					t.Fatal("pack episode invented")
				}
			}
		})
	}
}
