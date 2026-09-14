package acquisition

import (
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

func TestBuildRequestUsesOnlyCommissionedMovieParameters(t *testing.T) {
	year := uint16(2019)
	search := postgres.SearchContext{Media: catalog.MediaItem{
		Kind:  catalog.KindMovie,
		Title: "Klaus",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{
			{Namespace: "imdb", Value: " TT4729430 ", State: catalog.IdentityVerified},
			{Namespace: "tmdb", Value: " 508965 ", State: catalog.IdentityVerified},
			{Namespace: "tvdb", Value: "ignored", State: catalog.IdentityUnverified},
		},
	}}
	config := indexerConfig{
		Categories:     []int{2000, 2040},
		Extended:       true,
		MaximumLimit:   50,
		SearchDialects: newznab.SearchDialects{Movie: newznab.SearchDialect{Mode: "movie", Params: []string{"q", "imdbid", "tmdbid", "tvdbid", "year"}}},
	}
	request := buildRequest(search, config)
	want := newznab.SearchRequest{
		Mode:       "movie",
		Params:     []newznab.Param{{Name: "q", Value: "Klaus"}, {Name: "imdbid", Value: "4729430"}, {Name: "tmdbid", Value: "508965"}, {Name: "year", Value: "2019"}},
		Categories: []int{2000, 2040},
		Limit:      50,
		Extended:   true,
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request=%#v, want %#v", request, want)
	}
}

func TestRequiredSubtitleEvidenceOutranksUnknownWithoutRejectingUnknown(t *testing.T) {
	t.Parallel()
	rules := appendSubtitleRules(nil, subtitles.Policy{Required: []string{"da"}})
	known := releases.Observation{ID: uuid.NewV7(), Facts: []releases.Fact{{Name: "subtitle_languages", Values: []string{"da"}, State: releases.FactObserved}}}
	unknown := releases.Observation{ID: uuid.NewV7()}

	knownEvaluation, err := releases.Evaluate(known, rules)
	if err != nil {
		t.Fatal(err)
	}
	unknownEvaluation, err := releases.Evaluate(unknown, rules)
	if err != nil {
		t.Fatal(err)
	}
	if knownEvaluation.Verdict != releases.VerdictEligible || knownEvaluation.Score != requiredSubtitleEvidenceWeight {
		t.Fatalf("known evaluation=%+v", knownEvaluation)
	}
	if unknownEvaluation.Verdict != releases.VerdictEligible || unknownEvaluation.Score != 0 {
		t.Fatalf("unknown evidence was not neutral: %+v", unknownEvaluation)
	}
	ranked := releases.Rank([]releases.Observation{unknown, known}, []releases.Evaluation{unknownEvaluation, knownEvaluation})
	if len(ranked) != 2 || ranked[0].ObservationID != known.ID {
		t.Fatalf("positive required subtitle evidence was not ranked first: %+v", ranked)
	}
}

func TestUpgradeBaselineRequiresStrictImprovement(t *testing.T) {
	t.Parallel()
	indexerID := uuid.NewV7()
	current := releases.Observation{ID: uuid.NewV7(), IndexerID: indexerID, NativeGUID: "current", Title: "Movie.1080p"}
	baseline := releases.Evaluation{ObservationID: current.ID, Verdict: releases.VerdictEligible, Score: 40000}

	exact := applyUpgradeBaseline(current, releases.Evaluation{ObservationID: current.ID, Verdict: releases.VerdictEligible, Score: 40000}, &current, &baseline)
	if exact.Verdict != releases.VerdictRejected {
		t.Fatalf("exact current release remained eligible: %+v", exact)
	}
	equalObservation := releases.Observation{ID: uuid.NewV7(), IndexerID: uuid.NewV7(), NativeGUID: "equal"}
	equal := applyUpgradeBaseline(equalObservation, releases.Evaluation{ObservationID: equalObservation.ID, Verdict: releases.VerdictEligible, Score: 40000}, &current, &baseline)
	if equal.Verdict != releases.VerdictRejected {
		t.Fatalf("equal-quality replacement remained eligible: %+v", equal)
	}
	betterObservation := releases.Observation{ID: uuid.NewV7(), IndexerID: uuid.NewV7(), NativeGUID: "better"}
	better := applyUpgradeBaseline(betterObservation, releases.Evaluation{ObservationID: betterObservation.ID, Verdict: releases.VerdictEligible, Score: 50000}, &current, &baseline)
	if better.Verdict != releases.VerdictEligible {
		t.Fatalf("strictly better release was rejected: %+v", better)
	}

	currentFailsNewRequirement := releases.Evaluation{ObservationID: current.ID, Verdict: releases.VerdictRejected, Score: 50000}
	newlyCompliant := applyUpgradeBaseline(betterObservation, releases.Evaluation{ObservationID: betterObservation.ID, Verdict: releases.VerdictEligible, Score: 100}, &current, &currentFailsNewRequirement)
	if newlyCompliant.Verdict != releases.VerdictEligible {
		t.Fatalf("candidate satisfying a requirement the current release fails was rejected: %+v", newlyCompliant)
	}
}

func TestBuildRequestUsesVerifiedTVPackDialect(t *testing.T) {
	year := uint16(2025)
	search := postgres.SearchContext{Variant: "season:1", Media: catalog.MediaItem{
		Kind:  catalog.KindSeries,
		Title: "Adolescence",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{
			{Namespace: "tvdb", Value: " 451609 ", State: catalog.IdentityVerified},
			{Namespace: "tmdb", Value: " 249042 ", State: catalog.IdentityVerified},
			{Namespace: "imdb", Value: " TT13024830 ", State: catalog.IdentityVerified},
		},
	}}
	config := indexerConfig{
		Categories:     []int{5000, 5040},
		Extended:       true,
		MaximumLimit:   250,
		SearchDialects: newznab.SearchDialects{Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "tmdbid", "imdbid", "season", "year", "ep"}}},
	}
	request := buildRequest(search, config)
	want := newznab.SearchRequest{
		Mode:       "tvsearch",
		Params:     []newznab.Param{{Name: "q", Value: "Adolescence"}, {Name: "tvdbid", Value: "451609"}, {Name: "tmdbid", Value: "249042"}, {Name: "imdbid", Value: "13024830"}, {Name: "season", Value: "1"}, {Name: "year", Value: "2025"}},
		Categories: []int{5000, 5040},
		Limit:      100,
		Extended:   true,
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request=%#v, want %#v", request, want)
	}
}

func TestBuildRequestUsesVerifiedTVEpisodeDialect(t *testing.T) {
	year := uint16(2026)
	search := postgres.SearchContext{Variant: "season:01:episode:04", Media: catalog.MediaItem{
		Kind:  catalog.KindSeries,
		Title: "Rivalerne",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{
			{Namespace: "tvdb", Value: "481455", State: catalog.IdentityVerified},
		},
	}}
	config := indexerConfig{
		Categories:     []int{5000, 5040},
		Extended:       true,
		MaximumLimit:   250,
		SearchDialects: newznab.SearchDialects{Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season", "ep", "year"}}},
	}
	request := buildRequest(search, config)
	want := newznab.SearchRequest{
		Mode:       "tvsearch",
		Params:     []newznab.Param{{Name: "q", Value: "Rivalerne"}, {Name: "tvdbid", Value: "481455"}, {Name: "season", Value: "1"}, {Name: "ep", Value: "4"}, {Name: "year", Value: "2026"}},
		Categories: []int{5000, 5040},
		Limit:      100,
		Extended:   true,
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request=%#v, want %#v", request, want)
	}

	// A general-search dialect keeps the exact episode token inside the query
	// rather than emitting unsupported parameters.
	general := buildRequest(search, indexerConfig{Mode: "search", ModeVerified: true})
	if !reflect.DeepEqual(general.Params, []newznab.Param{{Name: "q", Value: "Rivalerne S01E04"}}) {
		t.Fatalf("general episode params=%#v", general.Params)
	}
}

func TestBuildRequestLimitBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		maximum int
		want    int
	}{
		{name: "not advertised", maximum: 0, want: 100},
		{name: "below client ceiling", maximum: 50, want: 50},
		{name: "at client ceiling", maximum: 100, want: 100},
		{name: "above client ceiling", maximum: 250, want: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := buildRequest(
				postgres.SearchContext{Media: catalog.MediaItem{Kind: catalog.KindMovie, Title: "Example"}},
				indexerConfig{MaximumLimit: test.maximum, SearchDialects: newznab.SearchDialects{Movie: newznab.SearchDialect{Mode: "movie", Params: []string{"q"}}}},
			)
			if request.Limit != test.want {
				t.Fatalf("limit=%d, want %d", request.Limit, test.want)
			}
		})
	}
}

func TestBuildRequestGenericFallbackKeepsUnsupportedFieldsInsideQuery(t *testing.T) {
	year := uint16(2024)
	movie := buildRequest(postgres.SearchContext{Media: catalog.MediaItem{Kind: catalog.KindMovie, Title: "Example", Year: &year}}, indexerConfig{Mode: "search", ModeVerified: true})
	if !reflect.DeepEqual(movie.Params, []newznab.Param{{Name: "q", Value: "Example 2024"}}) {
		t.Fatalf("movie params=%#v", movie.Params)
	}
	series := buildRequest(postgres.SearchContext{Variant: "season:3", Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "Example"}}, indexerConfig{Mode: "search", ModeVerified: true})
	if !reflect.DeepEqual(series.Params, []newznab.Param{{Name: "q", Value: "Example S03"}}) {
		t.Fatalf("series params=%#v", series.Params)
	}
}

func TestConstrainSeriesMatchRequiresPositiveIdentityOrYear(t *testing.T) {
	year := uint16(2005)
	media := catalog.MediaItem{
		Kind:  catalog.KindSeries,
		Title: "The Office",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{{
			Namespace: "tvdb",
			Value:     "73244",
			State:     catalog.IdentityVerified,
		}},
	}
	tests := []struct {
		name        string
		title       string
		facts       []releases.Fact
		wantVerdict releases.Verdict
	}{
		{name: "title only held", title: "The.Office.S01.1080p", wantVerdict: releases.VerdictHeld},
		{name: "canonical year accepted", title: "The.Office.2005.S01.1080p", wantVerdict: releases.VerdictEligible},
		{name: "matching identity accepted", title: "The.Office.S01.1080p", facts: []releases.Fact{{Name: "tvdb_id", Values: []string{"73244"}, State: releases.FactObserved}}, wantVerdict: releases.VerdictEligible},
		{name: "wrong identity rejected", title: "The.Office.S01.1080p", facts: []releases.Fact{{Name: "tvdb_id", Values: []string{"99999"}, State: releases.FactObserved}}, wantVerdict: releases.VerdictRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := releases.Observation{Title: test.title, Facts: test.facts}
			evaluation := releases.Evaluation{Verdict: releases.VerdictEligible}
			if got := constrainMediaMatch(observation, evaluation, media).Verdict; got != test.wantVerdict {
				t.Fatalf("verdict=%q, want %q", got, test.wantVerdict)
			}
		})
	}
}

func TestConstrainMovieMatchNormalizesIMDbPrefixOnly(t *testing.T) {
	year := uint16(2019)
	media := catalog.MediaItem{
		Kind:  catalog.KindMovie,
		Title: "Klaus",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{{
			Namespace: "imdb",
			Value:     "tt4729430",
			State:     catalog.IdentityVerified,
		}},
	}
	tests := []struct {
		name        string
		fact        releases.Fact
		wantVerdict releases.Verdict
	}{
		{name: "numeric observation matches canonical IMDb", fact: releases.Fact{Name: "imdb_id", Values: []string{"4729430"}, State: releases.FactObserved}, wantVerdict: releases.VerdictEligible},
		{name: "canonical observation matches", fact: releases.Fact{Name: "imdb_id", Values: []string{"tt4729430"}, State: releases.FactObserved}, wantVerdict: releases.VerdictEligible},
		{name: "different IMDb rejected", fact: releases.Fact{Name: "imdb_id", Values: []string{"4729431"}, State: releases.FactObserved}, wantVerdict: releases.VerdictRejected},
		{name: "malformed IMDb rejected", fact: releases.Fact{Name: "imdb_id", Values: []string{"Klaus"}, State: releases.FactObserved}, wantVerdict: releases.VerdictRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := releases.Observation{Title: "Klaus.2019.1080p.WEB-DL", Facts: []releases.Fact{test.fact}}
			evaluation := releases.Evaluation{Verdict: releases.VerdictEligible}
			if got := constrainMediaMatch(observation, evaluation, media).Verdict; got != test.wantVerdict {
				t.Fatalf("verdict=%q, want %q", got, test.wantVerdict)
			}
		})
	}
}

func TestApplyReleaseBlocksUsesNarrowScope(t *testing.T) {
	indexerID, otherIndexerID, mediaID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	search := postgres.SearchContext{Media: catalog.MediaItem{ID: mediaID}, Variant: "season:03", Blocks: []postgres.ReleaseBlock{
		{Scope: "native_release", IndexerInstanceID: &indexerID, NativeKey: "guid-1", ReleaseTitle: "Exact.Native", Reason: "passworded payload"},
		{Scope: "pack_release", MediaID: &mediaID, Variant: "season:03", ReleaseTitle: "Series.S03.PACK-GROUP", Reason: "missing expected episode"},
	}}
	eligible := releases.Evaluation{Verdict: releases.VerdictEligible}
	tests := []struct {
		name        string
		observation releases.Observation
		want        releases.Verdict
	}{
		{name: "native exact", observation: releases.Observation{IndexerID: indexerID, NativeGUID: "guid-1", Title: "Anything"}, want: releases.VerdictRejected},
		{name: "same guid other indexer", observation: releases.Observation{IndexerID: otherIndexerID, NativeGUID: "guid-1", Title: "Anything"}, want: releases.VerdictEligible},
		{name: "pack exact title", observation: releases.Observation{IndexerID: otherIndexerID, NativeGUID: "guid-2", Title: "series.s03.pack-group"}, want: releases.VerdictRejected},
		{name: "unrelated pack", observation: releases.Observation{IndexerID: otherIndexerID, NativeGUID: "guid-3", Title: "Series.S03.Other-GROUP"}, want: releases.VerdictEligible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := applyReleaseBlocks(test.observation, eligible, search).Verdict; got != test.want {
				t.Fatalf("verdict=%q, want %q", got, test.want)
			}
		})
	}
}

// TestWorkflowReleaseExclusionOnlyRejectsExactNativeRelease pins the durable
// exclusion scope: a failed release is rejected by exact indexer plus native
// key, while a different release on the same indexer and the same native key
// on a different indexer remain eligible.
func TestWorkflowReleaseExclusionOnlyRejectsExactNativeRelease(t *testing.T) {
	t.Parallel()
	indexerID, otherIndexerID, candidateID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	search := postgres.SearchContext{Exclusions: []postgres.WorkflowReleaseExclusion{{
		CandidateID:       candidateID,
		IndexerInstanceID: indexerID,
		NativeKey:         "bad-guid",
		FailureKind:       "release_retrieval",
		Reason:            "provider rejected retrieval",
	}}}
	eligible := releases.Evaluation{Verdict: releases.VerdictEligible}
	tests := []struct {
		name        string
		observation releases.Observation
		want        releases.Verdict
	}{
		{name: "exact excluded release", observation: releases.Observation{IndexerID: indexerID, NativeGUID: "bad-guid"}, want: releases.VerdictRejected},
		{name: "different release same indexer", observation: releases.Observation{IndexerID: indexerID, NativeGUID: "good-guid"}, want: releases.VerdictEligible},
		{name: "same native key other indexer", observation: releases.Observation{IndexerID: otherIndexerID, NativeGUID: "bad-guid"}, want: releases.VerdictEligible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := applyWorkflowExclusions(test.observation, eligible, search).Verdict; got != test.want {
				t.Fatalf("verdict=%q, want %q", got, test.want)
			}
		})
	}
}

func TestWorkflowScopeRulesSeparateEpisodeAndPackScope(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	evaluate := func(t *testing.T, variant, title string) releases.Verdict {
		t.Helper()
		rules, err := workflowScopeRules(variant)
		if err != nil {
			t.Fatalf("rules for %q: %v", variant, err)
		}
		observation := releases.InferTitleFacts(releases.Observation{Title: title}, now)
		evaluation, err := releases.Evaluate(observation, rules)
		if err != nil {
			t.Fatalf("evaluate %q: %v", title, err)
		}
		return evaluation.Verdict
	}

	// Airing episode workflow: exactly the released episode is eligible.
	if verdict := evaluate(t, "season:01:episode:01", "Rivalerne.2026.S01E01.DANiSH.1080p.WEB-DL.H.264.AAC2.0-SHOWTiME"); verdict != releases.VerdictEligible {
		t.Fatalf("released E01 verdict=%q, want eligible", verdict)
	}
	if verdict := evaluate(t, "season:01:episode:01", "Rivalerne.S01E02.DANiSH.1080p.WEB-DL.H.264.AAC2.0-SHOWTiME"); verdict != releases.VerdictRejected {
		t.Fatalf("future E02 verdict=%q, want rejected for the E01 workflow", verdict)
	}
	if verdict := evaluate(t, "season:01:episode:01", "Rivalerne.S01.DANiSH.1080p.WEB-DL.H.264.AAC2.0-SHOWTiME"); verdict != releases.VerdictRejected {
		t.Fatalf("season pack verdict=%q, want rejected for an episode workflow", verdict)
	}

	// Complete entirely-missing season workflow: only a pack is eligible.
	if verdict := evaluate(t, "season:01", "Rivalerne.S01.DANiSH.1080p.WEB-DL.H.264.AAC2.0-SHOWTiME"); verdict != releases.VerdictEligible {
		t.Fatalf("season pack verdict=%q, want eligible", verdict)
	}
	if verdict := evaluate(t, "season:01", "Rivalerne.S01E01.DANiSH.1080p.WEB-DL.H.264.AAC2.0-SHOWTiME"); verdict != releases.VerdictRejected {
		t.Fatalf("single episode verdict=%q, want rejected for a season workflow", verdict)
	}

	if rules, err := workflowScopeRules("default"); err != nil || len(rules) != 0 {
		t.Fatalf("default rules=%v err=%v", rules, err)
	}
	if _, err := workflowScopeRules("season:bad"); err == nil {
		t.Fatal("invalid season variant was accepted")
	}
}
