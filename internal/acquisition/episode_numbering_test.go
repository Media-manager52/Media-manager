package acquisition

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

func numberingSearch() postgres.SearchContext {
	year := uint16(2004)
	return postgres.SearchContext{Variant: "season:23:episode:02", Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "Operation X", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "imdb", Value: "tt0451553", State: catalog.IdentityVerified}}}, EpisodeNumbering: packs.AnnualEpisodeNumbering(23, []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 23, Episode: 1}, AirDate: "2026-04-15"}, {Key: packs.EpisodeKey{Season: 23, Episode: 2}, AirDate: "2026-06-10"}})}
}
func TestAnnualSearchAlternatesWithoutAdditionalProviderCalls(t *testing.T) {
	s := numberingSearch()
	req := newznab.SearchRequest{Mode: "tvsearch", Limit: 100, Extended: true, Categories: []int{5000}, Params: []newznab.Param{{Name: "q", Value: "Operation X"}, {Name: "imdbid", Value: "0451553"}, {Name: "season", Value: "23"}, {Name: "ep", Value: "2"}}}
	if got := numberedSearchRequest(s, req, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)); len(got.Params) != 4 {
		t.Fatal("primary changed")
	}
	s.CompleteSearches = 1
	got := numberedSearchRequest(s, req, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC))
	if len(got.Params) != 1 || got.Params[0].Name != "imdbid" || got.Limit != 100 || !got.Extended || got.Categories[0] != 5000 {
		t.Fatalf("alternate=%+v", got)
	}
	s.CompleteSearches = 2
	if got = numberedSearchRequest(s, req, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)); len(got.Params) != 4 {
		t.Fatal("primary did not rotate back")
	}
	s.CompleteSearches = 1
	req.Params = []newznab.Param{{Name: "q", Value: "Operation X"}, {Name: "season", Value: "23"}}
	if got = numberedSearchRequest(s, req, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)); len(got.Params) != 2 {
		t.Fatal("unidentified broad query enabled")
	}
}
func TestAnnualSearchAlternateSkipsOldTargets(t *testing.T) {
	s := numberingSearch()
	s.Variant = "season:33:episode:29"
	s.EpisodeNumbering = packs.AnnualEpisodeNumbering(33, []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 33, Episode: 29}, AirDate: "2025-07-21"}})
	s.CompleteSearches = 1
	req := newznab.SearchRequest{Mode: "tvsearch", Limit: 100, Extended: true, Categories: []int{5000}, Params: []newznab.Param{{Name: "q", Value: "Raw"}, {Name: "tvdbid", Value: "76779"}, {Name: "season", Value: "33"}, {Name: "ep", Value: "29"}}}
	got := numberedSearchRequest(s, req, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC))
	if len(got.Params) != 4 {
		t.Fatalf("old target used the ID-only alternate: %+v", got.Params)
	}
}

func TestAnnualReleaseMappingRequiresIdentityAndFrozenScope(t *testing.T) {
	s := numberingSearch()
	now := time.Now().UTC()
	for _, c := range []struct {
		title, id string
		eligible  bool
	}{
		{"Operation.X.S2026E02.DANiSH.1080p.WEB.mkv", "0451553", true},
		{"Operation.X.2026.06.10.Flaaet.Af.Skat.DANiSH.1080p.WEB.mkv", "0451553", true},
		{"Operation.X.S2025E02.DANiSH.mkv", "0451553", false},
		{"Operation.X.S2026E02.DANiSH.mkv", "", false},
		{"Operation.X.S2026E02.DANiSH.mkv", "9999999", false},
		{"Other.Show.S2026E02.DANiSH.mkv", "0451553", false},
	} {
		t.Run(c.title+"/"+c.id, func(t *testing.T) {
			o := releases.Observation{Title: c.title, Facts: []releases.Fact{{Name: "season_number", Values: []string{"20"}, State: releases.FactObserved}, {Name: "episode", Values: []string{"E00"}, State: releases.FactObserved}}}
			if c.id != "" {
				o.Facts = append(o.Facts, releases.Fact{Name: "imdb_id", Values: []string{c.id}, State: releases.FactObserved})
			}
			o = applyEpisodeNumbering(releases.InferTitleFacts(o, now), s, now)
			rules, _ := workflowScopeRules(s.Variant)
			evaluation, err := releases.Evaluate(o, rules)
			if err != nil {
				t.Fatal(err)
			}
			if (evaluation.Verdict == releases.VerdictEligible) != c.eligible {
				t.Fatalf("evaluation=%+v facts=%+v", evaluation, o.Facts)
			}
			original := false
			for _, f := range o.Facts {
				if f.Name == "unmapped_season_number" && f.Values[0] == "20" {
					original = true
				}
			}
			if !original {
				t.Fatal("original provider season evidence discarded")
			}
		})
	}
}

func TestAnnualPackMapsOnlyToCompleteSeasonWorkflow(t *testing.T) {
	s := numberingSearch()
	o := releases.Observation{Title: "Operation.X.S2026.DANiSH.1080p.WEB-DL", Facts: []releases.Fact{{Name: "imdb_id", Values: []string{"0451553"}, State: releases.FactObserved}, {Name: "season_number", Values: []string{"S2026"}, State: releases.FactObserved}}}
	o = applyEpisodeNumbering(releases.InferTitleFacts(o, time.Now().UTC()), s, time.Now().UTC())
	rules, _ := workflowScopeRules("season:23")
	e, err := releases.Evaluate(o, rules)
	if err != nil || e.Verdict != releases.VerdictEligible {
		t.Fatalf("annual pack blocked: %+v %v", e, err)
	}
	rules, _ = workflowScopeRules(s.Variant)
	e, err = releases.Evaluate(o, rules)
	if err != nil || e.Verdict != releases.VerdictRejected {
		t.Fatalf("episode workflow accepted whole pack: %+v %v", e, err)
	}
}
