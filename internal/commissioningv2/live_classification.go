package commissioningv2

import (
	"bytes"
	"errors"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

// LiveAttemptInputs is the canonical provider-derived input to completion
// classification. BudgetSnapshot is deliberately absent: scheduler
// configuration is never an input to ClassifyLiveAttempt.
type TransportVerdict string

const (
	TransportResponseReceived   TransportVerdict = "response_received"
	TransportNoCompleteResponse TransportVerdict = "no_complete_response"
)

type ResponseVerdict string

const (
	ResponseCAPSObserved          ResponseVerdict = "caps_observed"
	ResponseRecipeVerified        ResponseVerdict = "recipe_verified"
	ResponseNoResults             ResponseVerdict = "no_results"
	ResponseNoPlausibleMatch      ResponseVerdict = "no_plausible_match"
	ResponseUnsupported           ResponseVerdict = "unsupported"
	ResponsePermissionRestricted  ResponseVerdict = "permission_restricted"
	ResponseContradicted          ResponseVerdict = "contradicted"
	ResponseMalformed             ResponseVerdict = "malformed"
	ResponseSemanticAmbiguity     ResponseVerdict = "semantic_ambiguity"
	ResponseRateLimited           ResponseVerdict = "rate_limited"
	ResponseQuotaExhausted        ResponseVerdict = "quota_exhausted"
	ResponseAuthenticationFailure ResponseVerdict = "authentication_failure"
	ResponseSafetyCredentialEcho  ResponseVerdict = "safety_credential_echo"
	ResponseSafetyUnsafeRedirect  ResponseVerdict = "safety_unsafe_redirect"
	ResponseSafetyUnsafeText      ResponseVerdict = "safety_unsafe_text"
	ResponseSafetyBodyLimit       ResponseVerdict = "safety_body_limit"
	ResponseSafetyXMLLimit        ResponseVerdict = "safety_xml_limit"
)

type TransportFailure string

const (
	TransportFailureDNS        TransportFailure = "dns"
	TransportFailureTLS        TransportFailure = "tls"
	TransportFailureConnection TransportFailure = "connection"
)

type LiveAttemptInputs struct {
	Transport              TransportVerdict
	Response               ResponseVerdict
	Failure                TransportFailure
	FreshnessDrifted       bool
	RetryAfter             time.Time
	TargetKind             newznab.EvidenceTarget
	Evidence               *StepEvidenceDocument
	RawResultCount         *uint16
	PlausibleIdentityCount *uint16
	SafeResponseSHA256     []byte
	RawBodySHA256          []byte
}

// ClassifiedCompletion is the ONLY classification output. ConsecutiveFailure,
// CooldownUntil and NextAllowedAt are ABSENT: they are computed from the
// authoritative starting snapshot, not from classification. BudgetSnapshot is
// likewise absent.
type ClassifiedCompletion struct {
	Inputs LiveAttemptInputs

	SafeOutcome     SafeOutcome
	SafeCode        SafeCode
	SchedulerResult indexers.RecordResult
	ClearGate       bool

	Evidence               *StepEvidenceDocument
	RawResultCount         *uint16
	PlausibleIdentityCount *uint16
	SafeResponseSHA256     []byte
	RawBodySHA256          []byte
}

var errInvalidLiveClassification = errors.New("invalid live attempt classification")

func validResponseVerdict(response ResponseVerdict) bool {
	switch response {
	case ResponseCAPSObserved, ResponseRecipeVerified, ResponseNoResults,
		ResponseNoPlausibleMatch, ResponseUnsupported, ResponsePermissionRestricted,
		ResponseContradicted, ResponseMalformed, ResponseSemanticAmbiguity,
		ResponseRateLimited, ResponseQuotaExhausted, ResponseAuthenticationFailure,
		ResponseSafetyCredentialEcho, ResponseSafetyUnsafeRedirect,
		ResponseSafetyUnsafeText, ResponseSafetyBodyLimit, ResponseSafetyXMLLimit:
		return true
	default:
		return false
	}
}

func safetyResponseOutcome(response ResponseVerdict) (SafeOutcome, bool) {
	switch response {
	case ResponseSafetyCredentialEcho:
		return OutcomeSafetyCredentialEcho, true
	case ResponseSafetyUnsafeRedirect:
		return OutcomeSafetyUnsafeRedirect, true
	case ResponseSafetyUnsafeText:
		return OutcomeSafetyUnsafeText, true
	case ResponseSafetyBodyLimit:
		return OutcomeSafetyBodyLimit, true
	case ResponseSafetyXMLLimit:
		return OutcomeSafetyXMLLimit, true
	default:
		return "", false
	}
}

func safetyResponseCode(outcome SafeOutcome) SafeCode {
	if outcome == OutcomeSafetyCredentialEcho {
		return SafeCodeCredentialEcho
	}
	return ""
}

func optionalUint16Equal(left, right *uint16) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateLiveEvidenceShape(inputs LiveAttemptInputs, outcome SafeOutcome) error {
	hasEvidence := inputs.Evidence != nil
	hasCounts := inputs.RawResultCount != nil && inputs.PlausibleIdentityCount != nil
	hasDigests := len(inputs.SafeResponseSHA256) != 0 || len(inputs.RawBodySHA256) != 0
	switch outcome {
	case OutcomeCAPSObserved, OutcomeRecipeVerified, OutcomeNoResults,
		OutcomeNoPlausibleMatch, OutcomeUnsupported, OutcomePermissionRestricted,
		OutcomeContradicted:
		if inputs.Evidence == nil {
			return errInvalidLiveClassification
		}
		if err := ValidateStepEvidence(*inputs.Evidence); err != nil {
			return errInvalidLiveClassification
		}
		if inputs.Evidence.SafeOutcome != outcome {
			return errInvalidLiveClassification
		}
		if !hasCounts || *inputs.PlausibleIdentityCount > *inputs.RawResultCount {
			return errInvalidLiveClassification
		}
		if outcome == OutcomeRecipeVerified && (*inputs.PlausibleIdentityCount == 0 || *inputs.PlausibleIdentityCount != *inputs.RawResultCount) {
			return errInvalidLiveClassification
		}
		if len(inputs.RawBodySHA256) != 32 || !bytes.Equal(inputs.RawBodySHA256, inputs.Evidence.BodySHA256.Bytes()) {
			return errInvalidLiveClassification
		}
		if inputs.TargetKind != inputs.Evidence.Target {
			return errInvalidLiveClassification
		}
		return nil
	default:
		if hasEvidence || hasCounts || hasDigests {
			return errInvalidLiveClassification
		}
		return nil
	}
}

// ClassifyLiveAttempt is the pure constructor and the ONLY way to obtain a
// ClassifiedCompletion. Precedence: safety_* > FreshnessDrifted > ordinary
// response classification. It can never emit retry_after_deferred,
// credential_unavailable, cancelled, or indeterminate.
func ClassifyLiveAttempt(inputs LiveAttemptInputs) (ClassifiedCompletion, error) {
	if inputs.Transport != TransportResponseReceived && inputs.Transport != TransportNoCompleteResponse {
		return ClassifiedCompletion{}, errInvalidLiveClassification
	}
	if inputs.Transport == TransportResponseReceived {
		if !validResponseVerdict(inputs.Response) || inputs.Failure != "" {
			return ClassifiedCompletion{}, errInvalidLiveClassification
		}
	} else {
		if inputs.Response != "" || inputs.Failure != TransportFailureDNS && inputs.Failure != TransportFailureTLS && inputs.Failure != TransportFailureConnection {
			return ClassifiedCompletion{}, errInvalidLiveClassification
		}
		if inputs.Evidence != nil || inputs.RawResultCount != nil || inputs.PlausibleIdentityCount != nil || len(inputs.SafeResponseSHA256) != 0 || len(inputs.RawBodySHA256) != 0 {
			return ClassifiedCompletion{}, errInvalidLiveClassification
		}
	}
	if !inputs.RetryAfter.IsZero() && inputs.RetryAfter.Location() == nil {
		return ClassifiedCompletion{}, errInvalidLiveClassification
	}
	result := ClassifiedCompletion{Inputs: inputs}
	if inputs.Transport == TransportNoCompleteResponse {
		switch inputs.Failure {
		case TransportFailureDNS:
			result.SafeOutcome = OutcomeDNSFailure
		case TransportFailureTLS:
			result.SafeOutcome = OutcomeTLSFailure
		default:
			result.SafeOutcome = OutcomeProviderConnectionFailure
		}
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance}
		result.ClearGate = false
		if err := validateLiveEvidenceShape(inputs, result.SafeOutcome); err != nil {
			return ClassifiedCompletion{}, err
		}
		return finalizeClassification(result)
	}
	if outcome, ok := safetyResponseOutcome(inputs.Response); ok {
		result.SafeOutcome = outcome
		result.SafeCode = safetyResponseCode(outcome)
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance}
		result.ClearGate = true
		// A drifted safety outcome keeps ClearGate true: safety wins over drift
		// and the gate allowlist contains every safety_* outcome. Drifted 429
		// without a parsed Retry-After is still resolved accounting.
		if inputs.FreshnessDrifted {
			if _, ok := safetyResponseOutcome(inputs.Response); ok {
				result.ClearGate = true
			}
		}
		result.Evidence, result.RawResultCount, result.PlausibleIdentityCount = nil, nil, nil
		result.SafeResponseSHA256, result.RawBodySHA256 = nil, nil
		return finalizeClassification(result)
	}
	if inputs.FreshnessDrifted {
		var outcome indexers.Outcome
		var scope indexers.FailureScope
		var retryAt time.Time
		switch inputs.Response {
		case ResponseRateLimited:
			outcome, scope, retryAt = indexers.OutcomeRateLimited, indexers.FailureScopeInstance, inputs.RetryAfter.UTC()
		case ResponseQuotaExhausted:
			outcome, scope = indexers.OutcomeRateLimited, indexers.FailureScopeInstance
		case ResponseAuthenticationFailure:
			outcome, scope = indexers.OutcomeAuthFailed, indexers.FailureScopeInstance
		case ResponsePermissionRestricted:
			outcome, scope = indexers.OutcomeSystemFailure, indexers.FailureScopeOperation
		case ResponseMalformed:
			outcome, scope = indexers.OutcomeSystemFailure, indexers.FailureScopeInstance
		default:
			outcome, scope = indexers.OutcomeSuccess, indexers.FailureScopeNone
		}
		if inputs.Response == ResponseNoResults {
			outcome = indexers.OutcomeNoResults
		}
		result.SafeOutcome = OutcomeConfigurationStale
		result.SafeCode = SafeCodeConfigurationChanged
		result.SchedulerResult = indexers.RecordResult{Outcome: outcome, Scope: scope, RetryAt: retryAt}
		result.ClearGate = true
		result.Evidence, result.RawResultCount, result.PlausibleIdentityCount = nil, nil, nil
		result.SafeResponseSHA256, result.RawBodySHA256 = nil, nil
		return finalizeClassification(result)
	}
	switch inputs.Response {
	case ResponseCAPSObserved:
		result.SafeOutcome = OutcomeCAPSObserved
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone}
	case ResponseRecipeVerified:
		result.SafeOutcome = OutcomeRecipeVerified
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone}
	case ResponseNoResults:
		result.SafeOutcome = OutcomeNoResults
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeNoResults, Scope: indexers.FailureScopeNone}
	case ResponseNoPlausibleMatch:
		result.SafeOutcome = OutcomeNoPlausibleMatch
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone}
	case ResponseUnsupported:
		result.SafeOutcome = OutcomeUnsupported
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone}
	case ResponsePermissionRestricted:
		result.SafeOutcome = OutcomePermissionRestricted
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeOperation}
	case ResponseContradicted:
		result.SafeOutcome = OutcomeContradicted
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone}
	case ResponseMalformed:
		result.SafeOutcome = OutcomeMalformedResponse
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance}
	case ResponseSemanticAmbiguity:
		result.SafeOutcome = OutcomeSemanticAmbiguity
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone}
	case ResponseRateLimited:
		result.SafeOutcome = OutcomeRateLimited
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance, RetryAt: inputs.RetryAfter.UTC()}
	case ResponseQuotaExhausted:
		result.SafeOutcome = OutcomeQuotaExhausted
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance}
	case ResponseAuthenticationFailure:
		result.SafeOutcome = OutcomeAuthenticationFailure
		result.SchedulerResult = indexers.RecordResult{Outcome: indexers.OutcomeAuthFailed, Scope: indexers.FailureScopeInstance}
	default:
		return ClassifiedCompletion{}, errInvalidLiveClassification
	}
	result.ClearGate = true
	result.Evidence = inputs.Evidence
	result.RawResultCount = inputs.RawResultCount
	result.PlausibleIdentityCount = inputs.PlausibleIdentityCount
	result.SafeResponseSHA256 = append([]byte(nil), inputs.SafeResponseSHA256...)
	if len(result.SafeResponseSHA256) == 0 {
		result.SafeResponseSHA256 = nil
	}
	result.RawBodySHA256 = append([]byte(nil), inputs.RawBodySHA256...)
	if len(result.RawBodySHA256) == 0 {
		result.RawBodySHA256 = nil
	}
	if err := validateLiveEvidenceShape(inputs, result.SafeOutcome); err != nil {
		return ClassifiedCompletion{}, err
	}
	return finalizeClassification(result)
}

func finalizeClassification(result ClassifiedCompletion) (ClassifiedCompletion, error) {
	switch result.SafeOutcome {
	case OutcomeRetryAfterDeferred, OutcomeCredentialUnavailable, OutcomeCancelled, OutcomeIndeterminate:
		return ClassifiedCompletion{}, errInvalidLiveClassification
	}
	if !ValidSafeOutcome(result.SafeOutcome) {
		return ClassifiedCompletion{}, errInvalidLiveClassification
	}
	return result, nil
}

// ClassifiedCompletionMatchesInputs reports whether a submitted completion
// carries exactly the re-derived classification for its retained inputs.
func ClassifiedCompletionMatchesInputs(classified ClassifiedCompletion) bool {
	want, err := ClassifyLiveAttempt(classified.Inputs)
	if err != nil {
		return false
	}
	if want.SafeOutcome != classified.SafeOutcome || want.SafeCode != classified.SafeCode || want.SchedulerResult != classified.SchedulerResult || want.ClearGate != classified.ClearGate {
		return false
	}
	if (want.Evidence == nil) != (classified.Evidence == nil) {
		return false
	}
	if want.Evidence != nil {
		wantDocument, wantErr := CanonicalStepEvidence(*want.Evidence)
		gotDocument, gotErr := CanonicalStepEvidence(*classified.Evidence)
		if wantErr != nil || gotErr != nil || !bytes.Equal(wantDocument, gotDocument) {
			return false
		}
	}
	if !optionalUint16Equal(want.RawResultCount, classified.RawResultCount) || !optionalUint16Equal(want.PlausibleIdentityCount, classified.PlausibleIdentityCount) {
		return false
	}
	if !bytes.Equal(normalizeDigestBytes(want.SafeResponseSHA256), normalizeDigestBytes(classified.SafeResponseSHA256)) || !bytes.Equal(normalizeDigestBytes(want.RawBodySHA256), normalizeDigestBytes(classified.RawBodySHA256)) {
		return false
	}
	return true
}

func normalizeDigestBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return value
}
