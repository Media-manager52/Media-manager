package packs

import (
	"testing"
	"uuid"
)

func TestCompleteSeasonPackWithMultiEpisodeFile(t *testing.T) {
	expected := []ExpectedEpisode{}
	for episode := uint16(1); episode <= 4; episode++ {
		expected = append(expected, ExpectedEpisode{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: episode}})
	}
	assets := []ObservedAsset{
		{ID: uuid.NewV7(), RelativePath: "Show.S01E01.mkv", Size: 10},
		{ID: uuid.NewV7(), RelativePath: "nested/Show.S01E02.mkv", Size: 10},
		{ID: uuid.NewV7(), RelativePath: "Show.S01E03-E04.mkv", Size: 20},
	}
	report := Evaluate("tvdb:1", 1, expected, assets)
	if !report.Publishable() {
		t.Fatalf("expected publishable report, got %s with issues %#v", report.Verdict, report.Issues)
	}
}

func TestMissingEpisodeRemainsIncomplete(t *testing.T) {
	expected := []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 1}}, {Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 2}}}
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01.mkv", Size: 10}}
	report := Evaluate("tvdb:1", 1, expected, assets)
	if report.Verdict != VerdictIncomplete || report.Publishable() || len(report.Missing) != 1 {
		t.Fatalf("expected one missing episode, got %#v", report)
	}
}

func TestDuplicateEpisodeNeedsReview(t *testing.T) {
	expected := []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 1}}}
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "A.S01E01.mkv", Size: 10}, {ID: uuid.NewV7(), RelativePath: "B.S01E01.mkv", Size: 11}}
	report := Evaluate("tvdb:1", 1, expected, assets)
	if report.Verdict != VerdictNeedsReview {
		t.Fatalf("expected review, got %s", report.Verdict)
	}
}

func TestTenBitSuffixDoesNotInventEpisodeRange(t *testing.T) {
	expected := []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 1}}}
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01-10bit.mkv", Size: 10}}
	report := Evaluate("tvdb:1", 1, expected, assets)
	if !report.Publishable() {
		t.Fatalf("suffix must not become an episode range: %#v", report)
	}
}

func TestExplicitNonContiguousEpisodesNeedReview(t *testing.T) {
	expected := []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 1}}, {Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 3}}}
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01E03.mkv", Size: 10}}
	report := Evaluate("tvdb:1", 1, expected, assets)
	if report.Publishable() || report.Verdict != VerdictNeedsReview {
		t.Fatalf("non-contiguous file cannot receive a Plex-safe name: %#v", report)
	}
}

func TestThreeDigitSeasonCanPublish(t *testing.T) {
	expected := []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 100, Episode: 1}}}
	assets := []ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Show.S100E001.mkv", Size: 10}}
	report := Evaluate("tvdb:1", 100, expected, assets)
	if !report.Publishable() {
		t.Fatalf("three-digit season did not publish: %#v", report)
	}
}

func TestEmptyExpectationCannotPublish(t *testing.T) {
	report := Evaluate("tvdb:1", 1, nil, nil)
	if report.Publishable() || report.Verdict != VerdictInvalid {
		t.Fatalf("empty expectation published: %#v", report)
	}
}

func TestEvaluationIsDeterministic(t *testing.T) {
	expected := []ExpectedEpisode{{Key: EpisodeKey{SeriesID: "tvdb:1", Season: 1, Episode: 1}}}
	asset := ObservedAsset{ID: uuid.NewV7(), RelativePath: "Show.S01E01.mkv", Size: 10}
	first, second := Evaluate("tvdb:1", 1, expected, []ObservedAsset{asset}), Evaluate("tvdb:1", 1, expected, []ObservedAsset{asset})
	if first.InventoryDigest != second.InventoryDigest || first.Groups[0].ID != second.Groups[0].ID {
		t.Fatalf("evaluation is nondeterministic: %#v %#v", first, second)
	}
}
