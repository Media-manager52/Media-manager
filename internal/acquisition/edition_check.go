package acquisition

import (
	"fmt"

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
