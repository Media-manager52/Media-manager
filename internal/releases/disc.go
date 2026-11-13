package releases

import (
	"regexp"
	"strings"
)

var (
	// Tokens that only full disc images carry: Blu-ray and DVD folder
	// structures, disc sizes and ISO images.
	discStructureToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(bdmv|video_ts|bd25|bd50|bd66|bd100|dvd5|dvd9|iso)(?:[^a-z0-9]|$)`)
	// "COMPLETE BLURAY" / "COMPLETE UHD BLURAY" is the scene name for an
	// untouched disc.
	discCompleteBluray = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])complete[ ._-]+(?:uhd[ ._-]+)?blu-?ray(?:[^a-z0-9]|$)`)
	discBluray         = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])blu-?ray(?:[^a-z0-9]|$)`)
	// Untouched discs are named "Blu-ray" with a hyphen; encodes say "BluRay".
	discHyphenBluray = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])blu-ray(?:[^a-z0-9]|$)`)
	// Disc video codecs as an untouched disc names them. HEVC alone is not
	// one: encodes are often named "BluRay.HEVC"; only a UHD disc counts.
	// VC-1 counts only with the hyphenated "Blu-ray" (some encodes say VC1).
	discVideoCodec = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(avc|mpeg-?2|mvc)(?:[^a-z0-9]|$)`)
	discUHDHEVC    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])uhd(?:[^a-z0-9]|$).*(?:^|[^a-z0-9])hevc(?:[^a-z0-9]|$)`)
	discOtherToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(untouched|dvdr|dvd-r)(?:[^a-z0-9]|$)`)
	// Anything that says the video was re-encoded or remuxed into a file.
	discEncodeMarker = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(x26[45]|h[ .]?26[45]|remux[a-z0-9]*|[a-z]*rip|encode|av1|mkv|mp4|xvid|divx)(?:[^a-z0-9]|$)`)
)

// FullDiscRelease reports whether a release title names an untouched Blu-ray
// or DVD disc (folder structure or image). Such a release contains only
// .m2ts/.vob streams or an ISO, never one playable media file, so it can never
// be published. It matches the usual "BR-DISK" naming:
//
//   - a disc structure, size or image token (BDMV, VIDEO_TS, BD25/50/66/100,
//     DVD5, DVD9, ISO), or "COMPLETE BLURAY";
//   - otherwise, only without an encode or remux marker (x264/x265,
//     H.264/H.265, REMUX, *Rip, AV1): "Blu-ray" with a hyphen, Blu-ray with a
//     disc video codec (AVC, MPEG-2, MVC), a UHD Blu-ray in HEVC,
//     "Untouched", or DVDR.
//
// Encodes ("BluRay.x264", "BluRay.HEVC") and remuxes ("BluRay.REMUX.AVC")
// never match.
func FullDiscRelease(title string) bool {
	if discStructureToken.MatchString(title) || discCompleteBluray.MatchString(title) {
		return true
	}
	if discEncodeMarker.MatchString(strings.ReplaceAll(title, "_", ".")) {
		return false
	}
	if discOtherToken.MatchString(title) || discHyphenBluray.MatchString(title) {
		return true
	}
	return discBluray.MatchString(title) && (discVideoCodec.MatchString(title) || discUHDHEVC.MatchString(title))
}
