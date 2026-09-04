package commissioningv2

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

func TestStepEvidenceIsExactTargetBoundAndStrict(t *testing.T) {
	t.Parallel()
	capsEvidence := testCAPSEvidence()
	encoded, err := CanonicalStepEvidence(capsEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStepEvidence(encoded); err != nil {
		t.Fatal(err)
	}

	searchEvidence := StepEvidenceDocument{
		SchemaVersion:          DocumentSchemaVersion,
		Key:                    "evidence.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.extended"},
		ObservedAt:             time.Unix(1_700_000_001, 0).UTC(),
		SafeOutcome:            OutcomeNoPlausibleMatch,
		RawResultCount:         2,
		PlausibleIdentityCount: 0,
		BodySHA256:             SumSHA256([]byte("synthetic search response")),
	}
	if err := ValidateStepEvidence(searchEvidence); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*StepEvidenceDocument){
		func(value *StepEvidenceDocument) { value.Target.RecipeKey = "" },
		func(value *StepEvidenceDocument) {
			value.Target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetNFO}
		},
		func(value *StepEvidenceDocument) { value.SafeOutcome = OutcomeSafetyCredentialEcho },
		func(value *StepEvidenceDocument) { value.PlausibleIdentityCount = 1 },
		func(value *StepEvidenceDocument) { value.Key = "provider-controlled-id" },
	} {
		candidate := searchEvidence
		mutate(&candidate)
		if err := ValidateStepEvidence(candidate); err == nil {
			t.Fatalf("accepted invalid evidence %#v", candidate)
		}
	}
	for _, unproven := range []SafeOutcome{OutcomeRecipeVerified, OutcomeUnsupported, OutcomePermissionRestricted, OutcomeContradicted} {
		candidate := searchEvidence
		candidate.SafeOutcome = unproven
		candidate.RawResultCount = 2
		candidate.PlausibleIdentityCount = 1
		if err := ValidateStepEvidence(candidate); err == nil {
			t.Fatalf("accepted capability claim %q without closed semantic proof", unproven)
		}
	}

	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown"] = true
	unknown, _ := json.Marshal(object)
	if _, err := DecodeStepEvidence(unknown); err == nil {
		t.Fatal("accepted unknown evidence field")
	}
}

func TestCAPSEvidenceRequiresExactTimeAndEveryKnownMode(t *testing.T) {
	t.Parallel()
	evidence := testCAPSEvidence()
	if err := ValidateStepEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	differentTime := evidence
	differentTime.ObservedAt = differentTime.ObservedAt.Add(time.Second)
	if err := ValidateStepEvidence(differentTime); err == nil {
		t.Fatal("accepted contradictory CAPS observation times")
	}
	missingMode := evidence
	caps := *evidence.CAPS
	caps.ModeObservations = append([]newznab.CAPSModeObservation(nil), caps.ModeObservations[:2]...)
	missingMode.CAPS = &caps
	if err := ValidateStepEvidence(missingMode); err == nil {
		t.Fatal("accepted CAPS evidence that omitted a known-mode observation")
	}
	allAbsent := evidence
	absentCAPS := *evidence.CAPS
	absentCAPS.ModeObservations = []newznab.CAPSModeObservation{
		{Mode: newznab.SearchModeGeneral, ElementState: newznab.CAPSElementAbsent},
		{Mode: newznab.SearchModeMovie, ElementState: newznab.CAPSElementAbsent},
		{Mode: newznab.SearchModeTV, ElementState: newznab.CAPSElementAbsent},
	}
	allAbsent.CAPS = &absentCAPS
	if err := ValidateStepEvidence(allAbsent); err != nil {
		t.Fatalf("explicit absent observations should remain truthful CAPS evidence: %v", err)
	}
}

func TestStepEvidenceRejectsRawInvalidUTF8BeforeJSONDecoding(t *testing.T) {
	t.Parallel()
	evidence := testCAPSEvidence()
	encoded, err := CanonicalStepEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	needle := []byte("Synthetic Newznab")
	offset := bytes.Index(encoded, needle)
	if offset < 0 {
		t.Fatal("provider title missing from evidence fixture")
	}
	malformed := append([]byte(nil), encoded...)
	malformed[offset] = 0xff
	if _, err := DecodeStepEvidence(malformed); err == nil {
		t.Fatal("accepted raw invalid UTF-8 in CAPS provider title")
	}
}

func TestSearchPlanCAPSBindingRecomputesEligibility(t *testing.T) {
	t.Parallel()
	input := testSearchPlanInput()
	plan, err := BuildSearchPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	tampered := plan
	tampered.Steps = append([]ProbeStep(nil), plan.Steps...)
	for index := range tampered.Steps {
		if tampered.Steps[index].Scope == newznab.SearchScopeMovie {
			tampered.Steps[index].CAPSBasis = CAPSBasisOmittedGeneric
			tampered.Steps[index].Request.Extended = false
			tampered.Steps[index].Target.RecipeKey = stringsTrimExtended(tampered.Steps[index].Target.RecipeKey) + ".basic"
			tampered.Steps[index].StructuralSHA256, _ = StructuralRequestFingerprint(tampered.ConfigurationSHA256, tampered.ProviderContract, tampered.ProfileSchemaVersion, tampered.PlannerVersion, tampered.Steps[index])
		}
	}
	if err := ValidateProbePlan(tampered); err != nil {
		t.Fatalf("intrinsic structure should remain valid for this cross-evidence test: %v", err)
	}
	if err := ValidateSearchPlanCAPSEvidence(tampered, input.CAPSEvidence); err == nil {
		t.Fatal("CAPS evidence accepted a re-fingerprinted false omitted claim")
	}
}

func stringsTrimExtended(value string) string {
	if len(value) >= len(".extended") && value[len(value)-len(".extended"):] == ".extended" {
		return value[:len(value)-len(".extended")]
	}
	return value
}
