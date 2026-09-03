package acquisition

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
)

func searchPlanEvidence(recipeKey string) newznab.CommissionEvidence {
	digest := sha256.Sum256([]byte(recipeKey))
	return newznab.CommissionEvidence{
		Key:                    "evidence." + hex.EncodeToString(digest[:16]),
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: recipeKey},
		ObservedAt:             time.Unix(1, 0).UTC(),
		BodySHA256:             strings.Repeat("a", 64),
		SafeOutcome:            newznab.EvidenceOutcomeSearchSuccess,
		RawResultCount:         1,
		PlausibleIdentityCount: 1,
	}
}

func searchPlanCAPS() newznab.CAPSRecord {
	return newznab.CAPSRecord{
		BodySHA256:   strings.Repeat("b", 64),
		ObservedAt:   time.Unix(1, 0).UTC(),
		MaximumLimit: 100,
		Categories: []newznab.Category{
			{ID: 2000, Name: "Movies", Children: []newznab.Category{{ID: 2040, Name: "Movies HD"}}},
			{ID: 5000, Name: "TV", Children: []newznab.Category{{ID: 5040, Name: "TV HD"}}},
		},
	}
}

func verifiedRecipe(key string, scope newznab.SearchScope, role newznab.RecipeRole, priority uint8) newznab.SearchRecipe {
	categorySet := newznab.CategorySelectedMovie
	query := newznab.QuerySpec{Style: newznab.QueryTitle}
	mode := newznab.SearchModeMovie
	bindings := []newznab.WireBinding{{Parameter: newznab.WireIMDbID, Source: newznab.BindingIMDbNumeric}}
	if scope == newznab.SearchScopeCompleteSeason {
		categorySet = newznab.CategorySelectedSeries
		mode = newznab.SearchModeTV
		bindings = []newznab.WireBinding{
			{Parameter: newznab.WireTVDbID, Source: newznab.BindingTVDbNumeric},
			{Parameter: newznab.WireSeason, Source: newznab.BindingSeasonNumber},
		}
	}
	recipe := newznab.SearchRecipe{
		Key:           key,
		Scope:         scope,
		Mode:          mode,
		Query:         query,
		Bindings:      bindings,
		Extended:      true,
		Categories:    newznab.CategorySpec{Set: categorySet},
		VerifiedLimit: 100,
		Role:          role,
		Priority:      priority,
		Verification: newznab.RecipeVerification{
			State:        newznab.VerificationVerified,
			EvidenceKeys: []string{"probe.success"},
		},
	}
	if role == newznab.RecipeFallback {
		recipe.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackZeroRawResults}
	}
	return recipe
}

func searchPlanProfile(recipes ...newznab.SearchRecipe) newznab.CapabilityProfile {
	profile := newznab.CapabilityProfile{
		SchemaVersion: newznab.CapabilityProfileSchemaVersion,
		CAPS:          searchPlanCAPS(),
		SearchRecipes: recipes,
	}
	for index := range profile.SearchRecipes {
		recipe := &profile.SearchRecipes[index]
		evidence := searchPlanEvidence(recipe.Key)
		recipe.Verification.EvidenceKeys = []string{evidence.Key}
		profile.Evidence = append(profile.Evidence, evidence)
	}
	return profile
}

func moviePlanInput(identities ...catalog.ExternalIdentity) SearchPlanInput {
	year := uint16(2026)
	return SearchPlanInput{Media: catalog.MediaItem{Kind: catalog.KindMovie, Title: "Example Movie", Year: &year, Identities: identities}}
}

func TestPlanSearchUsesHighestPrioritySatisfiablePrimary(t *testing.T) {
	t.Parallel()
	imdb := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	tmdb := verifiedRecipe("movie.tmdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 1)
	tmdb.Bindings = []newznab.WireBinding{{Parameter: newznab.WireTMDbID, Source: newznab.BindingTMDbNumeric}}
	general := verifiedRecipe("movie.general", newznab.SearchScopeMovie, newznab.RecipePrimary, 2)
	general.Mode = newznab.SearchModeGeneral
	general.Query.Style = newznab.QueryTitleYear
	general.Bindings = nil
	general.Categories.Set = newznab.CategoryNone

	profile := searchPlanProfile(general, tmdb, imdb)
	input := moviePlanInput(
		catalog.ExternalIdentity{Namespace: "imdb", Value: "tt7654321", State: catalog.IdentityUnverified},
		catalog.ExternalIdentity{Namespace: "TMDB", Value: "12345", State: catalog.IdentityVerified},
	)
	plan, err := PlanSearch(profile, input, newznab.NewznabCategorySelection{Movie: []int{2000}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Primary.RecipeKey != "movie.tmdb" {
		t.Fatalf("selected recipe=%q", plan.Primary.RecipeKey)
	}
	want := newznab.SearchRequest{
		Mode:       "movie",
		Params:     []newznab.Param{{Name: "q", Value: "Example Movie"}, {Name: "tmdbid", Value: "12345"}},
		Categories: []int{2000},
		Limit:      100,
		Extended:   true,
	}
	if !reflect.DeepEqual(plan.Primary.Request, want) {
		t.Fatalf("request=%#v, want %#v", plan.Primary.Request, want)
	}
}

func TestPlanSearchEmitsExactIMDbMovieRequest(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt0111161", State: catalog.IdentityVerified})
	plan, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000, 2040}})
	if err != nil {
		t.Fatal(err)
	}
	wantParams := []newznab.Param{{Name: "q", Value: "Example Movie"}, {Name: "imdbid", Value: "0111161"}}
	if !reflect.DeepEqual(plan.Primary.Request.Params, wantParams) || !reflect.DeepEqual(plan.Primary.Request.Categories, []int{2000, 2040}) || plan.Primary.Request.Offset != 0 || plan.Primary.Request.Limit != 100 || !plan.Primary.Request.Extended {
		t.Fatalf("unexpected exact request: %#v", plan.Primary.Request)
	}
}

func TestPlanSearchEmitsGeneralMovieTitleYearRequest(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.general", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	recipe.Mode = newznab.SearchModeGeneral
	recipe.Query.Style = newznab.QueryTitleYear
	recipe.Bindings = nil
	recipe.Categories.Set = newznab.CategoryNone
	plan, err := PlanSearch(searchPlanProfile(recipe), moviePlanInput(), newznab.NewznabCategorySelection{})
	if err != nil {
		t.Fatal(err)
	}
	want := newznab.SearchRequest{
		Mode:     "search",
		Params:   []newznab.Param{{Name: "q", Value: "Example Movie 2026"}},
		Limit:    100,
		Extended: true,
	}
	if !reflect.DeepEqual(plan.Primary.Request, want) {
		t.Fatalf("general movie request=%#v, want %#v", plan.Primary.Request, want)
	}
}

func TestPlanSearchEmitsExactCompleteSeasonRequest(t *testing.T) {
	t.Parallel()
	season := uint16(2)
	recipe := verifiedRecipe("season.tvdb", newznab.SearchScopeCompleteSeason, newznab.RecipePrimary, 0)
	input := SearchPlanInput{Season: &season, Media: catalog.MediaItem{
		Kind:  catalog.KindSeries,
		Title: "Example Series",
		Identities: []catalog.ExternalIdentity{{
			Namespace: "tvdb",
			Value:     "98765",
			State:     catalog.IdentityVerified,
		}},
	}}
	plan, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Series: []int{5000, 5040}})
	if err != nil {
		t.Fatal(err)
	}
	want := []newznab.Param{{Name: "q", Value: "Example Series"}, {Name: "tvdbid", Value: "98765"}, {Name: "season", Value: "2"}}
	if plan.Primary.Request.Mode != "tvsearch" || !reflect.DeepEqual(plan.Primary.Request.Params, want) || !reflect.DeepEqual(plan.Primary.Request.Categories, []int{5000, 5040}) {
		t.Fatalf("season request=%#v", plan.Primary.Request)
	}
}

func TestPlanSearchEmitsGeneralCompleteSeasonTokenRequest(t *testing.T) {
	t.Parallel()
	season := uint16(2)
	recipe := verifiedRecipe("season.general", newznab.SearchScopeCompleteSeason, newznab.RecipePrimary, 0)
	recipe.Mode = newznab.SearchModeGeneral
	recipe.Query.Style = newznab.QueryTitleSeasonToken
	recipe.Bindings = nil
	input := SearchPlanInput{Season: &season, Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "Example Series"}}
	plan, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Series: []int{5000}})
	if err != nil {
		t.Fatal(err)
	}
	want := newznab.SearchRequest{
		Mode:       "search",
		Params:     []newznab.Param{{Name: "q", Value: "Example Series S02"}},
		Categories: []int{5000},
		Limit:      100,
		Extended:   true,
	}
	if !reflect.DeepEqual(plan.Primary.Request, want) {
		t.Fatalf("general season request=%#v, want %#v", plan.Primary.Request, want)
	}
}

func TestPlanSearchQueryStyles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		style      newznab.QueryStyle
		scope      newznab.SearchScope
		wantQuery  string
		wantParams int
		season     uint16
	}{
		{name: "none", style: newznab.QueryNone, scope: newznab.SearchScopeMovie, wantParams: 1},
		{name: "title", style: newznab.QueryTitle, scope: newznab.SearchScopeMovie, wantQuery: "Example Movie", wantParams: 2},
		{name: "title year", style: newznab.QueryTitleYear, scope: newznab.SearchScopeMovie, wantQuery: "Example Movie 2026", wantParams: 2},
		{name: "title season zero", style: newznab.QueryTitleSeasonToken, scope: newznab.SearchScopeCompleteSeason, wantQuery: "Example Series S00", wantParams: 2, season: 0},
		{name: "title season ten", style: newznab.QueryTitleSeasonToken, scope: newznab.SearchScopeCompleteSeason, wantQuery: "Example Series S10", wantParams: 2, season: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recipe := verifiedRecipe("recipe.test", test.scope, newznab.RecipePrimary, 0)
			recipe.Query.Style = test.style
			categories := newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}}
			var input SearchPlanInput
			if test.scope == newznab.SearchScopeMovie {
				input = moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})
			} else {
				input = SearchPlanInput{Season: &test.season, Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "Example Series", Identities: []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "123", State: catalog.IdentityVerified}}}}
				if test.style == newznab.QueryTitleSeasonToken {
					recipe.Bindings = []newznab.WireBinding{{Parameter: newznab.WireTVDbID, Source: newznab.BindingTVDbNumeric}}
				}
			}
			plan, err := PlanSearch(searchPlanProfile(recipe), input, categories)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Primary.Request.Params) != test.wantParams {
				t.Fatalf("params=%#v", plan.Primary.Request.Params)
			}
			if test.style == newznab.QueryNone {
				if plan.Primary.Request.Params[0].Name == "q" {
					t.Fatalf("QueryNone emitted q: %#v", plan.Primary.Request.Params)
				}
			} else if plan.Primary.Request.Params[0] != (newznab.Param{Name: "q", Value: test.wantQuery}) {
				t.Fatalf("query=%#v", plan.Primary.Request.Params[0])
			}
		})
	}
}

func TestPlanSearchSelectsOneFallbackOnlyForApprovedTrigger(t *testing.T) {
	t.Parallel()
	primary := verifiedRecipe("movie.primary", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	first := verifiedRecipe("movie.fallback.imdb", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	first.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackZeroRawResults, newznab.FallbackNoPlausibleIdentityScope, newznab.FallbackDeterministicContradiction}
	second := verifiedRecipe("movie.fallback.general", newznab.SearchScopeMovie, newznab.RecipeFallback, 1)
	second.Query.Style = newznab.QueryTitleYear
	second.Bindings = nil
	second.Mode = newznab.SearchModeGeneral
	second.Categories.Set = newznab.CategoryNone
	second.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackDeterministicContradiction}
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})
	plan, err := PlanSearch(searchPlanProfile(second, first, primary), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
	if err != nil {
		t.Fatal(err)
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackZeroRawResults); !ok || fallback.RecipeKey != first.Key {
		t.Fatal("approved zero-result fallback was unavailable")
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackNoPlausibleIdentityScope); !ok || fallback.RecipeKey != first.Key {
		t.Fatal("approved identity/scope fallback was unavailable")
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackDeterministicContradiction); !ok || fallback.RecipeKey != first.Key {
		t.Fatal("approved deterministic-contradiction fallback was unavailable")
	}
	for _, trigger := range []newznab.FallbackTrigger{
		"timeout", "quota", "rate_limit", "authentication", "server_failure", "dns_failure", "tls_failure",
		"malformed_xml", "quality_rejection", "danish_language_rejection", "blocklist_rejection", "upgrade_policy_rejection",
	} {
		if _, ok := plan.FallbackFor(trigger); ok {
			t.Fatalf("unapproved trigger %q returned fallback", trigger)
		}
	}
}

func TestPlanSearchSkipsUnsatisfiedFallbackAlternative(t *testing.T) {
	t.Parallel()
	primary := verifiedRecipe("movie.primary", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	primary.Query.Style = newznab.QueryTitleYear
	primary.Bindings = nil
	primary.Categories.Set = newznab.CategoryNone
	missingTMDb := verifiedRecipe("movie.fallback.tmdb", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	missingTMDb.Bindings = []newznab.WireBinding{{Parameter: newznab.WireTMDbID, Source: newznab.BindingTMDbNumeric}}
	general := verifiedRecipe("movie.fallback.general", newznab.SearchScopeMovie, newznab.RecipeFallback, 1)
	general.Query.Style = newznab.QueryTitle
	general.Bindings = nil
	general.Categories.Set = newznab.CategoryNone
	plan, err := PlanSearch(searchPlanProfile(missingTMDb, general, primary), moviePlanInput(), newznab.NewznabCategorySelection{})
	if err != nil {
		t.Fatal(err)
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackZeroRawResults); !ok || fallback.RecipeKey != general.Key {
		t.Fatalf("fallback=%+v, available=%v", fallback, ok)
	}
}

func TestPlanSearchSelectsFallbackAfterTriggerFiltering(t *testing.T) {
	t.Parallel()
	primary := verifiedRecipe("movie.primary", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	zeroOnly := verifiedRecipe("movie.fallback.a-zero", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	zeroOnly.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackZeroRawResults}
	contradictionOnly := verifiedRecipe("movie.fallback.b-contradiction", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	contradictionOnly.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackDeterministicContradiction}
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})

	plan, err := PlanSearch(searchPlanProfile(contradictionOnly, primary, zeroOnly), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
	if err != nil {
		t.Fatal(err)
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackZeroRawResults); !ok || fallback.RecipeKey != zeroOnly.Key {
		t.Fatalf("zero-results fallback=%+v, available=%v", fallback, ok)
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackDeterministicContradiction); !ok || fallback.RecipeKey != contradictionOnly.Key {
		t.Fatalf("contradiction fallback=%+v, available=%v", fallback, ok)
	}
}

func TestPlanSearchBreaksEqualPriorityFallbackTiesAfterTriggerFiltering(t *testing.T) {
	t.Parallel()
	primary := verifiedRecipe("movie.primary", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	first := verifiedRecipe("movie.fallback.a", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	second := verifiedRecipe("movie.fallback.b", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	first.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackNoPlausibleIdentityScope}
	second.FallbackOn = []newznab.FallbackTrigger{newznab.FallbackNoPlausibleIdentityScope}
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})

	for _, recipes := range [][]newznab.SearchRecipe{{first, second, primary}, {second, primary, first}} {
		plan, err := PlanSearch(searchPlanProfile(recipes...), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
		if err != nil {
			t.Fatal(err)
		}
		fallback, ok := plan.FallbackFor(newznab.FallbackNoPlausibleIdentityScope)
		if !ok || fallback.RecipeKey != first.Key {
			t.Fatalf("equal-priority fallback=%+v, available=%v", fallback, ok)
		}
	}
}

func TestPlanSearchCategorySelectionIsBoundedAndDetached(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})
	selected := newznab.NewznabCategorySelection{Movie: []int{2000, 2040}}
	plan, err := PlanSearch(searchPlanProfile(recipe), input, selected)
	if err != nil {
		t.Fatal(err)
	}
	selected.Movie[0] = 5000
	if plan.Primary.Request.Categories[0] != 2000 {
		t.Fatal("planned categories alias owner configuration")
	}
	plan.Primary.Request.Categories[0] = 5040
	second, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000, 2040}})
	if err != nil || second.Primary.Request.Categories[0] != 2000 {
		t.Fatalf("planner mutated profile or future plan: %#v, %v", second.Primary.Request.Categories, err)
	}

	if _, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{}); !errors.Is(err, ErrNoSatisfiablePrimary) {
		t.Fatalf("empty selected category error=%v", err)
	}
	if _, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{9999}}); !errors.Is(err, ErrInvalidCategorySelection) {
		t.Fatalf("unknown category error=%v", err)
	}
	if _, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2040, 2000}}); !errors.Is(err, ErrInvalidCategorySelection) {
		t.Fatalf("noncanonical category error=%v", err)
	}
	if _, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000}, ReviewRequired: true}); !errors.Is(err, ErrInvalidCategorySelection) {
		t.Fatalf("unreviewed category selection error=%v", err)
	}
}

func TestPlanSearchCategoryInvalidationOnlyAffectsReferencingRecipes(t *testing.T) {
	t.Parallel()
	selectedPrimary := verifiedRecipe("movie.selected-primary", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	categorylessPrimary := verifiedRecipe("movie.categoryless-primary", newznab.SearchScopeMovie, newznab.RecipePrimary, 1)
	categorylessPrimary.Mode = newznab.SearchModeGeneral
	categorylessPrimary.Query.Style = newznab.QueryTitleYear
	categorylessPrimary.Bindings = nil
	categorylessPrimary.Categories.Set = newznab.CategoryNone

	selectedFallback := verifiedRecipe("movie.selected-fallback", newznab.SearchScopeMovie, newznab.RecipeFallback, 0)
	categorylessFallback := verifiedRecipe("movie.categoryless-fallback", newznab.SearchScopeMovie, newznab.RecipeFallback, 1)
	categorylessFallback.Mode = newznab.SearchModeGeneral
	categorylessFallback.Query.Style = newznab.QueryTitle
	categorylessFallback.Bindings = nil
	categorylessFallback.Categories.Set = newznab.CategoryNone

	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})
	plan, err := PlanSearch(
		searchPlanProfile(selectedPrimary, categorylessPrimary, selectedFallback, categorylessFallback),
		input,
		newznab.NewznabCategorySelection{Movie: []int{9999}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Primary.RecipeKey != categorylessPrimary.Key {
		t.Fatalf("primary=%q, want unaffected recipe %q", plan.Primary.RecipeKey, categorylessPrimary.Key)
	}
	if fallback, ok := plan.FallbackFor(newznab.FallbackZeroRawResults); !ok || fallback.RecipeKey != categorylessFallback.Key {
		t.Fatalf("fallback=%+v, available=%v, want unaffected recipe %q", fallback, ok, categorylessFallback.Key)
	}
}

func TestPlanSearchUsesVerifiedFixedCategories(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.fixed", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	recipe.Categories = newznab.CategorySpec{Set: newznab.CategoryVerifiedFixed, FixedIDs: []int{2000, 2040}}
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified})
	plan, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Primary.Request.Categories, []int{2000, 2040}) {
		t.Fatalf("fixed categories=%v", plan.Primary.Request.Categories)
	}
}

func TestPlanSearchRejectsInvalidIntentAndConflictingIdentity(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	season := uint16(1)
	if _, err := PlanSearch(searchPlanProfile(recipe), SearchPlanInput{Season: &season, Media: catalog.MediaItem{Kind: catalog.KindMovie, Title: "marker-title"}}, newznab.NewznabCategorySelection{Movie: []int{2000}}); !errors.Is(err, ErrInvalidSearchPlanInput) {
		t.Fatalf("movie season error=%v", err)
	}
	if _, err := PlanSearch(searchPlanProfile(recipe), SearchPlanInput{Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "marker-title"}}, newznab.NewznabCategorySelection{Movie: []int{2000}}); !errors.Is(err, ErrInvalidSearchPlanInput) {
		t.Fatalf("series without season error=%v", err)
	}
	input := moviePlanInput(
		catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified},
		catalog.ExternalIdentity{Namespace: "IMDB", Value: "tt7654321", State: catalog.IdentityVerified},
	)
	_, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
	if !errors.Is(err, ErrInvalidSearchPlanInput) || strings.Contains(err.Error(), "1234567") || strings.Contains(err.Error(), "7654321") {
		t.Fatalf("conflicting identity error=%v", err)
	}
}

func TestPlanSearchEnforcesMovieYearBounds(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.general", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	recipe.Mode = newznab.SearchModeGeneral
	recipe.Query.Style = newznab.QueryTitleYear
	recipe.Bindings = nil
	recipe.Categories.Set = newznab.CategoryNone
	profile := searchPlanProfile(recipe)
	tests := []struct {
		year    uint16
		wantErr bool
	}{
		{year: 0, wantErr: true},
		{year: 1799, wantErr: true},
		{year: 1800},
		{year: 9999},
		{year: 10000, wantErr: true},
		{year: ^uint16(0), wantErr: true},
	}
	for _, test := range tests {
		t.Run(strconv.Itoa(int(test.year)), func(t *testing.T) {
			input := moviePlanInput()
			input.Media.Year = &test.year
			plan, err := PlanSearch(profile, input, newznab.NewznabCategorySelection{})
			if test.wantErr {
				if !errors.Is(err, ErrInvalidSearchPlanInput) {
					t.Fatalf("year %d error=%v", test.year, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("year %d: %v", test.year, err)
			}
			wantQuery := "Example Movie " + strconv.Itoa(int(test.year))
			if len(plan.Primary.Request.Params) != 1 || plan.Primary.Request.Params[0] != (newznab.Param{Name: "q", Value: wantQuery}) {
				t.Fatalf("year %d request=%#v", test.year, plan.Primary.Request)
			}
		})
	}
}

func TestPlanSearchEnforcesCompleteSeasonYearBounds(t *testing.T) {
	t.Parallel()
	season := uint16(1)
	input := SearchPlanInput{Season: &season, Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "Example Series"}}
	categories := newznab.NewznabCategorySelection{Series: []int{5000}}

	withoutYear := verifiedRecipe("season.general", newznab.SearchScopeCompleteSeason, newznab.RecipePrimary, 0)
	withoutYear.Mode = newznab.SearchModeGeneral
	withoutYear.Query.Style = newznab.QueryTitleSeasonToken
	withoutYear.Bindings = nil
	withoutYearProfile := searchPlanProfile(withoutYear)

	t.Run("absent year succeeds when recipe does not require it", func(t *testing.T) {
		plan, err := PlanSearch(withoutYearProfile, input, categories)
		if err != nil {
			t.Fatal(err)
		}
		want := []newznab.Param{{Name: "q", Value: "Example Series S01"}}
		if !reflect.DeepEqual(plan.Primary.Request.Params, want) {
			t.Fatalf("params=%#v, want %#v", plan.Primary.Request.Params, want)
		}
	})

	withYear := withoutYear
	withYear.Key = "season.general.year"
	withYear.Bindings = []newznab.WireBinding{{Parameter: newznab.WireYear, Source: newznab.BindingYearNumber}}
	withYearProfile := searchPlanProfile(withYear)

	t.Run("absent year makes year-binding recipe unsatisfiable", func(t *testing.T) {
		_, err := PlanSearch(withYearProfile, input, categories)
		if !errors.Is(err, ErrNoSatisfiablePrimary) {
			t.Fatalf("error=%v, want %v", err, ErrNoSatisfiablePrimary)
		}
	})

	for _, year := range []uint16{1800, 9999} {
		t.Run("emits year "+strconv.Itoa(int(year)), func(t *testing.T) {
			inputWithYear := input
			inputWithYear.Media.Year = &year
			plan, err := PlanSearch(withYearProfile, inputWithYear, categories)
			if err != nil {
				t.Fatal(err)
			}
			want := []newznab.Param{
				{Name: "q", Value: "Example Series S01"},
				{Name: "year", Value: strconv.Itoa(int(year))},
			}
			if !reflect.DeepEqual(plan.Primary.Request.Params, want) {
				t.Fatalf("params=%#v, want %#v", plan.Primary.Request.Params, want)
			}
		})
	}

	for _, year := range []uint16{0, 1799, 10000, ^uint16(0)} {
		t.Run("invalid year before recipe selection "+strconv.Itoa(int(year)), func(t *testing.T) {
			inputWithYear := input
			inputWithYear.Media.Year = &year
			_, err := PlanSearch(withoutYearProfile, inputWithYear, categories)
			if !errors.Is(err, ErrInvalidSearchPlanInput) {
				t.Fatalf("complete-season year %d error=%v", year, err)
			}
		})
	}
}

func TestPlanSearchEnforcesCompleteSeasonBounds(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("season.general", newznab.SearchScopeCompleteSeason, newznab.RecipePrimary, 0)
	recipe.Mode = newznab.SearchModeGeneral
	recipe.Query.Style = newznab.QueryTitleSeasonToken
	recipe.Bindings = nil
	profile := searchPlanProfile(recipe)
	tests := []struct {
		season uint16
		valid  bool
	}{
		{season: 0, valid: true},
		{season: 999, valid: true},
		{season: 1000},
		{season: ^uint16(0)},
	}
	for _, test := range tests {
		t.Run(strconv.Itoa(int(test.season)), func(t *testing.T) {
			input := SearchPlanInput{Season: &test.season, Media: catalog.MediaItem{Kind: catalog.KindSeries, Title: "Example Series"}}
			plan, err := PlanSearch(profile, input, newznab.NewznabCategorySelection{Series: []int{5000}})
			if !test.valid {
				if !errors.Is(err, ErrInvalidSearchPlanInput) {
					t.Fatalf("season %d error=%v", test.season, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("season %d: %v", test.season, err)
			}
			wantQuery := fmt.Sprintf("Example Series S%02d", test.season)
			if len(plan.Primary.Request.Params) != 1 || plan.Primary.Request.Params[0] != (newznab.Param{Name: "q", Value: wantQuery}) {
				t.Fatalf("season %d request=%#v", test.season, plan.Primary.Request)
			}
		})
	}
}

func TestPlanSearchAcceptsFullIMDbRangeAndRejectsInvalidVerifiedIDs(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	for _, value := range []string{"tt0123456", "tt012345678901"} {
		input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: value, State: catalog.IdentityVerified})
		plan, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
		if err != nil {
			t.Fatalf("IMDb %q: %v", value, err)
		}
		if got := plan.Primary.Request.Params[1].Value; got != strings.TrimPrefix(value, "tt") {
			t.Fatalf("IMDb numeric=%q", got)
		}
	}
	for _, value := range []string{"tt123456", "tt1234567890123", "tt123x567"} {
		input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: value, State: catalog.IdentityVerified})
		_, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
		if !errors.Is(err, ErrInvalidSearchPlanInput) {
			t.Fatalf("invalid IMDb %q error=%v", value, err)
		}
	}
}

func TestPlanSearchDoesNotExposeTransportOrCredentialData(t *testing.T) {
	t.Parallel()
	marker := "marker-secret-value"
	recipe := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	input := moviePlanInput(catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified, Source: marker})
	plan, err := PlanSearch(searchPlanProfile(recipe), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"api_key", "apikey", "secret_ref", "base_url", "http://", "https://", marker} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("planned request contains forbidden transport data %q", forbidden)
		}
	}
}

func TestPlanSearchRejectsInvalidProfileBeforePlanning(t *testing.T) {
	t.Parallel()
	recipe := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	profile := searchPlanProfile(recipe)
	profile.SchemaVersion = 99
	_, err := PlanSearch(profile, moviePlanInput(), newznab.NewznabCategorySelection{Movie: []int{2000}})
	if !errors.Is(err, newznab.ErrInvalidCapabilityProfile) {
		t.Fatalf("invalid profile error=%v", err)
	}
}

func TestPlanSearchBreaksEqualPriorityTiesByRecipeKey(t *testing.T) {
	t.Parallel()
	imdb := verifiedRecipe("movie.imdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	tmdb := verifiedRecipe("movie.tmdb", newznab.SearchScopeMovie, newznab.RecipePrimary, 0)
	tmdb.Bindings = []newznab.WireBinding{{Parameter: newznab.WireTMDbID, Source: newznab.BindingTMDbNumeric}}
	input := moviePlanInput(
		catalog.ExternalIdentity{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified},
		catalog.ExternalIdentity{Namespace: "tmdb", Value: "12345", State: catalog.IdentityVerified},
	)
	for _, recipes := range [][]newznab.SearchRecipe{{imdb, tmdb}, {tmdb, imdb}} {
		plan, err := PlanSearch(searchPlanProfile(recipes...), input, newznab.NewznabCategorySelection{Movie: []int{2000}})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Primary.RecipeKey != "movie.imdb" {
			t.Fatalf("equal-priority recipe=%q, want lexical first key", plan.Primary.RecipeKey)
		}
	}
}
