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

// One filter request per scan. Alternate ordinal/calendar labels for annual
// snapshots; every eight complete scans move to the next bounded result page.
// Query only original/canonical Danish titles, never foreign-title aliases.
func oldBoysSearchRequest(search postgres.SearchContext) oldboys.SearchRequest {
	title := search.Media.Title
	if search.Media.Metadata != nil && strings.TrimSpace(search.Media.Metadata.OriginalTitle) != "" {
		title = search.Media.Metadata.OriginalTitle
	}
	title = releases.SearchQueryTitle(title)
	category := oldboys.MovieCategory
	phase := search.CompleteSearches
	suffix := ""
	if search.Media.Kind == catalog.KindSeries {
		category = oldboys.SeriesCategory
		scope, ok := workflows.ParseVariant(search.Variant)
		if ok && (scope.Kind == workflows.VariantSeason || scope.Kind == workflows.VariantEpisode) {
			suffix = fmt.Sprintf(" S%02d", scope.Season)
			if scope.Kind == workflows.VariantEpisode {
				suffix += fmt.Sprintf("E%02d", scope.Episode)
			}
			if search.EpisodeNumbering.Year != 0 && phase%2 == 0 {
				// Bare year catches S2026 and date-named uploads. Frozen numbering and
				// exact workflow scope decide which returned episodes are acceptable.
				suffix = fmt.Sprintf(" %d", search.EpisodeNumbering.Year)
			}
		}
	} else if search.Media.Year != nil {
		suffix = fmt.Sprintf(" %d", *search.Media.Year)
	}
	return oldboys.SearchRequest{Name: title + suffix + " DANiSH", Category: category, Page: 1 + int((phase/8)%oldboys.MaxPage)}
}

var oldBoysWords = regexp.MustCompile(`[^\pL\pN]+`)
var oldBoysScopePrefix = regexp.MustCompile(`^(?:s[0-9]{1,4}(?:e[0-9]{1,3})?|(?:19|20)[0-9]{2})(?: |$)`)

// A native provider that supplies no IDs needs explicitly labelled inferred
// catalogue evidence. Bind only Danish-original series with an exact saved
// original/canonical title immediately followed by a scene scope/year token.
// This is never recorded as a provider-supplied TMDB/TVDB/IMDb identity.
func bindOldBoysCatalogue(observation releases.Observation, search postgres.SearchContext, now time.Time) releases.Observation {
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
	observation.Facts = append(observation.Facts, releases.Fact{Name: "oldboys_catalogue_binding", Values: []string{media.ID.String()}, State: releases.FactInferred, ObservedAt: now})
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
