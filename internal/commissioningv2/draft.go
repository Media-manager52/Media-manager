package commissioningv2

import (
	"encoding/json"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

// This file derives an immutable capability-profile DRAFT from durable,
// accepted commissioning evidence only. It performs no network work: every
// value it reads is a durable planning or evidence document, and the returned
// profile is a pure value. It never activates a profile, mutates an
// integration, or produces an operation-status row.
//
// Derivation rules (offline architecture section 16 and live-executor
// architecture sections 21/30.4/31):
//
//   - CAPS claims are preserved exactly as observed, including omitted and
//     uninterpretable modes; CAPS never verifies a search recipe.
//   - A recipe is verified, unsupported, permission-restricted, or
//     contradicted only when its own exact-target evidence proves that state.
//   - no_results and no_plausible_match remain untested: unknown never becomes
//     supported or unsupported. Their durable step evidence is untouched.
//   - VerifiedLimit is exactly the transmitted request limit, never an
//     advertised-but-untested maximum.
//   - A verified fallback is unusable/untested unless the same scope has a
//     verified primary; it is never silently promoted to primary.
//   - Only closed, registered mapping references and provider-contract
//     versions enter the document.
//   - Provider bodies, URLs, headers, error text, credentials, and
//     secret-derived fingerprints have no field in this model.
//   - Language/audio/subtitle policy is untouched: commissioning search
//     evidence proves recipe shape and identity, never track coverage.

// DraftProvenance is the non-secret identity of the exact durable
// commissioning state that produced a draft. It exists so an owner or reviewer
// can prove which indexer, run, configuration, plan, and evidence keys were
// used. It carries no provider text and no credential-derived value.
type DraftProvenance struct {
	IndexerInstanceID       string
	RunID                   string
	EndpointConfigSHA256    SHA256Digest
	ProviderContract        *newznab.ProviderContractRef
	ProfileSchemaVersion    uint16
	PlannerVersion          uint16
	CategorySelectionSHA256 SHA256Digest
	FixtureSHA256           SHA256Digest
	CAPSPlanSHA256          SHA256Digest
	SearchPlanSHA256        SHA256Digest
	CAPSEvidenceKey         string
	EvidenceKeys            []string
	DocumentSHA256          SHA256Digest
	Fingerprint             SHA256Digest
}

// DraftSearchStep pairs one sealed search-plan step with the exact durable
// evidence produced for it. Evidence is nil when a step carries no durable
// capability evidence (for example a failed or indeterminate probe); such a
// recipe is left untested.
type DraftSearchStep struct {
	Plan     ProbeStep
	Evidence *StepEvidenceDocument
}

// DraftInput is the complete durable state required to derive one draft. All
// fields come from the commissioned run and its immutable steps; the builder
// accepts no caller-supplied capability claim.
type DraftInput struct {
	IndexerInstanceID       string
	RunID                   string
	EndpointConfigSHA256    SHA256Digest
	ProviderContract        *newznab.ProviderContractRef
	ProfileSchemaVersion    uint16
	PlannerVersion          uint16
	CategorySelectionSHA256 SHA256Digest
	FixtureSHA256           SHA256Digest
	CAPSPlanSHA256          SHA256Digest
	SearchPlanSHA256        SHA256Digest
	CAPSEvidence            StepEvidenceDocument
	SearchSteps             []DraftSearchStep
}

// BuildCapabilityProfileDraft is the only supported path from durable
// commissioning evidence to a capability-profile document. It is deterministic:
// the same durable input always produces the same document and provenance. It
// fails closed on any contradictory, malformed, mismatched, or non-qualifying
// evidence rather than weakening a claim.
func BuildCapabilityProfileDraft(input DraftInput) (newznab.CapabilityProfile, DraftProvenance, error) {
	if err := validateDraftInput(input); err != nil {
		return newznab.CapabilityProfile{}, DraftProvenance{}, err
	}
	caps := *input.CAPSEvidence.CAPS
	profile := newznab.CapabilityProfile{
		SchemaVersion:    input.ProfileSchemaVersion,
		ProviderContract: cloneProviderContract(input.ProviderContract),
		CAPS:             caps,
		Operations:       []newznab.OperationCapability{},
		MappingRefs:      []newznab.MappingRef{{Key: newznab.MappingNewznabSearchV1, Version: 1}},
	}
	capsEvidence, err := capsCommissionEvidence(input.CAPSEvidence)
	if err != nil {
		return newznab.CapabilityProfile{}, DraftProvenance{}, err
	}
	profile.Evidence = append(profile.Evidence, capsEvidence)

	verifiedPrimaryScopes := make(map[newznab.SearchScope]bool, 2)
	for _, step := range input.SearchSteps {
		if step.Plan.Role == newznab.RecipePrimary && step.Evidence != nil && step.Evidence.Target == step.Plan.Target && step.Evidence.SafeOutcome == OutcomeRecipeVerified {
			verifiedPrimaryScopes[step.Plan.Scope] = true
		}
	}

	evidenceOrder := make([]string, 0, len(input.SearchSteps)+1)
	evidenceOrder = append(evidenceOrder, capsEvidence.Key)
	evidenceSeen := map[string]bool{capsEvidence.Key: true}

	for _, step := range input.SearchSteps {
		recipe := recipeForDraftStep(step.Plan)
		state := newznab.VerificationUntested
		var attachedKey string
		if step.Evidence != nil {
			switch step.Evidence.SafeOutcome {
			case OutcomeRecipeVerified:
				if step.Plan.Role == newznab.RecipeFallback && !verifiedPrimaryScopes[step.Plan.Scope] {
					// Rule 5: retain the durable evidence but leave the
					// fallback unusable/untested in the draft. It is never
					// promoted to primary.
					state = newznab.VerificationUntested
				} else {
					state = newznab.VerificationVerified
				}
			case OutcomeUnsupported:
				state = newznab.VerificationUnsupported
			case OutcomePermissionRestricted:
				state = newznab.VerificationPermissionRestricted
			case OutcomeContradicted:
				state = newznab.VerificationContradicted
			default:
				// no_results, no_plausible_match, and every non-verifying
				// outcome stay untested.
				state = newznab.VerificationUntested
			}
			if evidence, ok := searchCommissionEvidence(*step.Evidence); ok {
				if !evidenceSeen[evidence.Key] {
					evidenceSeen[evidence.Key] = true
					evidenceOrder = append(evidenceOrder, evidence.Key)
					profile.Evidence = append(profile.Evidence, evidence)
				}
				if state != newznab.VerificationUntested {
					attachedKey = evidence.Key
				}
			} else if state != newznab.VerificationUntested {
				// A non-untested state without a representable closed
				// evidence record must fail closed rather than assert a
				// claim the profile cannot bind to evidence.
				return newznab.CapabilityProfile{}, DraftProvenance{}, ErrInvalidCommissioningDocument
			}
		}
		recipe.Verification = newznab.RecipeVerification{State: state}
		if attachedKey != "" {
			recipe.Verification.EvidenceKeys = []string{attachedKey}
		}
		profile.SearchRecipes = append(profile.SearchRecipes, recipe)
	}

	if err := newznab.ValidateCapabilityProfile(profile); err != nil {
		return newznab.CapabilityProfile{}, DraftProvenance{}, ErrInvalidCommissioningDocument
	}
	document, err := json.Marshal(profile)
	if err != nil {
		return newznab.CapabilityProfile{}, DraftProvenance{}, ErrInvalidCommissioningDocument
	}
	provenance, err := buildDraftProvenance(input, evidenceOrder, document)
	if err != nil {
		return newznab.CapabilityProfile{}, DraftProvenance{}, err
	}
	return profile, provenance, nil
}

func validateDraftInput(input DraftInput) error {
	if input.IndexerInstanceID == "" || input.RunID == "" || input.EndpointConfigSHA256.IsZero() || input.FixtureSHA256.IsZero() || input.CAPSPlanSHA256.IsZero() || input.SearchPlanSHA256.IsZero() {
		return ErrInvalidCommissioningDocument
	}
	if input.ProfileSchemaVersion != newznab.CapabilityProfileSchemaVersion || input.PlannerVersion != PlannerVersion || !validOptionalProviderContract(input.ProviderContract) {
		return ErrInvalidCommissioningDocument
	}
	if err := ValidateStepEvidence(input.CAPSEvidence); err != nil {
		return ErrInvalidCommissioningDocument
	}
	if input.CAPSEvidence.Target != (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}) || input.CAPSEvidence.SafeOutcome != OutcomeCAPSObserved || input.CAPSEvidence.CAPS == nil {
		return ErrInvalidCommissioningDocument
	}
	if len(input.SearchSteps) == 0 || len(input.SearchSteps) > int(MaximumSearchCalls) {
		return ErrInvalidCommissioningDocument
	}
	evidenceKeys := map[string]bool{input.CAPSEvidence.Key: true}
	for _, step := range input.SearchSteps {
		if step.Plan.Phase != StepPhaseSearch || step.Plan.Target.Kind != newznab.EvidenceTargetSearchRecipe || !recipeKeyPattern.MatchString(step.Plan.Target.RecipeKey) || !validSearchScopeForPhase(step.Plan.Scope, step.Plan.Phase) || !validRoleForPhase(step.Plan.Role, step.Plan.Phase) || ValidateTypedRequest(step.Plan.Request) != nil || step.Plan.Request.Operation != newznab.OperationSearch || step.Plan.Request.Role != step.Plan.Role {
			return ErrInvalidCommissioningDocument
		}
		if step.Evidence == nil {
			continue
		}
		if err := validateSearchEvidenceShape(*step.Evidence); err != nil {
			return ErrInvalidCommissioningDocument
		}
		if step.Evidence.Target != step.Plan.Target {
			return ErrInvalidCommissioningDocument
		}
		if evidenceKeys[step.Evidence.Key] {
			return ErrInvalidCommissioningDocument
		}
		evidenceKeys[step.Evidence.Key] = true
	}
	return nil
}

func capsCommissionEvidence(evidence StepEvidenceDocument) (newznab.CommissionEvidence, error) {
	if err := ValidateStepEvidence(evidence); err != nil {
		return newznab.CommissionEvidence{}, ErrInvalidCommissioningDocument
	}
	if evidence.Target != (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}) || evidence.SafeOutcome != OutcomeCAPSObserved || evidence.CAPS == nil {
		return newznab.CommissionEvidence{}, ErrInvalidCommissioningDocument
	}
	return newznab.CommissionEvidence{
		Key:         evidence.Key,
		Kind:        newznab.EvidenceKindControlledProbe,
		Target:      newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		ObservedAt:  evidence.ObservedAt,
		BodySHA256:  evidence.BodySHA256.String(),
		SafeOutcome: newznab.EvidenceOutcomeSemanticallyVerified,
	}, nil
}

// searchCommissionEvidence maps a closed search-recipe evidence document to
// the profile's smaller closed evidence vocabulary. It reports false for
// outcomes the profile cannot represent (no_plausible_match), so the recipe
// stays untested and no claim is made.
func searchCommissionEvidence(evidence StepEvidenceDocument) (newznab.CommissionEvidence, bool) {
	var outcome newznab.EvidenceOutcome
	switch evidence.SafeOutcome {
	case OutcomeRecipeVerified:
		outcome = newznab.EvidenceOutcomeSearchSuccess
	case OutcomeNoResults:
		outcome = newznab.EvidenceOutcomeNoResults
	case OutcomeUnsupported:
		outcome = newznab.EvidenceOutcomeUnsupported
	case OutcomePermissionRestricted:
		outcome = newznab.EvidenceOutcomePermissionRestricted
	case OutcomeContradicted:
		outcome = newznab.EvidenceOutcomeContradicted
	default:
		return newznab.CommissionEvidence{}, false
	}
	return newznab.CommissionEvidence{
		Key:                    evidence.Key,
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 evidence.Target,
		ObservedAt:             evidence.ObservedAt,
		BodySHA256:             evidence.BodySHA256.String(),
		SafeOutcome:            outcome,
		RawResultCount:         int(evidence.RawResultCount),
		PlausibleIdentityCount: int(evidence.PlausibleIdentityCount),
	}, true
}

func recipeForDraftStep(step ProbeStep) newznab.SearchRecipe {
	categories := newznab.CategorySpec{Set: step.Request.CategorySet}
	if categories.Set == newznab.CategoryVerifiedFixed {
		categories.FixedIDs = append([]int(nil), step.CategoryIDs...)
	}
	return newznab.SearchRecipe{
		Key:           step.Target.RecipeKey,
		Scope:         step.Scope,
		Mode:          step.Request.Mode,
		Query:         step.Request.Query,
		Bindings:      append([]newznab.WireBinding(nil), step.Request.Bindings...),
		Extended:      step.Request.Extended,
		Categories:    categories,
		VerifiedLimit: step.Request.RequestLimit,
		Role:          step.Role,
		Priority:      step.Priority,
		FallbackOn:    append([]newznab.FallbackTrigger(nil), step.Request.FallbackOn...),
	}
}

func buildDraftProvenance(input DraftInput, evidenceKeys []string, document []byte) (DraftProvenance, error) {
	provenance := DraftProvenance{
		IndexerInstanceID:       input.IndexerInstanceID,
		RunID:                   input.RunID,
		EndpointConfigSHA256:    input.EndpointConfigSHA256,
		ProviderContract:        cloneProviderContract(input.ProviderContract),
		ProfileSchemaVersion:    input.ProfileSchemaVersion,
		PlannerVersion:          input.PlannerVersion,
		CategorySelectionSHA256: input.CategorySelectionSHA256,
		FixtureSHA256:           input.FixtureSHA256,
		CAPSPlanSHA256:          input.CAPSPlanSHA256,
		SearchPlanSHA256:        input.SearchPlanSHA256,
		CAPSEvidenceKey:         input.CAPSEvidence.Key,
		EvidenceKeys:            append([]string(nil), evidenceKeys...),
		DocumentSHA256:          SumSHA256(document),
	}
	// The fingerprint covers the identity fields exactly once and never
	// includes itself. Marshal an explicit field set rather than the value
	// with a still-zero Fingerprint field.
	encoded, err := json.Marshal(struct {
		Version                 uint16                       `json:"version"`
		IndexerInstanceID       string                       `json:"indexer_instance_id"`
		RunID                   string                       `json:"run_id"`
		EndpointConfigSHA256    SHA256Digest                 `json:"endpoint_config_sha256"`
		ProviderContract        *newznab.ProviderContractRef `json:"provider_contract,omitempty"`
		ProfileSchemaVersion    uint16                       `json:"profile_schema_version"`
		PlannerVersion          uint16                       `json:"planner_version"`
		CategorySelectionSHA256 SHA256Digest                 `json:"category_selection_sha256"`
		FixtureSHA256           SHA256Digest                 `json:"fixture_sha256"`
		CAPSPlanSHA256          SHA256Digest                 `json:"caps_plan_sha256"`
		SearchPlanSHA256        SHA256Digest                 `json:"search_plan_sha256"`
		CAPSEvidenceKey         string                       `json:"caps_evidence_key"`
		EvidenceKeys            []string                     `json:"evidence_keys"`
		DocumentSHA256          SHA256Digest                 `json:"document_sha256"`
	}{
		Version:                 DocumentSchemaVersion,
		IndexerInstanceID:       provenance.IndexerInstanceID,
		RunID:                   provenance.RunID,
		EndpointConfigSHA256:    provenance.EndpointConfigSHA256,
		ProviderContract:        provenance.ProviderContract,
		ProfileSchemaVersion:    provenance.ProfileSchemaVersion,
		PlannerVersion:          provenance.PlannerVersion,
		CategorySelectionSHA256: provenance.CategorySelectionSHA256,
		FixtureSHA256:           provenance.FixtureSHA256,
		CAPSPlanSHA256:          provenance.CAPSPlanSHA256,
		SearchPlanSHA256:        provenance.SearchPlanSHA256,
		CAPSEvidenceKey:         provenance.CAPSEvidenceKey,
		EvidenceKeys:            provenance.EvidenceKeys,
		DocumentSHA256:          provenance.DocumentSHA256,
	})
	if err != nil {
		return DraftProvenance{}, ErrInvalidCommissioningDocument
	}
	provenance.Fingerprint = SumSHA256(encoded)
	return provenance, nil
}
