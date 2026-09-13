package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
)

func testB31Time() time.Time { return time.Unix(1_700_000_100, 0).UTC() }

func commissioningv2TargetCAPS() newznab.EvidenceTarget {
	return newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}
}

func commissioningv2TargetSearch() newznab.EvidenceTarget {
	return newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
}

func commissioningRepositoryCAPSEvidenceForB31() commissioningv2.StepEvidenceDocument {
	return commissioningRepositoryCAPSEvidence(testB31Time())
}

func applyEvidenceB31(t *testing.T, inputs *commissioningv2.LiveAttemptInputs, response commissioningv2.ResponseVerdict) {
	t.Helper()
	needsEvidence := false
	switch response {
	case commissioningv2.ResponseRecipeVerified, commissioningv2.ResponseNoResults,
		commissioningv2.ResponseNoPlausibleMatch, commissioningv2.ResponseUnsupported,
		commissioningv2.ResponsePermissionRestricted, commissioningv2.ResponseContradicted:
		needsEvidence = true
	}
	if !needsEvidence {
		return
	}
	evidence := searchEvidenceForB31(t, response)
	inputs.Evidence = evidence
	inputs.RawResultCount = uint16PtrB31(evidence.RawResultCount)
	inputs.PlausibleIdentityCount = uint16PtrB31(evidence.PlausibleIdentityCount)
	inputs.RawBodySHA256 = evidence.BodySHA256.Bytes()
}

func searchEvidenceForB31(t *testing.T, response commissioningv2.ResponseVerdict) *commissioningv2.StepEvidenceDocument {
	t.Helper()
	var outcome commissioningv2.SafeOutcome
	switch response {
	case commissioningv2.ResponseRecipeVerified:
		outcome = commissioningv2.OutcomeRecipeVerified
	case commissioningv2.ResponseNoResults:
		outcome = commissioningv2.OutcomeNoResults
	case commissioningv2.ResponseNoPlausibleMatch:
		outcome = commissioningv2.OutcomeNoPlausibleMatch
	case commissioningv2.ResponseUnsupported:
		outcome = commissioningv2.OutcomeUnsupported
	case commissioningv2.ResponsePermissionRestricted:
		outcome = commissioningv2.OutcomePermissionRestricted
	case commissioningv2.ResponseContradicted:
		outcome = commissioningv2.OutcomeContradicted
	default:
		t.Fatalf("no evidence for %q", response)
	}
	body := commissioningv2.SumSHA256([]byte("b31-" + string(response)))
	raw, plausible := uint16(3), uint16(2)
	if response == commissioningv2.ResponseNoResults {
		raw, plausible = 0, 0
	} else if response == commissioningv2.ResponseNoPlausibleMatch {
		raw, plausible = 2, 0
	} else if response == commissioningv2.ResponseRecipeVerified {
		raw, plausible = 2, 2
	}
	return &commissioningv2.StepEvidenceDocument{
		SchemaVersion:          commissioningv2.DocumentSchemaVersion,
		Key:                    "evidence.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 commissioningv2TargetSearch(),
		ObservedAt:             testB31Time(),
		SafeOutcome:            outcome,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
		BodySHA256:             body,
	}
}
