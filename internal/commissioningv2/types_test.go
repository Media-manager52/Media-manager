package commissioningv2

import "testing"

func TestClosedRunAndStepTransitions(t *testing.T) {
	t.Parallel()
	validRunPath := []RunState{RunAwaitingCAPSAuthorization, RunCAPSAuthorized, RunAwaitingCategoryReview, RunAwaitingSearchAuthorization, RunSearchesAuthorized, RunReadyForDraft, RunCompleted}
	for index := 1; index < len(validRunPath); index++ {
		if !ValidRunTransition(validRunPath[index-1], validRunPath[index]) {
			t.Fatalf("rejected run transition %s -> %s", validRunPath[index-1], validRunPath[index])
		}
	}
	if ValidRunTransition(RunCompleted, RunAwaitingCAPSAuthorization) || ValidRunTransition("invented", RunCompleted) {
		t.Fatal("accepted invalid or reopened run transition")
	}
	if !ValidRunTransition(RunCAPSAuthorized, RunCancelRequested) || !ValidRunTransition(RunCancelRequested, RunCancelled) {
		t.Fatal("rejected bounded cancellation path")
	}

	if !ValidStepTransition(StepPlanned, StepIntentCommitted) || !ValidStepTransition(StepIntentCommitted, StepTransmitting) || !ValidStepTransition(StepTransmitting, StepCompleted) {
		t.Fatal("rejected valid step path")
	}
	if ValidStepTransition(StepTransmitting, StepPlanned) || ValidStepTransition(StepIndeterminate, StepTransmitting) {
		t.Fatal("accepted replay/reopen transition")
	}
}

func TestSafeOutcomeRegistryIsClosed(t *testing.T) {
	t.Parallel()
	if ValidSafeOutcome("") || ValidSafeOutcome("provider said something") {
		t.Fatal("accepted arbitrary safe outcome")
	}
	for _, outcome := range []SafeOutcome{OutcomeCredentialUnavailable, OutcomeRateLimited, OutcomeRetryAfterDeferred, OutcomeDNSFailure, OutcomeTLSFailure, OutcomeSafetyCredentialEcho, OutcomeSafetyUnsafeRedirect, OutcomeSafetyUnsafeText, OutcomeSafetyBodyLimit, OutcomeSafetyXMLLimit} {
		if !ValidSafeOutcome(outcome) {
			t.Fatalf("required outcome %q missing", outcome)
		}
	}
}

func TestSafeCodeRegistryIsClosed(t *testing.T) {
	t.Parallel()
	for _, code := range []SafeCode{SafeCodeConfigurationChanged, SafeCodeOwnerCancelled, SafeCodeCredentialEcho, SafeCodeIndeterminateAttempt} {
		if !ValidSafeCode(code) {
			t.Fatalf("required safe code %q missing", code)
		}
	}
	for _, code := range []SafeCode{"", "provider.message", "credential.echo.extra"} {
		if ValidSafeCode(code) {
			t.Fatalf("accepted arbitrary safe code %q", code)
		}
	}
}
