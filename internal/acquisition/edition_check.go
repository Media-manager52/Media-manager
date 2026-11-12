package acquisition

import (
	"fmt"
	"strings"

	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// editionResolutionMismatch reports a 4K edition file whose primary video is
// not 4K. A 4K picture is judged by width as well as height: a widescreen 4K
// film (3840x1600) is still 4K, while the probe's height-only label would call
// it 1440p.
func editionResolutionMismatch(job postgres.AltMountJobContext, verification subtitles.Verification) (string, bool) {
	if job.Root.Edition != postgres.Edition4K {
		return "", false
	}
	for _, video := range verification.Videos {
		width, height := primaryVideoSize(video.MediaInfo)
		if !is4KPicture(width, height) {
			return fmt.Sprintf("The file %s is %dx%d, not 4K; the 4K edition only keeps 2160p files.", video.RelativePath, width, height), true
		}
	}
	return "", false
}

func primaryVideoSize(evidence mediainfo.Evidence) (int, int) {
	width, height := 0, 0
	for _, track := range evidence.VideoTracks {
		if track.Width*track.Height > width*height {
			width, height = track.Width, track.Height
		}
	}
	return width, height
}

func is4KPicture(width, height int) bool {
	return width >= 3200 || height >= 2000
}

// resolutionRank orders the profile resolution values; unknown values rank 0.
var resolutionRank = map[string]int{"480p": 1, "576p": 2, "720p": 3, "1080p": 4, "2160p": 5}

// measuredResolutionRank judges a picture by width as well as height, so a
// widescreen 1080p film (1920x800) is still 1080p and a 4:3 one (1440x1080)
// too. Returns 0 when the size is unknown.
func measuredResolutionRank(width, height int) int {
	switch {
	case width <= 0 && height <= 0:
		return 0
	case is4KPicture(width, height):
		return 5
	case width >= 1700 || height >= 1000:
		return 4
	case width >= 1200 || height >= 680:
		return 3
	case width >= 1000 || height >= 560:
		return 2
	default:
		return 1
	}
}

// requiredResolutionMismatch reports a file whose measured picture is below
// every resolution a required rule allows: a release named 1080p that is
// really 720p never satisfies a required 1080p. A file at or above the lowest
// allowed resolution passes, so a correct release is never rejected by the
// measurement.
func requiredResolutionMismatch(required [][]string, verification subtitles.Verification) (string, bool) {
	for _, allowed := range required {
		lowest := 0
		for _, value := range allowed {
			if rank := resolutionRank[strings.ToLower(strings.TrimSpace(value))]; rank > 0 && (lowest == 0 || rank < lowest) {
				lowest = rank
			}
		}
		if lowest == 0 {
			continue
		}
		for _, video := range verification.Videos {
			width, height := primaryVideoSize(video.MediaInfo)
			measured := measuredResolutionRank(width, height)
			if measured > 0 && measured < lowest {
				return fmt.Sprintf("The file %s is %dx%d, below the required resolution (%s).", video.RelativePath, width, height, strings.Join(allowed, ", ")), true
			}
		}
	}
	return "", false
}
