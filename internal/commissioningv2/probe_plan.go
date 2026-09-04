package commissioningv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

const (
	maximumProbePlanBytes = 256 << 10
	maximumCategoryIDs    = 128
	defaultProbeLimit     = uint16(20)
)

var (
	recipeKeyPattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,99}$`)
	evidenceKeyPattern = regexp.MustCompile(`^evidence\.[a-f0-9]{32}$`)
)

type PlanPhase string

const (
	PlanPhaseCAPS   PlanPhase = "caps"
	PlanPhaseSearch PlanPhase = "search"
)

type ProbeStep struct {
	Ordinal          uint8                  `json:"ordinal"`
	Phase            StepPhase              `json:"phase"`
	Target           newznab.EvidenceTarget `json:"target"`
	Scope            newznab.SearchScope    `json:"scope,omitempty"`
	Role             newznab.RecipeRole     `json:"role,omitempty"`
	Priority         uint8                  `json:"priority,omitempty"`
	FixtureKey       string                 `json:"fixture_key,omitempty"`
	FixtureSHA256    *SHA256Digest          `json:"fixture_sha256,omitempty"`
	Request          TypedRequest           `json:"request"`
	CategoryIDs      []int                  `json:"category_ids,omitempty"`
	StructuralSHA256 SHA256Digest           `json:"structural_request_sha256"`
	Reason           ProbeReason            `json:"reason"`
	CAPSBasis        CAPSRecipeBasis        `json:"caps_basis"`
}

type ProbePlan struct {
	SchemaVersion           uint16                       `json:"schema_version"`
	PlannerVersion          uint16                       `json:"planner_version"`
	ProfileSchemaVersion    uint16                       `json:"profile_schema_version"`
	Phase                   PlanPhase                    `json:"phase"`
	ConfigurationSHA256     SHA256Digest                 `json:"configuration_sha256"`
	ProviderContract        *newznab.ProviderContractRef `json:"provider_contract,omitempty"`
	FixtureSHA256           SHA256Digest                 `json:"fixture_sha256"`
	CAPSEvidenceKey         string                       `json:"caps_evidence_key,omitempty"`
	CAPSRecordSHA256        *SHA256Digest                `json:"caps_record_sha256,omitempty"`
	CategorySelectionSHA256 *SHA256Digest                `json:"category_selection_sha256,omitempty"`
	SearchOptions           *SearchProbeOptions          `json:"search_options,omitempty"`
	MaximumAPICalls         uint8                        `json:"maximum_api_calls"`
	PlannedAPICalls         uint8                        `json:"planned_api_calls"`
	Steps                   []ProbeStep                  `json:"steps"`
}

type CAPSPlanInput struct {
	ConfigurationSHA256  SHA256Digest
	ProviderContract     *newznab.ProviderContractRef
	Fixtures             FixtureDocument
	ProfileSchemaVersion uint16
}

type SearchProbeOptions struct {
	IncludeMovieFallback            bool `json:"include_movie_fallback"`
	IncludeCompleteSeasonFallback   bool `json:"include_complete_season_fallback"`
	MovieWithoutCategories          bool `json:"movie_without_categories"`
	CompleteSeasonWithoutCategories bool `json:"complete_season_without_categories"`
}

type SearchPlanInput struct {
	ConfigurationSHA256  SHA256Digest
	ProviderContract     *newznab.ProviderContractRef
	Fixtures             FixtureDocument
	CAPSEvidence         StepEvidenceDocument
	Categories           newznab.NewznabCategorySelection
	Options              SearchProbeOptions
	ProfileSchemaVersion uint16
}

func BuildCAPSPlan(input CAPSPlanInput) (ProbePlan, error) {
	fixtures, fixtureDigest, err := normalizedFixturesAndDigest(input.Fixtures)
	if err != nil || input.ConfigurationSHA256.IsZero() || input.ProfileSchemaVersion != newznab.CapabilityProfileSchemaVersion || !validOptionalProviderContract(input.ProviderContract) {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	request := TypedRequest{
		Operation:   newznab.OperationCAPS,
		CategorySet: newznab.CategoryNone,
		Output:      OutputXML,
		Pagination:  PaginationNone,
	}
	step := ProbeStep{
		Ordinal:   1,
		Phase:     StepPhaseCAPS,
		Target:    newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		Request:   request,
		Reason:    ReasonObserveCAPS,
		CAPSBasis: CAPSBasisNotApplicable,
	}
	fingerprint, err := StructuralRequestFingerprint(input.ConfigurationSHA256, input.ProviderContract, input.ProfileSchemaVersion, PlannerVersion, step)
	if err != nil {
		return ProbePlan{}, err
	}
	step.StructuralSHA256 = fingerprint
	plan := ProbePlan{
		SchemaVersion:        DocumentSchemaVersion,
		PlannerVersion:       PlannerVersion,
		ProfileSchemaVersion: input.ProfileSchemaVersion,
		Phase:                PlanPhaseCAPS,
		ConfigurationSHA256:  input.ConfigurationSHA256,
		ProviderContract:     cloneProviderContract(input.ProviderContract),
		FixtureSHA256:        fixtureDigest,
		MaximumAPICalls:      MaximumAPICalls,
		PlannedAPICalls:      1,
		Steps:                []ProbeStep{step},
	}
	if err := ValidateProbePlan(plan); err != nil {
		return ProbePlan{}, err
	}
	if err := ValidateProbePlanForFixtures(plan, fixtures); err != nil {
		return ProbePlan{}, err
	}
	return plan, nil
}

func BuildSearchPlan(input SearchPlanInput) (ProbePlan, error) {
	fixtures, fixtureDigest, err := normalizedFixturesAndDigest(input.Fixtures)
	if err != nil || input.ConfigurationSHA256.IsZero() || input.ProfileSchemaVersion != newznab.CapabilityProfileSchemaVersion || !validOptionalProviderContract(input.ProviderContract) || input.Categories.ReviewRequired {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	if err := ValidateStepEvidence(input.CAPSEvidence); err != nil || input.CAPSEvidence.Target.Kind != newznab.EvidenceTargetCAPS || input.CAPSEvidence.CAPS == nil {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	caps := *input.CAPSEvidence.CAPS
	capsDigest, err := CAPSRecordDigest(caps)
	if err != nil {
		return ProbePlan{}, err
	}
	categories, categoryDigest, err := normalizeCategorySelection(input.Categories)
	if err != nil {
		return ProbePlan{}, err
	}
	availableCategories := make(map[int]struct{})
	collectCategoryIDs(caps.Categories, availableCategories)
	limit := defaultProbeLimit
	if caps.MaximumLimit > 0 && caps.MaximumLimit < int(limit) {
		limit = uint16(caps.MaximumLimit)
	}
	if limit == 0 {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}

	steps := make([]ProbeStep, 0, MaximumSearchCalls)
	effectiveOptions := SearchProbeOptions{}
	if fixtures.Movie != nil {
		effectiveOptions.MovieWithoutCategories = input.Options.MovieWithoutCategories
		categorySet, ids, categoryErr := selectedProbeCategories(newznab.SearchScopeMovie, categories, input.Options.MovieWithoutCategories, availableCategories)
		if categoryErr != nil {
			return ProbePlan{}, categoryErr
		}
		if primary, ok := selectMoviePrimary(*fixtures.Movie, caps, categorySet, ids, limit); ok {
			steps = append(steps, primary)
			if input.Options.IncludeMovieFallback {
				if fallback, fallbackOK := selectGeneralFallback(*fixtures.Movie, nil, caps, categorySet, ids, limit); fallbackOK {
					steps = append(steps, fallback)
					effectiveOptions.IncludeMovieFallback = true
				}
			}
		}
	}
	if fixtures.CompleteSeason != nil {
		effectiveOptions.CompleteSeasonWithoutCategories = input.Options.CompleteSeasonWithoutCategories
		categorySet, ids, categoryErr := selectedProbeCategories(newznab.SearchScopeCompleteSeason, categories, input.Options.CompleteSeasonWithoutCategories, availableCategories)
		if categoryErr != nil {
			return ProbePlan{}, categoryErr
		}
		if primary, ok := selectSeasonPrimary(*fixtures.CompleteSeason, caps, categorySet, ids, limit); ok {
			steps = append(steps, primary)
			if input.Options.IncludeCompleteSeasonFallback {
				if fallback, fallbackOK := selectGeneralFallback(MovieFixture{}, fixtures.CompleteSeason, caps, categorySet, ids, limit); fallbackOK {
					steps = append(steps, fallback)
					effectiveOptions.IncludeCompleteSeasonFallback = true
				}
			}
		}
	}
	if len(steps) == 0 || len(steps) > int(MaximumSearchCalls) {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	sort.Slice(steps, func(left, right int) bool {
		return lessProbeStep(steps[left], steps[right])
	})
	for index := range steps {
		steps[index].Ordinal = uint8(index + 2)
		fingerprint, fingerprintErr := StructuralRequestFingerprint(input.ConfigurationSHA256, input.ProviderContract, input.ProfileSchemaVersion, PlannerVersion, steps[index])
		if fingerprintErr != nil {
			return ProbePlan{}, fingerprintErr
		}
		steps[index].StructuralSHA256 = fingerprint
	}
	plan := ProbePlan{
		SchemaVersion:           DocumentSchemaVersion,
		PlannerVersion:          PlannerVersion,
		ProfileSchemaVersion:    input.ProfileSchemaVersion,
		Phase:                   PlanPhaseSearch,
		ConfigurationSHA256:     input.ConfigurationSHA256,
		ProviderContract:        cloneProviderContract(input.ProviderContract),
		FixtureSHA256:           fixtureDigest,
		CAPSEvidenceKey:         input.CAPSEvidence.Key,
		CAPSRecordSHA256:        digestPointer(capsDigest),
		CategorySelectionSHA256: &categoryDigest,
		SearchOptions:           &effectiveOptions,
		MaximumAPICalls:         MaximumAPICalls,
		PlannedAPICalls:         uint8(len(steps) + 1),
		Steps:                   steps,
	}
	if err := ValidateProbePlan(plan); err != nil {
		return ProbePlan{}, err
	}
	if err := ValidateProbePlanForFixtures(plan, fixtures); err != nil {
		return ProbePlan{}, err
	}
	if err := ValidateSearchPlanContext(plan, fixtures, input.CAPSEvidence, categories); err != nil {
		return ProbePlan{}, err
	}
	return plan, nil
}

// ValidateProbePlanForFixtures enforces the cross-document binding kept out of
// the structural plan: the run fixture document digest, and each search step's
// exact fixture key/digest, must match the immutable fixture snapshot.
func ValidateProbePlanForFixtures(plan ProbePlan, input FixtureDocument) error {
	if err := ValidateProbePlan(plan); err != nil {
		return err
	}
	fixtures, digest, err := normalizedFixturesAndDigest(input)
	if err != nil || digest != plan.FixtureSHA256 {
		return ErrInvalidCommissioningDocument
	}
	for _, step := range plan.Steps {
		if step.Phase != StepPhaseSearch {
			continue
		}
		var want SHA256Digest
		switch {
		case fixtures.Movie != nil && step.FixtureKey == fixtures.Movie.Key:
			want = mustFixtureDigest(*fixtures.Movie)
		case fixtures.CompleteSeason != nil && step.FixtureKey == fixtures.CompleteSeason.Key:
			want = mustFixtureDigest(*fixtures.CompleteSeason)
		default:
			return ErrInvalidCommissioningDocument
		}
		if step.FixtureSHA256 == nil || *step.FixtureSHA256 != want {
			return ErrInvalidCommissioningDocument
		}
		if step.Scope == newznab.SearchScopeMovie && (fixtures.Movie == nil || step.FixtureKey != fixtures.Movie.Key) || step.Scope == newznab.SearchScopeCompleteSeason && (fixtures.CompleteSeason == nil || step.FixtureKey != fixtures.CompleteSeason.Key) {
			return ErrInvalidCommissioningDocument
		}
	}
	return nil
}

// ValidateSearchPlanContext performs the required cross-document checks used
// when a search plan is sealed or loaded. Intrinsic JSON validation alone
// cannot prove its fixture, CAPS, and owner-category prerequisites.
func ValidateSearchPlanContext(plan ProbePlan, fixtures FixtureDocument, capsEvidence StepEvidenceDocument, categories newznab.NewznabCategorySelection) error {
	if plan.Phase != PlanPhaseSearch {
		return ErrInvalidCommissioningDocument
	}
	if err := ValidateProbePlanForFixtures(plan, fixtures); err != nil {
		return err
	}
	if err := ValidateSearchPlanCAPSEvidence(plan, capsEvidence); err != nil {
		return err
	}
	normalized, digest, err := NormalizeCategorySelection(categories)
	if err != nil || plan.CategorySelectionSHA256 == nil || digest != *plan.CategorySelectionSHA256 {
		return ErrInvalidCommissioningDocument
	}
	if plan.SearchOptions == nil {
		return ErrInvalidCommissioningDocument
	}
	options := *plan.SearchOptions
	if fixtures.Movie == nil && (options.IncludeMovieFallback || options.MovieWithoutCategories) || fixtures.CompleteSeason == nil && (options.IncludeCompleteSeasonFallback || options.CompleteSeasonWithoutCategories) {
		return ErrInvalidCommissioningDocument
	}
	available := make(map[int]struct{})
	collectCategoryIDs(capsEvidence.CAPS.Categories, available)
	usedCategorySets := make([][]int, 0, 2)
	if fixtures.Movie != nil && !options.MovieWithoutCategories {
		usedCategorySets = append(usedCategorySets, normalized.Movie)
	}
	if fixtures.CompleteSeason != nil && !options.CompleteSeasonWithoutCategories {
		usedCategorySets = append(usedCategorySets, normalized.Series)
	}
	for _, scopeCategories := range usedCategorySets {
		for _, id := range scopeCategories {
			if _, exists := available[id]; !exists {
				return ErrInvalidCommissioningDocument
			}
		}
	}
	stepsByScopeRole := make(map[string]ProbeStep, len(plan.Steps))
	for _, step := range plan.Steps {
		stepsByScopeRole[string(step.Scope)+"\x00"+string(step.Role)] = step
		expectedSet, selected, selectionErr := plannedCategorySelection(step.Scope, normalized, options)
		if selectionErr != nil || step.Request.CategorySet != expectedSet || !equalCategoryIDs(step.CategoryIDs, selected) {
			return ErrInvalidCommissioningDocument
		}
	}
	limit := defaultProbeLimit
	if capsEvidence.CAPS.MaximumLimit > 0 && capsEvidence.CAPS.MaximumLimit < int(limit) {
		limit = uint16(capsEvidence.CAPS.MaximumLimit)
	}
	for _, scope := range []newznab.SearchScope{newznab.SearchScopeMovie, newznab.SearchScopeCompleteSeason} {
		primary, hasPrimary := stepsByScopeRole[string(scope)+"\x00"+string(newznab.RecipePrimary)]
		fallback, hasFallback := stepsByScopeRole[string(scope)+"\x00"+string(newznab.RecipeFallback)]
		fixturePresent := scope == newznab.SearchScopeMovie && fixtures.Movie != nil || scope == newznab.SearchScopeCompleteSeason && fixtures.CompleteSeason != nil
		if !fixturePresent {
			if hasPrimary || hasFallback {
				return ErrInvalidCommissioningDocument
			}
			continue
		}
		categorySet, categoryIDs, categoryErr := plannedCategorySelection(scope, normalized, options)
		if categoryErr != nil {
			return ErrInvalidCommissioningDocument
		}
		wantFallback := options.IncludeMovieFallback
		if scope == newznab.SearchScopeCompleteSeason {
			wantFallback = options.IncludeCompleteSeasonFallback
		}
		if hasFallback != wantFallback {
			return ErrInvalidCommissioningDocument
		}
		if !hasPrimary {
			if hasFallback {
				return ErrInvalidCommissioningDocument
			}
			if scope == newznab.SearchScopeMovie && fixtures.Movie != nil {
				if _, available := selectMoviePrimary(*fixtures.Movie, *capsEvidence.CAPS, categorySet, categoryIDs, limit); available {
					return ErrInvalidCommissioningDocument
				}
			}
			if scope == newznab.SearchScopeCompleteSeason && fixtures.CompleteSeason != nil {
				if _, available := selectSeasonPrimary(*fixtures.CompleteSeason, *capsEvidence.CAPS, categorySet, categoryIDs, limit); available {
					return ErrInvalidCommissioningDocument
				}
			}
			continue
		}
		var expectedPrimary ProbeStep
		var primaryAvailable bool
		switch scope {
		case newznab.SearchScopeMovie:
			if fixtures.Movie == nil {
				return ErrInvalidCommissioningDocument
			}
			expectedPrimary, primaryAvailable = selectMoviePrimary(*fixtures.Movie, *capsEvidence.CAPS, categorySet, categoryIDs, limit)
		case newznab.SearchScopeCompleteSeason:
			if fixtures.CompleteSeason == nil {
				return ErrInvalidCommissioningDocument
			}
			expectedPrimary, primaryAvailable = selectSeasonPrimary(*fixtures.CompleteSeason, *capsEvidence.CAPS, categorySet, categoryIDs, limit)
		}
		if !primaryAvailable || !equivalentPlannedStep(primary, expectedPrimary) {
			return ErrInvalidCommissioningDocument
		}
		if hasFallback {
			var expectedFallback ProbeStep
			var fallbackAvailable bool
			if scope == newznab.SearchScopeMovie {
				expectedFallback, fallbackAvailable = selectGeneralFallback(*fixtures.Movie, nil, *capsEvidence.CAPS, categorySet, categoryIDs, limit)
			} else {
				expectedFallback, fallbackAvailable = selectGeneralFallback(MovieFixture{}, fixtures.CompleteSeason, *capsEvidence.CAPS, categorySet, categoryIDs, limit)
			}
			if !fallbackAvailable || !equivalentPlannedStep(fallback, expectedFallback) {
				return ErrInvalidCommissioningDocument
			}
		}
	}
	return nil
}

func equivalentPlannedStep(actual, expected ProbeStep) bool {
	return actual.Target == expected.Target && actual.Scope == expected.Scope && actual.Role == expected.Role && actual.Priority == expected.Priority && actual.FixtureKey == expected.FixtureKey && actual.FixtureSHA256 != nil && expected.FixtureSHA256 != nil && *actual.FixtureSHA256 == *expected.FixtureSHA256 && actual.Request.Operation == expected.Request.Operation && actual.Request.Mode == expected.Request.Mode && actual.Request.Query == expected.Request.Query && actual.Request.Extended == expected.Request.Extended && actual.Request.CategorySet == expected.Request.CategorySet && actual.Request.Output == expected.Request.Output && actual.Request.Pagination == expected.Request.Pagination && actual.Request.RequestLimit == expected.Request.RequestLimit && actual.Request.Role == expected.Request.Role && equalBindings(actual.Request.Bindings, expected.Request.Bindings) && equalFallbackTriggers(actual.Request.FallbackOn, expected.Request.FallbackOn) && equalCategoryIDs(actual.CategoryIDs, expected.CategoryIDs) && actual.Reason == expected.Reason && actual.CAPSBasis == expected.CAPSBasis
}

func ValidateProbePlan(plan ProbePlan) error {
	if plan.SchemaVersion != DocumentSchemaVersion || plan.PlannerVersion != PlannerVersion || plan.ProfileSchemaVersion != newznab.CapabilityProfileSchemaVersion || plan.ConfigurationSHA256.IsZero() || plan.FixtureSHA256.IsZero() || plan.MaximumAPICalls != MaximumAPICalls || !validOptionalProviderContract(plan.ProviderContract) {
		return ErrInvalidCommissioningDocument
	}
	switch plan.Phase {
	case PlanPhaseCAPS:
		if plan.CAPSEvidenceKey != "" || plan.CAPSRecordSHA256 != nil || plan.CategorySelectionSHA256 != nil || plan.SearchOptions != nil || plan.PlannedAPICalls != 1 || len(plan.Steps) != 1 {
			return ErrInvalidCommissioningDocument
		}
	case PlanPhaseSearch:
		if !evidenceKeyPattern.MatchString(plan.CAPSEvidenceKey) || plan.CAPSRecordSHA256 == nil || plan.CAPSRecordSHA256.IsZero() || plan.CategorySelectionSHA256 == nil || plan.CategorySelectionSHA256.IsZero() || plan.SearchOptions == nil || len(plan.Steps) == 0 || len(plan.Steps) > int(MaximumSearchCalls) || plan.PlannedAPICalls != uint8(len(plan.Steps)+1) || plan.PlannedAPICalls > plan.MaximumAPICalls {
			return ErrInvalidCommissioningDocument
		}
	default:
		return ErrInvalidCommissioningDocument
	}
	seenScopeRole := make(map[string]struct{}, 4)
	previous := ProbeStep{}
	for index, step := range plan.Steps {
		if err := ValidateProbeStep(step); err != nil {
			return err
		}
		wantOrdinal := uint8(1)
		if plan.Phase == PlanPhaseSearch {
			wantOrdinal = uint8(index + 2)
		}
		if step.Ordinal != wantOrdinal || step.Phase != StepPhase(plan.Phase) {
			return ErrInvalidCommissioningDocument
		}
		fingerprint, err := StructuralRequestFingerprint(plan.ConfigurationSHA256, plan.ProviderContract, plan.ProfileSchemaVersion, plan.PlannerVersion, step)
		if err != nil || fingerprint != step.StructuralSHA256 {
			return ErrInvalidCommissioningDocument
		}
		if plan.Phase == PlanPhaseSearch {
			key := string(step.Scope) + "\x00" + string(step.Role)
			if _, duplicate := seenScopeRole[key]; duplicate {
				return ErrInvalidCommissioningDocument
			}
			seenScopeRole[key] = struct{}{}
			if index > 0 && lessProbeStep(step, previous) {
				return ErrInvalidCommissioningDocument
			}
			previous = step
		}
	}
	if plan.Phase == PlanPhaseSearch {
		for key := range seenScopeRole {
			parts := strings.Split(key, "\x00")
			if len(parts) == 2 && parts[1] == string(newznab.RecipeFallback) {
				if _, primary := seenScopeRole[parts[0]+"\x00"+string(newznab.RecipePrimary)]; !primary {
					return ErrInvalidCommissioningDocument
				}
			}
		}
	}
	return nil
}

func DecodeProbePlan(raw []byte) (ProbePlan, error) {
	if len(raw) == 0 || len(raw) > maximumProbePlanBytes || !utf8.Valid(raw) {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan ProbePlan
	if err := decoder.Decode(&plan); err != nil {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ProbePlan{}, ErrInvalidCommissioningDocument
	}
	if err := ValidateProbePlan(plan); err != nil {
		return ProbePlan{}, err
	}
	return plan, nil
}

func CanonicalProbePlan(plan ProbePlan) ([]byte, error) {
	if err := ValidateProbePlan(plan); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, ErrInvalidCommissioningDocument
	}
	return encoded, nil
}

func ValidateProbeStep(step ProbeStep) error {
	if step.Ordinal == 0 || step.Ordinal > MaximumAPICalls || !ValidStepPhase(step.Phase) || step.StructuralSHA256.IsZero() || !validProbeReason(step.Reason) || !validCAPSBasis(step.CAPSBasis) || len(step.CategoryIDs) > maximumCategoryIDs {
		return ErrInvalidCommissioningDocument
	}
	if err := ValidateTypedRequest(step.Request); err != nil {
		return err
	}
	if step.Phase == StepPhaseCAPS {
		if step.Ordinal != 1 || step.Target != (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}) || step.Scope != "" || step.Role != "" || step.Priority != 0 || step.FixtureKey != "" || step.FixtureSHA256 != nil || len(step.CategoryIDs) != 0 || step.Reason != ReasonObserveCAPS || step.CAPSBasis != CAPSBasisNotApplicable || step.Request.Operation != newznab.OperationCAPS {
			return ErrInvalidCommissioningDocument
		}
		return nil
	}
	if step.Phase != StepPhaseSearch || step.Target.Kind != newznab.EvidenceTargetSearchRecipe || !recipeKeyPattern.MatchString(step.Target.RecipeKey) || step.Scope != newznab.SearchScopeMovie && step.Scope != newznab.SearchScopeCompleteSeason || step.Role != newznab.RecipePrimary && step.Role != newznab.RecipeFallback || !fixtureKeyPattern.MatchString(step.FixtureKey) || step.FixtureSHA256 == nil || step.FixtureSHA256.IsZero() || step.Request.Operation != newznab.OperationSearch || step.Request.Role != step.Role {
		return ErrInvalidCommissioningDocument
	}
	if step.Role == newznab.RecipePrimary && step.Reason != ReasonStrongestAvailableIdentity || step.Role == newznab.RecipeFallback && step.Reason != ReasonOwnerApprovedGeneralFallback || step.CAPSBasis == CAPSBasisNotApplicable || step.CAPSBasis == CAPSBasisOmittedGeneric && step.Request.Extended {
		return ErrInvalidCommissioningDocument
	}
	if !validScopeRequestCombination(step) || !knownRecipeStep(step) {
		return ErrInvalidCommissioningDocument
	}
	if step.Request.CategorySet == newznab.CategoryNone && len(step.CategoryIDs) != 0 || step.Request.CategorySet != newznab.CategoryNone && !validSortedCategoryIDs(step.CategoryIDs) {
		return ErrInvalidCommissioningDocument
	}
	return nil
}

func ValidateTypedRequest(request TypedRequest) error {
	if request.Output != OutputXML || request.Pagination != PaginationNone {
		return ErrInvalidCommissioningDocument
	}
	if request.Operation == newznab.OperationCAPS {
		if request.Mode != "" || request.Query.Style != "" || len(request.Bindings) != 0 || request.Extended || request.CategorySet != newznab.CategoryNone || request.RequestLimit != 0 || request.Role != "" || len(request.FallbackOn) != 0 {
			return ErrInvalidCommissioningDocument
		}
		return nil
	}
	if request.Operation != newznab.OperationSearch || !validSearchMode(request.Mode) || !validQueryStyle(request.Query.Style) || request.RequestLimit == 0 || request.RequestLimit > defaultProbeLimit || request.Role != newznab.RecipePrimary && request.Role != newznab.RecipeFallback || !validCategorySet(request.CategorySet) {
		return ErrInvalidCommissioningDocument
	}
	hasIdentity := false
	hasSeason := false
	parameters := make(map[newznab.WireParameter]struct{}, len(request.Bindings))
	for _, binding := range request.Bindings {
		if !validWireBinding(binding) {
			return ErrInvalidCommissioningDocument
		}
		if _, duplicate := parameters[binding.Parameter]; duplicate {
			return ErrInvalidCommissioningDocument
		}
		parameters[binding.Parameter] = struct{}{}
		switch binding.Parameter {
		case newznab.WireIMDbID, newznab.WireTMDbID, newznab.WireTVDbID, newznab.WireTVMazeID:
			hasIdentity = true
		case newznab.WireSeason:
			hasSeason = true
		}
	}
	if request.Query.Style == newznab.QueryNone && !hasIdentity {
		return ErrInvalidCommissioningDocument
	}
	if request.Role == newznab.RecipePrimary && len(request.FallbackOn) != 0 || request.Role == newznab.RecipeFallback && len(request.FallbackOn) == 0 {
		return ErrInvalidCommissioningDocument
	}
	seenTriggers := make(map[newznab.FallbackTrigger]struct{}, len(request.FallbackOn))
	for _, trigger := range request.FallbackOn {
		if !newznab.ValidFallbackTrigger(trigger) {
			return ErrInvalidCommissioningDocument
		}
		if _, duplicate := seenTriggers[trigger]; duplicate {
			return ErrInvalidCommissioningDocument
		}
		seenTriggers[trigger] = struct{}{}
	}
	if request.Mode == newznab.SearchModeTV && !hasSeason || request.Mode == newznab.SearchModeMovie && hasSeason {
		return ErrInvalidCommissioningDocument
	}
	return nil
}

func DecodeTypedRequest(raw []byte) (TypedRequest, error) {
	if len(raw) == 0 || len(raw) > 32<<10 || !utf8.Valid(raw) {
		return TypedRequest{}, ErrInvalidCommissioningDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request TypedRequest
	if err := decoder.Decode(&request); err != nil {
		return TypedRequest{}, ErrInvalidCommissioningDocument
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return TypedRequest{}, ErrInvalidCommissioningDocument
	}
	if err := ValidateTypedRequest(request); err != nil {
		return TypedRequest{}, err
	}
	return request, nil
}

func CanonicalTypedRequest(request TypedRequest) ([]byte, error) {
	if err := ValidateTypedRequest(request); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, ErrInvalidCommissioningDocument
	}
	return encoded, nil
}

type recipeCandidate struct {
	key       string
	scope     newznab.SearchScope
	mode      newznab.SearchMode
	query     newznab.QueryStyle
	bindings  []newznab.WireBinding
	role      newznab.RecipeRole
	priority  uint8
	fallbacks []newznab.FallbackTrigger
}

func selectMoviePrimary(fixture MovieFixture, caps newznab.CAPSRecord, categorySet newznab.CategorySetRef, ids []int, limit uint16) (ProbeStep, bool) {
	identities := fixtureIdentityMap(fixture.Identities)
	for _, candidate := range recipeCandidates(newznab.SearchScopeMovie, newznab.RecipePrimary) {
		if !candidateInputsAvailable(candidate, identities) {
			continue
		}
		basis, extended, reasonable := candidateCAPSBasis(caps, candidate.mode, candidate.query, candidate.bindings)
		if !reasonable {
			continue
		}
		return materializeCandidate(candidate, fixture.Key, mustFixtureDigest(fixture), categorySet, ids, limit, extended, basis), true
	}
	return ProbeStep{}, false
}

func selectSeasonPrimary(fixture CompleteSeasonFixture, caps newznab.CAPSRecord, categorySet newznab.CategorySetRef, ids []int, limit uint16) (ProbeStep, bool) {
	identities := fixtureIdentityMap(fixture.Identities)
	for _, candidate := range recipeCandidates(newznab.SearchScopeCompleteSeason, newznab.RecipePrimary) {
		if !candidateInputsAvailable(candidate, identities) {
			continue
		}
		basis, extended, reasonable := candidateCAPSBasis(caps, candidate.mode, candidate.query, candidate.bindings)
		if !reasonable {
			continue
		}
		return materializeCandidate(candidate, fixture.Key, mustFixtureDigest(fixture), categorySet, ids, limit, extended, basis), true
	}
	return ProbeStep{}, false
}

func selectGeneralFallback(movie MovieFixture, season *CompleteSeasonFixture, caps newznab.CAPSRecord, categorySet newznab.CategorySetRef, ids []int, limit uint16) (ProbeStep, bool) {
	candidate, _ := recipeCandidateByBaseKey("v2.movie.general-title-year")
	fixtureKey := movie.Key
	fixtureDigest := mustFixtureDigest(movie)
	if season != nil {
		candidate, _ = recipeCandidateByBaseKey("v2.season.general-title-token")
		fixtureKey = season.Key
		fixtureDigest = mustFixtureDigest(*season)
	} else if movie.Year == nil {
		return ProbeStep{}, false
	}
	basis, extended, reasonable := candidateCAPSBasis(caps, candidate.mode, candidate.query, nil)
	if !reasonable {
		return ProbeStep{}, false
	}
	return materializeCandidate(candidate, fixtureKey, fixtureDigest, categorySet, ids, limit, extended, basis), true
}

func materializeCandidate(candidate recipeCandidate, fixtureKey string, fixtureDigest SHA256Digest, categorySet newznab.CategorySetRef, ids []int, limit uint16, extended bool, basis CAPSRecipeBasis) ProbeStep {
	reason := ReasonStrongestAvailableIdentity
	if candidate.role == newznab.RecipeFallback {
		reason = ReasonOwnerApprovedGeneralFallback
	}
	key := candidate.key
	if extended {
		key += ".extended"
	} else {
		key += ".basic"
	}
	return ProbeStep{
		Phase:         StepPhaseSearch,
		Target:        newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: key},
		Scope:         candidate.scope,
		Role:          candidate.role,
		Priority:      candidate.priority,
		FixtureKey:    fixtureKey,
		FixtureSHA256: digestPointer(fixtureDigest),
		Request: TypedRequest{
			Operation:    newznab.OperationSearch,
			Mode:         candidate.mode,
			Query:        newznab.QuerySpec{Style: candidate.query},
			Bindings:     append([]newznab.WireBinding(nil), candidate.bindings...),
			Extended:     extended,
			CategorySet:  categorySet,
			Output:       OutputXML,
			Pagination:   PaginationNone,
			RequestLimit: limit,
			Role:         candidate.role,
			FallbackOn:   append([]newznab.FallbackTrigger(nil), candidate.fallbacks...),
		},
		CategoryIDs: append([]int(nil), ids...),
		Reason:      reason,
		CAPSBasis:   basis,
	}
}

func candidateInputsAvailable(candidate recipeCandidate, identities map[IdentityNamespace]string) bool {
	for _, binding := range candidate.bindings {
		var namespace IdentityNamespace
		switch binding.Source {
		case newznab.BindingIMDbNumeric:
			namespace = IdentityIMDb
		case newznab.BindingTMDbNumeric:
			namespace = IdentityTMDb
		case newznab.BindingTVDbNumeric:
			namespace = IdentityTVDb
		case newznab.BindingTVMazeNumeric:
			namespace = IdentityTVMaze
		case newznab.BindingSeasonNumber:
			continue
		default:
			return false
		}
		if identities[namespace] == "" {
			return false
		}
	}
	return true
}

func candidateCAPSBasis(caps newznab.CAPSRecord, mode newznab.SearchMode, query newznab.QueryStyle, bindings []newznab.WireBinding) (CAPSRecipeBasis, bool, bool) {
	observation, present := capsMode(caps, mode)
	if !present {
		return "", false, false
	}
	switch observation.ElementState {
	case newznab.CAPSElementAbsent:
		return CAPSBasisOmittedGeneric, false, true
	case newznab.CAPSElementPresentAvailable:
		recognized := make(map[newznab.WireParameter]struct{}, len(observation.RecognizedParameters))
		for _, parameter := range observation.RecognizedParameters {
			recognized[parameter] = struct{}{}
		}
		if query != newznab.QueryNone {
			if _, ok := recognized[newznab.WireQuery]; !ok {
				return "", false, false
			}
		}
		for _, binding := range bindings {
			if _, ok := recognized[binding.Parameter]; !ok {
				return "", false, false
			}
		}
		_, extended := recognized[newznab.WireExtended]
		return CAPSBasisPresentAvailable, extended, true
	default:
		return "", false, false
	}
}

func capsMode(caps newznab.CAPSRecord, mode newznab.SearchMode) (newznab.CAPSModeObservation, bool) {
	for _, observation := range caps.ModeObservations {
		if observation.Mode == mode {
			return observation, true
		}
	}
	return newznab.CAPSModeObservation{}, false
}

func selectedProbeCategories(scope newznab.SearchScope, selection newznab.NewznabCategorySelection, withoutCategories bool, available map[int]struct{}) (newznab.CategorySetRef, []int, error) {
	if withoutCategories {
		return newznab.CategoryNone, nil, nil
	}
	set := newznab.CategorySelectedMovie
	values := selection.Movie
	if scope == newznab.SearchScopeCompleteSeason {
		set = newznab.CategorySelectedSeries
		values = selection.Series
	}
	if len(values) == 0 || !validSortedCategoryIDs(values) {
		return "", nil, ErrInvalidCommissioningDocument
	}
	for _, value := range values {
		if _, exists := available[value]; !exists {
			return "", nil, ErrInvalidCommissioningDocument
		}
	}
	return set, append([]int(nil), values...), nil
}

func plannedCategorySelection(scope newznab.SearchScope, selection newznab.NewznabCategorySelection, options SearchProbeOptions) (newznab.CategorySetRef, []int, error) {
	switch scope {
	case newznab.SearchScopeMovie:
		if options.MovieWithoutCategories {
			return newznab.CategoryNone, nil, nil
		}
		if len(selection.Movie) == 0 {
			return "", nil, ErrInvalidCommissioningDocument
		}
		return newznab.CategorySelectedMovie, selection.Movie, nil
	case newznab.SearchScopeCompleteSeason:
		if options.CompleteSeasonWithoutCategories {
			return newznab.CategoryNone, nil, nil
		}
		if len(selection.Series) == 0 {
			return "", nil, ErrInvalidCommissioningDocument
		}
		return newznab.CategorySelectedSeries, selection.Series, nil
	default:
		return "", nil, ErrInvalidCommissioningDocument
	}
}

func normalizeCategorySelection(selection newznab.NewznabCategorySelection) (newznab.NewznabCategorySelection, SHA256Digest, error) {
	if selection.ReviewRequired {
		return newznab.NewznabCategorySelection{}, SHA256Digest{}, ErrInvalidCommissioningDocument
	}
	movie, err := normalizedCategoryIDs(selection.Movie)
	if err != nil {
		return newznab.NewznabCategorySelection{}, SHA256Digest{}, err
	}
	series, err := normalizedCategoryIDs(selection.Series)
	if err != nil {
		return newznab.NewznabCategorySelection{}, SHA256Digest{}, err
	}
	result := newznab.NewznabCategorySelection{Movie: movie, Series: series}
	encoded, encodeErr := json.Marshal(struct {
		Version uint16 `json:"version"`
		Movie   []int  `json:"movie"`
		Series  []int  `json:"series"`
	}{Version: DocumentSchemaVersion, Movie: movie, Series: series})
	if encodeErr != nil {
		return newznab.NewznabCategorySelection{}, SHA256Digest{}, ErrInvalidCommissioningDocument
	}
	return result, SumSHA256(encoded), nil
}

func normalizedCategoryIDs(values []int) ([]int, error) {
	if len(values) > maximumCategoryIDs {
		return nil, ErrInvalidCommissioningDocument
	}
	result := append([]int(nil), values...)
	sort.Ints(result)
	for index, value := range result {
		if value <= 0 || index > 0 && result[index-1] == value {
			return nil, ErrInvalidCommissioningDocument
		}
	}
	return result, nil
}

func validSortedCategoryIDs(values []int) bool {
	if len(values) == 0 || len(values) > maximumCategoryIDs {
		return false
	}
	previous := 0
	for _, value := range values {
		if value <= previous {
			return false
		}
		previous = value
	}
	return true
}

func equalCategoryIDs(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func collectCategoryIDs(categories []newznab.Category, result map[int]struct{}) {
	for _, category := range categories {
		result[category.ID] = struct{}{}
		collectCategoryIDs(category.Children, result)
	}
}

func validateCAPS(caps newznab.CAPSRecord) error {
	profile := newznab.CapabilityProfile{SchemaVersion: newznab.CapabilityProfileSchemaVersion, CAPS: caps}
	if err := newznab.ValidateCapabilityProfile(profile); err != nil || len(caps.ModeObservations) != 3 {
		return ErrInvalidCommissioningDocument
	}
	seen := make(map[newznab.SearchMode]struct{}, 3)
	for _, observation := range caps.ModeObservations {
		seen[observation.Mode] = struct{}{}
	}
	for _, mode := range []newznab.SearchMode{newznab.SearchModeGeneral, newznab.SearchModeMovie, newznab.SearchModeTV} {
		if _, exists := seen[mode]; !exists {
			return ErrInvalidCommissioningDocument
		}
	}
	return nil
}

func CAPSRecordDigest(caps newznab.CAPSRecord) (SHA256Digest, error) {
	if err := validateCAPS(caps); err != nil {
		return SHA256Digest{}, err
	}
	encoded, err := json.Marshal(caps)
	if err != nil {
		return SHA256Digest{}, ErrInvalidCommissioningDocument
	}
	return SumSHA256(encoded), nil
}

func validOptionalProviderContract(reference *newznab.ProviderContractRef) bool {
	return reference == nil || reference.Version == 1 && newznab.ValidProviderContractKey(reference.Key)
}

func cloneProviderContract(reference *newznab.ProviderContractRef) *newznab.ProviderContractRef {
	if reference == nil {
		return nil
	}
	clone := *reference
	return &clone
}

func digestPointer(value SHA256Digest) *SHA256Digest {
	clone := value
	return &clone
}

func normalizedFixturesAndDigest(input FixtureDocument) (FixtureDocument, SHA256Digest, error) {
	fixtures, err := NormalizeFixtureDocument(input)
	if err != nil {
		return FixtureDocument{}, SHA256Digest{}, err
	}
	digest, err := FixtureDocumentDigest(fixtures)
	return fixtures, digest, err
}

func fixtureIdentityMap(identities []VerifiedIdentity) map[IdentityNamespace]string {
	result := make(map[IdentityNamespace]string, len(identities))
	for _, identity := range identities {
		result[identity.Namespace] = identity.Value
	}
	return result
}

func mustFixtureDigest(value any) SHA256Digest {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("validated commissioning fixture could not be encoded")
	}
	return SumSHA256(encoded)
}

func lessProbeStep(left, right ProbeStep) bool {
	if left.Scope != right.Scope {
		return left.Scope == newznab.SearchScopeMovie
	}
	if left.Role != right.Role {
		return left.Role == newznab.RecipePrimary
	}
	if left.Priority != right.Priority {
		return left.Priority < right.Priority
	}
	return left.Target.RecipeKey < right.Target.RecipeKey
}

func validScopeRequestCombination(step ProbeStep) bool {
	request := step.Request
	hasSeason := false
	for _, binding := range request.Bindings {
		if binding.Parameter == newznab.WireSeason {
			hasSeason = true
		}
	}
	switch step.Scope {
	case newznab.SearchScopeMovie:
		return request.Mode != newznab.SearchModeTV && request.Query.Style != newznab.QueryTitleSeasonToken && !hasSeason && request.CategorySet != newznab.CategorySelectedSeries
	case newznab.SearchScopeCompleteSeason:
		return request.Mode != newznab.SearchModeMovie && request.Query.Style != newznab.QueryTitleYear && (hasSeason || request.Query.Style == newznab.QueryTitleSeasonToken) && request.CategorySet != newznab.CategorySelectedMovie
	default:
		return false
	}
}

func knownRecipeStep(step ProbeStep) bool {
	candidate, extended, exists := recipeCandidateForKey(step.Target.RecipeKey)
	if !exists {
		return false
	}
	return step.Scope == candidate.scope && step.Role == candidate.role && step.Priority == candidate.priority &&
		step.Request.Mode == candidate.mode && step.Request.Query.Style == candidate.query && step.Request.Extended == extended &&
		equalBindings(step.Request.Bindings, candidate.bindings) && equalFallbackTriggers(step.Request.FallbackOn, candidate.fallbacks)
}

func recipeCandidateForKey(key string) (recipeCandidate, bool, bool) {
	extended := false
	switch {
	case strings.HasSuffix(key, ".extended"):
		extended = true
		key = strings.TrimSuffix(key, ".extended")
	case strings.HasSuffix(key, ".basic"):
		key = strings.TrimSuffix(key, ".basic")
	default:
		return recipeCandidate{}, false, false
	}
	candidate, exists := recipeCandidateByBaseKey(key)
	return candidate, extended, exists
}

func recipeCandidates(scope newznab.SearchScope, role newznab.RecipeRole) []recipeCandidate {
	var keys []string
	switch {
	case scope == newznab.SearchScopeMovie && role == newznab.RecipePrimary:
		keys = []string{"v2.movie.q-imdb", "v2.movie.q-tmdb", "v2.movie.id-imdb", "v2.movie.id-tmdb"}
	case scope == newznab.SearchScopeCompleteSeason && role == newznab.RecipePrimary:
		keys = []string{"v2.season.q-tvdb-season", "v2.season.q-tmdb-season", "v2.season.q-tvmaze-season"}
	case scope == newznab.SearchScopeMovie && role == newznab.RecipeFallback:
		keys = []string{"v2.movie.general-title-year"}
	case scope == newznab.SearchScopeCompleteSeason && role == newznab.RecipeFallback:
		keys = []string{"v2.season.general-title-token"}
	default:
		return nil
	}
	result := make([]recipeCandidate, 0, len(keys))
	for _, key := range keys {
		candidate, exists := recipeCandidateByBaseKey(key)
		if !exists {
			panic("closed commissioning recipe catalog is inconsistent")
		}
		result = append(result, candidate)
	}
	return result
}

// recipeCandidateByBaseKey is the single closed generic search recipe catalog.
// Provider contracts do not yet expose an audited search catalog, so they bind
// a plan revision but cannot add a candidate in this offline milestone.
func recipeCandidateByBaseKey(key string) (recipeCandidate, bool) {
	switch key {
	case "v2.movie.q-imdb":
		return recipeCandidate{key: key, scope: newznab.SearchScopeMovie, mode: newznab.SearchModeMovie, query: newznab.QueryTitle, bindings: []newznab.WireBinding{{Parameter: newznab.WireIMDbID, Source: newznab.BindingIMDbNumeric}}, role: newznab.RecipePrimary, priority: 0}, true
	case "v2.movie.q-tmdb":
		return recipeCandidate{key: key, scope: newznab.SearchScopeMovie, mode: newznab.SearchModeMovie, query: newznab.QueryTitle, bindings: []newznab.WireBinding{{Parameter: newznab.WireTMDbID, Source: newznab.BindingTMDbNumeric}}, role: newznab.RecipePrimary, priority: 1}, true
	case "v2.movie.id-imdb":
		return recipeCandidate{key: key, scope: newznab.SearchScopeMovie, mode: newznab.SearchModeMovie, query: newznab.QueryNone, bindings: []newznab.WireBinding{{Parameter: newznab.WireIMDbID, Source: newznab.BindingIMDbNumeric}}, role: newznab.RecipePrimary, priority: 2}, true
	case "v2.movie.id-tmdb":
		return recipeCandidate{key: key, scope: newznab.SearchScopeMovie, mode: newznab.SearchModeMovie, query: newznab.QueryNone, bindings: []newznab.WireBinding{{Parameter: newznab.WireTMDbID, Source: newznab.BindingTMDbNumeric}}, role: newznab.RecipePrimary, priority: 3}, true
	case "v2.season.q-tvdb-season":
		return recipeCandidate{key: key, scope: newznab.SearchScopeCompleteSeason, mode: newznab.SearchModeTV, query: newznab.QueryTitle, bindings: []newznab.WireBinding{{Parameter: newznab.WireTVDbID, Source: newznab.BindingTVDbNumeric}, {Parameter: newznab.WireSeason, Source: newznab.BindingSeasonNumber}}, role: newznab.RecipePrimary, priority: 0}, true
	case "v2.season.q-tmdb-season":
		return recipeCandidate{key: key, scope: newznab.SearchScopeCompleteSeason, mode: newznab.SearchModeTV, query: newznab.QueryTitle, bindings: []newznab.WireBinding{{Parameter: newznab.WireTMDbID, Source: newznab.BindingTMDbNumeric}, {Parameter: newznab.WireSeason, Source: newznab.BindingSeasonNumber}}, role: newznab.RecipePrimary, priority: 1}, true
	case "v2.season.q-tvmaze-season":
		return recipeCandidate{key: key, scope: newznab.SearchScopeCompleteSeason, mode: newznab.SearchModeTV, query: newznab.QueryTitle, bindings: []newznab.WireBinding{{Parameter: newznab.WireTVMazeID, Source: newznab.BindingTVMazeNumeric}, {Parameter: newznab.WireSeason, Source: newznab.BindingSeasonNumber}}, role: newznab.RecipePrimary, priority: 2}, true
	case "v2.movie.general-title-year":
		return recipeCandidate{key: key, scope: newznab.SearchScopeMovie, mode: newznab.SearchModeGeneral, query: newznab.QueryTitleYear, role: newznab.RecipeFallback, priority: 0, fallbacks: approvedFallbackTriggers()}, true
	case "v2.season.general-title-token":
		return recipeCandidate{key: key, scope: newznab.SearchScopeCompleteSeason, mode: newznab.SearchModeGeneral, query: newznab.QueryTitleSeasonToken, role: newznab.RecipeFallback, priority: 0, fallbacks: approvedFallbackTriggers()}, true
	default:
		return recipeCandidate{}, false
	}
}

func approvedFallbackTriggers() []newznab.FallbackTrigger {
	return []newznab.FallbackTrigger{newznab.FallbackZeroRawResults, newznab.FallbackNoPlausibleIdentityScope, newznab.FallbackDeterministicContradiction}
}

func equalBindings(left, right []newznab.WireBinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalFallbackTriggers(left, right []newznab.FallbackTrigger) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validProbeReason(reason ProbeReason) bool {
	switch reason {
	case ReasonObserveCAPS, ReasonStrongestAvailableIdentity, ReasonOwnerApprovedGeneralFallback:
		return true
	default:
		return false
	}
}

func validCAPSBasis(basis CAPSRecipeBasis) bool {
	switch basis {
	case CAPSBasisNotApplicable, CAPSBasisPresentAvailable, CAPSBasisOmittedGeneric:
		return true
	default:
		return false
	}
}

func validSearchMode(mode newznab.SearchMode) bool {
	return mode == newznab.SearchModeGeneral || mode == newznab.SearchModeMovie || mode == newznab.SearchModeTV
}

func validQueryStyle(style newznab.QueryStyle) bool {
	switch style {
	case newznab.QueryNone, newznab.QueryTitle, newznab.QueryTitleYear, newznab.QueryTitleSeasonToken:
		return true
	default:
		return false
	}
}

func validCategorySet(set newznab.CategorySetRef) bool {
	switch set {
	case newznab.CategoryNone, newznab.CategorySelectedMovie, newznab.CategorySelectedSeries:
		return true
	default:
		return false
	}
}

func validWireBinding(binding newznab.WireBinding) bool {
	switch binding.Parameter {
	case newznab.WireIMDbID:
		return binding.Source == newznab.BindingIMDbNumeric
	case newznab.WireTMDbID:
		return binding.Source == newznab.BindingTMDbNumeric
	case newznab.WireTVDbID:
		return binding.Source == newznab.BindingTVDbNumeric
	case newznab.WireTVMazeID:
		return binding.Source == newznab.BindingTVMazeNumeric
	case newznab.WireYear:
		return binding.Source == newznab.BindingYearNumber
	case newznab.WireSeason:
		return binding.Source == newznab.BindingSeasonNumber
	default:
		return false
	}
}
