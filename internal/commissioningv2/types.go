// Package commissioningv2 contains the inert, pure domain foundation for
// owner-authorized Newznab commissioning. It has no transport, credential
// resolver, worker, HTTP, or profile-activation dependency.
package commissioningv2

import "github.com/media-manager/media-manager/internal/adapters/newznab"

const (
	DocumentSchemaVersion uint16 = 1
	PlannerVersion        uint16 = 1
	MaximumAPICalls       uint8  = 5
	MaximumSearchCalls    uint8  = 4
)

// RunState is the durable owner-driven commissioning lifecycle. Defining the
// closed set here does not cause a run to execute; later repository/service
// milestones must enforce the same transitions.
type RunState string

const (
	RunAwaitingCAPSAuthorization   RunState = "awaiting_caps_authorization"
	RunCAPSAuthorized              RunState = "caps_authorized"
	RunAwaitingCategoryReview      RunState = "awaiting_category_review"
	RunAwaitingSearchAuthorization RunState = "awaiting_search_authorization"
	RunSearchesAuthorized          RunState = "searches_authorized"
	RunReadyForDraft               RunState = "ready_for_draft"
	RunCompleted                   RunState = "completed"
	RunOwnerAttention              RunState = "owner_attention"
	RunCancelRequested             RunState = "cancel_requested"
	RunCancelled                   RunState = "cancelled"
	RunStale                       RunState = "stale"
	RunSafetyBlocked               RunState = "safety_blocked"
)

func ValidRunState(state RunState) bool {
	switch state {
	case RunAwaitingCAPSAuthorization,
		RunCAPSAuthorized,
		RunAwaitingCategoryReview,
		RunAwaitingSearchAuthorization,
		RunSearchesAuthorized,
		RunReadyForDraft,
		RunCompleted,
		RunOwnerAttention,
		RunCancelRequested,
		RunCancelled,
		RunStale,
		RunSafetyBlocked:
		return true
	default:
		return false
	}
}

func TerminalRunState(state RunState) bool {
	switch state {
	case RunCompleted, RunOwnerAttention, RunCancelled, RunStale, RunSafetyBlocked:
		return true
	default:
		return false
	}
}

// ValidRunTransition deliberately exposes only the approved forward lifecycle
// and bounded stop states. It never treats a restart as authorization.
func ValidRunTransition(from, to RunState) bool {
	if !ValidRunState(from) || !ValidRunState(to) || TerminalRunState(from) {
		return false
	}
	if to == RunOwnerAttention || to == RunStale || to == RunSafetyBlocked {
		return true
	}
	if to == RunCancelRequested {
		return from != RunCancelRequested
	}
	if from == RunCancelRequested {
		return to == RunCancelled
	}
	switch from {
	case RunAwaitingCAPSAuthorization:
		return to == RunCAPSAuthorized
	case RunCAPSAuthorized:
		return to == RunAwaitingCategoryReview
	case RunAwaitingCategoryReview:
		return to == RunAwaitingSearchAuthorization
	case RunAwaitingSearchAuthorization:
		return to == RunSearchesAuthorized
	case RunSearchesAuthorized:
		return to == RunReadyForDraft
	case RunReadyForDraft:
		return to == RunCompleted
	default:
		return false
	}
}

type StepPhase string

const (
	StepPhaseCAPS   StepPhase = "caps"
	StepPhaseSearch StepPhase = "search"
)

func ValidStepPhase(phase StepPhase) bool {
	return phase == StepPhaseCAPS || phase == StepPhaseSearch
}

type StepState string

const (
	StepPlanned            StepState = "planned"
	StepIntentCommitted    StepState = "intent_committed"
	StepTransmitting       StepState = "transmitting"
	StepCompleted          StepState = "completed"
	StepDeferredBeforeSend StepState = "deferred_before_send"
	StepIndeterminate      StepState = "indeterminate"
	StepCancelled          StepState = "cancelled"
)

func ValidStepState(state StepState) bool {
	switch state {
	case StepPlanned, StepIntentCommitted, StepTransmitting, StepCompleted,
		StepDeferredBeforeSend, StepIndeterminate, StepCancelled:
		return true
	default:
		return false
	}
}

func TerminalStepState(state StepState) bool {
	switch state {
	case StepCompleted, StepDeferredBeforeSend, StepIndeterminate, StepCancelled:
		return true
	default:
		return false
	}
}

func ValidStepTransition(from, to StepState) bool {
	if !ValidStepState(from) || !ValidStepState(to) || TerminalStepState(from) {
		return false
	}
	switch from {
	case StepPlanned:
		return to == StepIntentCommitted || to == StepCancelled
	case StepIntentCommitted:
		return to == StepTransmitting || to == StepDeferredBeforeSend || to == StepCancelled
	case StepTransmitting:
		return to == StepCompleted || to == StepIndeterminate
	default:
		return false
	}
}

type SafeOutcome string

const (
	OutcomeCAPSObserved              SafeOutcome = "caps_observed"
	OutcomeRecipeVerified            SafeOutcome = "recipe_verified"
	OutcomeNoResults                 SafeOutcome = "no_results"
	OutcomeNoPlausibleMatch          SafeOutcome = "no_plausible_match"
	OutcomeUnsupported               SafeOutcome = "unsupported"
	OutcomePermissionRestricted      SafeOutcome = "permission_restricted"
	OutcomeContradicted              SafeOutcome = "contradicted"
	OutcomeCredentialUnavailable     SafeOutcome = "credential_unavailable"
	OutcomeAuthenticationFailure     SafeOutcome = "authentication_failure"
	OutcomeQuotaExhausted            SafeOutcome = "quota_exhausted"
	OutcomeRateLimited               SafeOutcome = "rate_limited"
	OutcomeRetryAfterDeferred        SafeOutcome = "retry_after_deferred"
	OutcomeDNSFailure                SafeOutcome = "dns_failure"
	OutcomeTLSFailure                SafeOutcome = "tls_failure"
	OutcomeProviderConnectionFailure SafeOutcome = "provider_connection_failure"
	OutcomeMalformedResponse         SafeOutcome = "malformed_response"
	OutcomeSemanticAmbiguity         SafeOutcome = "semantic_ambiguity"
	OutcomeSafetyCredentialEcho      SafeOutcome = "safety_credential_echo"
	OutcomeSafetyUnsafeRedirect      SafeOutcome = "safety_unsafe_redirect"
	OutcomeSafetyUnsafeText          SafeOutcome = "safety_unsafe_text"
	OutcomeSafetyBodyLimit           SafeOutcome = "safety_body_limit"
	OutcomeSafetyXMLLimit            SafeOutcome = "safety_xml_limit"
	OutcomeConfigurationStale        SafeOutcome = "configuration_stale"
	OutcomeIndeterminate             SafeOutcome = "indeterminate"
	OutcomeCancelled                 SafeOutcome = "cancelled"
)

func ValidSafeOutcome(outcome SafeOutcome) bool {
	switch outcome {
	case OutcomeCAPSObserved,
		OutcomeRecipeVerified,
		OutcomeNoResults,
		OutcomeNoPlausibleMatch,
		OutcomeUnsupported,
		OutcomePermissionRestricted,
		OutcomeContradicted,
		OutcomeCredentialUnavailable,
		OutcomeAuthenticationFailure,
		OutcomeQuotaExhausted,
		OutcomeRateLimited,
		OutcomeRetryAfterDeferred,
		OutcomeDNSFailure,
		OutcomeTLSFailure,
		OutcomeProviderConnectionFailure,
		OutcomeMalformedResponse,
		OutcomeSemanticAmbiguity,
		OutcomeSafetyCredentialEcho,
		OutcomeSafetyUnsafeRedirect,
		OutcomeSafetyUnsafeText,
		OutcomeSafetyBodyLimit,
		OutcomeSafetyXMLLimit,
		OutcomeConfigurationStale,
		OutcomeIndeterminate,
		OutcomeCancelled:
		return true
	default:
		return false
	}
}

// SafeCode is a small owner-facing reason code. Provider error text and other
// arbitrary strings cannot enter this field; new reasons require reviewed Go
// and SQL changes together.
type SafeCode string

const (
	SafeCodeConfigurationChanged SafeCode = "configuration.changed"
	SafeCodeOwnerCancelled       SafeCode = "owner.cancelled"
	SafeCodeCredentialEcho       SafeCode = "credential.echo"
	SafeCodeIndeterminateAttempt SafeCode = "indeterminate.attempt"
)

func ValidSafeCode(code SafeCode) bool {
	switch code {
	case SafeCodeConfigurationChanged, SafeCodeOwnerCancelled, SafeCodeCredentialEcho, SafeCodeIndeterminateAttempt:
		return true
	default:
		return false
	}
}

type OutputFormat string

const OutputXML OutputFormat = "xml"

type PaginationMode string

const PaginationNone PaginationMode = "none"

type BudgetFamily string

const BudgetQuery BudgetFamily = "query"

type ProbeReason string

const (
	ReasonObserveCAPS                  ProbeReason = "observe_caps"
	ReasonStrongestAvailableIdentity   ProbeReason = "strongest_available_identity"
	ReasonOwnerApprovedGeneralFallback ProbeReason = "owner_approved_general_fallback"
)

type CAPSRecipeBasis string

const (
	CAPSBasisNotApplicable    CAPSRecipeBasis = "not_applicable"
	CAPSBasisPresentAvailable CAPSRecipeBasis = "present_available"
	CAPSBasisOmittedGeneric   CAPSRecipeBasis = "omitted_generic_candidate"
)

// TypedRequest describes only the credential-free structural request. It
// intentionally excludes concrete fixture values, endpoint data, paths,
// headers, credentials, and query strings.
type TypedRequest struct {
	Operation    newznab.Operation         `json:"operation"`
	Mode         newznab.SearchMode        `json:"mode,omitempty"`
	Query        newznab.QuerySpec         `json:"query"`
	Bindings     []newznab.WireBinding     `json:"bindings,omitempty"`
	Extended     bool                      `json:"extended"`
	CategorySet  newznab.CategorySetRef    `json:"category_set"`
	Output       OutputFormat              `json:"output"`
	Pagination   PaginationMode            `json:"pagination"`
	RequestLimit uint16                    `json:"request_limit,omitempty"`
	Role         newznab.RecipeRole        `json:"role,omitempty"`
	FallbackOn   []newznab.FallbackTrigger `json:"fallback_on,omitempty"`
}
