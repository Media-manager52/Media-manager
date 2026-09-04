package commissioningv2

import (
	"encoding/json"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

// StructuralRequestFingerprint hashes a versioned canonical structural tuple.
// Concrete title/identifier values live only in the separately hashed fixture;
// endpoint, path, credential, header, and URL data have no field here.
func StructuralRequestFingerprint(configuration SHA256Digest, contract *newznab.ProviderContractRef, profileVersion, plannerVersion uint16, step ProbeStep) (SHA256Digest, error) {
	if configuration.IsZero() || profileVersion != newznab.CapabilityProfileSchemaVersion || plannerVersion != PlannerVersion || !validOptionalProviderContract(contract) || !ValidStepPhase(step.Phase) || !validTargetForPhase(step.Target, step.Phase) || !validSearchScopeForPhase(step.Scope, step.Phase) || !validRoleForPhase(step.Role, step.Phase) || !validSortedOrEmptyCategoryIDs(step.CategoryIDs) {
		return SHA256Digest{}, ErrInvalidCommissioningDocument
	}
	if err := ValidateTypedRequest(step.Request); err != nil {
		return SHA256Digest{}, err
	}
	canonical := struct {
		Version              uint16                       `json:"version"`
		PlannerVersion       uint16                       `json:"planner_version"`
		ProfileSchemaVersion uint16                       `json:"profile_schema_version"`
		ConfigurationSHA256  SHA256Digest                 `json:"configuration_sha256"`
		ProviderContract     *newznab.ProviderContractRef `json:"provider_contract,omitempty"`
		Target               newznab.EvidenceTarget       `json:"target"`
		Scope                newznab.SearchScope          `json:"scope,omitempty"`
		Role                 newznab.RecipeRole           `json:"role,omitempty"`
		Request              TypedRequest                 `json:"request"`
		CategoryIDs          []int                        `json:"category_ids,omitempty"`
	}{
		Version:              DocumentSchemaVersion,
		PlannerVersion:       plannerVersion,
		ProfileSchemaVersion: profileVersion,
		ConfigurationSHA256:  configuration,
		ProviderContract:     cloneProviderContract(contract),
		Target:               step.Target,
		Scope:                step.Scope,
		Role:                 step.Role,
		Request:              step.Request,
		CategoryIDs:          append([]int(nil), step.CategoryIDs...),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return SHA256Digest{}, ErrInvalidCommissioningDocument
	}
	return SumSHA256(encoded), nil
}

func ProbePlanDocumentDigest(plan ProbePlan) (SHA256Digest, error) {
	document, err := CanonicalProbePlan(plan)
	if err != nil {
		return SHA256Digest{}, err
	}
	return SumSHA256(document), nil
}

func CategorySelectionDigest(selection newznab.NewznabCategorySelection) (SHA256Digest, error) {
	_, digest, err := NormalizeCategorySelection(selection)
	return digest, err
}

// NormalizeCategorySelection returns sorted copies and rejects duplicates,
// non-positive IDs, unresolved review state, and the shared 128-ID bound.
func NormalizeCategorySelection(selection newznab.NewznabCategorySelection) (newznab.NewznabCategorySelection, SHA256Digest, error) {
	return normalizeCategorySelection(selection)
}

func validTargetForPhase(target newznab.EvidenceTarget, phase StepPhase) bool {
	if phase == StepPhaseCAPS {
		return target == (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS})
	}
	return phase == StepPhaseSearch && target.Kind == newznab.EvidenceTargetSearchRecipe && recipeKeyPattern.MatchString(target.RecipeKey)
}

func validSearchScopeForPhase(scope newznab.SearchScope, phase StepPhase) bool {
	return phase == StepPhaseCAPS && scope == "" || phase == StepPhaseSearch && (scope == newznab.SearchScopeMovie || scope == newznab.SearchScopeCompleteSeason)
}

func validRoleForPhase(role newznab.RecipeRole, phase StepPhase) bool {
	return phase == StepPhaseCAPS && role == "" || phase == StepPhaseSearch && (role == newznab.RecipePrimary || role == newznab.RecipeFallback)
}

func validSortedOrEmptyCategoryIDs(values []int) bool {
	return len(values) == 0 || validSortedCategoryIDs(values)
}
