package commissioningv2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

// LiveReservation is the closed outcome of ReserveLiveStep.
type LiveReservation struct {
	Granted     bool
	AlreadyHeld bool
	RetryAt     time.Time
}

// LiveReservationStore is the narrow durable reservation surface for one
// owner-authorized commissioning attempt. *postgres.Store implements it via
// its own reserve method; the executor never reserves through the legacy
// scheduler Acquire path.
type LiveReservationStore interface {
	ReserveLiveStep(ctx context.Context, indexerID, stepID string, budget indexers.CommissioningBudgetSnapshot, now time.Time) (LiveReservation, error)
}

const (
	liveExecutionLeaseWindow     = 30 * time.Second
	liveExecutionMaxCredential   = 4096
	liveDraftFinalizationTimeout = 15 * time.Second
)

// safeCodeError carries a closed safe code through the Newznab guard
// sanitizer (which preserves any error implementing SafeCode) without
// exposing provider text.
type safeCodeError struct {
	code  string
	cause error
}

func (err safeCodeError) Error() string    { return err.code }
func (err safeCodeError) Unwrap() error    { return err.cause }
func (err safeCodeError) SafeCode() string { return err.code }

// liveTransmissionGuard adapts the pure exact-secret inspector and a
// cancellation/freshness callback to the Newznab guarded-client interface.
// It never receives or returns provider text.
type liveTransmissionGuard struct {
	credential string
	beforeSend func(ctx context.Context) error
}

func (guard liveTransmissionGuard) BeforeSend(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if guard.beforeSend != nil {
		return guard.beforeSend(ctx)
	}
	return nil
}

func (guard liveTransmissionGuard) InspectSecret(raw []byte, headers http.Header, decodedAttrs [][]byte, decodedTexts [][]byte) error {
	if guard.credential == "" {
		return safeCodeError{code: "commissioning_guard_rejected", cause: ErrInvalidSecretGuardInput}
	}
	materials := make([]ProviderMaterial, 0, 2+len(decodedAttrs)+len(decodedTexts))
	if len(raw) != 0 {
		materials = append(materials, ProviderMaterial{Context: MaterialRaw, Value: raw})
	}
	for name, values := range headers {
		for _, value := range values {
			materials = append(materials, ProviderMaterial{Context: MaterialHeader, Value: []byte(name + ": " + value)})
		}
	}
	for _, attribute := range decodedAttrs {
		materials = append(materials, ProviderMaterial{Context: MaterialXMLDecoded, Value: attribute})
	}
	for _, text := range decodedTexts {
		materials = append(materials, ProviderMaterial{Context: MaterialXMLDecoded, Value: text})
	}
	if err := InspectExactSecret(guard.credential, SecretWireUse{QueryParameter: true}, materials...); err != nil {
		if errors.Is(err, ErrExactSecretDetected) {
			return safeCodeError{code: "commissioning_secret_echo", cause: err}
		}
		return safeCodeError{code: "commissioning_guard_rejected", cause: err}
	}
	return nil
}

func (guard liveTransmissionGuard) InspectStructure([]byte, http.Header) error { return nil }

// BuildLiveCAPSEvidence turns one safety-checked CAPS response into the
// approved exact-target evidence document. It parses only through the
// bounded standard parser and revalidates the closed evidence shape.
func BuildLiveCAPSEvidence(result newznab.CommissioningResult, observedAt time.Time, key string) (StepEvidenceDocument, error) {
	observedAt = observedAt.UTC().Truncate(time.Microsecond)
	if len(result.Body) == 0 || result.BodySHA256 == [32]byte{} {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	snapshot, err := newznab.ParseCaps(result.Body, observedAt)
	if err != nil {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	digest, err := SHA256DigestFromBytes(result.BodySHA256[:])
	if err != nil {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	record := newznab.CAPSRecord{
		BodySHA256:       digest.String(),
		ObservedAt:       observedAt,
		ServerTitle:      snapshot.ServerTitle,
		ServerVersion:    snapshot.ServerVersion,
		DefaultLimit:     snapshot.DefaultLimit,
		MaximumLimit:     snapshot.MaximumLimit,
		Categories:       snapshot.Categories,
		ModeObservations: snapshot.ModeObservations,
	}
	document := StepEvidenceDocument{
		SchemaVersion: DocumentSchemaVersion,
		Key:           key,
		Kind:          newznab.EvidenceKindControlledProbe,
		Target:        newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		ObservedAt:    observedAt,
		SafeOutcome:   OutcomeCAPSObserved,
		BodySHA256:    digest,
		CAPS:          &record,
	}
	if err := ValidateStepEvidence(document); err != nil {
		return StepEvidenceDocument{}, ErrInvalidCommissioningDocument
	}
	return document, nil
}

func newznabSafeCode(err error) string {
	var coded interface{ SafeCode() string }
	if errors.As(err, &coded) {
		return coded.SafeCode()
	}
	return ""
}

// liveCapsAttemptInputs is the closed mapping from one guarded transmission
// result to the canonical classification input. An unclassifiable error must
// never be turned into a success or a resend; the caller closes the attempt
// indeterminate instead.
func liveCapsAttemptInputs(result newznab.CommissioningResult, fetchErr error, observedAt time.Time, evidenceKey string) (LiveAttemptInputs, error) {
	capsTarget := newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}
	if fetchErr == nil {
		evidence, err := BuildLiveCAPSEvidence(result, observedAt, evidenceKey)
		if err != nil {
			return LiveAttemptInputs{}, err
		}
		zero := uint16(0)
		return LiveAttemptInputs{
			Transport:              TransportResponseReceived,
			Response:               ResponseCAPSObserved,
			TargetKind:             capsTarget,
			Evidence:               &evidence,
			RawResultCount:         &zero,
			PlausibleIdentityCount: &zero,
			RawBodySHA256:          result.BodySHA256[:],
		}, nil
	}
	return liveAttemptInputsFromError(fetchErr, capsTarget)
}

// liveAttemptInputsFromError maps one guarded transport failure to the closed
// canonical classification input. An unclassifiable error is returned so the
// caller closes the attempt indeterminate rather than fabricating a verdict.
func liveAttemptInputsFromError(fetchErr error, target newznab.EvidenceTarget) (LiveAttemptInputs, error) {
	code := newznabSafeCode(fetchErr)
	switch code {
	case "commissioning_dns_rejected", "commissioning_address_rejected":
		return LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: TransportFailureDNS, TargetKind: target}, nil
	case "commissioning_tls_rejected", "commissioning_downgrade_rejected":
		return LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: TransportFailureTLS, TargetKind: target}, nil
	case "commissioning_transport_failed", "commissioning_read_failed",
		"commissioning_deadline_missing", "commissioning_deadline_expired",
		"commissioning_invalid_probe", "commissioning_request_rejected", "commissioning_guard_missing",
		"commissioning_http_error":
		return LiveAttemptInputs{Transport: TransportNoCompleteResponse, Failure: TransportFailureConnection, TargetKind: target}, nil
	case "commissioning_rate_limited":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseRateLimited, TargetKind: target}, nil
	case "commissioning_auth_failed":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseAuthenticationFailure, TargetKind: target}, nil
	case "commissioning_redirect_rejected":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseSafetyUnsafeRedirect, TargetKind: target}, nil
	case "commissioning_secret_echo":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseSafetyCredentialEcho, TargetKind: target}, nil
	case "commissioning_guard_rejected":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseSafetyUnsafeText, TargetKind: target}, nil
	case "commissioning_xml_limit":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseSafetyXMLLimit, TargetKind: target}, nil
	case "commissioning_body_limit", "commissioning_header_limit":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseSafetyBodyLimit, TargetKind: target}, nil
	case "commissioning_malformed", "commissioning_trailing_root", "commissioning_content_encoding", "commissioning_media_type":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseMalformed, TargetKind: target}, nil
	}
	if strings.HasPrefix(code, "commissioning_http_") {
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseMalformed, TargetKind: target}, nil
	}
	return LiveAttemptInputs{}, fmt.Errorf("unclassifiable commissioning transport failure")
}

// Execute performs exactly one owner-authorized CAPS attempt and returns only
// after the attempt reached a safe durable state or failed closed. It never
// advances to a second step and never resends.
//
// Owner authorization is mandatory: the loaded step must be durably
// authorized (the run crossed awaiting_caps_authorization ->
// caps_authorized), the phase must be CAPS, and the command must bind the
// exact run/step/revision/fingerprint. A run that merely exists authorizes
// nothing. The one-use Newznab send cell plus the durable transmission
// boundary make replay send zero additional requests.
func (executor *LiveExecutor) Execute(ctx context.Context, command LiveExecuteCommand) (LiveExecuteResult, error) {
	if executor == nil || executor.deps.Steps == nil || executor.deps.Scheduler == nil || executor.deps.Resolver == nil ||
		executor.deps.Client == nil || executor.deps.Reservations == nil || executor.deps.Authorizer == nil {
		return LiveExecuteResult{}, ErrLiveExecutorMisconfigured
	}
	if err := checkPolicyVersion(command.PlannerVersion); err != nil {
		return LiveExecuteResult{}, err
	}
	snapshot, err := executor.deps.Steps.LoadAuthorizedStep(ctx, command.StepID)
	if err != nil {
		return LiveExecuteResult{}, err
	}
	if !snapshot.Authorized {
		return LiveExecuteResult{}, ErrLiveExecutorNotAuthorized
	}
	if snapshot.Phase != StepPhaseCAPS && snapshot.Phase != StepPhaseSearch {
		return LiveExecuteResult{}, ErrLiveExecutorNotAuthorized
	}
	if snapshot.SecretReference == "" {
		return LiveExecuteResult{}, ErrLiveExecutorNotAuthorized
	}
	if command.RunID != snapshot.RunID || command.StepID != snapshot.StepID ||
		command.ExpectedRunRev != snapshot.RunRevision || command.Fingerprint != snapshot.Fingerprint ||
		command.ExpectedStepState != StepPlanned || snapshot.StepState != StepPlanned {
		return LiveExecuteResult{}, ErrLiveExecutorStateConflict
	}
	if command.LeaseOwner == "" {
		command.LeaseOwner = "owner-caps"
	}
	// 1. Durable intent before any scheduler/network work.
	if _, err := executor.ClaimIntent(ctx, command, snapshot); err != nil {
		return LiveExecuteResult{}, fmt.Errorf("claim intent: %w", err)
	}
	// 2. Resolve only the exact snapshotted secret reference.
	credentialBytes, err := executor.deps.Resolver.ResolveCommissioningCredential(ctx, snapshot.IndexerID, snapshot.SecretReference)
	if err != nil || len(credentialBytes) == 0 || len(credentialBytes) > liveExecutionMaxCredential {
		return executor.closeDeferred(ctx, snapshot, OutcomeCredentialUnavailable, nil)
	}
	credential := string(credentialBytes)
	// 3. Shared scheduler permit (WorkloadCommissioning query family).
	permit, err := executor.deps.Scheduler.AcquireCommissioningV2(ctx, snapshot.IndexerID, snapshot.StepID)
	if err != nil {
		return executor.closeDeferred(ctx, snapshot, preSendOutcome(err), preSendRetryAt(err))
	}
	defer permit.ReleaseWithoutOutcome()
	budget := permit.CommissioningBudgetSnapshot()
	// 4. Durable reservation under the shared advisory-lock namespace.
	reservation, err := executor.deps.Reservations.ReserveLiveStep(ctx, snapshot.IndexerID, snapshot.StepID, budget, executor.now())
	if err != nil {
		return executor.closeDeferred(ctx, snapshot, OutcomeQuotaExhausted, nil)
	}
	if !reservation.Granted && !reservation.AlreadyHeld {
		var retryAt *time.Time
		if !reservation.RetryAt.IsZero() {
			value := reservation.RetryAt.UTC()
			retryAt = &value
		}
		return executor.closeDeferred(ctx, snapshot, OutcomeRateLimited, retryAt)
	}
	// 5. Durable transmission boundary. Only a committed boundary permits HTTP.
	boundaryAt := executor.now()
	deadline := boundaryAt.Add(liveExecutionLeaseWindow + 15*time.Second)
	marker := LiveTransmitMarker{
		RunID:             snapshot.RunID,
		StepID:            snapshot.StepID,
		IndexerInstanceID: snapshot.IndexerID,
		ExpectedStepState: StepIntentCommitted,
		ExpectedRunRev:    snapshot.RunRevision,
		LeaseOwner:        command.LeaseOwner,
		ExecutionDeadline: deadline,
		BudgetSnapshot:    budget,
	}
	if _, err := executor.deps.Steps.MarkClaimedStepTransmitting(ctx, marker, boundaryAt); err != nil {
		if errors.Is(err, ErrLiveExecutorCancelled) {
			return executor.closeDeferred(ctx, snapshot, OutcomeQuotaExhausted, nil)
		}
		return executor.closeDeferred(ctx, snapshot, OutcomeQuotaExhausted, nil)
	}
	// 6. Exactly one HTTP attempt through the M-5 one-use binding.
	probe, _, probeErr := materializeCommissioningProbe(snapshot)
	if probeErr != nil {
		return executor.closeDeferred(ctx, snapshot, OutcomeConfigurationStale, nil)
	}
	capability := newznab.NewCommissioningTransmissionFromSnapshot(budget, executor.deps.Authorizer, snapshot.IndexerID, snapshot.StepID)
	guard := liveTransmissionGuard{credential: credential}
	result, fetchErr := executor.deps.Client.FetchCommissioningWithTransmission(ctx, capability,
		newznab.CommissioningTransmitClaim{IndexerID: snapshot.IndexerID, StepID: snapshot.StepID},
		probe, executor.deps.Destination, guard, deadline)
	observedAt := executor.now()
	var inputs LiveAttemptInputs
	var inputErr error
	if snapshot.Phase == StepPhaseSearch {
		inputs, inputErr = classifySearchAttemptInputs(snapshot, result, fetchErr, observedAt, executor.evidenceKey())
	} else {
		inputs, inputErr = liveCapsAttemptInputs(result, fetchErr, observedAt, executor.evidenceKey())
	}
	if inputErr != nil {
		if _, closeErr := executor.closeTransmittedIndeterminate(ctx, snapshot, permit); closeErr != nil {
			return LiveExecuteResult{}, fmt.Errorf("classify after transmit: %w", errors.Join(inputErr, closeErr))
		}
		return LiveExecuteResult{}, fmt.Errorf("classify after transmit: %w", inputErr)
	}
	classified, classifyErr := ClassifyLiveAttemptForTarget(inputs)
	if classifyErr != nil {
		return executor.closeTransmittedIndeterminate(ctx, snapshot, permit)
	}
	// 7. One atomic completion using the accepted outcome machinery.
	completedAt := observedAt.Add(time.Millisecond)
	completion := LiveCompletion{
		RunID:                         snapshot.RunID,
		StepID:                        snapshot.StepID,
		IndexerInstanceID:             snapshot.IndexerID,
		ExpectedRunRev:                snapshot.RunRevision,
		LeaseOwner:                    command.LeaseOwner,
		Classified:                    classified,
		BudgetSnapshot:                budget,
		ExpectedStructuralFingerprint: snapshot.Fingerprint,
		ExpectedTargetKind:            TargetIdentity{Kind: string(snapshot.Target.Kind), RecipeKey: snapshot.Target.RecipeKey},
		ExpectedCategoryIDs:           append([]int(nil), snapshot.CategoryIDs...),
		ApplyRunTransition:            true,
	}
	if _, err := executor.deps.Steps.CompleteLiveAttempt(ctx, completion, completedAt); err != nil {
		// Uncertain finalization: reload authoritative state, never resend.
		closeErr := executor.CloseInterruptedAfterUncertainFinalization(ctx, snapshot.IndexerID, permit, nil)
		if closeErr != nil {
			return LiveExecuteResult{}, fmt.Errorf("uncertain completion: %w", errors.Join(err, closeErr))
		}
		return LiveExecuteResult{}, fmt.Errorf("uncertain completion: %w", err)
	}
	// Known commit: refresh the lane cache while still holding the permit.
	_ = executor.CloseSucceededAfterKnownSuccessfulCompletion(ctx, snapshot.IndexerID, permit, nil)
	// Normal completion path for a semantic search probe: once the durable
	// completion has committed, attempt the local, offline draft
	// finalization transaction. Only a search phase can complete a search
	// plan; the CAPS completion transitions the run to category review and is
	// not draft-eligible. The call reads authoritative durable state and is a
	// no-op unless every authorized search step is now terminal safe. It uses
	// a detached, bounded context so a client disconnect after the durable
	// commit cannot strand an otherwise completed run, and it never resolves
	// a credential, constructs a client, acquires a permit, or sends HTTP. A
	// failure here is best-effort: the committed evidence remains intact and
	// the owner-triggered FinalizeDraft path retries the same local
	// transaction without any probe resend.
	if snapshot.Phase == StepPhaseSearch {
		finalizeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveDraftFinalizationTimeout)
		_, _ = executor.deps.Steps.FinalizeReadyCommissioningDraft(finalizeContext, snapshot.IndexerID, snapshot.RunID, snapshot.RunRevision, completedAt)
		cancel()
	}
	return LiveExecuteResult{State: StepCompleted, SafeOutcome: classified.SafeOutcome, SafeCode: classified.SafeCode, Sent: true}, nil
}

func (executor *LiveExecutor) evidenceKey() string {
	if executor.deps.NewIDs != nil {
		if value := strings.TrimSpace(executor.deps.NewIDs()); value != "" {
			return value
		}
	}
	return newEvidenceKey()
}

// newEvidenceKey mints one bounded local evidence key. It never carries
// provider data; a generation failure yields an empty key that fails the
// closed evidence validator rather than a fabricated identifier.
func newEvidenceKey() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return "evidence." + hex.EncodeToString(raw[:])
}

// preSendOutcome maps a pre-send scheduler/config block to a closed
// deferred_before_send outcome. It never fabricates a success.
func preSendOutcome(err error) SafeOutcome {
	switch {
	case errors.Is(err, ErrLiveExecutorCancelled):
		return OutcomeQuotaExhausted
	case errors.Is(err, indexers.ErrInvalidBudget):
		return OutcomeConfigurationStale
	default:
		var cooldown indexers.CooldownError
		var budgetError indexers.RequestBudgetError
		if errors.As(err, &cooldown) || errors.As(err, &budgetError) {
			return OutcomeRateLimited
		}
		return OutcomeQuotaExhausted
	}
}

func preSendRetryAt(err error) *time.Time {
	var cooldown indexers.CooldownError
	if errors.As(err, &cooldown) {
		value := cooldown.RetryAt().UTC()
		return &value
	}
	var budgetError indexers.RequestBudgetError
	if errors.As(err, &budgetError) {
		value := budgetError.RetryAt().UTC()
		return &value
	}
	return nil
}

func (executor *LiveExecutor) closeDeferred(ctx context.Context, snapshot LiveAuthorizedStep, outcome SafeOutcome, retryAt *time.Time) (LiveExecuteResult, error) {
	state, err := executor.deps.Steps.DeferClaimedStepBeforeSend(ctx, LiveStepDeferral{
		RunID:             snapshot.RunID,
		StepID:            snapshot.StepID,
		IndexerInstanceID: snapshot.IndexerID,
		ExpectedStepState: StepIntentCommitted,
		SafeOutcome:       outcome,
		RetryAt:           retryAt,
	}, executor.now())
	if err != nil {
		return LiveExecuteResult{}, err
	}
	return LiveExecuteResult{State: state.State, SafeOutcome: outcome, RetryAt: retryAt, Sent: false}, nil
}

// closeTransmittedIndeterminate closes a transmitted attempt whose result
// could not be classified. It never resends.
func (executor *LiveExecutor) closeTransmittedIndeterminate(ctx context.Context, snapshot LiveAuthorizedStep, permit *indexers.Permit) (LiveExecuteResult, error) {
	if _, err := executor.deps.Steps.MarkInterruptedAttemptIndeterminate(ctx, LiveRecovery{
		RunID:             snapshot.RunID,
		StepID:            snapshot.StepID,
		IndexerInstanceID: snapshot.IndexerID,
		ExpectedRunRev:    snapshot.RunRevision,
		Cause:             SafeCodeIndeterminateAttempt,
	}, executor.now()); err != nil {
		_ = executor.CloseInterruptedAfterUncertainFinalization(ctx, snapshot.IndexerID, permit, nil)
		return LiveExecuteResult{}, err
	}
	if err := executor.CloseInterruptedAfterUncertainFinalization(ctx, snapshot.IndexerID, permit, nil); err != nil {
		return LiveExecuteResult{}, err
	}
	return LiveExecuteResult{State: StepIndeterminate, SafeOutcome: OutcomeIndeterminate, Sent: true}, nil
}
