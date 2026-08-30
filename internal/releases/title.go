package releases

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	resolutionToken = regexp.MustCompile(`(?i)(?:^|[ ._\-])(2160p|1080p|720p|576p|480p)(?:$|[ ._\-])`)
	seasonToken     = regexp.MustCompile(`(?i)(?:^|[ ._\-])S(\d{1,3})(?:$|[ ._\-]|E)`)
	episodeToken    = regexp.MustCompile(`(?i)S\d{1,3}E\d{1,3}`)
	episodeRange    = regexp.MustCompile(`(?i)S\d{1,3}E\d{1,3}(?:-E?\d{1,3}|E\d{1,3})(?:$|[ ._\-])`)
	titleSeparator  = regexp.MustCompile(`[^\pL\pN]+`)
)

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
		inferred = append(inferred, Fact{Name: "resolution", Values: []string{strings.ToLower(match[1])}, State: FactInferred, ObservedAt: observedAt.UTC()})
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
