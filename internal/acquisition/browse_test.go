package acquisition

import (
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"testing"
	"time"
)

func TestBrowseExactMovieAndEpisode(t *testing.T) {
	year := uint16(2020)
	for _, tc := range []struct {
		title, kind     string
		season, episode uint16
		want            bool
	}{
		{"Example.2020.1080p.WEB-DL", "movie", 0, 0, true},
		{"Example.1990.1080p.WEB-DL", "movie", 0, 0, false},
		{"Different.2020.1080p", "movie", 0, 0, false},
		{"Example.2020.S01E02.1080p", "series", 1, 2, true},
		{"Example.2020.S01E03.1080p", "series", 1, 2, false},
		{"Example.2020.S02.1080p", "series", 1, 2, false},
		{"Example.2020.S01.1080p", "series", 1, 2, true},
	} {
		t.Run(tc.title, func(t *testing.T) {
			o := releases.InferTitleFacts(releases.Observation{Title: tc.title}, time.Now())
			media := catalog.MediaItem{Kind: catalog.MediaKind(tc.kind), Title: "Example", Year: &year}
			in := BrowseInput{Kind: media.Kind, Season: tc.season, Episode: tc.episode}
			if got := browseMatches(o, media, in); got != tc.want {
				t.Fatalf("match=%v want=%v facts=%+v", got, tc.want, o.Facts)
			}
		})
	}
}
func TestBrowseSubtitleRankingDoesNotUseAudioOrNordic(t *testing.T) {
	for _, name := range []string{"audio_languages", "audio_language_hints"} {
		if browseRank([]releases.Fact{{Name: name, Values: []string{"da"}, State: releases.FactObserved}}) != 0 {
			t.Fatal("audio ranked as subtitles")
		}
	}
	reported := browseRank([]releases.Fact{{Name: "subtitle_languages", Values: []string{"da"}, State: releases.FactObserved}})
	inferred := browseRank([]releases.Fact{{Name: "subtitle_languages", Values: []string{"da"}, State: releases.FactInferred}})
	if reported <= inferred || inferred <= 0 {
		t.Fatal("subtitle provenance not ranked")
	}
}
func TestBrowseInputRejectsAmbiguousScope(t *testing.T) {
	base := BrowseInput{Kind: catalog.KindMovie, IMDb: "tt123", Title: "Example", Year: 2020}
	if !base.Valid() {
		t.Fatal("valid movie rejected")
	}
	base.Episode = 1
	if base.Valid() {
		t.Fatal("movie episode accepted")
	}
	base.Kind = catalog.KindSeries
	if base.Valid() {
		t.Fatal("seasonless episode accepted")
	}
	base.Season = 1
	if !base.Valid() {
		t.Fatal("valid episode rejected")
	}
}
