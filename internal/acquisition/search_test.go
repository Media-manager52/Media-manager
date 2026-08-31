package acquisition

import (
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

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
