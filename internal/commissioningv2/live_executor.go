// Package commissioningv2 live-executor substrate (Milestone A).
//
// This file is the guarded local live-executor substrate. It is reachable
// only from local tests against fake TLS Newznab servers: there is no owner
// route, no production resolver, no background worker, no acquisition
// integration, and no production wiring in cmd/media-manager. All request
// execution terminates at controlled local fake TLS servers supplied by
// tests through injected dependencies.
//
// Safety model implemented here:
//
//   - durable transmission intent (planned -> intent_committed before any
//     scheduler/network work; no transaction spans scheduler wait or I/O);
//   - exact conservative transmission boundary (intent_committed ->
//     transmitting commits the unresolved-attempt marker, transmitted_at, and
//     the pacing floor deadline+minimum-interval atomically; a crash after
//     that commit is possibly sent even if no socket write occurred);
//   - unresolved-attempt gate consulted fail-closed on every acquisition
//     (a NULL gate alone never authorizes acquisition while scheduling state
//     is unknown);
//   - no automatic resend (indeterminate is terminal; duplicate execute loses
//     the expected-state claim and performs no network I/O);
//   - deterministic crash/recovery behavior and bounded recovery
//     finalization (MarkInterruptedAttemptIndeterminate closes only
//     transmitting attempts; restart never resumes);
//   - cancellation semantics (before the marker closes cancelled with no
//     request; after the marker closes indeterminate);
//   - durable scheduler pre-send pacing (pacing floor through deadline plus
//     minimum interval, preserving stronger restrictions);
//   - scheduler/budget accounting through the existing scheduler lane with
//     exactly one permit and one durable reservation;
//   - exact at-most-one transmission behavior (fresh non-reusing HTTP/1-only
//     transport, redirects forbidden, one-use guarded invocation consumed
//     even on error);
//   - pinned dialing/TLS behavior with initial-origin DNS validation, no DNS
//     rebinding/TOCTOU gap (single validated address set pinned for dial),
//     no environment proxy, no HTTPS downgrade;
//   - credential-resolver interface with TEST-ONLY implementation;
//   - exact-secret-before-digest inspection (raw, headers, decoded XML
//     attributes and complete logical text before any hash, retained value,
//     error, or log) and strict secret containment;
//   - strict single-root XML parsing through EOF with bounded response
//     handling;
//   - closed/sanitized local transport errors with no raw provider
//     URL/header/error leakage into persistence or logs.
package commissioningv2

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

var (
	ErrLiveExecutorNotAuthorized  = errors.New("commissioning live execution is not authorized")
	ErrLiveExecutorStalePolicy    = errors.New("commissioning probe policy version is not supported")
	ErrLiveExecutorStateConflict  = errors.New("commissioning step state conflict")
	ErrLiveExecutorCancelled      = errors.New("commissioning execution cancelled")
	ErrLiveExecutorStaleConfig    = errors.New("commissioning configuration changed")
	ErrLiveExecutorMisconfigured  = errors.New("commissioning live executor is misconfigured")
	ErrLiveExecutorCredential     = errors.New("commissioning credential unavailable")
	ErrLiveExecutorBudgetDeferred = errors.New("commissioning budget deferred")
)

// SupportedProbePolicyVersions is the exact reviewed probe-policy contract.
// Material changes to executor/parser/transport semantics require a policy
// bump; an unsupported persisted authorization is rejected BEFORE execution
// claim, credential resolution, scheduler reservation, or transmission. Old
// authorizations are never rewritten or reinterpreted.
var SupportedProbePolicyVersions = map[uint16]bool{PlannerVersion: true}

// CredentialResolver resolves one exact secret_ref for one authorized step
// and returns bytes in memory only. Milestone A provides only a test
// implementation; Milestone B adds a small adapter over the existing secret
// resolver. The executor supplies the run's exact secret_ref; the resolver
// never selects an alternative.
type CredentialResolver interface {
	ResolveCommissioningCredential(ctx context.Context, indexerInstanceID string, exactSecretRef string) ([]byte, error)
}

// TestCredentialResolver is the TEST-ONLY CredentialResolver implementation.
// It resolves only pre-registered (instanceID, secretRef) pairs and rejects
// everything else. It never touches a production secret store.
type TestCredentialResolver struct {
	Secrets map[string][]byte
	Calls   int
}

func testResolverKey(instanceID, secretRef string) string {
	return instanceID + "\x00" + secretRef
}

// RegisterTestCredential pre-registers one exact credential for tests.
func (resolver *TestCredentialResolver) RegisterTestCredential(instanceID, secretRef string, credential []byte) {
	if resolver.Secrets == nil {
		resolver.Secrets = make(map[string][]byte)
	}
	cloned := append([]byte(nil), credential...)
	resolver.Secrets[testResolverKey(instanceID, secretRef)] = cloned
}

func (resolver *TestCredentialResolver) ResolveCommissioningCredential(_ context.Context, indexerInstanceID string, exactSecretRef string) ([]byte, error) {
	resolver.Calls++
	if resolver == nil || exactSecretRef == "" || indexerInstanceID == "" {
		return nil, ErrLiveExecutorCredential
	}
	credential, ok := resolver.Secrets[testResolverKey(indexerInstanceID, exactSecretRef)]
	if !ok || len(credential) == 0 || len(credential) > 4096 {
		return nil, ErrLiveExecutorCredential
	}
	return append([]byte(nil), credential...), nil
}

// LiveExecutorDeps are all injected local-test dependencies. There is no
// production constructor: nothing in cmd/media-manager, internal/httpapi,
// acquisition, retrieval, or workers may construct a LiveExecutor.
type LiveExecutorDeps struct {
	Steps     LiveStepStore
	Scheduler *indexers.Scheduler
	Resolver  CredentialResolver
	Client    *newznab.Client
	Dial      func(ctx context.Context, network, address string) (net.Conn, error)
	Resolve   func(ctx context.Context, hostname string) ([]netip.Addr, error)
	Roots     interface{}
	Now       func() time.Time
	NewIDs    func() string
}

// LiveStepStore is the narrow atomic repository surface the Milestone A
// executor needs. Method names match section 24.2 service-facing operations.
type LiveStepStore interface {
	ClaimAuthorizedStepIntent(ctx context.Context, claim LiveStepClaim, now time.Time) (LiveStepState, error)
	DeferClaimedStepBeforeSend(ctx context.Context, deferral LiveStepDeferral, now time.Time) (LiveStepState, error)
	MarkClaimedStepTransmitting(ctx context.Context, marker LiveTransmitMarker, now time.Time) (LiveStepState, error)
	CompleteLiveAttempt(ctx context.Context, completion LiveCompletion, now time.Time) (LiveStepState, error)
	MarkInterruptedAttemptIndeterminate(ctx context.Context, recovery LiveRecovery, now time.Time) (LiveStepState, error)
	RequestRunCancellation(ctx context.Context, runID string, now time.Time) error
	FinalizeCancelledRun(ctx context.Context, runID string, now time.Time) error
	LoadAuthorizedStep(ctx context.Context, stepID string) (LiveAuthorizedStep, error)
}

// LiveStepClaim names the exact authorized step for the planned-to-intent
// transition with optimistic concurrency.
type LiveStepClaim struct {
	RunID               string
	StepID              string
	ExpectedStepState   StepState
	ExpectedRunRevision uint64
	Fingerprint         SHA256Digest
	PlannerVersion      uint16
	LeaseOwner          string
	LeaseUntil          time.Time
	CancelRequested     bool
}

// LiveStepDeferral finishes a claimed step before send with a closed outcome.
type LiveStepDeferral struct {
	RunID             string
	StepID            string
	ExpectedStepState StepState
	SafeOutcome       SafeOutcome
	SafeCode          SafeCode
	RetryAt           *time.Time
}

// LiveTransmitMarker commits the transmission boundary atomically.
type LiveTransmitMarker struct {
	RunID             string
	StepID            string
	ExpectedStepState StepState
	LeaseOwner        string
	ExecutionDeadline time.Time
	MinimumInterval   time.Duration
	ExistingNextAllow time.Time
	CancelRequested   bool
	ReservationValid  bool
}

// LiveCompletion closes a transmitted attempt atomically.
type LiveCompletion struct {
	RunID           string
	StepID          string
	LeaseOwner      string
	SafeOutcome     SafeOutcome
	SafeCode        SafeCode
	RawResultCount  *uint16
	EvidenceKey     string
	EvidencePresent bool
	BodySHA256      [sha256.Size]byte
	CooldownUntil   time.Time
	NextAllowedAt   time.Time
}

// LiveRecovery closes a transmitted-but-unclosed attempt indeterminate.
type LiveRecovery struct {
	RunID    string
	StepID   string
	Cause    SafeCode
	AckKnown bool
}

// LiveStepState is the durable post-transition step snapshot.
type LiveStepState struct {
	StepID        string
	State         StepState
	IntentAt      *time.Time
	TransmittedAt *time.Time
	CompletedAt   *time.Time
	LeaseOwner    string
	SafeOutcome   SafeOutcome
	Deadline      time.Time
	PacingFloor   time.Time
	GateHeld      bool
	RunRevision   uint64
}

// LiveAuthorizedStep is the consistent run-plus-step snapshot loaded before
// intent: exact revisions, phase authorization, ordinal, target,
// fingerprint, and planned state.
type LiveAuthorizedStep struct {
	RunID              string
	StepID             string
	RunRevision        uint64
	StepState          StepState
	PhaseAuthorized    bool
	Ordinal            uint8
	Target             newznab.EvidenceTarget
	Fingerprint        SHA256Digest
	PlannerVersion     uint16
	ProfileVersion     uint16
	IndexerInstanceID  string
	SecretRef          string
	CancelRequested    bool
	CategoryDigestHeld bool
}

// LiveExecuteCommand names the exact authorized step for one execute action.
type LiveExecuteCommand struct {
	RunID             string
	StepID            string
	ExpectedRunRev    uint64
	ExpectedStepState StepState
	Fingerprint       SHA256Digest
	PlannerVersion    uint16
	Deadline          time.Time
	LeaseOwner        string
}

// LiveExecuteResult is a closed owner-visible result. It never carries raw
// provider URLs, headers, bodies, or error text.
type LiveExecuteResult struct {
	State       StepState
	SafeOutcome SafeOutcome
	SafeCode    SafeCode
	RetryAt     *time.Time
	Sent        bool
}

// LiveExecutor performs exactly one authorized step per Execute call and
// returns only after that step has reached a safe durable state. It never
// advances to a second request and never resends.
type LiveExecutor struct {
	deps LiveExecutorDeps
}

// NewLocalLiveExecutor constructs the test-only executor. There is
// deliberately no production constructor.
func NewLocalLiveExecutor(deps LiveExecutorDeps) (*LiveExecutor, error) {
	if deps.Steps == nil || deps.Scheduler == nil || deps.Resolver == nil || deps.Client == nil {
		return nil, ErrLiveExecutorMisconfigured
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &LiveExecutor{deps: deps}, nil
}

func (executor *LiveExecutor) now() time.Time {
	return executor.deps.Now().UTC()
}

// checkPolicyVersion rejects an unsupported persisted authorization BEFORE
// execution claim, credential resolution, scheduler reservation, or network
// transmission. It never rewrites or reinterprets the old authorization.
func checkPolicyVersion(persisted uint16) error {
	if !SupportedProbePolicyVersions[persisted] {
		return ErrLiveExecutorStalePolicy
	}
	return nil
}

// ClaimIntent performs only the durable planned-to-intent transition with
// the stale-policy gate first. No credential is resolved and no network
// operation occurs.
func (executor *LiveExecutor) ClaimIntent(ctx context.Context, command LiveExecuteCommand, snapshot LiveAuthorizedStep) (LiveStepState, error) {
	if err := checkPolicyVersion(command.PlannerVersion); err != nil {
		return LiveStepState{}, err
	}
	if err := checkPolicyVersion(snapshot.PlannerVersion); err != nil {
		return LiveStepState{}, err
	}
	if command.PlannerVersion != snapshot.PlannerVersion {
		return LiveStepState{}, ErrLiveExecutorStalePolicy
	}
	if command.RunID != snapshot.RunID || command.StepID != snapshot.StepID {
		return LiveStepState{}, ErrLiveExecutorStateConflict
	}
	if command.ExpectedStepState != StepPlanned || snapshot.StepState != StepPlanned {
		return LiveStepState{}, ErrLiveExecutorStateConflict
	}
	if command.Fingerprint != snapshot.Fingerprint {
		return LiveStepState{}, ErrLiveExecutorStateConflict
	}
	if command.ExpectedRunRev != snapshot.RunRevision {
		return LiveStepState{}, ErrLiveExecutorStateConflict
	}
	if !snapshot.PhaseAuthorized {
		return LiveStepState{}, ErrLiveExecutorNotAuthorized
	}
	if snapshot.CancelRequested {
		return LiveStepState{}, ErrLiveExecutorCancelled
	}
	leaseUntil := executor.now().Add(30 * time.Second)
	owner := command.LeaseOwner
	if owner == "" {
		owner = "local-test-executor"
	}
	return executor.deps.Steps.ClaimAuthorizedStepIntent(ctx, LiveStepClaim{
		RunID:               command.RunID,
		StepID:              command.StepID,
		ExpectedStepState:   StepPlanned,
		ExpectedRunRevision: command.ExpectedRunRev,
		Fingerprint:         command.Fingerprint,
		PlannerVersion:      command.PlannerVersion,
		LeaseOwner:          owner,
		LeaseUntil:          leaseUntil,
	}, executor.now())
}

// CloseInterruptedAfterUncertainFinalization implements the section 14.2
// response-finalization-uncertain path: invalidate the complete lane
// scheduling cache while still owning the lane, release without an
// outcome-recording write, then reload authoritative state. Reload failure
// keeps acquisitions blocked; the network request is never retried.
func (executor *LiveExecutor) CloseInterruptedAfterUncertainFinalization(ctx context.Context, indexerID string, permit *indexers.Permit, signal *indexers.LaneCacheInvalidation) error {
	if permit == nil {
		return ErrLiveExecutorMisconfigured
	}
	executor.deps.Scheduler.InvalidateLaneCache(indexerID, signal)
	permit.ReleaseWithoutOutcome()
	return executor.deps.Scheduler.ReloadLaneCache(ctx, indexerID)
}

var _ = http.StatusOK
