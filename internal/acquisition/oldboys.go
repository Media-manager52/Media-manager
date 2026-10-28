package acquisition

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/oldboys"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

func newOldBoysClient(ctx context.Context, store *postgres.Store, scheduler *indexers.Scheduler, vault *oldboyscredentials.Service, integration postgres.Integration, transport http.RoundTripper) (*oldboys.Client, error) {
	if vault == nil {
		return nil, errors.New("OldBoys credentials are unavailable")
	}
	credentials, _, err := vault.LoadVersioned(ctx)
	if err != nil {
		return nil, err
	}
	scheduler.SetCommissioningGateSource(integration.ID.String(), store)
	return oldboys.New(integration.ID, credentials, scheduler, transport, func(ctx context.Context) error { return store.ValidateOldBoysSnapshot(ctx, integration, false) })
}

// One filter request per scan. Include a title-only shape so collection packs
// are discoverable; each shape visits four pages across completed scans.
// Query only original/canonical Danish titles, never foreign-title aliases.
func oldBoysSearchRequest(search postgres.SearchContext) oldboys.SearchRequest {
	title := search.Media.Title
	if search.Media.Metadata != nil && strings.TrimSpace(search.Media.Metadata.OriginalTitle) != "" {
		title = search.Media.Metadata.OriginalTitle
	}
	title = releases.SearchQueryTitle(title)
	category := oldboys.MovieCategory
	phase := search.CompleteSearches
	page := 1 + int((phase/8)%oldboys.MaxPage)
	suffix := ""
	if search.Media.Kind == catalog.KindSeries {
		category = oldboys.SeriesCategory
		scope, ok := workflows.ParseVariant(search.Variant)
		if ok && (scope.Kind == workflows.VariantSeason || scope.Kind == workflows.VariantEpisode) {
			cycle := uint64(2)
			if search.EpisodeNumbering.Year != 0 {
				cycle = 3
			}
			page = 1 + int((phase/cycle)%oldboys.MaxPage)
			suffix = fmt.Sprintf(" S%02d", scope.Season)
			if scope.Kind == workflows.VariantEpisode {
				suffix += fmt.Sprintf("E%02d", scope.Episode)
			}
			if phase%cycle == 0 {
				suffix = ""
			} else if search.EpisodeNumbering.Year != 0 && phase%cycle == 1 {
				// Bare year catches S2026 and date-named uploads. Frozen numbering and
				// exact workflow scope decide which returned episodes are acceptable.
				suffix = fmt.Sprintf(" %d", search.EpisodeNumbering.Year)
			}
		}
	} else if search.Media.Year != nil {
		suffix = fmt.Sprintf(" %d", *search.Media.Year)
	}
	return oldboys.SearchRequest{Name: title + suffix + " DANiSH", Category: category, Page: page}
}

// The range is title evidence only. Exact episode coverage remains a mandatory
// output check; no provider episode IDs or single-episode facts are invented.
func oldBoysSeasonRange(observation releases.Observation, media catalog.MediaItem) (packs.SeasonRange, bool) {
	if !hasOldBoysCatalogueBinding(observation, media) {
		return packs.SeasonRange{}, false
	}
	return packs.DeclaredSeasonRange(observation.Title)
}

func oldBoysRangeScope(observation releases.Observation, search postgres.SearchContext, scopeRules, qualityRules []releases.Rule) (releases.Observation, []releases.Rule) {
	rangeScope, ok := oldBoysSeasonRange(observation, search.Media)
	if !ok && (!hasOldBoysCatalogueBinding(observation, search.Media) || !packs.HasSeasonRange(observation.Title)) {
		return observation, append(append([]releases.Rule{}, scopeRules...), qualityRules...)
	}
	variant, validVariant := workflows.ParseVariant(search.Variant)
	if !validVariant || (variant.Kind != workflows.VariantSeason && variant.Kind != workflows.VariantEpisode) {
		return observation, append(append([]releases.Rule{}, scopeRules...), qualityRules...)
	}
	values := []string{}
	state := releases.FactUnknown
	if ok && search.Media.Metadata.SeasonCount != nil && rangeScope.Last <= *search.Media.Metadata.SeasonCount {
		state = releases.FactInferred
		for season := rangeScope.First; season <= rangeScope.Last; season++ {
			values = append(values, strconv.Itoa(int(season)))
		}
	}
	observation.Facts = replaceDerivedFact(observation.Facts, releases.Fact{Name: "declared_pack_seasons", Values: values, State: state, ObservedAt: derivedFactTime(observation)})
	// Preserve the generic title facts (including its first season) as evidence.
	// Native range admission uses membership rather than rewriting those facts.
	rules := []releases.Rule{{Name: "Requested season in declared collection", Fact: "declared_pack_seasons", AllowedValues: []string{strconv.Itoa(int(variant.Season))}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	return observation, append(rules, qualityRules...)
}

var oldBoysWords = regexp.MustCompile(`[^\pL\pN]+`)
var oldBoysScopePrefix = regexp.MustCompile(`^(?:s[0-9]{1,4}(?:e[0-9]{1,3})?|(?:19|20)[0-9]{2})(?: |$)`)

// A native provider that supplies no IDs needs explicitly labelled inferred
// catalogue evidence. Bind only Danish-original series with an exact saved
// original/canonical title immediately followed by a scene scope/year token.
// This is never recorded as a provider-supplied TMDB/TVDB/IMDb identity.
func bindOldBoysCatalogue(observation releases.Observation, search postgres.SearchContext, _ time.Time) releases.Observation {
	media := search.Media
	if !oldBoysVerifiedCatalogue(media) {
		return observation
	}
	if observation.ParseVersion != "oldboys-v1" || media.Kind != catalog.KindSeries || !postgres.OldBoysMediaAllowed(media) || !oldboys.DanishTitle(observation.Title) || !oldBoysExactSeriesPrefix(observation.Title, media) {
		return observation
	}
	suffix, _ := oldBoysSeriesSuffix(observation.Title, media)
	if words := strings.Fields(suffix); len(words) > 0 {
		if year, err := strconv.Atoi(words[0]); err == nil && year >= 1900 && year <= 2099 {
			if (media.Year == nil || year != int(*media.Year)) && year != int(search.EpisodeNumbering.Year) {
				return observation
			}
		}
	}
	observation.Facts = replaceDerivedFact(observation.Facts, releases.Fact{Name: "oldboys_catalogue_binding", Values: []string{media.ID.String()}, State: releases.FactInferred, ObservedAt: derivedFactTime(observation)})
	return observation
}
func oldBoysExactSeriesPrefix(title string, media catalog.MediaItem) bool {
	_, ok := oldBoysSeriesSuffix(title, media)
	return ok
}
func oldBoysSeriesSuffix(title string, media catalog.MediaItem) (string, bool) {
	normalize := func(value string) string {
		return strings.Join(strings.Fields(oldBoysWords.ReplaceAllString(strings.ToLower(value), " ")), " ")
	}
	release := normalize(title)
	titles := []string{media.Title}
	if media.Metadata != nil {
		titles = append(titles, media.Metadata.OriginalTitle)
	}
	for _, name := range titles {
		for _, spelling := range releases.TitleSpellings(name) {
			prefix := normalize(spelling) + " "
			if prefix != " " && strings.HasPrefix(release, prefix) && oldBoysScopePrefix.MatchString(strings.TrimPrefix(release, prefix)) {
				return strings.TrimPrefix(release, prefix), true
			}
		}
	}
	return "", false
}
func hasOldBoysCatalogueBinding(observation releases.Observation, media catalog.MediaItem) bool {
	if !oldBoysVerifiedCatalogue(media) || observation.ParseVersion != "oldboys-v1" || !postgres.OldBoysMediaAllowed(media) || !oldBoysExactSeriesPrefix(observation.Title, media) || !oldboys.DanishTitle(observation.Title) {
		return false
	}
	for _, fact := range observation.Facts {
		if fact.Name == "oldboys_catalogue_binding" && fact.State == releases.FactInferred && len(fact.Values) == 1 && fact.Values[0] == media.ID.String() {
			return true
		}
	}
	return false
}

func oldBoysVerifiedCatalogue(media catalog.MediaItem) bool {
	if media.ID == (uuid.UUID{}) {
		return false
	}
	for _, id := range media.Identities {
		if id.State == catalog.IdentityVerified && strings.TrimSpace(id.Value) != "" && (id.Namespace == "imdb" || id.Namespace == "tmdb" || id.Namespace == "tvdb") {
			return true
		}
	}
	return false
}
