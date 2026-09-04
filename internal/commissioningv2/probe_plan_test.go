package commissioningv2

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

func testConfigurationDigest() SHA256Digest {
	return SumSHA256([]byte("non-secret structural configuration"))
}

func testCAPSRecord() newznab.CAPSRecord {
	body := SumSHA256([]byte("synthetic caps body"))
	return newznab.CAPSRecord{
		BodySHA256:    body.String(),
		ObservedAt:    time.Unix(1_700_000_000, 0).UTC(),
		ServerTitle:   "Synthetic Newznab",
		ServerVersion: "1.0",
		DefaultLimit:  20,
		MaximumLimit:  100,
		Categories: []newznab.Category{
			{ID: 2000, Name: "Movies", Children: []newznab.Category{{ID: 2040, Name: "Movies HD"}, {ID: 9999, Name: "Movies – Special"}}},
			{ID: 5000, Name: "TV", Children: []newznab.Category{{ID: 5040, Name: "TV HD"}}},
		},
		ModeObservations: []newznab.CAPSModeObservation{
			capsModeObservation(newznab.SearchModeGeneral, newznab.WireQuery, newznab.WireExtended),
			capsModeObservation(newznab.SearchModeMovie, newznab.WireQuery, newznab.WireIMDbID, newznab.WireTMDbID, newznab.WireExtended),
			capsModeObservation(newznab.SearchModeTV, newznab.WireQuery, newznab.WireTVDbID, newznab.WireTMDbID, newznab.WireTVMazeID, newznab.WireSeason, newznab.WireExtended),
		},
	}
}

func capsModeObservation(mode newznab.SearchMode, parameters ...newznab.WireParameter) newznab.CAPSModeObservation {
	names := make([]string, len(parameters))
	for index, parameter := range parameters {
		names[index] = string(parameter)
	}
	return newznab.CAPSModeObservation{Mode: mode, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: names, RecognizedParameters: parameters}
}

func testCAPSEvidence() StepEvidenceDocument {
	caps := testCAPSRecord()
	body, _ := ParseSHA256Digest(caps.BodySHA256)
	return StepEvidenceDocument{
		SchemaVersion: DocumentSchemaVersion,
		Key:           "evidence.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Kind:          newznab.EvidenceKindControlledProbe,
		Target:        newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		ObservedAt:    caps.ObservedAt,
		SafeOutcome:   OutcomeCAPSObserved,
		BodySHA256:    body,
		CAPS:          &caps,
	}
}

func testSearchPlanInput() SearchPlanInput {
	return SearchPlanInput{
		ConfigurationSHA256: testConfigurationDigest(),
		Fixtures:            validFixtureDocument(),
		CAPSEvidence:        testCAPSEvidence(),
		Categories: newznab.NewznabCategorySelection{
			Movie:  []int{2040, 2000},
			Series: []int{5040},
		},
		Options: SearchProbeOptions{
			IncludeMovieFallback:          true,
			IncludeCompleteSeasonFallback: true,
		},
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	}
}

func TestBuildTwoPhaseProbePlansAreBoundedAndStructural(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	input.Fixtures.Movie.CanonicalTitle = "needle-secret-title"
	input.Fixtures.Movie.Identities[0].Value = "tt7654321"
	capsPlan, err := BuildCAPSPlan(CAPSPlanInput{
		ConfigurationSHA256:  input.ConfigurationSHA256,
		Fixtures:             input.Fixtures,
		ProfileSchemaVersion: input.ProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capsPlan.PlannedAPICalls != 1 || len(capsPlan.Steps) != 1 || capsPlan.Steps[0].Ordinal != 1 || capsPlan.Steps[0].Target.Kind != newznab.EvidenceTargetCAPS {
		t.Fatalf("unexpected CAPS plan: %#v", capsPlan)
	}
	searchPlan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if searchPlan.MaximumAPICalls != 5 || searchPlan.PlannedAPICalls != 5 || len(searchPlan.Steps) != 4 {
		t.Fatalf("unexpected bounded counts: %#v", searchPlan)
	}
	wantKeys := []string{"v2.movie.q-imdb.extended", "v2.movie.general-title-year.extended", "v2.season.q-tvdb-season.extended", "v2.season.general-title-token.extended"}
	for index, step := range searchPlan.Steps {
		if step.Ordinal != uint8(index+2) || step.Target.RecipeKey != wantKeys[index] || step.Request.Output != OutputXML || step.Request.Pagination != PaginationNone || step.Request.RequestLimit != 20 {
			t.Fatalf("step %d=%#v", index, step)
		}
	}
	encoded, err := json.Marshal(searchPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"needle-secret-title", "tt7654321", "apikey", "authorization", `"value"`} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("structural plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSearchPlanUsesNextOfflineIdentityAndIDOnlyShape(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	input.Options = SearchProbeOptions{}
	input.Fixtures.Movie.Identities = []VerifiedIdentity{{Namespace: IdentityTMDb, Value: "12345"}}
	plan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].Target.RecipeKey != "v2.movie.q-tmdb.extended" {
		t.Fatalf("selected %q", plan.Steps[0].Target.RecipeKey)
	}

	input.Fixtures.CompleteSeason = nil
	caps := *input.CAPSEvidence.CAPS
	for index := range caps.ModeObservations {
		if caps.ModeObservations[index].Mode == newznab.SearchModeMovie {
			caps.ModeObservations[index] = capsModeObservation(newznab.SearchModeMovie, newznab.WireIMDbID)
		}
	}
	caps.BodySHA256 = SumSHA256([]byte("id-only caps")).String()
	evidence := input.CAPSEvidence
	evidence.CAPS = &caps
	evidence.BodySHA256, _ = ParseSHA256Digest(caps.BodySHA256)
	input.CAPSEvidence = evidence
	input.Fixtures.Movie.Identities = []VerifiedIdentity{{Namespace: IdentityIMDb, Value: "tt0111161"}}
	plan, err = BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Steps[0]; got.Target.RecipeKey != "v2.movie.id-imdb.basic" || got.Request.Query.Style != newznab.QueryNone {
		t.Fatalf("unexpected ID-only step %#v", got)
	}
}

func TestOmittedCAPSModeIsExplicitAndUninterpretableModeIsNotUsed(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	input.Fixtures.CompleteSeason = nil
	input.Options = SearchProbeOptions{}
	caps := *input.CAPSEvidence.CAPS
	for index := range caps.ModeObservations {
		if caps.ModeObservations[index].Mode == newznab.SearchModeMovie {
			caps.ModeObservations[index] = newznab.CAPSModeObservation{Mode: newznab.SearchModeMovie, ElementState: newznab.CAPSElementAbsent}
		}
	}
	caps.BodySHA256 = SumSHA256([]byte("omitted mode")).String()
	evidence := input.CAPSEvidence
	evidence.CAPS = &caps
	evidence.BodySHA256, _ = ParseSHA256Digest(caps.BodySHA256)
	input.CAPSEvidence = evidence
	plan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].CAPSBasis != CAPSBasisOmittedGeneric || plan.Steps[0].Request.Extended {
		t.Fatalf("omitted mode not safely marked: %#v", plan.Steps[0])
	}

	caps.ModeObservations[1] = newznab.CAPSModeObservation{Mode: newznab.SearchModeMovie, ElementState: newznab.CAPSElementPresentUninterpretable, Repeated: true, AdvertisedParameterNames: []string{"q", "imdbid"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery, newznab.WireIMDbID}}
	caps.BodySHA256 = SumSHA256([]byte("repeated mode")).String()
	evidence.CAPS = &caps
	evidence.BodySHA256, _ = ParseSHA256Digest(caps.BodySHA256)
	input.CAPSEvidence = evidence
	if _, err := BuildSearchPlan(input); err == nil {
		t.Fatal("repeated/uninterpretable mode justified a generic probe")
	}
}

func TestSearchPlanBindsCategoriesFixturesAndCAPSEvidence(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	plan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Steps[0].CategoryIDs; len(got) != 2 || got[0] != 2000 || got[1] != 2040 {
		t.Fatalf("categories not normalized: %v", got)
	}
	if err := ValidateProbePlanForFixtures(plan, input.Fixtures); err != nil {
		t.Fatal(err)
	}
	changedFixtures := input.Fixtures
	changedMovie := *changedFixtures.Movie
	changedMovie.CanonicalTitle = "Different"
	changedFixtures.Movie = &changedMovie
	if err := ValidateProbePlanForFixtures(plan, changedFixtures); err == nil {
		t.Fatal("plan accepted a different fixture snapshot")
	}
	if err := ValidateSearchPlanCAPSEvidence(plan, input.CAPSEvidence); err != nil {
		t.Fatal(err)
	}
	changedEvidence := input.CAPSEvidence
	changedCAPS := *changedEvidence.CAPS
	changedCAPS.MaximumLimit = 19
	changedEvidence.CAPS = &changedCAPS
	if err := ValidateSearchPlanCAPSEvidence(plan, changedEvidence); err == nil {
		t.Fatal("plan accepted a different CAPS observation")
	}

	badCategories := input
	badCategories.Categories.Movie = []int{7777}
	if _, err := BuildSearchPlan(badCategories); err == nil {
		t.Fatal("accepted category absent from nested CAPS tree")
	}
	input.Categories.Movie = []int{9999}
	if _, err := BuildSearchPlan(input); err != nil {
		t.Fatalf("rejected unusual category present in CAPS: %v", err)
	}
}

func TestPlanStrictDecodeRejectsTamperedClosedRecipeAndScope(t *testing.T) {
	t.Parallel()
	plan, err := BuildSearchPlan(testSearchPlanInput())
	if err != nil {
		t.Fatal(err)
	}
	valid, err := CanonicalProbePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeProbePlan(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ProbePlan){
		func(value *ProbePlan) { value.Steps[0].Target.RecipeKey = "v2.movie.arbitrary.basic" },
		func(value *ProbePlan) { value.Steps[0].Scope = newznab.SearchScopeCompleteSeason },
		func(value *ProbePlan) { value.Steps[0].Request.CategorySet = newznab.CategorySelectedSeries },
		func(value *ProbePlan) { value.Steps[0].Request.Query.Style = newznab.QueryTitleSeasonToken },
		func(value *ProbePlan) { value.Steps[0].Request.CategorySet = newznab.CategoryVerifiedFixed },
		func(value *ProbePlan) { value.Steps = value.Steps[1:2] },
	} {
		candidate := plan
		candidate.Steps = append([]ProbeStep(nil), plan.Steps...)
		mutate(&candidate)
		if len(candidate.Steps) > 0 {
			candidate.Steps[0].StructuralSHA256, _ = StructuralRequestFingerprint(candidate.ConfigurationSHA256, candidate.ProviderContract, candidate.ProfileSchemaVersion, candidate.PlannerVersion, candidate.Steps[0])
		}
		if err := ValidateProbePlan(candidate); err == nil {
			t.Fatal("accepted tampered but structurally plausible plan")
		}
	}
	unknown := append(valid[:len(valid)-1], []byte(`,"unknown":true}`)...)
	if _, err := DecodeProbePlan(unknown); err == nil {
		t.Fatal("accepted unknown plan field")
	}
	if _, err := DecodeProbePlan(append(valid, []byte(` {}`)...)); err == nil {
		t.Fatal("accepted trailing plan JSON")
	}
}

func TestSearchPlanContextRejectsWeakerOrIncompleteReFingerprintedPlan(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	plan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}

	weaker := plan
	weaker.Steps = append([]ProbeStep(nil), plan.Steps...)
	candidate, _ := recipeCandidateByBaseKey("v2.movie.q-tmdb")
	basis, extended, ok := candidateCAPSBasis(*input.CAPSEvidence.CAPS, candidate.mode, candidate.query, candidate.bindings)
	if !ok {
		t.Fatal("test candidate unexpectedly unavailable")
	}
	movieDigest := mustFixtureDigest(*input.Fixtures.Movie)
	weaker.Steps[0] = materializeCandidate(candidate, input.Fixtures.Movie.Key, movieDigest, newznab.CategorySelectedMovie, []int{2000, 2040}, 20, extended, basis)
	weaker.Steps[0].Ordinal = 2
	weaker.Steps[0].StructuralSHA256, _ = StructuralRequestFingerprint(weaker.ConfigurationSHA256, weaker.ProviderContract, weaker.ProfileSchemaVersion, weaker.PlannerVersion, weaker.Steps[0])
	if err := ValidateProbePlan(weaker); err != nil {
		t.Fatalf("weaker plan should be intrinsically closed for context test: %v", err)
	}
	if err := ValidateSearchPlanContext(weaker, input.Fixtures, input.CAPSEvidence, input.Categories); err == nil {
		t.Fatal("context accepted lower-priority primary while IMDb recipe was satisfiable")
	}

	incomplete := plan
	incomplete.Steps = append([]ProbeStep(nil), plan.Steps[:2]...)
	incomplete.PlannedAPICalls = 3
	if err := ValidateProbePlan(incomplete); err != nil {
		t.Fatalf("incomplete plan should be intrinsically closed for context test: %v", err)
	}
	if err := ValidateSearchPlanContext(incomplete, input.Fixtures, input.CAPSEvidence, input.Categories); err == nil {
		t.Fatal("context accepted omission of a plannable supplied season scope")
	}

	wrongLimit := plan
	wrongLimit.Steps = append([]ProbeStep(nil), plan.Steps...)
	wrongLimit.Steps[0].Request.RequestLimit = 19
	wrongLimit.Steps[0].StructuralSHA256, _ = StructuralRequestFingerprint(wrongLimit.ConfigurationSHA256, wrongLimit.ProviderContract, wrongLimit.ProfileSchemaVersion, wrongLimit.PlannerVersion, wrongLimit.Steps[0])
	if err := ValidateSearchPlanContext(wrongLimit, input.Fixtures, input.CAPSEvidence, input.Categories); err == nil {
		t.Fatal("context accepted a limit other than the deterministic tested limit")
	}
}

func TestSearchPlanContextBindsCategoryAndFallbackChoices(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	plan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	tamperedCategory := plan
	tamperedCategory.Steps = append([]ProbeStep(nil), plan.Steps...)
	for index := range tamperedCategory.Steps {
		if tamperedCategory.Steps[index].Scope != newznab.SearchScopeMovie {
			continue
		}
		tamperedCategory.Steps[index].Request.CategorySet = newznab.CategoryNone
		tamperedCategory.Steps[index].CategoryIDs = nil
		tamperedCategory.Steps[index].StructuralSHA256, _ = StructuralRequestFingerprint(tamperedCategory.ConfigurationSHA256, tamperedCategory.ProviderContract, tamperedCategory.ProfileSchemaVersion, tamperedCategory.PlannerVersion, tamperedCategory.Steps[index])
	}
	if err := ValidateProbePlan(tamperedCategory); err != nil {
		t.Fatalf("tampered plan should remain intrinsically closed for context test: %v", err)
	}
	if err := ValidateSearchPlanContext(tamperedCategory, input.Fixtures, input.CAPSEvidence, input.Categories); err == nil {
		t.Fatal("context accepted no-category requests without the plan's explicit choice")
	}

	tamperedFallback := plan
	options := *plan.SearchOptions
	options.IncludeMovieFallback = false
	tamperedFallback.SearchOptions = &options
	if err := ValidateSearchPlanContext(tamperedFallback, input.Fixtures, input.CAPSEvidence, input.Categories); err == nil {
		t.Fatal("context accepted fallback steps that the plan options did not authorize")
	}
}

func TestSearchPlanIgnoresUnusedScopeCategoryTree(t *testing.T) {
	t.Parallel()
	movieOnly := testSearchPlanInput()
	movieOnly.Fixtures.CompleteSeason = nil
	movieOnly.Categories.Series = []int{7777}
	movieOnly.Options.IncludeCompleteSeasonFallback = false
	if _, err := BuildSearchPlan(movieOnly); err != nil {
		t.Fatalf("movie-only plan depended on unused series categories: %v", err)
	}

	seasonOnly := testSearchPlanInput()
	seasonOnly.Fixtures.Movie = nil
	seasonOnly.Categories.Movie = []int{7777}
	seasonOnly.Options.IncludeMovieFallback = false
	if _, err := BuildSearchPlan(seasonOnly); err != nil {
		t.Fatalf("season-only plan depended on unused movie categories: %v", err)
	}
}

func TestStructuralFingerprintIsStableAndSensitiveOnlyToStructure(t *testing.T) {
	t.Parallel()
	firstInput := testSearchPlanInput()
	first, err := BuildSearchPlan(firstInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := testSearchPlanInput()
	secondInput.Fixtures.Movie.CanonicalTitle = "Another Title"
	secondInput.Fixtures.Movie.Identities[0].Value = "tt7654321"
	second, err := BuildSearchPlan(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Steps[0].StructuralSHA256 != second.Steps[0].StructuralSHA256 {
		t.Fatal("structural fingerprint included concrete fixture values")
	}
	if *first.Steps[0].FixtureSHA256 == *second.Steps[0].FixtureSHA256 {
		t.Fatal("fixture digest did not bind concrete fixture values")
	}
	secondInput.ConfigurationSHA256 = SumSHA256([]byte("different account/config"))
	third, err := BuildSearchPlan(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Steps[0].StructuralSHA256 == third.Steps[0].StructuralSHA256 {
		t.Fatal("structural fingerprint did not bind config/account digest")
	}
}

func TestUnresolvedCategoryReviewBlocksSearchPlanning(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	input.Categories.ReviewRequired = true
	if _, err := BuildSearchPlan(input); err == nil {
		t.Fatal("unresolved category review did not block plan")
	}
}
