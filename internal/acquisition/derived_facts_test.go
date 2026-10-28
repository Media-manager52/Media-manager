package acquisition

import (
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestEpisodeNumberingFactsAreStableAndNormalizeHistoricalDuplicates(t *testing.T) {
	search := numberingSearch()
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	base := releases.Observation{
		Title:        "Operation.X.S2026E02.DANiSH.1080p.WEB",
		Facts:        []releases.Fact{{Name: "imdb_id", Values: []string{"0451553"}, State: releases.FactObserved}},
		ObservedAt:   now,
		ParseVersion: "newznab-v1",
	}
	first := applyEpisodeNumbering(base, search, now)
	second := applyEpisodeNumbering(first, search, now.Add(time.Hour))
	if !reflect.DeepEqual(first.Facts, second.Facts) {
		t.Fatalf("unchanged mapping changed facts\nfirst=%+v\nsecond=%+v", first.Facts, second.Facts)
	}
	assertDerivedFactsSingle(t, first.Facts)

	// Simulate an observation written by the previous non-idempotent evaluator:
	// duplicate derived facts and duplicate unmapped aliases. The corrected
	// evaluator must normalize this once and then remain stable.
	historical := first
	historical.Facts = append([]releases.Fact(nil), first.Facts...)
	for _, duplicate := range []releases.Fact{
		{Name: "season_number", Values: []string{"23"}, State: releases.FactInferred, ObservedAt: now.Add(time.Minute)},
		{Name: "episode_number", Values: []string{"2"}, State: releases.FactInferred, ObservedAt: now.Add(time.Minute)},
		{Name: "release_scope", Values: []string{"episode"}, State: releases.FactInferred, ObservedAt: now.Add(time.Minute)},
		{Name: "episode_numbering", Values: []string{"frozen annual season snapshot"}, State: releases.FactInferred, ObservedAt: now.Add(time.Minute)},
		{Name: "unmapped_season_number", Values: []string{"2026"}, State: releases.FactInferred, ObservedAt: now.Add(time.Minute)},
	} {
		historical.Facts = append(historical.Facts, duplicate)
	}
	normalized := applyEpisodeNumbering(historical, search, now.Add(2*time.Hour))
	repeated := applyEpisodeNumbering(normalized, search, now.Add(3*time.Hour))
	assertDerivedFactsSingle(t, normalized.Facts)
	if !reflect.DeepEqual(normalized.Facts, repeated.Facts) {
		t.Fatalf("normalized facts are not stable\nfirst=%+v\nsecond=%+v", normalized.Facts, repeated.Facts)
	}
	if factCount(normalized.Facts, "unmapped_season_number") != 1 {
		t.Fatalf("historical source evidence was not normalized: %+v", normalized.Facts)
	}
}

func TestEpisodeNumberingMeaningfulMappingChangeIsDetectable(t *testing.T) {
	search := numberingSearch()
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	observation := releases.Observation{
		Title:        "Operation.X.2026.06.10.Flaaet.Af.Skat.DANiSH.1080p.WEB",
		Facts:        []releases.Fact{{Name: "imdb_id", Values: []string{"0451553"}, State: releases.FactObserved}},
		ObservedAt:   now,
		ParseVersion: "newznab-v1",
	}
	first := applyEpisodeNumbering(observation, search, now)
	changed := search
	changed.EpisodeNumbering.Dates = map[string]uint16{"2026-06-10": 1}
	changed.EpisodeNumbering.Episodes[1] = true
	second := applyEpisodeNumbering(first, changed, now.Add(time.Hour))
	if reflect.DeepEqual(first.Facts, second.Facts) {
		t.Fatalf("a changed episode mapping was not detected: %+v", first.Facts)
	}
	firstEpisode := factValues(first.Facts, "episode_number")
	secondEpisode := factValues(second.Facts, "episode_number")
	if !reflect.DeepEqual(firstEpisode, []string{"2"}) || !reflect.DeepEqual(secondEpisode, []string{"1"}) {
		t.Fatalf("mapped episode facts first=%v second=%v", firstEpisode, secondEpisode)
	}
}

func TestOldBoysDerivedFactsAreStableAndChangesRemainDetectable(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	seasons := uint16(16)
	media := catalog.MediaItem{
		ID:         uuid.NewV7(),
		Kind:       catalog.KindSeries,
		Title:      "Example Series",
		Identities: []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "378992", State: catalog.IdentityVerified}},
		Metadata:   &catalog.MediaMetadata{OriginalLanguage: "da", SeasonCount: &seasons},
	}
	search := numberingSearch()
	search.Media = media
	search.Variant = "season:16"
	search.EpisodeNumbering = packs.AnnualEpisodeNumbering(16, []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 16, Episode: 6}, AirDate: "2026-06-10"}})
	observation := releases.Observation{
		Title:        "Example.Series.S2026.DANiSH.S01-S16.1080p.WEB",
		ObservedAt:   now,
		ParseVersion: "oldboys-v1",
	}
	first := bindOldBoysCatalogue(observation, search, now)
	first = applyEpisodeNumbering(first, search, now)
	first, _ = oldBoysRangeScope(first, search, nil, nil)
	second := bindOldBoysCatalogue(first, search, now.Add(time.Hour))
	second = applyEpisodeNumbering(second, search, now.Add(time.Hour))
	second, _ = oldBoysRangeScope(second, search, nil, nil)
	if !reflect.DeepEqual(first.Facts, second.Facts) {
		t.Fatalf("unchanged OldBoys evidence changed facts\nfirst=%+v\nsecond=%+v", first.Facts, second.Facts)
	}
	for _, name := range []string{"oldboys_catalogue_binding", "declared_pack_seasons"} {
		if count := factCount(first.Facts, name); count != 1 {
			t.Fatalf("derived OldBoys fact %s count=%d: %+v", name, count, first.Facts)
		}
	}

	changed := search
	changedSeasons := uint16(15)
	changed.Media.Metadata = &catalog.MediaMetadata{OriginalLanguage: "da", SeasonCount: &changedSeasons}
	third := bindOldBoysCatalogue(first, changed, now.Add(2*time.Hour))
	third = applyEpisodeNumbering(third, changed, now.Add(2*time.Hour))
	third, _ = oldBoysRangeScope(third, changed, nil, nil)
	if reflect.DeepEqual(second.Facts, third.Facts) {
		t.Fatalf("a changed OldBoys range was not detected: %+v", second.Facts)
	}
}

func factValues(facts []releases.Fact, name string) []string {
	for _, fact := range facts {
		if fact.Name == name {
			return fact.Values
		}
	}
	return nil
}
