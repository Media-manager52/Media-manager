package acquisition

import (
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// Alternate one existing scan with a bounded ID-only discovery. Removing the
// unreliable season/episode filters finds year/date releases without guessing
// provider season numbers or increasing requests per scan. Full snapshot-based
// mapping and the normal identity/scope/policy checks still decide eligibility.
func numberedSearchRequest(search postgres.SearchContext, request newznab.SearchRequest) newznab.SearchRequest {
	if search.EpisodeNumbering.Year == 0 || search.CompleteSearches%2 == 0 || request.Mode != "tvsearch" {
		return request
	}
	var ids []newznab.Param
	for _, p := range request.Params {
		switch p.Name {
		case "tvdbid", "tmdbid", "imdbid":
			ids = append(ids, p)
		}
	}
	if len(ids) == 0 {
		return request
	}
	request.Params = ids
	return request
}

func applyEpisodeNumbering(observation releases.Observation, search postgres.SearchContext, now time.Time) releases.Observation {
	if search.Media.Kind != catalog.KindSeries {
		return observation
	}
	episode, broadcast := search.EpisodeNumbering.BroadcastEpisode(observation.Title)
	if !broadcast {
		return observation
	}
	positive := false
	for _, f := range observation.Facts {
		for _, id := range search.Media.Identities {
			if id.State == catalog.IdentityVerified && f.Name == id.Namespace+"_id" && f.State != releases.FactUnknown && f.State != releases.FactConflicting {
				for _, v := range f.Values {
					if providerIdentityEqual(f.Name, v, id.Value) {
						positive = true
					}
				}
			}
		}
	}
	identity := constrainMediaMatch(observation, releases.Evaluation{Verdict: releases.VerdictEligible}, search.Media)
	resolved := positive && identity.Verdict == releases.VerdictEligible && episode > 0
	// Preserve every previous scope fact under an explicit provenance name; raw
	// provider fields also remain unchanged in the persisted observation.
	facts := make([]releases.Fact, 0, len(observation.Facts)+4)
	for _, f := range observation.Facts {
		switch strings.ToLower(f.Name) {
		case "season_number", "episode_number", "episode", "release_scope":
			f.Name = "unmapped_" + f.Name
		}
		facts = append(facts, f)
	}
	state := releases.FactUnknown
	seasonValues, episodeValues := []string(nil), []string(nil)
	if resolved {
		state = releases.FactInferred
		seasonValues = []string{strconv.Itoa(int(search.EpisodeNumbering.Season))}
		episodeValues = []string{strconv.Itoa(int(episode))}
	}
	facts = append(facts, releases.Fact{Name: "season_number", Values: seasonValues, State: state, ObservedAt: now}, releases.Fact{Name: "episode_number", Values: episodeValues, State: state, ObservedAt: now}, releases.Fact{Name: "release_scope", Values: []string{"episode"}, State: state, ObservedAt: now})
	provenance := "unresolved broadcast numbering"
	if resolved {
		provenance = "frozen annual season snapshot"
	}
	facts = append(facts, releases.Fact{Name: "episode_numbering", Values: []string{provenance}, State: state, ObservedAt: now})
	observation.Facts = facts
	return observation
}
