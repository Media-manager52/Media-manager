package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// Shared season answers.
//
// A back-catalogue season with many missing episodes sent one search per
// episode to every indexer: about four fifths of all searches went to such
// seasons, although an indexer's answer to "season N" holds every episode of
// it. When a season has at least seasonShareMinEpisodes open episode
// workflows, an episode search asks the indexer for the whole season once and
// stores the answer; the season's other episodes reuse it for
// seasonShareLifetime. Each episode keeps only the releases its own scope
// rules do not reject and evaluates them under its own language and quality
// rules, exactly as if the indexer had returned them to an episode search.
//
// An answer is not reused by an episode workflow created after it, after the
// newest-uploads reader matched a new upload to that workflow, or after the
// owner asked for a search of it. An answer the indexer could not deliver in
// full (more results than the page bound) is never shared: the episode then
// searches on its own as before.
const (
	seasonShareMinEpisodes = 3
	seasonShareLifetime    = 12 * time.Hour
)

// seasonShareApplies reports whether this search is an episode search in a
// season with enough open episodes to share one season answer.
func (service *SearchService) seasonShareApplies(ctx context.Context, search postgres.SearchContext) (bool, error) {
	if search.Media.Kind != catalog.KindSeries {
		return false, nil
	}
	scope, ok := workflows.ParseVariant(search.Variant)
	if !ok || scope.Kind != workflows.VariantEpisode {
		return false, nil
	}
	open, err := service.Store.OpenEpisodeWorkflows(ctx, search.Media.ID, int(scope.Season))
	if err != nil {
		return false, err
	}
	return open >= seasonShareMinEpisodes, nil
}

// seasonRequest turns an identity-based episode search into the same search
// for the whole season, asking with title (when the request carries a title
// at all). Only tvsearch requests qualify: a free-text "Title S02" is not
// reliably matched against "S02E05" names.
func seasonRequest(request newznab.SearchRequest, title string) (newznab.SearchRequest, bool) {
	if request.Mode != "tvsearch" || request.Offset != 0 {
		return request, false
	}
	params := make([]newznab.Param, 0, len(request.Params))
	removed, season := false, false
	for _, param := range request.Params {
		switch param.Name {
		case "ep":
			removed = true
			continue
		case "season":
			season = true
		case "q":
			if title != "" {
				param.Value = title
			}
		}
		params = append(params, param)
	}
	if !removed || !season {
		return request, false
	}
	request.Params = params
	return request, true
}

// seasonTitle is the title every episode of a season asks with during one
// seasonShareLifetime slot. Episode searches rotate through the title's names
// one per search, so two episodes of a season usually ask with different
// names and could never share an answer; the season answer rotates through
// the same names by time instead.
func seasonTitle(search postgres.SearchContext, now time.Time) string {
	slot := search
	slot.CompleteSearches = uint64(now.Unix() / int64(seasonShareLifetime/time.Second))
	if slot.EpisodeNumbering.Year != 0 {
		// searchTitle halves the phase for annually numbered series.
		slot.CompleteSearches *= 2
	}
	return searchTitle(slot)
}

// seasonRequestFingerprint identifies one indexer's season request.
func seasonRequestFingerprint(indexerID uuid.UUID, request newznab.SearchRequest) []byte {
	encoded, _ := json.Marshal(struct {
		Indexer    string
		Mode       string
		Params     []newznab.Param
		Categories []int
		Limit      int
		Extended   bool
	}{indexerID.String(), request.Mode, request.Params, request.Categories, request.Limit, request.Extended})
	sum := sha256.Sum256(encoded)
	return sum[:]
}

// seasonAnswer returns a complete season answer for this episode search from
// one indexer: a stored one, or a new one it asks the indexer for and
// stores. ok is false when there is none to use; the caller then runs the
// episode search as before. An error is the indexer's error for the season
// request and is handled like an episode search error.
func (service *SearchService) seasonAnswer(ctx context.Context, indexerID uuid.UUID, search postgres.SearchContext, request newznab.SearchRequest, now time.Time, pagingWait *time.Duration, searchPage func(newznab.SearchRequest) (newznab.SearchPage, error)) (observations []releases.Observation, locators []string, detail string, ok bool, err error) {
	season, applies := seasonRequest(request, seasonTitle(search, now))
	if !applies {
		return nil, nil, "", false, nil
	}
	fingerprint := seasonRequestFingerprint(indexerID, season)
	shared, found, err := service.Store.SharedSeasonResponse(ctx, indexerID, fingerprint, search.Workflow.ID, now.Add(-seasonShareLifetime))
	if err != nil {
		return nil, nil, "", false, err
	}
	if found {
		if !shared.Complete {
			return nil, nil, "", false, nil
		}
		return shared.Observations, shared.Locators, "reused season search", true, nil
	}
	first, err := searchPage(season)
	if err != nil {
		return nil, nil, "", false, err
	}
	all, complete := searchAllPages(ctx, season, first, pagingWait, searchPage)
	sanitized := make([]releases.Observation, 0, len(all))
	allLocators := make([]string, 0, len(all))
	for _, observation := range all {
		sanitized = append(sanitized, newznab.SanitizeObservation(observation))
		allLocators = append(allLocators, newznab.DownloadLocatorReference(observation.DownloadURL, observation.NativeGUID))
	}
	stored, storedLocators, err := service.Store.SaveSeasonResponse(ctx, indexerID, fingerprint, sanitized, allLocators, complete, time.Now().UTC())
	if err != nil {
		return nil, nil, "", false, err
	}
	if !complete {
		return nil, nil, "", false, nil
	}
	return stored, storedLocators, "season search", true, nil
}

// episodeInScope reports whether the episode's scope rules accept or hold a
// release from a season answer; releases of other episodes are left out, so
// an episode keeps only what an episode search would have returned for it.
func episodeInScope(observation releases.Observation, scopeRules []releases.Rule) bool {
	evaluation, err := releases.Evaluate(observation, scopeRules)
	return err == nil && evaluation.Verdict != releases.VerdictRejected
}
