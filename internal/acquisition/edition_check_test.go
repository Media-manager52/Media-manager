package acquisition

import (
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

func editionObservation(title string) releases.Observation {
	return releases.InferTitleFacts(releases.Observation{Title: title}, time.Now().UTC())
}

// TestEditionRulesKeepEachEditionInItsResolution pins the edition rules: a 4K
// edition only takes clearly 2160p releases, and once a 4K edition exists the
// standard edition no longer takes 2160p releases. Without a 4K edition the
// standard edition is unchanged.
func TestEditionRulesKeepEachEditionInItsResolution(t *testing.T) {
	cases := []struct {
		name         string
		title        string
		edition      string
		fourKExists  bool
		wantRejected bool
	}{
		{"4K takes 2160p", "Movie.2026.2160p.WEB-DL.DDP5.1-GRP", postgres.Edition4K, false, false},
		{"4K takes UHD alias", "Movie.2026.UHD.BluRay-GRP", postgres.Edition4K, false, false},
		{"4K rejects 1080p", "Movie.2026.1080p.WEB-DL-GRP", postgres.Edition4K, false, true},
		{"4K rejects unknown resolution", "Movie.2026.WEB-DL-GRP", postgres.Edition4K, false, true},
		{"standard keeps 2160p without a 4K edition", "Movie.2026.2160p.WEB-DL-GRP", postgres.EditionStandard, false, false},
		{"standard rejects 2160p beside a 4K edition", "Movie.2026.2160p.WEB-DL-GRP", postgres.EditionStandard, true, true},
		{"standard takes 1080p beside a 4K edition", "Movie.2026.1080p.WEB-DL-GRP", postgres.EditionStandard, true, false},
		{"standard takes unknown beside a 4K edition", "Movie.2026.WEB-DL-GRP", postgres.EditionStandard, true, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			search := postgres.SearchContext{Edition: testCase.edition, FourKEditionExists: testCase.fourKExists}
			evaluation := applyEditionRules(editionObservation(testCase.title), releases.Evaluation{Verdict: releases.VerdictEligible}, search)
			if rejected := evaluation.Verdict == releases.VerdictRejected; rejected != testCase.wantRejected {
				t.Fatalf("rejected=%v, want %v (%+v)", rejected, testCase.wantRejected, evaluation.Explanations)
			}
		})
	}
	conflicting := releases.Observation{Title: "Movie", Facts: []releases.Fact{{Name: "resolution", Values: []string{"2160p", "1080p"}, State: releases.FactConflicting}}}
	if evaluation := applyEditionRules(conflicting, releases.Evaluation{Verdict: releases.VerdictEligible}, postgres.SearchContext{Edition: postgres.Edition4K}); evaluation.Verdict != releases.VerdictRejected {
		t.Fatal("4K edition accepted conflicting resolution evidence")
	}
}

// TestEditionResolutionMismatchJudgesWidthAndHeight proves a widescreen 4K
// file counts as 4K, a 1080p file in a 4K edition is a release failure, and
// the standard edition is never checked.
func TestEditionResolutionMismatchJudgesWidthAndHeight(t *testing.T) {
	verification := func(width, height int) subtitles.Verification {
		return subtitles.Verification{Videos: []subtitles.VideoEvidence{{RelativePath: "Movie.mkv", MediaInfo: mediainfo.Evidence{VideoTracks: []mediainfo.Track{{Width: width, Height: height}}}}}}
	}
	fourK := postgres.AltMountJobContext{Root: postgres.LibraryRoot{Edition: postgres.Edition4K}}
	for _, size := range [][2]int{{3840, 2160}, {3840, 1600}, {4096, 1716}} {
		if detail, mismatch := editionResolutionMismatch(fourK, verification(size[0], size[1])); mismatch {
			t.Fatalf("%dx%d rejected as not 4K: %s", size[0], size[1], detail)
		}
	}
	detail, mismatch := editionResolutionMismatch(fourK, verification(1920, 1080))
	if !mismatch || !strings.Contains(detail, "1920x1080") {
		t.Fatalf("1080p in the 4K edition: mismatch=%v detail=%q", mismatch, detail)
	}
	standard := postgres.AltMountJobContext{Root: postgres.LibraryRoot{Edition: postgres.EditionStandard}}
	if _, mismatch := editionResolutionMismatch(standard, verification(1920, 1080)); mismatch {
		t.Fatal("the standard edition was held to 4K")
	}
}

// TestRequiredResolutionIsCheckedOnTheMeasuredFile proves a required
// resolution rule is enforced on the downloaded file, not only on the release
// name: a release named 1080p that is really 720p fails, while widescreen and
// 4:3 1080p, a higher resolution, and an unknown size pass.
func TestRequiredResolutionIsCheckedOnTheMeasuredFile(t *testing.T) {
	video := func(width, height int) subtitles.Verification {
		return subtitles.Verification{Videos: []subtitles.VideoEvidence{{RelativePath: "Movie.mkv", MediaInfo: mediainfo.Evidence{VideoTracks: []mediainfo.Track{{Width: width, Height: height}}}}}}
	}
	required1080 := [][]string{{"1080p", "2160p"}}
	cases := []struct {
		name     string
		required [][]string
		width    int
		height   int
		mismatch bool
	}{
		{"720p file under a required 1080p", required1080, 1280, 720, true},
		{"576p file under a required 720p", [][]string{{"720p"}}, 720, 576, true},
		{"1080p file", required1080, 1920, 1080, false},
		{"widescreen 1080p file", required1080, 1920, 800, false},
		{"4:3 1080p file", required1080, 1440, 1080, false},
		{"4K file under a required 1080p", [][]string{{"1080p"}}, 3840, 1600, false},
		{"unknown size", required1080, 0, 0, false},
		{"no required resolution", nil, 640, 360, false},
		{"widescreen 720p under a required 720p", [][]string{{"720p"}}, 1280, 536, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			detail, mismatch := requiredResolutionMismatch(testCase.required, video(testCase.width, testCase.height))
			if mismatch != testCase.mismatch {
				t.Fatalf("mismatch=%v detail=%q, want %v", mismatch, detail, testCase.mismatch)
			}
			if mismatch && !strings.Contains(detail, "Movie.mkv") {
				t.Fatalf("detail does not name the file: %q", detail)
			}
		})
	}
}
