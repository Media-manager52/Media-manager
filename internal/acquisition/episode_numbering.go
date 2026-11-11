package acquisition

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// Alternate one existing scan with a bounded ID-only discovery. Removing the
// unreliable season/episode filters finds year/date releases without guessing
// provider season numbers or increasing requests per scan. Full snapshot-based
// mapping and the normal identity/scope/policy checks still decide eligibility.
const numberedIDOnlyMaxAge = 180 * 24 * time.Hour

// The ID-only alternate deliberately drops season/episode filters so date- and
// year-named releases can be found even when an indexer's scoped search hides
// them. It returns the newest results, so it is only useful while the target
// date is recent. On an old season it doubles the query count while observing
// only modern episodes and storing hundreds of irrelevant candidates. An old
// episode uses the same alternate scan to ask for its air date by text, which
// is how those releases are named. The request count stays the same.
//
// An old episode rotates through three forms, one per complete search: the
// provider's own daily-episode search (series ID, season = air year, ep =
// month/day, the Newznab form for date-named shows, which needs no name), its
// air date by text under the shortest saved name, and the scoped catalogue
// search. Daily shows are named by date in many spellings ("WWE.Smackdown.
// 2016.12.27" for "WWE Friday Night SmackDown"), so the long canonical name
// alone rarely matched.
func numberedSearchRequest(search postgres.SearchContext, request newznab.SearchRequest, now time.Time) newznab.SearchRequest {
	if search.EpisodeNumbering.Year == 0 || request.Mode != "tvsearch" {
		return request
	}
	if !recentNumberedTarget(search, now) {
		switch search.CompleteSearches % 3 {
		case 0:
			return dailyEpisodeSearchRequest(search, request)
		case 1:
			return airDateSearchRequest(search, request)
		}
		return request
	}
	if search.CompleteSearches%2 == 0 {
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

// airDateSearchRequest replaces the scoped parameters with the series name and
// air date, written with spaces so dotted and dashed release names both match.
// Identity parameters are dropped because indexers may then ignore the text.
// Only a single selected episode has one date to ask for.
func airDateSearchRequest(search postgres.SearchContext, request newznab.SearchRequest) newznab.SearchRequest {
	if selectedNumberedEpisode(search.Variant) == 0 {
		return request
	}
	date := latestNumberedAirDate(search)
	if date.IsZero() {
		return request
	}
	for _, p := range request.Params {
		if p.Name == "q" {
			request.Params = []newznab.Param{{Name: "q", Value: shortSearchTitle(search, search.CompleteSearches/3) + " " + date.Format("2006 01 02")}}
			return request
		}
	}
	return request
}

// dailyEpisodeSearchRequest asks for one dated episode the way Newznab names
// daily shows: the series IDs with season = air year and ep = month/day. It
// needs no name, so it finds every spelling the indexer filed under the
// series. Without a series ID or a season/ep parameter the request is left as
// it is.
func dailyEpisodeSearchRequest(search postgres.SearchContext, request newznab.SearchRequest) newznab.SearchRequest {
	if selectedNumberedEpisode(search.Variant) == 0 {
		return request
	}
	date := latestNumberedAirDate(search)
	if date.IsZero() {
		return request
	}
	var ids []newznab.Param
	season, episode := false, false
	for _, p := range request.Params {
		switch p.Name {
		case "tvdbid", "tmdbid", "imdbid":
			ids = append(ids, p)
		case "season":
			season = true
		case "ep":
			episode = true
		}
	}
	if len(ids) == 0 || !season || !episode {
		return request
	}
	request.Params = append(ids, newznab.Param{Name: "season", Value: date.Format("2006")}, newznab.Param{Name: "ep", Value: date.Format("01/02")})
	return request
}

// shortSearchTitle is one of the saved names with the fewest words, written
// for searching; phase rotates among names of that length. Release names of
// date-named shows mostly use the short form of the name, and an indexer
// matches every word of the query.
func shortSearchTitle(search postgres.SearchContext, phase uint64) string {
	var shortest []string
	fewest := 0
	for _, title := range searchTitles(search) {
		if !latinTitle(title) {
			continue
		}
		words := len(strings.Fields(title))
		switch {
		case len(shortest) == 0 || words < fewest:
			shortest, fewest = []string{title}, words
		case words == fewest:
			shortest = append(shortest, title)
		}
	}
	if len(shortest) == 0 {
		return releases.SearchQueryTitle(search.Media.Title)
	}
	return shortest[phase%uint64(len(shortest))]
}

// latinTitle reports whether a search title is written in Latin letters,
// digits and spaces: release names are, so a Japanese alias cannot match.
func latinTitle(title string) bool {
	for _, r := range title {
		if r > unicode.MaxLatin1 && !unicode.In(r, unicode.Latin) {
			return false
		}
	}
	return strings.TrimSpace(title) != ""
}

func recentNumberedTarget(search postgres.SearchContext, now time.Time) bool {
	latest := latestNumberedAirDate(search)
	return !latest.IsZero() && !latest.Before(now.UTC().Add(-numberedIDOnlyMaxAge))
}

func selectedNumberedEpisode(variant string) uint16 {
	selected := uint16(0)
	parts := strings.Split(variant, ":")
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] != "episode" {
			continue
		}
		value, err := strconv.Atoi(parts[index+1])
		if err == nil && value > 0 && value <= 65535 {
			selected = uint16(value)
		}
	}
	return selected
}

func latestNumberedAirDate(search postgres.SearchContext) time.Time {
	selected := selectedNumberedEpisode(search.Variant)
	var latest time.Time
	for date, episode := range search.EpisodeNumbering.Dates {
		if selected > 0 && episode != selected {
			continue
		}
		parsed, err := time.Parse("2006-01-02", date)
		if err == nil && parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

func applyEpisodeNumbering(observation releases.Observation, search postgres.SearchContext, _ time.Time) releases.Observation {
	if search.Media.Kind != catalog.KindSeries {
		return observation
	}
	episode, broadcast := search.EpisodeNumbering.BroadcastEpisode(observation.Title)
	pack, broadcastPack := search.EpisodeNumbering.BroadcastSeasonPack(observation.Title)
	if !broadcast && !broadcastPack {
		return observation
	}
	positive := hasOldBoysCatalogueBinding(observation, search.Media) || danishTaggedSeriesRelease(observation.Title, search.Media)
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
	resolved := positive && identity.Verdict == releases.VerdictEligible && (episode > 0 || pack)
	// Reconstruct evaluator-owned facts from the original evidence on every run.
	// Existing unmapped_ facts are the provider aliases preserved by the first
	// application; never rename those again. On later runs, replace the derived
	// group at its original position so unrelated evidence (for example an
	// OldBoys range fact) does not move and change the persisted JSON.
	hasDerivedNumbering := false
	for _, f := range observation.Facts {
		if strings.EqualFold(strings.TrimSpace(f.Name), "episode_numbering") {
			hasDerivedNumbering = true
			break
		}
	}
	state := releases.FactUnknown
	seasonValues, episodeValues := []string(nil), []string(nil)
	if resolved {
		state = releases.FactInferred
		seasonValues = []string{strconv.Itoa(int(search.EpisodeNumbering.Season))}
		if episode > 0 {
			episodeValues = []string{strconv.Itoa(int(episode))}
		}
	}
	scope := "episode"
	if pack {
		scope = "season_pack"
	}
	derivedAt := derivedFactTime(observation)
	provenance := "unresolved broadcast numbering"
	if resolved {
		provenance = "frozen annual season snapshot"
	}
	derived := []releases.Fact{
		{Name: "season_number", Values: seasonValues, State: state, ObservedAt: derivedAt},
		{Name: "episode_number", Values: episodeValues, State: state, ObservedAt: derivedAt},
		{Name: "release_scope", Values: []string{scope}, State: state, ObservedAt: derivedAt},
		{Name: "episode_numbering", Values: []string{provenance}, State: state, ObservedAt: derivedAt},
	}
	facts := make([]releases.Fact, 0, len(observation.Facts)+4)
	insertedDerived := false
	for _, f := range observation.Facts {
		name := strings.ToLower(strings.TrimSpace(f.Name))
		switch name {
		case "episode_numbering", "season_number", "episode_number", "episode", "release_scope":
			if hasDerivedNumbering {
				if !insertedDerived {
					facts = append(facts, derived...)
					insertedDerived = true
				}
				continue
			}
			if name != "episode_numbering" {
				f.Name = "unmapped_" + f.Name
			}
		}
		facts = append(facts, f)
	}
	if !insertedDerived {
		facts = append(facts, derived...)
	}
	facts = dedupeUnmappedFacts(facts)
	observation.Facts = facts
	return observation
}
