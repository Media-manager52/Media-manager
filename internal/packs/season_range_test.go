package packs

import (
	"testing"
	"uuid"
)

func TestDeclaredSeasonRangeRequiresOneBoundedUnambiguousRange(t *testing.T) {
	for _, title := range []string{"Operation.X.S01-S16.DANISH.720p", "Operation X S1-S16 DANISH"} {
		if got, ok := DeclaredSeasonRange(title); !ok || got.First != 1 || got.Last != 16 {
			t.Fatalf("range %q: %+v %v", title, got, ok)
		}
	}
	for _, title := range []string{"Operation.X.S16.DANISH", "Operation.X.S16-S01.DANISH", "Operation.X.S01-S99.DANISH", "Operation.X.S00-S16.DANISH", "Operation.X.S01-S16E06.DANISH", "Operation.X.S01-S16.S17-S20.DANISH"} {
		if _, ok := DeclaredSeasonRange(title); ok {
			t.Fatalf("invalid range accepted: %q", title)
		}
	}
}

func TestCollectionInventoryVerifiesExactRequestedScopeAndKeepsFailures(t *testing.T) {
	key := EpisodeKey{SeriesID: "tvdb:79591", Season: 16, Episode: 6}
	expected := []ExpectedEpisode{{Key: key}}
	asset := func(path string) ObservedAsset { return ObservedAsset{ID: uuid.NewV7(), RelativePath: path, Size: 100} }
	base := []ObservedAsset{asset("Season 01/Operation.X.S01E01.mkv"), asset("Season 16/Operation.X.S16E05.mkv"), asset("Season 16/Operation.X.S16E06.mkv"), asset("Season 16/Operation.X.S16E07.mkv")}
	declared := SeasonRange{First: 1, Last: 16}
	report := EvaluateSeasonRange(key.SeriesID, 16, expected, base, EpisodeNumbering{}, declared)
	if !report.Publishable() || len(report.Groups) != 1 || len(report.Covered) != 1 || report.Groups[0].AssetIDs[0] != base[2].ID {
		t.Fatalf("exact episode projection: %+v", report)
	}
	if report.InventoryDigest != Evaluate(key.SeriesID, 16, expected, base).InventoryDigest || len(base) != 4 {
		t.Fatal("whole inventory identity or retained files were changed")
	}
	for _, spec := range []struct {
		name   string
		assets []ObservedAsset
	}{
		{"missing", []ObservedAsset{base[0], base[1], base[3]}},
		{"duplicate", append(append([]ObservedAsset{}, base...), asset("Other/Operation.X.S16E06.mkv"))},
		{"unlabelled", append(append([]ObservedAsset{}, base...), asset("Unknown.mkv"))},
		{"outside declaration", append(append([]ObservedAsset{}, base...), asset("Operation.X.S17E01.mkv"))},
		{"unsafe unused file", append(append([]ObservedAsset{}, base...), asset("../Operation.X.S01E01.mkv"))},
		{"composite episode", []ObservedAsset{base[0], asset("Operation.X.S16E06E07.mkv")}},
		{"case collision in unused season", append(append([]ObservedAsset{}, base...), asset("season 01/operation.x.s01e01.mkv"))},
	} {
		t.Run(spec.name, func(t *testing.T) {
			if got := EvaluateSeasonRange(key.SeriesID, 16, expected, spec.assets, EpisodeNumbering{}, declared); got.Publishable() {
				t.Fatalf("invalid inventory published: %+v", got)
			}
		})
	}
	if got := EvaluateSeasonRange(key.SeriesID, 16, expected, base, EpisodeNumbering{}, SeasonRange{First: 1, Last: 15}); got.Publishable() {
		t.Fatal("out-of-range workflow published")
	}
}
