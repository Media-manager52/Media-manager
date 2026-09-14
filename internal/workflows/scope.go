package workflows

import (
	"fmt"
	"strconv"
	"strings"
)

// VariantScope describes the durable acquisition scope encoded in a workflow
// variant. Media Manager historically encoded a single "default" (movie) or
// complete-season scope ("season:01"). Airing television adds an explicit
// episode scope ("season:01:episode:02") that acquires one released episode
// with its own single-episode expectation and AltMount job.
type VariantScope struct {
	Kind    VariantKind
	Season  uint16
	Episode uint16
}

type VariantKind string

const (
	VariantDefault VariantKind = "default"
	VariantSeason  VariantKind = "season"
	VariantEpisode VariantKind = "episode"
)

// SeasonVariant renders the durable complete-season variant for a season.
func SeasonVariant(season uint16) string {
	return fmt.Sprintf("season:%02d", season)
}

// EpisodeVariant renders the durable single-episode variant for a season and
// episode. It is deliberately distinct from the season variant so it has its
// own immutable expectation, AltMount job, acquisition round, and publication.
func EpisodeVariant(season, episode uint16) string {
	return fmt.Sprintf("season:%02d:episode:%02d", season, episode)
}

// ParseVariant decodes a durable workflow variant. An unrecognized variant
// returns ok=false so callers never guess at an acquisition scope.
func ParseVariant(variant string) (VariantScope, bool) {
	trimmed := strings.TrimSpace(variant)
	if trimmed == "" || trimmed == string(VariantDefault) {
		return VariantScope{Kind: VariantDefault}, true
	}
	if !strings.HasPrefix(trimmed, "season:") {
		return VariantScope{}, false
	}
	rest := strings.TrimPrefix(trimmed, "season:")
	if !strings.Contains(rest, ":") {
		season, ok := parseScopeNumber(rest, 999)
		if !ok {
			return VariantScope{}, false
		}
		return VariantScope{Kind: VariantSeason, Season: season}, true
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 3 || parts[1] != "episode" {
		return VariantScope{}, false
	}
	season, seasonOK := parseScopeNumber(parts[0], 999)
	episode, episodeOK := parseScopeNumber(parts[2], 9999)
	if !seasonOK || !episodeOK || episode == 0 {
		return VariantScope{}, false
	}
	return VariantScope{Kind: VariantEpisode, Season: season, Episode: episode}, true
}

func parseScopeNumber(value string, maximum int) (uint16, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > maximum {
		return 0, false
	}
	return uint16(parsed), true
}
