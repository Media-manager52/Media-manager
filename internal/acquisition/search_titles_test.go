package acquisition

import (
	"testing"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestSearchTitlesRotateSavedNamesWithoutExtraRequests(t *testing.T) {
	media := catalog.MediaItem{Kind: catalog.KindSeries, Title: "Time travels", Metadata: &catalog.MediaMetadata{
		OriginalTitle: "Tidsrejsen",
		Aliases: []catalog.MetadataAlias{
			{Name: "Tidsrejsen", Language: "da", Source: "tmdb"},
			{Name: "Tidsrejsen 2014", Language: "DK", Source: "tmdb"},
			{Name: "Untrusted name"},
		},
	}}
	config := indexerConfig{SearchDialects: newznab.SearchDialects{Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "season"}}}}
	for ordinal, want := range []string{"Tidsrejsen", "Time travels", "Tidsrejsen 2014", "Tidsrejsen"} {
		search := postgres.SearchContext{Media: media, Variant: "season:02", CompleteSearches: uint64(ordinal)}
		request := buildRequest(search, config)
		if len(request.Params) != 2 || request.Params[0].Value != want || request.Params[1].Value != "2" {
			t.Fatalf("scan %d request=%+v, want one query %q and exact season", ordinal, request, want)
		}
	}
}

func TestEventYearFormatKeepsIdentityAndPartGuards(t *testing.T) {
	year := uint16(2026)
	media := catalog.MediaItem{Kind: catalog.KindMovie, Title: "WWE SummerSlam 2026: Sunday", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "imdb", Value: "tt43732886", State: catalog.IdentityVerified}}}
	for _, tc := range []struct {
		title, id string
		want      releases.Verdict
	}{
		{"WWE.SummerSlam.Sunday.2026.1080p.WEB", "43732886", releases.VerdictEligible},
		{"WWE.SummerSlam.Sunday.2026.1080p.WEB", "9999999", releases.VerdictRejected},
		{"WWE.SummerSlam.Saturday.2026.1080p.WEB", "43732886", releases.VerdictRejected},
		{"WWE.SummerSlam.Sunday.2025.1080p.WEB", "43732886", releases.VerdictRejected},
	} {
		observation := releases.Observation{Title: tc.title, Facts: []releases.Fact{{Name: "imdb_id", Values: []string{tc.id}, State: releases.FactObserved}}}
		if got := constrainMediaMatch(observation, releases.Evaluation{Verdict: releases.VerdictEligible}, media).Verdict; got != tc.want {
			t.Fatalf("title %q id %q: got %s want %s", tc.title, tc.id, got, tc.want)
		}
	}
}

func TestSavedAliasesStillRequireIdentityAndYear(t *testing.T) {
	year := uint16(1995)
	media := catalog.MediaItem{Kind: catalog.KindMovie, Title: "Final Hour", Year: &year,
		Identities: []catalog.ExternalIdentity{{Namespace: "imdb", Value: "tt0114432", State: catalog.IdentityVerified}},
		Metadata:   &catalog.MediaMetadata{OriginalTitle: "Sidste time", Aliases: []catalog.MetadataAlias{{Name: "Sista timmen", Source: "tmdb"}}},
	}
	for _, tc := range []struct {
		title string
		facts []releases.Fact
		want  releases.Verdict
	}{
		{"Sidste.Time.1995.DANiSH.1080p", nil, releases.VerdictEligible},
		{"Sista.Timmen.1995.1080p", nil, releases.VerdictEligible},
		{"Sidste.Time.2025.1080p", nil, releases.VerdictHeld},
		{"Sidste.Time.1995.1080p", []releases.Fact{{Name: "imdb_id", Values: []string{"9999999"}, State: releases.FactObserved}}, releases.VerdictRejected},
		{"Different.Movie.1995.1080p", []releases.Fact{{Name: "imdb_id", Values: []string{"0114432"}, State: releases.FactObserved}}, releases.VerdictRejected},
	} {
		got := constrainMediaMatch(releases.Observation{Title: tc.title, Facts: tc.facts}, releases.Evaluation{Verdict: releases.VerdictEligible}, media)
		if got.Verdict != tc.want {
			t.Fatalf("%s verdict=%s want=%s", tc.title, got.Verdict, tc.want)
		}
	}
}

func TestRoastAliasDoesNotAcceptAnotherRoast(t *testing.T) {
	year := uint16(2018)
	media := catalog.MediaItem{Kind: catalog.KindMovie, Title: "Comedy Central Roast of Bruce Willis", Year: &year,
		Metadata: &catalog.MediaMetadata{Aliases: []catalog.MetadataAlias{{Name: "The Comedy Central Roast of Bruce Willis", Source: "tmdb"}}},
	}
	for _, tc := range []struct {
		title string
		want  releases.Verdict
	}{
		{"The.Comedy.Central.Roast.of.Bruce.Willis.2018.1080p", releases.VerdictEligible},
		{"The.Comedy.Central.Roast.of.Alec.Baldwin.2018.1080p", releases.VerdictRejected},
	} {
		got := constrainMediaMatch(releases.Observation{Title: tc.title}, releases.Evaluation{Verdict: releases.VerdictEligible}, media)
		if got.Verdict != tc.want {
			t.Fatalf("%s verdict=%s want=%s", tc.title, got.Verdict, tc.want)
		}
	}
}
