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
		{"Operation.X.S2026E02.NORDiC.1080p.WEB", "oldboys-v1", "da", "", false},
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
func TestOldBoysSearchAlternatesCalendarWithoutForeignIDsOrAdditionalCalls(t *testing.T) {
	search := numberingSearch()
	search.Media.Metadata = &catalog.MediaMetadata{OriginalLanguage: "da", OriginalTitle: "Operation X"}
	if got := oldBoysSearchRequest(search); got.Name != "Operation X 2026 DANiSH" || got.Category != 2 || got.Page != 1 {
		t.Fatalf("ordinal=%+v", got)
	}
	search.CompleteSearches = 1
	if got := oldBoysSearchRequest(search); got.Name != "Operation X S23E02 DANiSH" || got.Page != 1 {
		t.Fatalf("calendar=%+v", got)
	}
	search.CompleteSearches = 8
	if got := oldBoysSearchRequest(search); got.Page != 2 {
		t.Fatal("bounded pagination did not advance")
	}
	search.CompleteSearches = 32
	if got := oldBoysSearchRequest(search); got.Page != 1 {
		t.Fatal("pagination exceeded bound")
	}
}
