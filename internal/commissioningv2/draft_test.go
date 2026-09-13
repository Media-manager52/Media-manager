package commissioningv2

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

func draftTestCAPSEvidence() StepEvidenceDocument {
	observedAt := time.Unix(1_700_000_000, 0).UTC()
	bodyDigest := SumSHA256([]byte("synthetic CAPS response"))
	caps := newznab.CAPSRecord{
		BodySHA256:    bodyDigest.String(),
		ObservedAt:    observedAt,
		DefaultLimit:  20,
		MaximumLimit:  100,
		ServerTitle:   "Synthetic Newznab",
		ServerVersion: "1.2.3",
		Categories:    []newznab.Category{{ID: 2000, Name: "Movies"}, {ID: 5000, Name: "TV"}},
		ModeObservations: []newznab.CAPSModeObservation{
			{Mode: newznab.SearchModeGeneral, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery}},
			{Mode: newznab.SearchModeMovie, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q", "imdbid", "tmdbid", "extended"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery, newznab.WireIMDbID, newznab.WireTMDbID, newznab.WireExtended}},
			{Mode: newznab.SearchModeTV, ElementState: newznab.CAPSElementAbsent},
		},
	}
	return StepEvidenceDocument{
		SchemaVersion: DocumentSchemaVersion,
		Key:           "evidence." + strings.Repeat("a", 32),
		Kind:          newznab.EvidenceKindControlledProbe,
		Target:        newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		ObservedAt:    observedAt,
		SafeOutcome:   OutcomeCAPSObserved,
		BodySHA256:    bodyDigest,
		CAPS:          &caps,
	}
}

func draftTestFixtures() FixtureDocument {
	movieYear, seasonYear := uint16(2026), uint16(2022)
	return FixtureDocument{
		SchemaVersion: DocumentSchemaVersion,
		Movie: &MovieFixture{
			Key: "cfx_00000000000000000000000000000001", CanonicalTitle: "Draft Movie", Year: &movieYear,
			Identities: []VerifiedIdentity{{Namespace: IdentityIMDb, Value: "tt7654321"}, {Namespace: IdentityTMDb, Value: "321"}},
			Source:     FixtureExplicitOwner,
		},
		CompleteSeason: &CompleteSeasonFixture{
			Key: "cfx_00000000000000000000000000000002", CanonicalTitle: "Draft Series", Season: 3, Year: &seasonYear,
			Identities: []VerifiedIdentity{{Namespace: IdentityTVDb, Value: "654"}},
			Source:     FixtureExplicitOwner,
		},
	}
}

func draftTestSearchEvidence(step ProbeStep, outcome SafeOutcome, raw, plausible uint16, key string) StepEvidenceDocument {
	proof := SearchProof{
		SchemaVersion:          DocumentSchemaVersion,
		Scope:                  string(step.Scope),
		ProviderOutcome:        searchProviderNone,
		IdentityMatch:          searchIdentityObserved,
		TitleMatch:             searchTitleExact,
		YearMatch:              searchYearNotRequired,
		SeasonMatch:            searchSeasonNotRequired,
		CompletePackBasis:      searchPackNone,
		ValidItemCount:         raw,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
	}
	switch step.Scope {
	case newznab.SearchScopeMovie:
		proof.YearMatch = searchYearExact
	case newznab.SearchScopeCompleteSeason:
		proof.YearMatch = searchYearNotRequired
		proof.SeasonMatch = searchSeasonExact
		proof.SeasonRequested = 3
		proof.CompletePackBasis = searchPackProviderField
	}
	if outcome == OutcomeUnsupported {
		proof.ProviderOutcome = searchProviderUnsupported
		proof.IdentityMatch = searchIdentityAbsent
		proof.PlausibleIdentityCount = 0
		proof.ValidItemCount = 0
		proof.RawResultCount = 0
	}
	return StepEvidenceDocument{
		SchemaVersion:          DocumentSchemaVersion,
		Key:                    key,
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 step.Target,
		ObservedAt:             time.Unix(1_700_000_100, 0).UTC(),
		SafeOutcome:            outcome,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
		BodySHA256:             SumSHA256([]byte(key)),
		Search:                 &proof,
	}
}

func draftTestInput(t *testing.T) DraftInput {
	t.Helper()
	capsEvidence := draftTestCAPSEvidence()
	fixtures := draftTestFixtures()
	configuration := SumSHA256([]byte("draft configuration"))
	if _, err := BuildCAPSPlan(CAPSPlanInput{ConfigurationSHA256: configuration, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	searchPlan, err := BuildSearchPlan(SearchPlanInput{
		ConfigurationSHA256:  configuration,
		Fixtures:             fixtures,
		CAPSEvidence:         capsEvidence,
		Categories:           newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}},
		Options:              SearchProbeOptions{IncludeMovieFallback: true, IncludeCompleteSeasonFallback: true},
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := make([]DraftSearchStep, len(searchPlan.Steps))
	for index, step := range searchPlan.Steps {
		steps[index] = DraftSearchStep{Plan: step}
	}
	return DraftInput{
		IndexerInstanceID:       "11111111-1111-4111-8111-111111111111",
		RunID:                   "22222222-2222-4222-8222-222222222222",
		EndpointConfigSHA256:    configuration,
		ProfileSchemaVersion:    newznab.CapabilityProfileSchemaVersion,
		PlannerVersion:          PlannerVersion,
		CategorySelectionSHA256: *searchPlan.CategorySelectionSHA256,
		FixtureSHA256:           searchPlan.FixtureSHA256,
		CAPSPlanSHA256:          SumSHA256([]byte("caps plan")),
		SearchPlanSHA256:        SumSHA256([]byte("search plan")),
		CAPSEvidence:            capsEvidence,
		SearchSteps:             steps,
	}
}

func draftEvidenceKey(seed byte) string {
	return "evidence." + strings.Repeat(string("0123456789abcdef"[seed%16]), 32)
}

func TestBuildCapabilityProfileDraftDerivesOnlySupportedClaims(t *testing.T) {
	t.Parallel()
	input := draftTestInput(t)
	// Movie primary and fallback verify; the season primary returns no
	// results, so the season fallback has no verified primary and must stay
	// untested even though its own probe passed.
	for index := range input.SearchSteps {
		step := input.SearchSteps[index]
		var outcome SafeOutcome
		var raw, plausible uint16
		switch {
		case step.Plan.Scope == newznab.SearchScopeMovie:
			outcome, raw, plausible = OutcomeRecipeVerified, 1, 1
		case step.Plan.Role == newznab.RecipePrimary:
			outcome, raw, plausible = OutcomeNoResults, 0, 0
		default:
			outcome, raw, plausible = OutcomeRecipeVerified, 1, 1
		}
		evidence := draftTestSearchEvidence(step.Plan, outcome, raw, plausible, draftEvidenceKey(byte(index+1)))
		input.SearchSteps[index].Evidence = &evidence
	}
	profile, provenance, err := BuildCapabilityProfileDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	// CAPS claims are preserved exactly, including the absent TV mode.
	if profile.CAPS.ServerTitle != "Synthetic Newznab" || profile.CAPS.MaximumLimit != 100 || len(profile.CAPS.ModeObservations) != 3 {
		t.Fatalf("CAPS record not preserved: %+v", profile.CAPS)
	}
	var absentTV bool
	for _, observation := range profile.CAPS.ModeObservations {
		if observation.Mode == newznab.SearchModeTV && observation.ElementState == newznab.CAPSElementAbsent {
			absentTV = true
		}
	}
	if !absentTV {
		t.Fatal("absent CAPS mode was converted into a claim")
	}
	if len(profile.MappingRefs) != 1 || profile.MappingRefs[0].Key != newznab.MappingNewznabSearchV1 {
		t.Fatalf("mapping references=%+v", profile.MappingRefs)
	}
	if profile.Retrieval.NativeIdentity.Method != "" || profile.Retrieval.PreferredMethod != "" || profile.Retrieval.AdvertisedLocator != nil || profile.Retrieval.DirectDownload != nil {
		t.Fatalf("retrieval must stay empty/unknown: %+v", profile.Retrieval)
	}
	for _, recipe := range profile.SearchRecipes {
		switch {
		case recipe.Scope == newznab.SearchScopeMovie:
			if recipe.Verification.State != newznab.VerificationVerified || recipe.VerifiedLimit == 0 || len(recipe.Verification.EvidenceKeys) != 1 {
				t.Fatalf("verified movie recipe=%+v", recipe)
			}
		case recipe.Role == newznab.RecipePrimary:
			if recipe.Verification.State != newznab.VerificationUntested || len(recipe.Verification.EvidenceKeys) != 0 {
				t.Fatalf("no_results must stay untested: %+v", recipe)
			}
		default:
			if recipe.Verification.State != newznab.VerificationUntested || len(recipe.Verification.EvidenceKeys) != 0 {
				t.Fatalf("fallback without a verified primary must stay untested: %+v", recipe)
			}
		}
	}
	// The verified season fallback's durable evidence is retained in the
	// document but never referenced by a recipe claim.
	retained := false
	for _, evidence := range profile.Evidence {
		if evidence.Target.Kind == newznab.EvidenceTargetSearchRecipe && evidence.SafeOutcome == newznab.EvidenceOutcomeSearchSuccess {
			retained = true
		}
	}
	if !retained {
		t.Fatal("expected retained but unreferenced season fallback evidence")
	}
	document, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"language", "audio", "subtitle", "danish", "dansk", "http://", "https://", "authorization", "cookie"} {
		if bytes.Contains(bytes.ToLower(document), []byte(forbidden)) {
			t.Fatalf("draft document contains forbidden token %q: %s", forbidden, document)
		}
	}
	if provenance.IndexerInstanceID != input.IndexerInstanceID || provenance.RunID != input.RunID || provenance.Fingerprint.IsZero() || provenance.DocumentSHA256 != SumSHA256(document) {
		t.Fatalf("provenance=%+v", provenance)
	}
}

func TestBuildCapabilityProfileDraftRejectsUnknownPlannerVersion(t *testing.T) {
	t.Parallel()
	input := draftTestInput(t)
	input.PlannerVersion = PlannerVersion + 1
	if _, _, err := BuildCapabilityProfileDraft(input); err == nil {
		t.Fatal("accepted an unsupported planner version")
	}
}

func TestBuildCapabilityProfileDraftFailsClosedOnMismatchedEvidenceTarget(t *testing.T) {
	t.Parallel()
	input := draftTestInput(t)
	step := input.SearchSteps[0].Plan
	evidence := draftTestSearchEvidence(step, OutcomeRecipeVerified, 1, 1, draftEvidenceKey(2))
	evidence.Target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "different.recipe"}
	input.SearchSteps[0].Evidence = &evidence
	if _, _, err := BuildCapabilityProfileDraft(input); err == nil {
		t.Fatal("accepted evidence bound to a different recipe")
	}
}

func TestBuildCapabilityProfileDraftFailsClosedOnZeroIdentity(t *testing.T) {
	t.Parallel()
	input := draftTestInput(t)
	input.EndpointConfigSHA256 = SHA256Digest{}
	if _, _, err := BuildCapabilityProfileDraft(input); err == nil {
		t.Fatal("accepted a draft without a configuration binding")
	}
}

func TestBuildCapabilityProfileDraftKeepsNoPlausibleMatchUnknown(t *testing.T) {
	t.Parallel()
	input := draftTestInput(t)
	for index := range input.SearchSteps {
		step := input.SearchSteps[index].Plan
		evidence := draftTestSearchEvidence(step, OutcomeNoPlausibleMatch, 3, 0, draftEvidenceKey(byte(index+3)))
		input.SearchSteps[index].Evidence = &evidence
	}
	profile, _, err := BuildCapabilityProfileDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, recipe := range profile.SearchRecipes {
		if recipe.Verification.State != newznab.VerificationUntested || len(recipe.Verification.EvidenceKeys) != 0 {
			t.Fatalf("no_plausible_match must remain untested: %+v", recipe)
		}
	}
	for _, evidence := range profile.Evidence {
		if evidence.Target.Kind == newznab.EvidenceTargetSearchRecipe {
			t.Fatalf("no_plausible_match evidence must not be misrepresented in the profile: %+v", evidence)
		}
	}
}

func TestBuildCapabilityProfileDraftIsDeterministic(t *testing.T) {
	t.Parallel()
	input := draftTestInput(t)
	for index := range input.SearchSteps {
		step := input.SearchSteps[index].Plan
		evidence := draftTestSearchEvidence(step, OutcomeRecipeVerified, 1, 1, draftEvidenceKey(byte(index+5)))
		input.SearchSteps[index].Evidence = &evidence
	}
	firstProfile, firstProvenance, err := BuildCapabilityProfileDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	secondProfile, secondProvenance, err := BuildCapabilityProfileDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	firstDocument, _ := json.Marshal(firstProfile)
	secondDocument, _ := json.Marshal(secondProfile)
	if !bytes.Equal(firstDocument, secondDocument) {
		t.Fatal("draft derivation is not deterministic")
	}
	if firstProvenance.Fingerprint != secondProvenance.Fingerprint || firstProvenance.DocumentSHA256 != secondProvenance.DocumentSHA256 {
		t.Fatal("draft provenance is not deterministic")
	}
}
