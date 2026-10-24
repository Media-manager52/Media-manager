package acquisition

import (
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/subtitles"
)

func runtimeJob(kind catalog.MediaKind, variant string, runtime *uint16) postgres.AltMountJobContext {
	job := postgres.AltMountJobContext{Variant: variant}
	job.Media.Kind = kind
	if runtime != nil {
		job.Media.Metadata = &catalog.MediaMetadata{RuntimeMinutes: runtime}
	}
	return job
}

func runtimeVideo(seconds float64, path string) subtitles.VideoEvidence {
	return subtitles.VideoEvidence{RelativePath: path, MediaInfo: mediainfo.Evidence{DurationSeconds: &seconds}}
}

func TestOutputTooShortDetail(t *testing.T) {
	ninetyFive := uint16(95)
	fortyFive := uint16(45)
	tests := []struct {
		name     string
		job      postgres.AltMountJobContext
		videos   []subtitles.VideoEvidence
		want     bool
		contains string
	}{
		{
			name:     "movie far below runtime",
			job:      runtimeJob(catalog.KindMovie, "default", &ninetyFive),
			videos:   []subtitles.VideoEvidence{runtimeVideo(60, "Film/Film.mkv")},
			want:     true,
			contains: "only 1 min long; the movie is about 95 min",
		},
		{
			name:   "movie at expected runtime",
			job:    runtimeJob(catalog.KindMovie, "default", &ninetyFive),
			videos: []subtitles.VideoEvidence{runtimeVideo(95*60, "Film/Film.mkv")},
		},
		{
			name:     "unknown movie runtime uses the ten-minute floor",
			job:      runtimeJob(catalog.KindMovie, "default", nil),
			videos:   []subtitles.VideoEvidence{runtimeVideo(5*60, "Film/Film.mkv")},
			want:     true,
			contains: "too short to be a movie",
		},
		{
			name:   "unknown movie runtime above the floor",
			job:    runtimeJob(catalog.KindMovie, "default", nil),
			videos: []subtitles.VideoEvidence{runtimeVideo(11*60, "Film/Film.mkv")},
		},
		{
			name:   "short subject below the judged runtime",
			job:    runtimeJob(catalog.KindMovie, "default", uint16Ptr(10)),
			videos: []subtitles.VideoEvidence{runtimeVideo(2*60, "Short/Short.mkv")},
		},
		{
			name:   "season zero specials are not judged",
			job:    runtimeJob(catalog.KindSeries, "season:00:episode:01", &fortyFive),
			videos: []subtitles.VideoEvidence{runtimeVideo(60, "Show/Show S00E01.mkv")},
		},
		{
			name:     "episode far below runtime",
			job:      runtimeJob(catalog.KindSeries, "season:01:episode:01", &fortyFive),
			videos:   []subtitles.VideoEvidence{runtimeVideo(60, "Show/Show S01E01.mkv")},
			want:     true,
			contains: "only 1 min long; the episode is about 45 min",
		},
		{
			name:   "episode at expected runtime",
			job:    runtimeJob(catalog.KindSeries, "season:01:episode:01", &fortyFive),
			videos: []subtitles.VideoEvidence{runtimeVideo(44*60, "Show/Show S01E01.mkv")},
		},
		{
			name:   "episode without a parseable scope is not judged",
			job:    runtimeJob(catalog.KindSeries, "default", &fortyFive),
			videos: []subtitles.VideoEvidence{runtimeVideo(60, "Show/Show S01E01.mkv")},
		},
		{
			name:   "unreadable duration is not judged",
			job:    runtimeJob(catalog.KindMovie, "default", &ninetyFive),
			videos: []subtitles.VideoEvidence{{RelativePath: "Film/Film.mkv"}},
		},
		{
			name:     "a season pack fails on its short file",
			job:      runtimeJob(catalog.KindSeries, "season:01", &fortyFive),
			videos:   []subtitles.VideoEvidence{runtimeVideo(44*60, "Show/S01E01.mkv"), runtimeVideo(4*60, "Show/S01E02.mkv")},
			want:     true,
			contains: "Show/S01E02.mkv",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			detail, got := outputTooShortDetail(test.job, subtitles.Verification{Videos: test.videos})
			if got != test.want {
				t.Fatalf("got too-short=%v detail=%q, want %v", got, detail, test.want)
			}
			if got && !strings.Contains(detail, test.contains) {
				t.Fatalf("detail %q does not mention %q", detail, test.contains)
			}
		})
	}
}

func uint16Ptr(value uint16) *uint16 { return &value }
