package releases

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	resolutionToken = regexp.MustCompile(`(?i)(?:^|[ ._\-])(2160p|4k|uhd|1080p|720p|576p|480p)(?:$|[ ._\-])`)
	resolutionSize  = regexp.MustCompile(`(?i)^\s*(\d{3,4})\s*[x×]\s*(2160|1080|720|576|480)(?:\s|$)`)
	seasonToken     = regexp.MustCompile(`(?i)(?:^|[ ._\-])S(\d{1,3})(?:$|[ ._\-]|E)`)
	episodeToken    = regexp.MustCompile(`(?i)S\d{1,3}E\d{1,3}`)
	episodeRange    = regexp.MustCompile(`(?i)S\d{1,3}E\d{1,3}(?:-E?\d{1,3}|E\d{1,3})(?:$|[ ._\-])`)
	titleSeparator  = regexp.MustCompile(`[^\pL\pN]+`)
	audioTrueHD     = regexp.MustCompile(`(?i)(?:^|[ ._\-])true[ ._\-]?hd(?:$|[ ._\-])`)
	audioDTSHDMA    = regexp.MustCompile(`(?i)(?:^|[ ._\-])dts[ ._\-]?hd[ ._\-]?(?:ma|master[ ._\-]?audio)(?:$|[ ._\-])`)
	audioDTSHD      = regexp.MustCompile(`(?i)(?:^|[ ._\-])dts[ ._\-]?hd(?:$|[ ._\-])`)
	audioDTSX       = regexp.MustCompile(`(?i)(?:^|[ ._\-])dts[ ._\-]?x(?:$|[ ._\-])`)
	audioEAC3       = regexp.MustCompile(`(?i)(?:^|[ ._\-])(?:e[ ._\-]?ac[ ._\-]?3|ddp(?:\d|$|[ ._\-])|dd\+)(?:$|[ ._\-])`)
	audioAC3        = regexp.MustCompile(`(?i)(?:^|[ ._\-])(?:ac[ ._\-]?3|dd(?:\d|$|[ ._\-]))(?:$|[ ._\-])`)
	audioDTS        = regexp.MustCompile(`(?i)(?:^|[ ._\-])dts(?:$|[ ._\-])`)
	audioFLAC       = regexp.MustCompile(`(?i)(?:^|[ ._\-])flac(?:$|[ ._\-])`)
	audioLPCM       = regexp.MustCompile(`(?i)(?:^|[ ._\-])(?:lpcm|pcm)(?:$|[ ._\-])`)
	audioOpus       = regexp.MustCompile(`(?i)(?:^|[ ._\-])opus(?:$|[ ._\-])`)
	audioAAC        = regexp.MustCompile(`(?i)(?:^|[ ._\-])aac(?:\d|$|[ ._\-])`)
	audioMP3        = regexp.MustCompile(`(?i)(?:^|[ ._\-])mp3(?:$|[ ._\-])`)
	audioAtmos      = regexp.MustCompile(`(?i)(?:^|[ ._\-])atmos(?:$|[ ._\-])`)
)

// CanonicalResolution normalizes only explicit, reliable release resolution
// values. The original provider field remains untouched as evidence.
func CanonicalResolution(value string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	switch trimmed {
	case "4k", "uhd", "2160", "2160p":
		return "2160p", true
	case "1080", "1080p":
		return "1080p", true
	case "720", "720p":
		return "720p", true
	case "576", "576p":
		return "576p", true
	case "480", "480p":
		return "480p", true
	}
	match := resolutionSize.FindStringSubmatch(trimmed)
	if len(match) != 3 {
		return "", false
	}
	width, err := strconv.Atoi(match[1])
	if err != nil {
		return "", false
	}
	height := match[2]
	minimumWidth := map[string]int{"2160": 3840, "1080": 1440, "720": 960, "576": 704, "480": 640}[height]
	if width < minimumWidth {
		return "", false
	}
	return height + "p", true
}

func TitleMatches(releaseTitle, requestedTitle string) bool {
	release := strings.Join(strings.Fields(titleSeparator.ReplaceAllString(strings.ToLower(releaseTitle), " ")), " ")
	requested := strings.Join(strings.Fields(titleSeparator.ReplaceAllString(strings.ToLower(requestedTitle), " ")), " ")
	return requested != "" && (release == requested || strings.HasPrefix(release, requested+" "))
}

func TitleContainsYear(releaseTitle string, year uint16) bool {
	if year == 0 {
		return false
	}
	tokens := strings.Fields(titleSeparator.ReplaceAllString(releaseTitle, " "))
	wanted := strconv.Itoa(int(year))
	for _, token := range tokens {
		if token == wanted {
			return true
		}
	}
	return false
}

// InferTitleFacts adds visibly inferred facts without replacing provider
// observations. Contradictions remain conflicting and can never silently pass
// a hard requirement.
func InferTitleFacts(observation Observation, observedAt time.Time) Observation {
	inferred := []Fact{}
	if match := resolutionToken.FindStringSubmatch(observation.Title); len(match) == 2 {
		if resolution, reliable := CanonicalResolution(match[1]); reliable {
			inferred = append(inferred, Fact{Name: "resolution", Values: []string{resolution}, State: FactInferred, ObservedAt: observedAt.UTC()})
		}
	}
	lower := strings.ToLower(observation.Title)
	if match := seasonToken.FindStringSubmatch(observation.Title); len(match) == 2 {
		if season, err := strconv.Atoi(match[1]); err == nil {
			inferred = append(inferred, Fact{Name: "season_number", Values: []string{strconv.Itoa(season)}, State: FactInferred, ObservedAt: observedAt.UTC()})
		}
	}
	for _, source := range []struct{ token, value string }{{"remux", "remux"}, {"blu-ray", "bluray"}, {"bluray", "bluray"}, {"web-dl", "webdl"}, {"webdl", "webdl"}, {"webrip", "webrip"}, {"hdtv", "hdtv"}} {
		if strings.Contains(lower, source.token) {
			inferred = append(inferred, Fact{Name: "source", Values: []string{source.value}, State: FactInferred, ObservedAt: observedAt.UTC()})
			break
		}
	}
	for _, codec := range []struct{ token, value string }{{"av1", "av1"}, {"x265", "hevc"}, {"h265", "hevc"}, {"hevc", "hevc"}, {"x264", "avc"}, {"h264", "avc"}} {
		if strings.Contains(lower, codec.token) {
			inferred = append(inferred, Fact{Name: "video_codec", Values: []string{codec.value}, State: FactInferred, ObservedAt: observedAt.UTC()})
			break
		}
	}
	if audio := inferredAudioCodec(observation.Title); audio != "" {
		inferred = append(inferred, Fact{Name: "audio_codec", Values: []string{audio}, State: FactInferred, ObservedAt: observedAt.UTC()})
	}
	if strings.Contains(lower, "dolby vision") || strings.Contains(lower, "dovi") || strings.Contains(lower, " dv ") {
		inferred = append(inferred, Fact{Name: "dynamic_range", Values: []string{"dolby_vision"}, State: FactInferred, ObservedAt: observedAt.UTC()})
	} else if strings.Contains(lower, "hdr10+") {
		inferred = append(inferred, Fact{Name: "dynamic_range", Values: []string{"hdr10plus"}, State: FactInferred, ObservedAt: observedAt.UTC()})
	} else if strings.Contains(lower, "hdr") {
		inferred = append(inferred, Fact{Name: "dynamic_range", Values: []string{"hdr"}, State: FactInferred, ObservedAt: observedAt.UTC()})
	}
	if episodeRange.MatchString(observation.Title) {
		inferred = append(inferred, Fact{Name: "release_scope", Values: []string{"season_pack"}, State: FactInferred, ObservedAt: observedAt.UTC()})
	} else if episodeToken.MatchString(observation.Title) {
		inferred = append(inferred, Fact{Name: "release_scope", Values: []string{"episode"}, State: FactInferred, ObservedAt: observedAt.UTC()})
	} else if seasonToken.MatchString(observation.Title) || strings.Contains(lower, "complete season") || strings.Contains(lower, "season complete") {
		inferred = append(inferred, Fact{Name: "release_scope", Values: []string{"season_pack"}, State: FactInferred, ObservedAt: observedAt.UTC()})
	}
	for _, fact := range inferred {
		observation.Facts = mergeFact(observation.Facts, fact)
	}
	return observation
}

func inferredAudioCodec(title string) string {
	atmos := audioAtmos.MatchString(title)
	switch {
	case audioTrueHD.MatchString(title) && atmos:
		return "truehd_atmos"
	case audioDTSX.MatchString(title):
		return "dts_x"
	case audioTrueHD.MatchString(title):
		return "truehd"
	case audioDTSHDMA.MatchString(title):
		return "dts_hd_ma"
	case audioDTSHD.MatchString(title):
		return "dts_hd"
	case audioFLAC.MatchString(title):
		return "flac"
	case audioLPCM.MatchString(title):
		return "lpcm"
	case audioEAC3.MatchString(title) && atmos:
		return "eac3_atmos"
	case audioEAC3.MatchString(title):
		return "eac3"
	case audioDTS.MatchString(title):
		return "dts"
	case audioAC3.MatchString(title):
		return "ac3"
	case audioOpus.MatchString(title):
		return "opus"
	case audioAAC.MatchString(title):
		return "aac"
	case audioMP3.MatchString(title):
		return "mp3"
	default:
		return ""
	}
}

func mergeFact(facts []Fact, incoming Fact) []Fact {
	for index := range facts {
		if normalize(facts[index].Name) != normalize(incoming.Name) {
			continue
		}
		if intersectsFold(facts[index].Values, incoming.Values) {
			return facts
		}
		facts[index].Values = append(facts[index].Values, incoming.Values...)
		facts[index].State = FactConflicting
		return facts
	}
	return append(facts, incoming)
}
