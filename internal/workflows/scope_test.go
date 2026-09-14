package workflows

import "testing"

func TestParseVariantRecognizesScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		variant string
		ok      bool
		kind    VariantKind
		season  uint16
		episode uint16
	}{
		{variant: "default", ok: true, kind: VariantDefault},
		{variant: "", ok: true, kind: VariantDefault},
		{variant: "season:01", ok: true, kind: VariantSeason, season: 1},
		{variant: "season:1", ok: true, kind: VariantSeason, season: 1},
		{variant: "season:999", ok: true, kind: VariantSeason, season: 999},
		{variant: "season:01:episode:02", ok: true, kind: VariantEpisode, season: 1, episode: 2},
		{variant: "season:12:episode:34", ok: true, kind: VariantEpisode, season: 12, episode: 34},
		{variant: "season:01:episode:0", ok: false},
		{variant: "season:1000", ok: false},
		{variant: "season:ab", ok: false},
		{variant: "season:01:episode:01:extra", ok: false},
		{variant: "season:01:pack:01", ok: false},
		{variant: "movie", ok: false},
	}
	for _, test := range tests {
		t.Run(test.variant, func(t *testing.T) {
			t.Parallel()
			scope, ok := ParseVariant(test.variant)
			if ok != test.ok {
				t.Fatalf("ParseVariant(%q) ok=%v, want %v", test.variant, ok, test.ok)
			}
			if !test.ok {
				return
			}
			if scope.Kind != test.kind || scope.Season != test.season || scope.Episode != test.episode {
				t.Fatalf("ParseVariant(%q)=%+v, want kind=%s season=%d episode=%d", test.variant, scope, test.kind, test.season, test.episode)
			}
		})
	}
}

func TestVariantRenderingRoundTrips(t *testing.T) {
	t.Parallel()
	if SeasonVariant(3) != "season:03" {
		t.Fatalf("SeasonVariant(3)=%q", SeasonVariant(3))
	}
	if EpisodeVariant(3, 7) != "season:03:episode:07" {
		t.Fatalf("EpisodeVariant(3,7)=%q", EpisodeVariant(3, 7))
	}
	parsed, ok := ParseVariant(EpisodeVariant(3, 7))
	if !ok || parsed.Kind != VariantEpisode || parsed.Season != 3 || parsed.Episode != 7 {
		t.Fatalf("round trip failed: %+v ok=%v", parsed, ok)
	}
}
