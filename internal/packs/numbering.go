package packs

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// EpisodeNumbering binds annual scene numbering to one immutable
// calendar-year season snapshot. A snapshot may have gaps where an episode was
// preempted; each present episode is still mapped exactly. It never changes the
// canonical episode keys.
type EpisodeNumbering struct {
	Season   uint16
	Year     uint16
	Dates    map[string]uint16
	Episodes map[uint16]bool
}

var broadcastEpisodePattern = regexp.MustCompile(`(?i)S(\d{4})E(\d{1,3})`)
var broadcastSeasonPattern = regexp.MustCompile(`(?i)S(\d{4})(?:$|[._ -])`)
var broadcastEpisodeSuffix = regexp.MustCompile(`(?i)^(?:[-_ ]E?\d{1,3}|[._ -]E\d{1,3})(?:$|[._ -])`)
var broadcastDatePattern = regexp.MustCompile(`(\d{4})[._-](\d{2})[._-](\d{2})`)

// SeasonAirYear identifies a season's calendar year from its dated episodes.
// Undated episodes cannot establish a year, and conflicting dated years fail.
func SeasonAirYear(expected []ExpectedEpisode) uint16 {
	var year uint16
	for _, e := range expected {
		if e.AirDate == "" {
			continue
		}
		d, err := time.Parse("2006-01-02", e.AirDate)
		if err != nil || d.Year() < 1900 || d.Year() > 2099 {
			return 0
		}
		if year != 0 && year != uint16(d.Year()) {
			return 0
		}
		year = uint16(d.Year())
	}
	return year
}

func AnnualEpisodeNumbering(season uint16, expected []ExpectedEpisode) EpisodeNumbering {
	n := EpisodeNumbering{Season: season, Dates: map[string]uint16{}, Episodes: map[uint16]bool{}}
	if season == 0 || len(expected) == 0 {
		return EpisodeNumbering{}
	}
	dates := map[string]bool{}
	for _, e := range expected {
		d, err := time.Parse("2006-01-02", e.AirDate)
		if err != nil || e.Key.Season != season || e.Key.Episode == 0 || n.Episodes[e.Key.Episode] || d.Year() < 1900 || d.Year() > 2099 {
			return EpisodeNumbering{}
		}
		if n.Year != 0 && n.Year != uint16(d.Year()) {
			return EpisodeNumbering{}
		}
		n.Year = uint16(d.Year())
		n.Episodes[e.Key.Episode] = true
		if dates[e.AirDate] {
			n.Dates[e.AirDate] = 0
		} else {
			n.Dates[e.AirDate] = e.Key.Episode
			dates[e.AirDate] = true
		}
	}
	return n
}

// BroadcastEpisode reports whether a filename explicitly uses year/date
// numbering, and resolves it only against the frozen mapping. Wrong years,
// dates, duplicate tokens and composite episodes remain unresolved.
func (n EpisodeNumbering) BroadcastEpisode(name string) (uint16, bool) {
	tokens := broadcastEpisodePattern.FindAllStringSubmatchIndex(name, -1)
	dates := broadcastDatePattern.FindAllStringSubmatchIndex(name, -1)
	if len(tokens) == 0 && (len(dates) == 0 || episodePattern.MatchString(name)) {
		return 0, false
	}
	if n.Year == 0 || len(tokens)+len(dates) != 1 || episodePattern.MatchString(name) {
		return 0, true
	}
	var start, end int
	var episode uint16
	if len(tokens) == 1 {
		m := tokens[0]
		start, end = m[0], m[1]
		year, _ := strconv.Atoi(name[m[2]:m[3]])
		e, _ := strconv.Atoi(name[m[4]:m[5]])
		if year != int(n.Year) || e < 1 || !n.Episodes[uint16(e)] {
			return 0, true
		}
		episode = uint16(e)
	} else {
		m := dates[0]
		start, end = m[0], m[1]
		date := fmt.Sprintf("%s-%s-%s", name[m[2]:m[3]], name[m[4]:m[5]], name[m[6]:m[7]])
		episode = n.Dates[date]
	}
	if broadcastEpisodeSuffix.MatchString(name[end:]) || (start > 0 && isASCIIAlphaNumeric(name[start-1])) || (end < len(name) && isASCIIAlphaNumeric(name[end])) {
		return 0, true
	}
	return episode, true
}

// BroadcastSeasonPack recognises a bare annual season label, not an episode
// or range. A mapped pack still needs exact inventory episode coverage.
func (n EpisodeNumbering) BroadcastSeasonPack(name string) (bool, bool) {
	matches := broadcastSeasonPattern.FindAllStringSubmatchIndex(name, -1)
	if len(matches) == 0 {
		return false, false
	}
	if n.Year == 0 || len(matches) != 1 || broadcastEpisodePattern.MatchString(name) || broadcastDatePattern.MatchString(name) || episodePattern.MatchString(name) {
		return false, true
	}
	m := matches[0]
	if broadcastEpisodeSuffix.MatchString(name[m[3]:]) || (m[0] > 0 && isASCIIAlphaNumeric(name[m[0]-1])) {
		return false, true
	}
	year, _ := strconv.Atoi(name[m[2]:m[3]])
	return year == int(n.Year), true
}
