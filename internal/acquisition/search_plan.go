package acquisition

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
)

const maxPlannedCategoryIDs = 128

var (
	ErrInvalidSearchPlanInput   = errors.New("invalid Newznab search plan input")
	ErrInvalidCategorySelection = errors.New("invalid Newznab category selection")
	ErrNoSatisfiablePrimary     = errors.New("no verified Newznab primary recipe is satisfiable")
)

type SearchPlanInput struct {
	Media  catalog.MediaItem
	Season *uint16
}

type PlannedSearch struct {
	RecipeKey string                `json:"recipe_key"`
	Role      newznab.RecipeRole    `json:"role"`
	Request   newznab.SearchRequest `json:"request"`
}

type SearchPlan struct {
	Primary    PlannedSearch             `json:"primary"`
	Fallback   *PlannedSearch            `json:"fallback,omitempty"`
	FallbackOn []newznab.FallbackTrigger `json:"fallback_on,omitempty"`
}

// FallbackFor returns the single planned fallback only for a trigger approved
// by that exact recipe. Operational failures and policy decisions have no
// representation in this deliberately narrow API.
func (plan SearchPlan) FallbackFor(trigger newznab.FallbackTrigger) (PlannedSearch, bool) {
	if plan.Fallback == nil || !newznab.ValidFallbackTrigger(trigger) {
		return PlannedSearch{}, false
	}
	for _, allowed := range plan.FallbackOn {
		if trigger == allowed {
			return clonePlannedSearch(*plan.Fallback), true
		}
	}
	return PlannedSearch{}, false
}

// PlanSearch is pure and is intentionally not connected to the production
// search worker in profile schema v1. It emits an in-memory request containing
// no endpoint, credential, secret reference, output choice, or locator.
func PlanSearch(profile newznab.CapabilityProfile, input SearchPlanInput, categories newznab.NewznabCategorySelection) (SearchPlan, error) {
	if err := newznab.ValidateCapabilityProfile(profile); err != nil {
		return SearchPlan{}, err
	}
	if categories.ReviewRequired {
		return SearchPlan{}, ErrInvalidCategorySelection
	}
	scope, err := planScope(input)
	if err != nil {
		return SearchPlan{}, err
	}
	identities, err := verifiedPlanIdentities(input.Media.Identities)
	if err != nil {
		return SearchPlan{}, err
	}
	availableCategories := make(map[int]struct{})
	collectPlanCategoryIDs(profile.CAPS.Categories, availableCategories)

	primaryRecipes := recipesFor(profile.SearchRecipes, scope, newznab.RecipePrimary)
	var primary PlannedSearch
	foundPrimary := false
	invalidPrimaryCategories := false
	for _, recipe := range primaryRecipes {
		planned, satisfiable, planErr := planRecipe(recipe, input, identities, categories, availableCategories)
		if planErr != nil {
			if errors.Is(planErr, ErrInvalidCategorySelection) {
				invalidPrimaryCategories = true
				continue
			}
			return SearchPlan{}, planErr
		}
		if satisfiable {
			primary, foundPrimary = planned, true
			break
		}
	}
	if !foundPrimary {
		if invalidPrimaryCategories {
			return SearchPlan{}, ErrInvalidCategorySelection
		}
		return SearchPlan{}, ErrNoSatisfiablePrimary
	}

	result := SearchPlan{Primary: primary}
	for _, recipe := range recipesFor(profile.SearchRecipes, scope, newznab.RecipeFallback) {
		planned, satisfiable, planErr := planRecipe(recipe, input, identities, categories, availableCategories)
		if planErr != nil {
			if errors.Is(planErr, ErrInvalidCategorySelection) {
				continue
			}
			return SearchPlan{}, planErr
		}
		if !satisfiable {
			continue
		}
		result.Fallback = &planned
		result.FallbackOn = append([]newznab.FallbackTrigger(nil), recipe.FallbackOn...)
		break
	}
	return result, nil
}

func planScope(input SearchPlanInput) (newznab.SearchScope, error) {
	switch input.Media.Kind {
	case catalog.KindMovie:
		if input.Season != nil {
			return "", ErrInvalidSearchPlanInput
		}
		return newznab.SearchScopeMovie, nil
	case catalog.KindSeries:
		if input.Season == nil {
			return "", ErrInvalidSearchPlanInput
		}
		return newznab.SearchScopeCompleteSeason, nil
	default:
		return "", ErrInvalidSearchPlanInput
	}
}

func recipesFor(recipes []newznab.SearchRecipe, scope newznab.SearchScope, role newznab.RecipeRole) []newznab.SearchRecipe {
	result := make([]newznab.SearchRecipe, 0, len(recipes))
	for _, recipe := range recipes {
		if recipe.Scope == scope && recipe.Role == role && recipe.Verification.State == newznab.VerificationVerified {
			result = append(result, recipe)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Priority != result[right].Priority {
			return result[left].Priority < result[right].Priority
		}
		return result[left].Key < result[right].Key
	})
	return result
}

func planRecipe(recipe newznab.SearchRecipe, input SearchPlanInput, identities map[newznab.BindingSource]string, configured newznab.NewznabCategorySelection, availableCategories map[int]struct{}) (PlannedSearch, bool, error) {
	query, queryAvailable := plannedQuery(recipe.Query.Style, input)
	if !queryAvailable {
		return PlannedSearch{}, false, nil
	}
	params := make([]newznab.Param, 0, len(recipe.Bindings)+1)
	if recipe.Query.Style != newznab.QueryNone {
		params = append(params, newznab.Param{Name: string(newznab.WireQuery), Value: query})
	}
	for _, binding := range recipe.Bindings {
		value, available := bindingValue(binding.Source, input, identities)
		if !available {
			return PlannedSearch{}, false, nil
		}
		params = append(params, newznab.Param{Name: string(binding.Parameter), Value: value})
	}
	categoryIDs, categoriesAvailable, err := planCategories(recipe.Categories, configured, availableCategories)
	if err != nil {
		return PlannedSearch{}, false, err
	}
	if !categoriesAvailable {
		return PlannedSearch{}, false, nil
	}
	return PlannedSearch{
		RecipeKey: recipe.Key,
		Role:      recipe.Role,
		Request: newznab.SearchRequest{
			Mode:       string(recipe.Mode),
			Params:     params,
			Categories: categoryIDs,
			Limit:      int(recipe.VerifiedLimit),
			Offset:     0,
			Extended:   recipe.Extended,
		},
	}, true, nil
}

func plannedQuery(style newznab.QueryStyle, input SearchPlanInput) (string, bool) {
	title := strings.TrimSpace(input.Media.Title)
	switch style {
	case newznab.QueryNone:
		return "", true
	case newznab.QueryTitle:
		return title, title != ""
	case newznab.QueryTitleYear:
		if title == "" || input.Media.Year == nil {
			return "", false
		}
		return fmt.Sprintf("%s %d", title, *input.Media.Year), true
	case newznab.QueryTitleSeasonToken:
		if title == "" || input.Season == nil {
			return "", false
		}
		return fmt.Sprintf("%s S%02d", title, *input.Season), true
	default:
		return "", false
	}
}

func bindingValue(source newznab.BindingSource, input SearchPlanInput, identities map[newznab.BindingSource]string) (string, bool) {
	switch source {
	case newznab.BindingIMDbNumeric, newznab.BindingTMDbNumeric, newznab.BindingTVDbNumeric, newznab.BindingTVMazeNumeric:
		value, exists := identities[source]
		return value, exists
	case newznab.BindingYearNumber:
		if input.Media.Year == nil {
			return "", false
		}
		return strconv.Itoa(int(*input.Media.Year)), true
	case newznab.BindingSeasonNumber:
		if input.Season == nil {
			return "", false
		}
		return strconv.Itoa(int(*input.Season)), true
	default:
		return "", false
	}
}

func verifiedPlanIdentities(values []catalog.ExternalIdentity) (map[newznab.BindingSource]string, error) {
	result := make(map[newznab.BindingSource]string, 4)
	for _, identity := range values {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		var source newznab.BindingSource
		var normalized string
		var valid bool
		switch strings.ToLower(strings.TrimSpace(identity.Namespace)) {
		case "imdb":
			source = newznab.BindingIMDbNumeric
			normalized, valid = planIMDbNumeric(identity.Value)
		case "tmdb":
			source = newznab.BindingTMDbNumeric
			normalized, valid = planPositiveNumeric(identity.Value)
		case "tvdb":
			source = newznab.BindingTVDbNumeric
			normalized, valid = planPositiveNumeric(identity.Value)
		case "tvmaze":
			source = newznab.BindingTVMazeNumeric
			normalized, valid = planPositiveNumeric(identity.Value)
		default:
			continue
		}
		if !valid {
			return nil, ErrInvalidSearchPlanInput
		}
		if previous, exists := result[source]; exists && previous != normalized {
			return nil, ErrInvalidSearchPlanInput
		}
		result[source] = normalized
	}
	return result, nil
}

func planIMDbNumeric(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(value, "tt") {
		return "", false
	}
	value = strings.TrimPrefix(value, "tt")
	return value, decimalDigits(value, 7, 12, false)
}

func planPositiveNumeric(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, decimalDigits(value, 1, 20, true)
}

func decimalDigits(value string, minimum, maximum int, positiveCanonical bool) bool {
	if len(value) < minimum || len(value) > maximum || positiveCanonical && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return !positiveCanonical || value != "0"
}

func planCategories(specification newznab.CategorySpec, configured newznab.NewznabCategorySelection, available map[int]struct{}) ([]int, bool, error) {
	var values []int
	switch specification.Set {
	case newznab.CategoryNone:
		return nil, true, nil
	case newznab.CategorySelectedMovie:
		values = configured.Movie
	case newznab.CategorySelectedSeries:
		values = configured.Series
	case newznab.CategoryVerifiedFixed:
		values = specification.FixedIDs
	default:
		return nil, false, ErrInvalidCategorySelection
	}
	if len(values) == 0 {
		return nil, false, nil
	}
	if len(values) > maxPlannedCategoryIDs {
		return nil, false, ErrInvalidCategorySelection
	}
	result := make([]int, len(values))
	copy(result, values)
	previous := 0
	for index, value := range result {
		if value <= 0 || index > 0 && value <= previous {
			return nil, false, ErrInvalidCategorySelection
		}
		if _, exists := available[value]; !exists {
			return nil, false, ErrInvalidCategorySelection
		}
		previous = value
	}
	return result, true, nil
}

func collectPlanCategoryIDs(categories []newznab.Category, result map[int]struct{}) {
	for _, category := range categories {
		result[category.ID] = struct{}{}
		collectPlanCategoryIDs(category.Children, result)
	}
}

func clonePlannedSearch(value PlannedSearch) PlannedSearch {
	value.Request.Params = append([]newznab.Param(nil), value.Request.Params...)
	value.Request.Categories = append([]int(nil), value.Request.Categories...)
	return value
}
