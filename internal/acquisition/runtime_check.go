package acquisition

import (
	"fmt"
	"math"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

// outputTooShortDetail returns a release-attributable reason when a selected
// video is far shorter than the movie or episode it claims to be. The probe
// already runs for subtitle and playable-media evidence, so the duration is
// free; a fake, sample or cut-off download must not be published as a success.
//
// The expected runtime is TMDB's runtime for the title (a series runtime is
// its usual episode length). When it is unknown, only an implausibly short file
// is refused. Specials (season 0) and short subjects are never judged by
// runtime because their lengths vary legitimately.
func outputTooShortDetail(job postgres.AltMountJobContext, verification subtitles.Verification) (string, bool) {
	kind, ok := runtimeLengthKind(job)
	if !ok {
		return "", false
	}
	expectedMinutes := 0.0
	if job.Media.Metadata != nil && job.Media.Metadata.RuntimeMinutes != nil {
		expectedMinutes = float64(*job.Media.Metadata.RuntimeMinutes)
	}
	for _, video := range verification.Videos {
		if video.MediaInfo.DurationSeconds == nil {
			continue
		}
		if !mediainfo.TooShort(*video.MediaInfo.DurationSeconds, expectedMinutes, kind) {
			continue
		}
		actual := formatLength(*video.MediaInfo.DurationSeconds)
		label := "movie"
		if kind == mediainfo.LengthEpisode {
			label = "episode"
		}
		if expectedMinutes > 0 {
			return fmt.Sprintf("The file %s is only %s long; the %s is about %s. This looks like a fake, sample or truncated release.", video.RelativePath, actual, label, formatLength(expectedMinutes*60)), true
		}
		return fmt.Sprintf("The file %s is only %s long, too short to be a %s. This looks like a fake, sample or truncated release.", video.RelativePath, actual, label), true
	}
	return "", false
}

func runtimeLengthKind(job postgres.AltMountJobContext) (mediainfo.LengthKind, bool) {
	switch job.Media.Kind {
	case catalog.KindMovie:
		return mediainfo.LengthMovie, true
	case catalog.KindSeries:
		scope, ok := workflows.ParseVariant(job.Variant)
		if !ok || scope.Kind == workflows.VariantDefault || scope.Season == 0 {
			return 0, false
		}
		return mediainfo.LengthEpisode, true
	default:
		return 0, false
	}
}

func formatLength(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.0f s", seconds)
	}
	return fmt.Sprintf("%.0f min", math.Round(seconds/60))
}
