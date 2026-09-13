package commissioningv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

const maximumEvidenceDocumentBytes = 1 << 20

// StepEvidenceDocument is deliberately smaller than a release observation.
// The offline milestone stores no provider body, URL, header, error text, or
// arbitrary semantic mapping. Later live code may construct one only after the
// structural and exact-secret gates have passed.
type StepEvidenceDocument struct {
	SchemaVersion          uint16                 `json:"schema_version"`
	Key                    string                 `json:"key"`
	Kind                   newznab.EvidenceKind   `json:"kind"`
	Target                 newznab.EvidenceTarget `json:"target"`
	ObservedAt             time.Time              `json:"observed_at"`
	SafeOutcome            SafeOutcome            `json:"safe_outcome"`
	RawResultCount         uint16                 `json:"raw_result_count,omitempty"`
	PlausibleIdentityCount uint16                 `json:"plausible_identity_count,omitempty"`
	BodySHA256             SHA256Digest           `json:"body_sha256"`
	CAPS                   *newznab.CAPSRecord    `json:"caps,omitempty"`
	Search                 *SearchProof           `json:"search_proof,omitempty"`
}

func ValidateStepEvidence(document StepEvidenceDocument) error {
	if document.SchemaVersion != DocumentSchemaVersion || !evidenceKeyPattern.MatchString(document.Key) || document.Kind != newznab.EvidenceKindControlledProbe || document.ObservedAt.IsZero() || document.BodySHA256.IsZero() || document.PlausibleIdentityCount > document.RawResultCount || document.RawResultCount > defaultProbeLimit {
		return ErrInvalidCommissioningDocument
	}
	switch document.Target.Kind {
	case newznab.EvidenceTargetCAPS:
		if document.Target.RecipeKey != "" || document.SafeOutcome != OutcomeCAPSObserved || document.RawResultCount != 0 || document.PlausibleIdentityCount != 0 || document.CAPS == nil || document.Search != nil || document.CAPS.BodySHA256 != document.BodySHA256.String() || !document.CAPS.ObservedAt.Equal(document.ObservedAt) || validateCAPS(*document.CAPS) != nil {
			return ErrInvalidCommissioningDocument
		}
	case newznab.EvidenceTargetSearchRecipe:
		if !recipeKeyPattern.MatchString(document.Target.RecipeKey) || document.CAPS != nil {
			return ErrInvalidCommissioningDocument
		}
		if document.Search != nil && ValidateSearchProof(*document.Search) != nil {
			return ErrInvalidCommissioningDocument
		}
		// Explicit §9.4 evidence allowlist: evidence is permitted ONLY for
		// recipe_verified, no_results, no_plausible_match, unsupported,
		// permission_restricted, contradicted. CAPS carries evidence only on
		// the caps_observed target above. Every other outcome — including
		// all safety_*, transport, deferral, stale, cancelled, indeterminate
		// and unknown labels — returns ErrInvalidCommissioningDocument.
		switch document.SafeOutcome {
		case OutcomeNoResults:
			if document.RawResultCount != 0 || document.PlausibleIdentityCount != 0 {
				return ErrInvalidCommissioningDocument
			}
		case OutcomeNoPlausibleMatch:
			if document.RawResultCount == 0 || document.PlausibleIdentityCount != 0 {
				return ErrInvalidCommissioningDocument
			}
		case OutcomeRecipeVerified, OutcomeUnsupported, OutcomePermissionRestricted, OutcomeContradicted:
			// General evidence invariant (§9.4/M-1): verified claims require
			// a plausible identity (PlausibleIdentityCount > 0) with every
			// raw result plausibly matching (PlausibleIdentityCount ==
			// RawResultCount); otherwise the claim is no_plausible_match,
			// not verified. Unsupported/permission-restricted/contradicted
			// carry exact-target proof in the bounded count fields; the
			// global PlausibleIdentityCount <= RawResultCount bound (checked
			// above) plus the outcome/target/shape checks are the coherent
			// invariant.
			if document.SafeOutcome == OutcomeRecipeVerified && (document.PlausibleIdentityCount == 0 || document.PlausibleIdentityCount != document.RawResultCount) {
				return ErrInvalidCommissioningDocument
			}
		default:
			return ErrInvalidCommissioningDocument
		}
	default:
		return ErrInvalidCommissioningDocument
	}
	return nil
}

func DecodeStepEvidence(raw []byte) (StepEvidenceDocument, error) {
	if len(raw) == 0 || len(raw) > maximumEvidenceDocumentBytes || !utf8.Valid(raw) {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document StepEvidenceDocument
	if err := decoder.Decode(&document); err != nil {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	if document.Target.Kind == newznab.EvidenceTargetSearchRecipe && document.Search != nil {
		if err := validateSearchEvidenceShape(document); err != nil {
			return StepEvidenceDocument{}, err
		}
		return document, nil
	}
	if err := ValidateStepEvidence(document); err != nil {
		return StepEvidenceDocument{}, err
	}
	return document, nil
}

func CanonicalStepEvidence(document StepEvidenceDocument) ([]byte, error) {
	if err := ValidateStepEvidence(document); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ErrInvalidCommissioningDocument
	}
	return encoded, nil
}

// CanonicalStepEvidenceForTarget canonicalizes a search-recipe evidence
// document with the Section-20 search rules and every other document with the
// frozen generic validator. The generic classifier/validator is unchanged.
func CanonicalStepEvidenceForTarget(document StepEvidenceDocument) ([]byte, error) {
	if document.Target.Kind == newznab.EvidenceTargetSearchRecipe && document.Search != nil {
		if err := validateSearchEvidenceShape(document); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return nil, ErrInvalidCommissioningDocument
		}
		return encoded, nil
	}
	return CanonicalStepEvidence(document)
}

// ValidateSearchPlanCAPSEvidence binds a sealed search plan to the exact safe
// richer CAPS observation that justified its request bases.
func ValidateSearchPlanCAPSEvidence(plan ProbePlan, evidence StepEvidenceDocument) error {
	if err := ValidateProbePlan(plan); err != nil {
		return err
	}
	if plan.Phase != PlanPhaseSearch || evidence.Key != plan.CAPSEvidenceKey {
		return ErrInvalidCommissioningDocument
	}
	if err := ValidateStepEvidence(evidence); err != nil || evidence.Target.Kind != newznab.EvidenceTargetCAPS || evidence.CAPS == nil {
		return ErrInvalidCommissioningDocument
	}
	digest, err := CAPSRecordDigest(*evidence.CAPS)
	if err != nil || plan.CAPSRecordSHA256 == nil || digest != *plan.CAPSRecordSHA256 {
		return ErrInvalidCommissioningDocument
	}
	for _, step := range plan.Steps {
		candidate, declaredExtended, exists := recipeCandidateForKey(step.Target.RecipeKey)
		if !exists {
			return ErrInvalidCommissioningDocument
		}
		basis, expectedExtended, reasonable := candidateCAPSBasis(*evidence.CAPS, candidate.mode, candidate.query, candidate.bindings)
		if !reasonable || basis != step.CAPSBasis || expectedExtended != declaredExtended || expectedExtended != step.Request.Extended {
			return ErrInvalidCommissioningDocument
		}
	}
	return nil
}
