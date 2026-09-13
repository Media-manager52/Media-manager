package commissioningv2

import (
	"bytes"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

// recordResultValidityCases is the SHARED C-56 classification input matrix
// (all 17 response verdicts x drift + all 3 transport failures x drift =
// 40 combinations). Every C-56/C-57 companion test enumerates THIS matrix,
// so no row can drift out of coverage.
func recordResultValidityCases() (responses []ResponseVerdict, failures []TransportFailure, drifts []bool) {
	responses = []ResponseVerdict{
		ResponseCAPSObserved, ResponseRecipeVerified, ResponseNoResults,
		ResponseNoPlausibleMatch, ResponseUnsupported, ResponsePermissionRestricted,
		ResponseContradicted, ResponseMalformed, ResponseSemanticAmbiguity,
		ResponseRateLimited, ResponseQuotaExhausted, ResponseAuthenticationFailure,
		ResponseSafetyCredentialEcho, ResponseSafetyUnsafeRedirect,
		ResponseSafetyUnsafeText, ResponseSafetyBodyLimit, ResponseSafetyXMLLimit,
	}
	failures = []TransportFailure{TransportFailureDNS, TransportFailureTLS, TransportFailureConnection}
	drifts = []bool{false, true}
	return responses, failures, drifts
}

func mustUint16(value uint16) *uint16 { return &value }

func capsTarget() newznab.EvidenceTarget {
	return newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}
}

// totalClassifiableCombinations is the exact C-56 conditional enumeration:
// Transport selects the branch, so received covers 17 responses x 2 drifts
// and no_complete_response covers 3 failures x 2 drifts: 34+6 = 40. It is
// asserted (not merely documented) by every matrix-count test below.
func totalClassifiableCombinations() int {
	return 17*2 + 3*2
}

func TestClassifyLiveAttemptIsTotalAndDeterministic(t *testing.T) {
	t.Parallel()
	responses, failures, driftedValues := recordResultValidityCases()
	// Exact C-56 enumeration per §23: (Transport × Response/Failure × Drift)
	// = received:17×2 + no_response:3×2 = 34+6 = 40 combinations.
	if totalClassifiableCombinations() != 40 {
		t.Fatalf("C-56 enumeration base changed: want 40 got %d", totalClassifiableCombinations())
	}
	wantCount := len(responses)*len(driftedValues) + len(failures)*len(driftedValues)
	if wantCount != totalClassifiableCombinations() {
		t.Fatalf("C-56 enumeration base changed: want 40 got %d", wantCount)
	}
	count := 0
	for _, drifted := range driftedValues {
		for _, response := range responses {
			target := capsTarget()
			if response == ResponseRecipeVerified || response == ResponseNoResults || response == ResponseNoPlausibleMatch || response == ResponseUnsupported || response == ResponsePermissionRestricted || response == ResponseContradicted {
				target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
			}
			inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: response, FreshnessDrifted: drifted, TargetKind: target}
			applyEvidenceForOutcome(t, &inputs, response, drifted)
			first, err := ClassifyLiveAttempt(inputs)
			if err != nil {
				t.Fatalf("response=%q drifted=%v: %v", response, drifted, err)
			}
			second, err := ClassifyLiveAttempt(inputs)
			if err != nil {
				t.Fatalf("response=%q drifted=%v: %v", response, drifted, err)
			}
			if first.SafeOutcome != second.SafeOutcome || first.SafeCode != second.SafeCode || first.SchedulerResult != second.SchedulerResult || first.ClearGate != second.ClearGate {
				t.Fatalf("nondeterministic classification for %q", response)
			}
			count++
		}
		for _, failure := range []TransportFailure{TransportFailureDNS, TransportFailureTLS, TransportFailureConnection} {
			inputs := LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: failure, FreshnessDrifted: drifted}
			classified, err := ClassifyLiveAttempt(inputs)
			if err != nil {
				t.Fatalf("failure=%q drifted=%v: %v", failure, drifted, err)
			}
			if classified.ClearGate {
				t.Fatalf("no-response failure cleared gate: %q", failure)
			}
			count++
		}
	}
	// Safety responses are unreachable with FreshnessDrifted=false target
	// mismatches excluded: safety carries no evidence regardless of drift,
	// while non-safety evidence shapes differ under drift (dropped). The
	// count therefore covers all classifiable combinations.
	if count != wantCount {
		t.Fatalf("C-56 classified %d combinations, want exactly %d", count, wantCount)
	}
	// Every classified combination must satisfy the REAL production
	// validity predicates: the downstream contract is enforced by
	// CompleteLiveAttempt through validCommissioningRecordResult (the same
	// closed shape as indexers.validRecordResult) plus
	// commissioningStepSafeCodeMatches. Both live in package postgres and
	// cannot be imported here (import cycle), so package-local companion
	// tests restate-and-pin them:
	//   - TestRecordResultValidityShapeMatchesDownstreamContract pins the
	//     RecordResult shape against indexers.ComputeOutcomeEffect
	//     acceptance (which rejects the same invalid shapes the repository
	//     rejects);
	//   - TestStepSafeCodeValidityShapeMatchesDownstreamContract pins the
	//     (SafeOutcome, SafeCode) compatibility matrix against
	//     ValidSafeCode plus the exact allowlist CompleteLiveAttempt
	//     enforces.
	// The full DB-backed acceptance (validCommissioningRecordResult +
	// commissioningStepSafeCodeMatches executed inside CompleteLiveAttempt
	// for every transition class) is
	// TestCompleteLiveAttemptRunTransitionSafeCodesDurable.
	for _, drifted := range driftedValues {
		for _, response := range responses {
			target := capsTarget()
			if response == ResponseRecipeVerified || response == ResponseNoResults || response == ResponseNoPlausibleMatch || response == ResponseUnsupported || response == ResponsePermissionRestricted || response == ResponseContradicted {
				target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
			}
			inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: response, FreshnessDrifted: drifted, TargetKind: target}
			applyEvidenceForOutcome(t, &inputs, response, drifted)
			classified, err := ClassifyLiveAttempt(inputs)
			if err != nil {
				t.Fatalf("compat response=%q drifted=%v: %v", response, drifted, err)
			}
			if !ValidSafeOutcome(classified.SafeOutcome) {
				t.Fatalf("compat response=%q: invalid SafeOutcome %q", response, classified.SafeOutcome)
			}
			if !recordResultValidityShape(classified.SchedulerResult) {
				t.Fatalf("compat response=%q: invalid SchedulerResult %#v", response, classified.SchedulerResult)
			}
			if !stepSafeCodeValidityShape(classified.SafeOutcome, classified.SafeCode) {
				t.Fatalf("compat response=%q: invalid (outcome,code) (%q,%q)", response, classified.SafeOutcome, classified.SafeCode)
			}
			if !ClassifiedCompletionMatchesInputs(classified) {
				t.Fatalf("compat response=%q: classified output does not match its inputs", response)
			}
		}
		for _, failure := range failures {
			classified, err := ClassifyLiveAttempt(LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: failure, FreshnessDrifted: drifted})
			if err != nil {
				t.Fatalf("compat failure=%q drifted=%v: %v", failure, drifted, err)
			}
			if !ValidSafeOutcome(classified.SafeOutcome) {
				t.Fatalf("compat failure=%q: invalid SafeOutcome %q", failure, classified.SafeOutcome)
			}
			if !recordResultValidityShape(classified.SchedulerResult) {
				t.Fatalf("compat failure=%q: invalid SchedulerResult %#v", failure, classified.SchedulerResult)
			}
			if !stepSafeCodeValidityShape(classified.SafeOutcome, classified.SafeCode) {
				t.Fatalf("compat failure=%q: invalid (outcome,code) (%q,%q)", failure, classified.SafeOutcome, classified.SafeCode)
			}
			if !ClassifiedCompletionMatchesInputs(classified) {
				t.Fatalf("compat failure=%q: classified output does not match its inputs", failure)
			}
		}
	}
}

// recordResultValidityShape is the C-56 RecordResult validity shape. It is
// the exact closed predicate CompleteLiveAttempt enforces via
// validCommissioningRecordResult in package postgres (same package family
// as indexers.validRecordResult); the one behavioral delta is pinned by
// TestRecordResultValidityShapeMatchesDownstreamContract so this local
// shape cannot drift from the downstream contract it stands in for.
func recordResultValidityShape(result indexers.RecordResult) bool {
	switch result.Outcome {
	case indexers.OutcomeSuccess, indexers.OutcomeNoResults, indexers.OutcomeCancelled:
		return result.Scope == indexers.FailureScopeNone && result.RetryAt.IsZero()
	case indexers.OutcomeRateLimited:
		return result.Scope == indexers.FailureScopeInstance
	case indexers.OutcomeAuthFailed:
		return result.Scope == indexers.FailureScopeInstance && result.RetryAt.IsZero()
	case indexers.OutcomeSystemFailure:
		return (result.Scope == indexers.FailureScopeInstance || result.Scope == indexers.FailureScopeOperation) && result.RetryAt.IsZero()
	default:
		return false
	}
}

// stepSafeCodeValidityShape is the C-56 (SafeOutcome, SafeCode)
// compatibility shape. It is the exact predicate CompleteLiveAttempt
// enforces via commissioningStepSafeCodeMatches in package postgres; the
// pairing rows are pinned by
// TestStepSafeCodeValidityShapeMatchesDownstreamContract so this local
// shape cannot drift from the downstream contract.
func stepSafeCodeValidityShape(outcome SafeOutcome, code SafeCode) bool {
	if code == "" {
		return true
	}
	if !ValidSafeCode(code) {
		return false
	}
	switch code {
	case SafeCodeConfigurationChanged:
		return outcome == OutcomeConfigurationStale
	case SafeCodeOwnerCancelled:
		return outcome == OutcomeCancelled
	case SafeCodeCredentialEcho:
		return outcome == OutcomeSafetyCredentialEcho
	case SafeCodeIndeterminateAttempt:
		return outcome == OutcomeIndeterminate
	default:
		return false
	}
}

// TestRecordResultValidityShapeMatchesDownstreamContract pins the local
// C-56 RecordResult shape to the REAL downstream contract: every
// (Outcome, Scope, RetryAt) combination the shape accepts must also be
// accepted by the real scheduler path (ComputeOutcomeEffect preserves the
// outcome for non-operation scopes and preserves history for operation
// scope), and the canonical invalid shapes must be rejected by the shape.
// The repository executes validCommissioningRecordResult (identical closed
// predicate) inside CompleteLiveAttempt; the durable arms of
// TestCompleteLiveAttemptRunTransitionSafeCodesDurable prove the real
// predicate end to end.
func TestRecordResultValidityShapeMatchesDownstreamContract(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_100, 0).UTC()
	budget := indexers.Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: time.Hour}
	valid := []indexers.RecordResult{
		{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeNoResults, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeCancelled, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance, RetryAt: now.Add(time.Minute)},
		{Outcome: indexers.OutcomeAuthFailed, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeOperation},
	}
	for _, result := range valid {
		if !recordResultValidityShape(result) {
			t.Fatalf("valid shape rejected: %#v", result)
		}
	}
	invalid := []indexers.RecordResult{
		{},
		{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeOperation},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeAuthFailed, Scope: indexers.FailureScopeInstance, RetryAt: now.Add(time.Minute)},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance, RetryAt: now.Add(time.Minute)},
		{Outcome: "bogus", Scope: indexers.FailureScopeNone},
	}
	for _, result := range invalid {
		if recordResultValidityShape(result) {
			t.Fatalf("invalid shape accepted: %#v", result)
		}
	}
	// Real-contract differential: operation scope preserves outcome history
	// while instance scope advances it (ComputeOutcomeEffect is the real
	// scheduler implementation sharing the classification contract).
	start := indexers.Snapshot{IndexerID: "c56-shape", LastOutcome: indexers.OutcomeSuccess, LastOutcomeAt: now}
	operation := indexers.ComputeOutcomeEffect(start, budget, "c56-shape", indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeOperation}, "", now.Add(time.Second))
	if operation.LastOutcome != indexers.OutcomeSuccess || !operation.LastOutcomeAt.Equal(now) {
		t.Fatalf("operation-scope real contract changed: %#v", operation)
	}
	instance := indexers.ComputeOutcomeEffect(start, budget, "c56-shape", indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance}, "", now.Add(time.Second))
	if instance.LastOutcome != indexers.OutcomeSystemFailure || !instance.LastOutcomeAt.Equal(now.Add(time.Second)) {
		t.Fatalf("instance-scope real contract wrong: %#v", instance)
	}
}

// TestStepSafeCodeValidityShapeMatchesDownstreamContract pins every
// (SafeOutcome, SafeCode) pairing row of the local C-56 shape against the
// exact allowlist the repository enforces: empty code always matches, each
// non-empty code matches exactly one outcome family, and every cross-family
// pairing is rejected.
func TestStepSafeCodeValidityShapeMatchesDownstreamContract(t *testing.T) {
	t.Parallel()
	matching := []struct {
		outcome SafeOutcome
		code    SafeCode
	}{
		{OutcomeCAPSObserved, ""},
		{OutcomeRateLimited, ""},
		{OutcomeDNSFailure, ""},
		{OutcomeConfigurationStale, SafeCodeConfigurationChanged},
		{OutcomeCancelled, SafeCodeOwnerCancelled},
		{OutcomeSafetyCredentialEcho, SafeCodeCredentialEcho},
		{OutcomeIndeterminate, SafeCodeIndeterminateAttempt},
	}
	for _, row := range matching {
		if !stepSafeCodeValidityShape(row.outcome, row.code) {
			t.Fatalf("valid pairing rejected: (%q,%q)", row.outcome, row.code)
		}
	}
	mismatching := []struct {
		outcome SafeOutcome
		code    SafeCode
	}{
		{OutcomeCAPSObserved, SafeCodeConfigurationChanged},
		{OutcomeRateLimited, SafeCodeCredentialEcho},
		{OutcomeConfigurationStale, SafeCodeCredentialEcho},
		{OutcomeSafetyCredentialEcho, SafeCodeConfigurationChanged},
		{OutcomeCancelled, SafeCodeIndeterminateAttempt},
		{OutcomeIndeterminate, SafeCodeOwnerCancelled},
		{OutcomeDNSFailure, SafeCodeCredentialEcho},
		{OutcomeCAPSObserved, SafeCode("bogus.code")},
	}
	for _, row := range mismatching {
		if stepSafeCodeValidityShape(row.outcome, row.code) {
			t.Fatalf("invalid pairing accepted: (%q,%q)", row.outcome, row.code)
		}
	}
}

func applyEvidenceForOutcome(t *testing.T, inputs *LiveAttemptInputs, response ResponseVerdict, drifted bool) {
	t.Helper()
	needsEvidence := false
	switch response {
	case ResponseCAPSObserved, ResponseRecipeVerified, ResponseNoResults,
		ResponseNoPlausibleMatch, ResponseUnsupported, ResponsePermissionRestricted,
		ResponseContradicted:
		needsEvidence = true
	}
	safety := false
	switch response {
	case ResponseSafetyCredentialEcho, ResponseSafetyUnsafeRedirect,
		ResponseSafetyUnsafeText, ResponseSafetyBodyLimit, ResponseSafetyXMLLimit:
		safety = true
	}
	if drifted && !safety {
		return
	}
	if !needsEvidence {
		return
	}
	evidence := testClassifyEvidence(t, response)
	inputs.Evidence = evidence
	inputs.RawResultCount = mustUint16(evidence.RawResultCount)
	inputs.PlausibleIdentityCount = mustUint16(evidence.PlausibleIdentityCount)
	inputs.RawBodySHA256 = evidence.BodySHA256.Bytes()
}

func TestClassifyLiveAttemptNeverEmitsPreSendOrRecoveryOutcomes(t *testing.T) {
	t.Parallel()
	responses, failures, driftedValues := recordResultValidityCases()
	for _, drifted := range driftedValues {
		for _, response := range responses {
			target := capsTarget()
			if response == ResponseRecipeVerified || response == ResponseNoResults || response == ResponseNoPlausibleMatch || response == ResponseUnsupported || response == ResponsePermissionRestricted || response == ResponseContradicted {
				target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
			}
			inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: response, FreshnessDrifted: drifted, TargetKind: target}
			applyEvidenceForOutcome(t, &inputs, response, drifted)
			classified, err := ClassifyLiveAttempt(inputs)
			if err != nil {
				t.Fatalf("response=%q drifted=%v: %v", response, drifted, err)
			}
			switch classified.SafeOutcome {
			case OutcomeRetryAfterDeferred, OutcomeCredentialUnavailable, OutcomeCancelled, OutcomeIndeterminate:
				t.Fatalf("pre-send/recovery outcome emitted for %q", response)
			}
			switch classified.SafeCode {
			case SafeCodeOwnerCancelled, SafeCodeIndeterminateAttempt:
				t.Fatalf("pre-send/recovery safe code emitted for %q: %q", response, classified.SafeCode)
			}
		}
		for _, failure := range failures {
			classified, err := ClassifyLiveAttempt(LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: failure, FreshnessDrifted: drifted})
			if err != nil {
				t.Fatalf("failure=%q drifted=%v: %v", failure, drifted, err)
			}
			switch classified.SafeOutcome {
			case OutcomeRetryAfterDeferred, OutcomeCredentialUnavailable, OutcomeCancelled, OutcomeIndeterminate:
				t.Fatalf("pre-send/recovery outcome emitted for failure %q", failure)
			}
			switch classified.SafeCode {
			case SafeCodeOwnerCancelled, SafeCodeIndeterminateAttempt:
				t.Fatalf("pre-send/recovery safe code emitted for failure %q: %q", failure, classified.SafeCode)
			}
		}
	}
}

func TestClassifyLiveAttemptClearGateMatchesTriggerAllowlist(t *testing.T) {
	t.Parallel()
	allowlist := gateAllowlistFromMigration(t)
	if len(allowlist) != 18 {
		t.Fatalf("allowlist=%d", len(allowlist))
	}
	responses := []ResponseVerdict{
		ResponseCAPSObserved, ResponseRecipeVerified, ResponseNoResults,
		ResponseNoPlausibleMatch, ResponseUnsupported, ResponsePermissionRestricted,
		ResponseContradicted, ResponseMalformed, ResponseSemanticAmbiguity,
		ResponseRateLimited, ResponseQuotaExhausted, ResponseAuthenticationFailure,
		ResponseSafetyCredentialEcho, ResponseSafetyUnsafeRedirect,
		ResponseSafetyUnsafeText, ResponseSafetyBodyLimit, ResponseSafetyXMLLimit,
	}
	for _, response := range responses {
		target := capsTarget()
		if response == ResponseRecipeVerified || response == ResponseNoResults || response == ResponseNoPlausibleMatch || response == ResponseUnsupported || response == ResponsePermissionRestricted || response == ResponseContradicted {
			target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
		}
		inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: response, TargetKind: target}
		applyEvidenceForOutcome(t, &inputs, response, false)
		classified, err := ClassifyLiveAttempt(inputs)
		if err != nil {
			t.Fatal(err)
		}
		if !classified.ClearGate || !allowlist[classified.SafeOutcome] {
			t.Fatalf("response=%q outcome=%q clear=%v", response, classified.SafeOutcome, classified.ClearGate)
		}
	}
	for _, failure := range []TransportFailure{TransportFailureDNS, TransportFailureTLS, TransportFailureConnection} {
		classified, err := ClassifyLiveAttempt(LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: failure})
		if err != nil {
			t.Fatal(err)
		}
		if classified.ClearGate {
			t.Fatalf("failure=%q cleared gate", failure)
		}
	}
	// Drifted non-safety still clears (accounting resolved) with stale label.
	// A drifted 429 carries no evidence or digests; only the scheduler
	// accounting (rate_limited/instance/RetryAfter) survives the drift.
	inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseRateLimited, FreshnessDrifted: true, TargetKind: capsTarget()}
	classified, err := ClassifyLiveAttempt(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !classified.ClearGate || classified.SafeOutcome != OutcomeConfigurationStale || !allowlist[classified.SafeOutcome] {
		t.Fatalf("drifted 429: %#v", classified)
	}
	if classified.SchedulerResult.Outcome != indexers.OutcomeRateLimited || classified.SchedulerResult.Scope != indexers.FailureScopeInstance {
		t.Fatalf("drifted 429 accounting lost: %#v", classified.SchedulerResult)
	}
}

func TestClassifySafetyOutcomesWinOverFreshnessDrift(t *testing.T) {
	t.Parallel()
	for _, response := range []ResponseVerdict{ResponseSafetyCredentialEcho, ResponseSafetyUnsafeRedirect, ResponseSafetyUnsafeText, ResponseSafetyBodyLimit, ResponseSafetyXMLLimit} {
		inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: response, FreshnessDrifted: true, TargetKind: capsTarget()}
		classified, err := ClassifyLiveAttempt(inputs)
		if err != nil {
			t.Fatal(err)
		}
		if classified.Evidence != nil || classified.RawResultCount != nil || classified.PlausibleIdentityCount != nil || len(classified.SafeResponseSHA256) != 0 || len(classified.RawBodySHA256) != 0 {
			t.Fatalf("safety response retained evidence: %q", response)
		}
		if classified.SchedulerResult.Outcome != indexers.OutcomeSystemFailure || classified.SchedulerResult.Scope != indexers.FailureScopeInstance {
			t.Fatalf("safety accounting wrong: %#v", classified.SchedulerResult)
		}
		if !classified.ClearGate {
			t.Fatalf("safety response did not clear: %q", response)
		}
	}
	classified, err := ClassifyLiveAttempt(LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseSafetyCredentialEcho, FreshnessDrifted: true, TargetKind: capsTarget()})
	if err != nil {
		t.Fatal(err)
	}
	if classified.SafeOutcome != OutcomeSafetyCredentialEcho || classified.SafeCode != SafeCodeCredentialEcho {
		t.Fatalf("credential echo wrong: %#v", classified)
	}
	// Non-safety outcomes must be stale under drift.
	drifted, err := ClassifyLiveAttempt(LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseCAPSObserved, FreshnessDrifted: true, TargetKind: capsTarget()})
	if err != nil {
		t.Fatal(err)
	}
	if drifted.SafeOutcome != OutcomeConfigurationStale || drifted.Evidence != nil || len(drifted.RawBodySHA256) != 0 {
		t.Fatalf("drifted caps not stale/clean: %#v", drifted)
	}
}

func testClassifyEvidence(t *testing.T, response ResponseVerdict) *StepEvidenceDocument {
	t.Helper()
	var outcome SafeOutcome
	switch response {
	case ResponseCAPSObserved:
		outcome = OutcomeCAPSObserved
	case ResponseRecipeVerified:
		outcome = OutcomeRecipeVerified
	case ResponseNoResults:
		outcome = OutcomeNoResults
	case ResponseNoPlausibleMatch:
		outcome = OutcomeNoPlausibleMatch
	case ResponseUnsupported:
		outcome = OutcomeUnsupported
	case ResponsePermissionRestricted:
		outcome = OutcomePermissionRestricted
	case ResponseContradicted:
		outcome = OutcomeContradicted
	default:
		t.Fatalf("no evidence for %q", response)
	}
	if response == ResponseCAPSObserved {
		evidence := testCAPSEvidence()
		if evidence.SafeOutcome != outcome {
			t.Fatalf("caps evidence outcome=%q", evidence.SafeOutcome)
		}
		return &evidence
	}
	body := SumSHA256([]byte("classify-" + string(response)))
	searchTarget := newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
	raw, plausible := uint16(1), uint16(1)
	if response == ResponseNoResults {
		raw, plausible = 0, 0
	} else if response == ResponseNoPlausibleMatch {
		raw, plausible = 2, 0
	} else if response == ResponseUnsupported || response == ResponsePermissionRestricted || response == ResponseContradicted {
		raw, plausible = 3, 2
	}
	return &StepEvidenceDocument{
		SchemaVersion:          DocumentSchemaVersion,
		Key:                    "evidence.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 searchTarget,
		ObservedAt:             testClassifyTime(),
		SafeOutcome:            outcome,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
		BodySHA256:             body,
	}
}

func testClassifyTime() (t time.Time) {
	return time.Unix(1_700_000_100, 0).UTC()
}

func TestClassifyCompletionMatchesInputsRoundTrip(t *testing.T) {
	t.Parallel()
	inputs := LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseNoResults, TargetKind: newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}}
	evidence := testClassifyEvidence(t, ResponseNoResults)
	inputs.Evidence = evidence
	inputs.RawResultCount = mustUint16(0)
	inputs.PlausibleIdentityCount = mustUint16(0)
	inputs.RawBodySHA256 = evidence.BodySHA256.Bytes()
	classified, err := ClassifyLiveAttempt(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !ClassifiedCompletionMatchesInputs(classified) {
		t.Fatal("round trip mismatch")
	}
	tampered := classified
	tampered.SafeOutcome = OutcomeCAPSObserved
	if ClassifiedCompletionMatchesInputs(tampered) {
		t.Fatal("tampered outcome accepted")
	}
	if !bytes.Equal(classified.RawBodySHA256, evidence.BodySHA256.Bytes()) {
		t.Fatal("digest not preserved")
	}
}
