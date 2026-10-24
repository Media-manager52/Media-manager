package mediainfo

// LengthKind says what a video file is meant to be. It only selects the floor
// used when the expected runtime is unknown.
type LengthKind int

const (
	LengthMovie LengthKind = iota
	LengthEpisode
)

const (
	// shortFraction is the part of the expected runtime below which a file is
	// treated as a fake, a sample or a truncated download.
	shortFraction = 0.5
	// shortestJudgedMinutes is the shortest expected runtime the duration
	// check judges. Shorts, cartoons and bonus segments vary too much to use
	// runtime as evidence.
	shortestJudgedMinutes = 12.0
	// When the expected runtime is unknown, only a file this short is
	// refused. No real movie or episode is that short.
	unknownFloorMovieSeconds   = 10 * 60.0
	unknownFloorEpisodeSeconds = 3 * 60.0
)

// TooShort reports whether a video of seconds length is too short to be the
// movie or episode it claims to be. expectedMinutes is the runtime TMDB gives
// (0 when it is not known). A length of 0 or less means the probe could not
// read the duration, and that is never judged.
func TooShort(seconds, expectedMinutes float64, kind LengthKind) bool {
	if !(seconds > 0) {
		return false
	}
	if expectedMinutes > 0 {
		if expectedMinutes < shortestJudgedMinutes {
			return false
		}
		return seconds < expectedMinutes*60*shortFraction
	}
	if kind == LengthEpisode {
		return seconds < unknownFloorEpisodeSeconds
	}
	return seconds < unknownFloorMovieSeconds
}
