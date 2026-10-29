package acquisition

import (
	"context"
	"errors"

	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

// errNoActiveSearchProfile marks an indexer whose immutable active capability
// profile is unavailable or cannot satisfy the current item. It is not an
// operational failure: the caller keeps the legacy commissioned dialect so an
// indexer that has not completed profile-backed commissioning keeps working.
var errNoActiveSearchProfile = errors.New("no usable active Newznab capability profile")

// searchProfilePlan resolves the indexer's active capability profile and plans
// the production request through the same pure planner that commissioning
// characterized. The returned plan is executed by the shared paced Newznab
// client; this function performs no network I/O and no scheduler work.
func (service *SearchService) searchProfilePlan(ctx context.Context, indexerID uuid.UUID, search postgres.SearchContext, config indexerConfig) (SearchPlan, error) {
	plan, _, _, err := service.searchProfilePlanWithIdentity(ctx, indexerID, search, config)
	return plan, err
}

// searchProfilePlanWithIdentity also returns the immutable active profile
// identity. Search uses it in the retry intent fingerprint; the planner output
// alone is not a complete capability/configuration revision.
func (service *SearchService) searchProfilePlanWithIdentity(ctx context.Context, indexerID uuid.UUID, search postgres.SearchContext, config indexerConfig) (SearchPlan, uuid.UUID, uint64, error) {
	profile, err := service.Store.ActiveIndexerCapabilityProfile(ctx, indexerID)
	if err != nil {
		if isUnusableSearchProfileError(err) {
			return SearchPlan{}, uuid.UUID{}, 0, errNoActiveSearchProfile
		}
		return SearchPlan{}, uuid.UUID{}, 0, err
	}
	document, err := newznab.DecodeCapabilityProfile(profile.Document)
	if err != nil {
		return SearchPlan{}, uuid.UUID{}, 0, errNoActiveSearchProfile
	}
	input := SearchPlanInput{Media: search.Media}
	input.Media.Title = searchTitle(search)
	if search.Media.Kind == catalog.KindSeries {
		season := seasonNumber(search.Variant)
		if season < 0 || season > maximumPlannedSeason {
			return SearchPlan{}, uuid.UUID{}, 0, errNoActiveSearchProfile
		}
		value := uint16(season)
		input.Season = &value
	}
	categories := newznab.NewznabCategorySelection{
		Movie:          append([]int(nil), config.MovieCategoryIDs...),
		Series:         append([]int(nil), config.SeriesCategoryIDs...),
		ReviewRequired: config.CategorySelectionReviewRequired,
	}
	plan, err := PlanSearch(document, input, categories)
	if err != nil {
		if errors.Is(err, ErrNoSatisfiablePrimary) || errors.Is(err, ErrInvalidCategorySelection) || errors.Is(err, ErrInvalidSearchPlanInput) {
			return SearchPlan{}, uuid.UUID{}, 0, errNoActiveSearchProfile
		}
		return SearchPlan{}, uuid.UUID{}, 0, err
	}
	return plan, profile.ID, profile.Revision, nil
}

// isUnusableSearchProfileError reports repository conditions that mean "this
// indexer has no profile-backed path right now" rather than an operational
// failure. Every listed condition is a deliberate, non-transient gate that the
// legacy dialect path already handled before the profile cutover.
func isUnusableSearchProfileError(err error) bool {
	return errors.Is(err, postgres.ErrNotFound) ||
		errors.Is(err, postgres.ErrInvalidIndexerCapabilityProfile) ||
		errors.Is(err, postgres.ErrIndexerCategoryReviewRequired) ||
		errors.Is(err, postgres.ErrIndexerEndpointConfigurationChanged) ||
		errors.Is(err, postgres.ErrIndexerProviderContractMismatch)
}
