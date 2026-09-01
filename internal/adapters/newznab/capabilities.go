package newznab

import (
	"errors"
	"sort"
	"strings"
)

type SearchDialect struct {
	Mode   string   `json:"mode"`
	Params []string `json:"params"`
}

type SearchDialects struct {
	Movie  SearchDialect `json:"movie"`
	Series SearchDialect `json:"series"`
}

var ErrSearchCapabilityRequired = errors.New("Newznab CAPS does not advertise a usable search capability")

// SelectSearchDialects records only capabilities advertised by the provider.
// Specialized modes are preferred, with general search retained as the safe
// fallback for implementations that do not expose movie or TV search.
func SelectSearchDialects(caps CapsSnapshot) (SearchDialects, error) {
	capabilities := make(map[string]SearchDialect, len(caps.Searches))
	for _, capability := range caps.Searches {
		if !capability.Available {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(capability.Mode))
		wireMode := mode
		switch mode {
		case "movie-search":
			wireMode = "movie"
		case "tv-search":
			wireMode = "tvsearch"
		case "search":
		default:
			continue
		}
		params := canonicalParams(capability.Params)
		if !supportsParam(params, "q") {
			continue
		}
		capabilities[mode] = SearchDialect{Mode: wireMode, Params: params}
	}
	generic, genericOK := capabilities["search"]
	movie, movieOK := capabilities["movie-search"]
	series, seriesOK := capabilities["tv-search"]
	if !movieOK {
		movie = generic
	}
	if !seriesOK {
		series = generic
	}
	if movie.Mode == "" || series.Mode == "" || (!genericOK && !movieOK && !seriesOK) {
		return SearchDialects{}, ErrSearchCapabilityRequired
	}
	return SearchDialects{Movie: movie, Series: series}, nil
}

func (dialect SearchDialect) Supports(name string) bool {
	return supportsParam(dialect.Params, name)
}

func canonicalParams(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func supportsParam(params []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, value := range params {
		if strings.ToLower(strings.TrimSpace(value)) == wanted {
			return true
		}
	}
	return false
}
