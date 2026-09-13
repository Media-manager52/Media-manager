package postgres

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"uuid"
)

var (
	ErrCommissioningReservationStale = errors.New("commissioning reservation is stale")
	ErrCommissioningGateUnresolved   = errors.New("unresolved commissioning gate")
	ErrLiveExecutorCancelled         = errors.New("commissioning execution cancelled")
)

// CommissioningReservation is the ReserveCommissioningRequest outcome. Granted
// means a new reservation unit was consumed; AlreadyHeld means this step's
// reservation already existed in window; RetryAt is non-zero only when
// deferred by the shared limit.
type CommissioningReservation struct {
	Granted     bool
	AlreadyHeld bool
	RetryAt     time.Time
}

func canonicalBoundaryIdentity(value string) (uuid.UUID, string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, "", err
	}
	return parsed, parsed.String(), nil
}

// reservationLockKey derives the single shared advisory-lock namespace for
// the indexer request-budget ledger. UUID-shaped indexer IDs are
// canonicalized through parsed.String() so equivalent textual spellings
// (uppercase, URN, braces) map to the same lock; non-UUID IDs keep the
// existing literal spelling so legacy behavior is preserved.
func reservationLockKey(indexerID string) string {
	if parsed, err := uuid.Parse(indexerID); err == nil {
		return "indexer-request-budget:" + parsed.String()
	}
	return "indexer-request-budget:" + indexerID
}

// ReservationLockKeyForTest exposes the shared lock-key derivation for
// focused unit coverage without a database.
func ReservationLockKeyForTest(indexerID string) string { return reservationLockKey(indexerID) }

func checkBoundarySnapshot(budget indexers.CommissioningBudgetSnapshot, indexerID, stepID string) (string, string, error) {
	if !budget.Valid() {
		return "", "", ErrInvalidIndexerCommissioning
	}
	indexerParsed, indexerCanonical, err := canonicalBoundaryIdentity(indexerID)
	if err != nil {
		return "", "", ErrInvalidIndexerCommissioning
	}
	_ = indexerParsed
	stepParsed, stepCanonical, err := canonicalBoundaryIdentity(stepID)
	if err != nil {
		return "", "", ErrInvalidIndexerCommissioning
	}
	_ = stepParsed
	if !budget.MatchesAttempt(indexerCanonical, stepCanonical) {
		return "", "", ErrInvalidIndexerCommissioning
	}
	return indexerCanonical, stepCanonical, nil
}

// ReserveCommissioningRequest reserves one shared query-family unit for the
// exact commissioning step, using the step UUID as reservation identity. It
// uses the SAME advisory-lock key and the SAME indexer_request_reservations
// ledger as legacy ReserveIndexerRequest, so legacy and v2 query-family
// reservations serialize and share the window. It is idempotent for the same
// step and consumes no second unit on retry. AcquireCommissioningV2 itself
// takes NO reservation. limit/window derive from the opaque permit-bound
// snapshot; no second pacing/reservation system is created.
func (store *Store) ReserveCommissioningRequest(ctx context.Context, indexerID, stepID string, budget indexers.CommissioningBudgetSnapshot, now time.Time) (CommissioningReservation, error) {
	canonicalIndexer, canonicalStep, err := checkBoundarySnapshot(budget, indexerID, stepID)
	if err != nil {
		return CommissioningReservation{}, indexers.ErrInvalidBudget
	}
	limit := budget.QueryLimit()
	window := budget.LimitWindow()
	if limit <= 0 || window <= 0 {
		return CommissioningReservation{}, indexers.ErrInvalidBudget
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return CommissioningReservation{}, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, reservationLockKey(canonicalIndexer)); err != nil {
		return CommissioningReservation{}, err
	}
	windowStart := now.UTC().Add(-window)
	if _, err := transaction.Exec(ctx, `DELETE FROM indexer_request_reservations WHERE id IN (SELECT id FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND reserved_at <= $2 ORDER BY reserved_at LIMIT 1000)`, canonicalIndexer, windowStart); err != nil {
		return CommissioningReservation{}, err
	}
	var existingIndexer, existingFamily *string
	var existingAt *time.Time
	if err := transaction.QueryRow(ctx, `SELECT indexer_instance_id::text,request_family,reserved_at FROM indexer_request_reservations WHERE id=$1`, canonicalStep).Scan(&existingIndexer, &existingFamily, &existingAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommissioningReservation{}, err
	}
	if existingAt != nil {
		if existingIndexer != nil && *existingIndexer == canonicalIndexer && existingFamily != nil && *existingFamily == "query" && existingAt.After(windowStart) {
			if err := transaction.Commit(ctx); err != nil {
				return CommissioningReservation{}, err
			}
			return CommissioningReservation{AlreadyHeld: true}, nil
		}
		return CommissioningReservation{}, ErrCommissioningReservationStale
	}
	var used int64
	var oldest *time.Time
	if err := transaction.QueryRow(ctx, `SELECT count(*),min(reserved_at) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family=$2 AND reserved_at > $3`, canonicalIndexer, "query", windowStart).Scan(&used, &oldest); err != nil {
		return CommissioningReservation{}, err
	}
	if used >= int64(limit) {
		retryAt := now.UTC().Add(window)
		if oldest != nil {
			retryAt = oldest.UTC().Add(window)
		}
		if err := transaction.Commit(ctx); err != nil {
			return CommissioningReservation{}, err
		}
		return CommissioningReservation{RetryAt: retryAt}, nil
	}
	command, err := transaction.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'query',$3) ON CONFLICT (id) DO NOTHING`, canonicalStep, canonicalIndexer, now.UTC())
	if err != nil {
		return CommissioningReservation{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return CommissioningReservation{}, err
	}
	if command.RowsAffected() == 1 {
		return CommissioningReservation{Granted: true}, nil
	}
	return CommissioningReservation{AlreadyHeld: true}, nil
}

// ClaimAuthorizedStepIntent moves planned -> intent_committed with a bounded
// executor lease. Cancellation is re-read from the locked run; no
// caller-supplied cancellation field exists.
func (store *Store) ClaimAuthorizedStepIntent(ctx context.Context, claim commissioningv2.LiveStepClaim, now time.Time) (commissioningv2.LiveStepState, error) {
	if claim.RunID == "" || claim.StepID == "" || claim.IndexerInstanceID == "" || claim.ExpectedStepState != commissioningv2.StepPlanned || claim.LeaseOwner == "" || !capabilitySafeKeyPattern.MatchString(claim.LeaseOwner) || claim.LeaseUntil.IsZero() {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if err := checkLivePolicyVersion(claim.PlannerVersion); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	runParsed, err := uuid.Parse(claim.RunID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	stepParsed, err := uuid.Parse(claim.StepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	indexerParsed, err := uuid.Parse(claim.IndexerInstanceID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	leaseUntil := claim.LeaseUntil.UTC().Truncate(time.Microsecond)
	if !leaseUntil.After(now) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerParsed, runParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	step, err := lockIndexerCommissioningStep(ctx, transaction, runParsed, stepParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if step.State != commissioningv2.StepPlanned {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if run.Revision != claim.ExpectedRunRevision {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if run.CancelRequestedAt != nil || run.State == commissioningv2.RunCancelRequested {
		return commissioningv2.LiveStepState{}, ErrLiveExecutorCancelled
	}
	if !commissioningRunConfigurationCurrent(configuration, digest, run) {
		return commissioningv2.LiveStepState{}, ErrIndexerCommissioningConfigChanged
	}
	if !fingerprintMatchesStep(claim.Fingerprint, step) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if run.PlannerVersion != claim.PlannerVersion {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if step.Phase == commissioningv2.StepPhaseCAPS && run.State != commissioningv2.RunCAPSAuthorized || step.Phase == commissioningv2.StepPhaseSearch && run.State != commissioningv2.RunSearchesAuthorized {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningStepIsNext(run, step) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='intent_committed',intent_at=$3,
		executor_lease_owner=$4,executor_lease_until=$5
		WHERE id=$1 AND run_id=$2 AND state='planned'`, stepParsed.String(), runParsed.String(), now, claim.LeaseOwner, leaseUntil)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	intentAt := now
	return commissioningv2.LiveStepState{StepID: stepParsed.String(), State: commissioningv2.StepIntentCommitted, IntentAt: &intentAt, LeaseOwner: claim.LeaseOwner, RunRevision: run.Revision}, nil
}

func checkLivePolicyVersion(persisted uint16) error {
	if !commissioningv2.SupportedProbePolicyVersions[persisted] {
		return ErrInvalidIndexerCommissioning
	}
	return nil
}

func fingerprintMatchesStep(fingerprint commissioningv2.SHA256Digest, step IndexerCommissioningStep) bool {
	if fingerprint.IsZero() || len(step.StructuralRequestSHA256) != 32 {
		return false
	}
	return bytes.Equal(fingerprint.Bytes(), step.StructuralRequestSHA256)
}

// DeferClaimedStepBeforeSend finishes a claimed step before send with a closed
// pre-send outcome. No gate is created and no transmission occurs.
func (store *Store) DeferClaimedStepBeforeSend(ctx context.Context, deferral commissioningv2.LiveStepDeferral, now time.Time) (commissioningv2.LiveStepState, error) {
	runParsed, err := uuid.Parse(deferral.RunID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	stepParsed, err := uuid.Parse(deferral.StepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	indexerParsed, err := uuid.Parse(deferral.IndexerInstanceID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if deferral.ExpectedStepState != commissioningv2.StepIntentCommitted {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !validLiveDeferralOutcome(deferral.SafeOutcome) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	if _, _, err := lockCommissioningIntegration(ctx, transaction, indexerParsed); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerParsed, runParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	step, err := lockIndexerCommissioningStep(ctx, transaction, runParsed, stepParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if step.State != commissioningv2.StepIntentCommitted {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if !commissioningStepSafeCodeMatches(deferral.SafeOutcome, deferral.SafeCode) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	retryAt := normalizeCommissioningTimePointer(deferral.RetryAt)
	if (retryAt == nil) != (deferral.RetryAt == nil) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='deferred_before_send',completed_at=$3,
		executor_lease_owner=NULL,executor_lease_until=NULL,
		safe_outcome=$4,safe_code=NULLIF($5,''),retry_at=$6
		WHERE id=$1 AND run_id=$2 AND state='intent_committed'`,
		stepParsed.String(), runParsed.String(), now, string(deferral.SafeOutcome), string(deferral.SafeCode), retryAt)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	completedAt := now
	_ = run
	return commissioningv2.LiveStepState{StepID: stepParsed.String(), State: commissioningv2.StepDeferredBeforeSend, CompletedAt: &completedAt, SafeOutcome: deferral.SafeOutcome}, nil
}

func validLiveDeferralOutcome(outcome commissioningv2.SafeOutcome) bool {
	switch outcome {
	case commissioningv2.OutcomeCredentialUnavailable, commissioningv2.OutcomeQuotaExhausted,
		commissioningv2.OutcomeRateLimited, commissioningv2.OutcomeRetryAfterDeferred,
		commissioningv2.OutcomeConfigurationStale:
		return true
	default:
		return false
	}
}

// intent_committed -> transmitting with transmitted_at fixed once, the exact
// durable gate, and the pacing floor (deadline + snapshot minimum interval,
// preserving stronger restrictions). All predicates are re-read under the
// transaction in the fixed lock order integration -> run -> step -> budget.
// HTTP/network I/O may occur ONLY after a known successful commit; an
// ambiguous/uncertain commit must NEVER result in transmission.
func (store *Store) MarkClaimedStepTransmitting(ctx context.Context, marker commissioningv2.LiveTransmitMarker, now time.Time) (commissioningv2.LiveStepState, error) {
	canonicalIndexer, canonicalStep, err := checkBoundarySnapshot(marker.BudgetSnapshot, marker.IndexerInstanceID, marker.StepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	canonicalRun, err := uuid.Parse(marker.RunID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if marker.ExpectedStepState != commissioningv2.StepIntentCommitted || marker.ExecutionDeadline.IsZero() {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if marker.LeaseOwner == "" || !capabilitySafeKeyPattern.MatchString(marker.LeaseOwner) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	if !now.Before(marker.ExecutionDeadline) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	indexerID := mustParseBoundaryUUID(canonicalIndexer)
	stepID := mustParseBoundaryUUID(canonicalStep)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerID, canonicalRun)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	step, err := lockIndexerCommissioningStep(ctx, transaction, canonicalRun, stepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if step.State != commissioningv2.StepIntentCommitted || step.State != marker.ExpectedStepState {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if step.RunID != canonicalRun {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if run.IndexerInstanceID != indexerID || run.ID != canonicalRun {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if run.Revision != marker.ExpectedRunRev {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if step.ExecutorLeaseOwner != marker.LeaseOwner || step.ExecutorLeaseUntil == nil || !step.ExecutorLeaseUntil.After(now) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if step.Phase == commissioningv2.StepPhaseCAPS && run.State != commissioningv2.RunCAPSAuthorized || step.Phase == commissioningv2.StepPhaseSearch && run.State != commissioningv2.RunSearchesAuthorized {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if run.CancelRequestedAt != nil || run.State == commissioningv2.RunCancelRequested {
		return commissioningv2.LiveStepState{}, ErrLiveExecutorCancelled
	}
	if !commissioningRunConfigurationCurrent(configuration, digest, run) {
		return commissioningv2.LiveStepState{}, ErrIndexerCommissioningConfigChanged
	}
	window := marker.BudgetSnapshot.LimitWindow()
	var reserved bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM indexer_request_reservations WHERE id=$1 AND indexer_instance_id=$2 AND request_family='query' AND reserved_at > $3)`, canonicalStep, canonicalIndexer, now.UTC().Add(-window)).Scan(&reserved); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if !reserved {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='transmitting',transmitted_at=$3 WHERE id=$1 AND run_id=$2 AND state='intent_committed'`, canonicalStep, canonicalRun.String(), now)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	minimumIntervalMS := marker.BudgetSnapshot.MinimumInterval().Milliseconds()
	if minimumIntervalMS < 0 {
		minimumIntervalMS = 0
	}
	pacingFloor := indexers.PacingFloor(marker.ExecutionDeadline, marker.BudgetSnapshot.MinimumInterval(), indexers.NextAllowedHint{})
	budgetCommand, err := transaction.Exec(ctx, `INSERT INTO indexer_budget_state
		(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at,commissioning_gate_step_id,last_outcome_at)
		VALUES($1,$2,$3,NULL,0,NULL,1,$4,$5,NULL)
		ON CONFLICT (indexer_instance_id) DO UPDATE SET
			minimum_interval_ms=EXCLUDED.minimum_interval_ms,
			next_allowed_at=GREATEST(indexer_budget_state.next_allowed_at,EXCLUDED.next_allowed_at),
			revision=indexer_budget_state.revision+1,
			updated_at=EXCLUDED.updated_at,
			commissioning_gate_step_id=EXCLUDED.commissioning_gate_step_id
		WHERE indexer_budget_state.commissioning_gate_step_id IS NULL`,
		canonicalIndexer, minimumIntervalMS, pacingFloor, now, canonicalStep)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if budgetCommand.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrCommissioningGateUnresolved
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	transmittedAt := now
	return commissioningv2.LiveStepState{StepID: canonicalStep, State: commissioningv2.StepTransmitting, TransmittedAt: &transmittedAt, LeaseOwner: marker.LeaseOwner, Deadline: marker.ExecutionDeadline, PacingFloor: pacingFloor, GateHeld: true, RunRevision: run.Revision}, nil
}

func mustParseBoundaryUUID(value string) uuid.UUID {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}
	}
	return parsed
}

// CompleteLiveAttempt is the approved transactional finalization. It
// re-derives the classification from retained inputs (never trusting the
// submitted derived fields), validates the permit-bound snapshot on its own
// axis, reads dynamic scheduler state from the LOCKED durable row, computes
// the outcome effect from that row, and atomically persists the terminal
// step, optional run transition, scheduler accounting, cooldown, outcome
// times, and gate clearance only when allowed. Duplicate completion returns
// ErrConcurrentModification with zero writes and never rewrites scheduler
// state.
func (store *Store) CompleteLiveAttempt(ctx context.Context, completion commissioningv2.LiveCompletion, now time.Time) (commissioningv2.LiveStepState, error) {
	canonicalIndexer, canonicalStep, err := checkBoundarySnapshot(completion.BudgetSnapshot, completion.IndexerInstanceID, completion.StepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	canonicalRun, err := uuid.Parse(completion.RunID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if completion.LeaseOwner == "" {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	want, err := commissioningv2.ClassifyLiveAttempt(completion.Classified.Inputs)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningv2.ClassifiedCompletionMatchesInputs(completion.Classified) || want.SafeOutcome != completion.Classified.SafeOutcome {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	classified := completion.Classified
	if classified.SchedulerResult.Outcome == "" {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !validCommissioningRecordResult(classified.SchedulerResult) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningStepSafeCodeMatches(classified.SafeOutcome, classified.SafeCode) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if classified.ClearGate && !allowlistedGateOutcome(classified.SafeOutcome) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if err := validateCompletionEvidenceShape(classified); err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	indexerID := mustParseBoundaryUUID(canonicalIndexer)
	stepID := mustParseBoundaryUUID(canonicalStep)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerID, canonicalRun)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	step, err := lockIndexerCommissioningStep(ctx, transaction, canonicalRun, stepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if step.State != commissioningv2.StepTransmitting || step.TransmittedAt == nil || step.CompletedAt != nil {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if step.ExecutorLeaseOwner != completion.LeaseOwner {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if step.RunID != canonicalRun || run.ID != canonicalRun || run.IndexerInstanceID != indexerID {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if run.Revision != completion.ExpectedRunRev {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	// §10.4 target/fingerprint revalidation: compare the caller-carried
	// expected completion identity (structural fingerprint + target kind +
	// category ids, bound at claim/authorize time) against the LOCKED
	// durable step. The locked step itself is never the source of the
	// expected value. Classification target identity is likewise checked
	// against the locked durable target before any run transition.
	if err := revalidateCompletionTarget(completion, step); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	drifted := !commissioningRunConfigurationCurrent(configuration, digest, run)
	if drifted != classified.Inputs.FreshnessDrifted {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	var locked indexers.Snapshot
	var gateValue *string
	var minimumMS int64
	var nextVal, cooldownVal, outcomeAtVal, updatedVal *time.Time
	var lastOutcomeVal *string
	var consecutive int32
	var revisionVal int64
	if err := transaction.QueryRow(ctx, `SELECT minimum_interval_ms,next_allowed_at,cooldown_until,
		consecutive_failure,last_outcome,last_outcome_at,updated_at,revision,commissioning_gate_step_id::text
		FROM indexer_budget_state WHERE indexer_instance_id=$1 FOR UPDATE`,
		canonicalIndexer).Scan(&minimumMS, &nextVal, &cooldownVal, &consecutive, &lastOutcomeVal, &outcomeAtVal, &updatedVal, &revisionVal, &gateValue); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if gateValue == nil || *gateValue != canonicalStep {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	locked.IndexerID = canonicalIndexer
	locked.MinimumInterval = time.Duration(minimumMS) * time.Millisecond
	if nextVal != nil {
		locked.NextAllowedAt = *nextVal
	}
	if cooldownVal != nil {
		locked.CooldownUntil = *cooldownVal
	}
	locked.ConsecutiveFailure = uint32(consecutive)
	if lastOutcomeVal != nil {
		locked.LastOutcome = indexers.Outcome(*lastOutcomeVal)
	}
	if outcomeAtVal != nil {
		locked.LastOutcomeAt = *outcomeAtVal
	}
	if updatedVal != nil {
		locked.UpdatedAt = *updatedVal
	}
	if completion.BudgetSnapshot.MinimumInterval().Milliseconds() != locked.MinimumInterval.Milliseconds() {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	effect, err := indexers.ComputeCommissioningOutcomeEffect(locked, completion.BudgetSnapshot, classified.SchedulerResult, now)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	evidenceDocument, evidenceKey, rawCount, plausibleCount, safeDigest, rawDigest, err := completionEvidenceColumns(classified, now)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	stepCommand, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='completed',completed_at=$3,
		executor_lease_owner=NULL,executor_lease_until=NULL,
		safe_outcome=$4,safe_code=NULLIF($5,''),retry_at=NULL,
		raw_result_count=$6,plausible_identity_count=$7,
		safe_response_sha256=$8,raw_body_sha256=$9,
		evidence_key=$10,evidence_document=$11
		WHERE id=$1 AND run_id=$2 AND state='transmitting'`,
		canonicalStep, canonicalRun.String(), now,
		string(classified.SafeOutcome), string(classified.SafeCode),
		rawCount, plausibleCount, safeDigest, rawDigest, evidenceKey, evidenceDocument)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if stepCommand.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if completion.ApplyRunTransition {
		if err := applyLiveRunTransition(ctx, transaction, run, classified, now); err != nil {
			return commissioningv2.LiveStepState{}, err
		}
	}
	budgetCommand, err := transaction.Exec(ctx, `UPDATE indexer_budget_state SET
		next_allowed_at=GREATEST(next_allowed_at,$3),
		cooldown_until=GREATEST(cooldown_until,$4),
		consecutive_failure=$5,last_outcome=$6,last_outcome_at=$7,
		commissioning_gate_step_id=CASE WHEN $8 THEN NULL ELSE commissioning_gate_step_id END,
		revision=revision+1,updated_at=$9
		WHERE indexer_instance_id=$1 AND commissioning_gate_step_id=$2`,
		canonicalIndexer, canonicalStep,
		nullableTime(effect.NextAllowedAt), nullableTime(effect.CooldownUntil),
		int32(effect.ConsecutiveFailure), string(effect.LastOutcome), nullableTime(effect.LastOutcomeAt),
		classified.ClearGate, now)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if budgetCommand.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	completedAt := now
	return commissioningv2.LiveStepState{StepID: canonicalStep, State: commissioningv2.StepCompleted, CompletedAt: &completedAt, SafeOutcome: classified.SafeOutcome, RunRevision: run.Revision}, nil
}

// revalidateCompletionTarget implements §10.4: the caller-carried expected
// structural fingerprint (from the authorized/claimed request identity, not
// from the locked step) must equal the locked durable step's
// structural_request_sha256, and the classification input's target identity
// (target kind + category ids) must equal the locked durable target before
// any run transition. Foreign/mismatched identities fail closed.
func revalidateCompletionTarget(completion commissioningv2.LiveCompletion, step IndexerCommissioningStep) error {
	if completion.ExpectedStructuralFingerprint.IsZero() || len(step.StructuralRequestSHA256) != 32 {
		return ErrInvalidIndexerCommissioning
	}
	if !bytes.Equal(completion.ExpectedStructuralFingerprint.Bytes(), step.StructuralRequestSHA256) {
		return ErrInvalidIndexerCommissioning
	}
	if string(completion.ExpectedTargetKind.Kind) != string(step.Target.Kind) || completion.ExpectedTargetKind.RecipeKey != step.Target.RecipeKey {
		return ErrInvalidIndexerCommissioning
	}
	if !equalCompletionCategoryIDs(completion.ExpectedCategoryIDs, step.CategoryIDs) {
		return ErrInvalidIndexerCommissioning
	}
	if string(completion.Classified.Inputs.TargetKind.Kind) != string(step.Target.Kind) || completion.Classified.Inputs.TargetKind.RecipeKey != step.Target.RecipeKey {
		return ErrInvalidIndexerCommissioning
	}
	return nil
}

func equalCompletionCategoryIDs(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validCommissioningRecordResult(result indexers.RecordResult) bool {
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

func allowlistedGateOutcome(outcome commissioningv2.SafeOutcome) bool {
	switch outcome {
	case commissioningv2.OutcomeCAPSObserved, commissioningv2.OutcomeRecipeVerified,
		commissioningv2.OutcomeNoResults, commissioningv2.OutcomeNoPlausibleMatch,
		commissioningv2.OutcomeUnsupported, commissioningv2.OutcomePermissionRestricted,
		commissioningv2.OutcomeContradicted, commissioningv2.OutcomeMalformedResponse,
		commissioningv2.OutcomeSemanticAmbiguity, commissioningv2.OutcomeRateLimited,
		commissioningv2.OutcomeQuotaExhausted, commissioningv2.OutcomeAuthenticationFailure,
		commissioningv2.OutcomeConfigurationStale, commissioningv2.OutcomeSafetyCredentialEcho,
		commissioningv2.OutcomeSafetyUnsafeRedirect, commissioningv2.OutcomeSafetyUnsafeText,
		commissioningv2.OutcomeSafetyBodyLimit, commissioningv2.OutcomeSafetyXMLLimit:
		return true
	default:
		return false
	}
}

func validateCompletionEvidenceShape(classified commissioningv2.ClassifiedCompletion) error {
	hasEvidence := classified.Evidence != nil
	hasCounts := classified.RawResultCount != nil && classified.PlausibleIdentityCount != nil
	hasDigests := len(classified.SafeResponseSHA256) != 0 || len(classified.RawBodySHA256) != 0
	switch classified.SafeOutcome {
	case commissioningv2.OutcomeCAPSObserved, commissioningv2.OutcomeRecipeVerified,
		commissioningv2.OutcomeNoResults, commissioningv2.OutcomeNoPlausibleMatch,
		commissioningv2.OutcomeUnsupported, commissioningv2.OutcomePermissionRestricted,
		commissioningv2.OutcomeContradicted:
		if !hasEvidence || !hasCounts {
			return ErrInvalidIndexerCommissioning
		}
		if err := commissioningv2.ValidateStepEvidence(*classified.Evidence); err != nil {
			return ErrInvalidIndexerCommissioning
		}
		if classified.Evidence.SafeOutcome != classified.SafeOutcome {
			return ErrInvalidIndexerCommissioning
		}
		if !bytes.Equal(classified.RawBodySHA256, classified.Evidence.BodySHA256.Bytes()) {
			return ErrInvalidIndexerCommissioning
		}
		return nil
	default:
		if hasEvidence || hasCounts || hasDigests {
			return ErrInvalidIndexerCommissioning
		}
		return nil
	}
}

func completionEvidenceColumns(classified commissioningv2.ClassifiedCompletion, now time.Time) (any, any, any, any, any, any, error) {
	if classified.Evidence == nil {
		return nil, nil, nil, nil, nil, nil, nil
	}
	document, err := commissioningv2.CanonicalStepEvidence(*classified.Evidence)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, ErrInvalidIndexerCommissioning
	}
	if classified.Evidence.ObservedAt.After(now) {
		return nil, nil, nil, nil, nil, nil, ErrInvalidIndexerCommissioning
	}
	var rawCount, plausibleCount any
	if classified.RawResultCount != nil {
		rawCount = int16(*classified.RawResultCount)
	}
	if classified.PlausibleIdentityCount != nil {
		plausibleCount = int16(*classified.PlausibleIdentityCount)
	}
	var safeDigest, rawDigest any
	if len(classified.SafeResponseSHA256) != 0 {
		safeDigest = append([]byte(nil), classified.SafeResponseSHA256...)
	}
	if len(classified.RawBodySHA256) != 0 {
		rawDigest = append([]byte(nil), classified.RawBodySHA256...)
	}
	return string(document), classified.Evidence.Key, rawCount, plausibleCount, safeDigest, rawDigest, nil
}

// applyLiveRunTransition applies the section 15 run transition inside the
// completion transaction. Unsupported/permission_restricted/contradicted,
// no_results, no_plausible_match and semantic_ambiguity produce NO
// transition.
func applyLiveRunTransition(ctx context.Context, transaction pgx.Tx, run IndexerCommissioningRun, classified commissioningv2.ClassifiedCompletion, now time.Time) error {
	to, code, terminal, setStale, ok := commissioningv2.DeriveRunTransition(classified, run.State)
	if !ok {
		return nil
	}
	if !commissioningv2.ValidRunTransition(run.State, to) || !commissioningRunSafeCodeMatches(to, code) {
		return ErrInvalidIndexerCommissioning
	}
	if !now.After(run.UpdatedAt) {
		return ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state=$4,revision=revision+1,
		safe_code=NULLIF($5,''),updated_at=$6,
		stale_at=CASE WHEN $7 THEN $6 ELSE stale_at END,
		completed_at=CASE WHEN $8 THEN $6 ELSE completed_at END
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`,
		run.ID.String(), run.IndexerInstanceID.String(), int64(run.Revision), string(to), string(code), now, setStale, terminal)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

// RequestRunCancellation writes only the run row (integration -> run).
func (store *Store) RequestRunCancellation(ctx context.Context, indexerID, runID string, expectedRevision uint64, now time.Time) (commissioningv2.LiveStepState, error) {
	indexerParsed, err := uuid.Parse(indexerID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	runParsed, err := uuid.Parse(runID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if expectedRevision == 0 {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	if _, _, err := lockCommissioningIntegration(ctx, transaction, indexerParsed); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerParsed, runParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if run.Revision != expectedRevision {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if !now.After(run.UpdatedAt) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningv2.ValidRunTransition(run.State, commissioningv2.RunCancelRequested) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='cancel_requested',revision=revision+1,
		safe_code='owner.cancelled',cancel_requested_at=$4,updated_at=$4
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`,
		runParsed.String(), indexerParsed.String(), int64(expectedRevision), now)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	return commissioningv2.LiveStepState{State: commissioningv2.StepPlanned, RunRevision: expectedRevision + 1}, nil
}

// FinalizeCancelledRun closes remaining planned/intent steps cancelled after a
// cancellation request. It never touches budget state.
func (store *Store) FinalizeCancelledRun(ctx context.Context, indexerID, runID string, expectedRevision uint64, now time.Time) (commissioningv2.LiveStepState, error) {
	indexerParsed, err := uuid.Parse(indexerID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	runParsed, err := uuid.Parse(runID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if expectedRevision == 0 {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	if _, _, err := lockCommissioningIntegration(ctx, transaction, indexerParsed); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerParsed, runParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if run.Revision != expectedRevision || run.State != commissioningv2.RunCancelRequested {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !now.After(run.UpdatedAt) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	for _, step := range run.Steps {
		switch step.State {
		case commissioningv2.StepPlanned, commissioningv2.StepIntentCommitted:
		default:
			if !commissioningv2.TerminalStepState(step.State) {
				return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
			}
		}
	}
	for _, step := range run.Steps {
		if commissioningv2.TerminalStepState(step.State) {
			continue
		}
		command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='cancelled',completed_at=$3,
			executor_lease_owner=NULL,executor_lease_until=NULL,
			safe_outcome='cancelled',safe_code='owner.cancelled'
			WHERE id=$1 AND run_id=$2 AND state=$4`,
			step.ID.String(), runParsed.String(), now, string(step.State))
		if err != nil {
			return commissioningv2.LiveStepState{}, err
		}
		if command.RowsAffected() != 1 {
			return commissioningv2.LiveStepState{}, ErrConcurrentModification
		}
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='cancelled',revision=revision+1,
		safe_code='owner.cancelled',completed_at=$4,updated_at=$4
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`,
		runParsed.String(), indexerParsed.String(), int64(expectedRevision), now)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	return commissioningv2.LiveStepState{State: commissioningv2.StepCancelled, RunRevision: expectedRevision + 1}, nil
}

// AuthorizedTransmittingCommissioningStep implements the narrow
// newznab.CommissioningTransmittingStepAuthorizer: it reports whether the
// exact (indexer, step) attempt has crossed the durable transmission
// boundary (state transmitting, transmitted_at set, exact attempt identity,
// unresolved gate held for that step). Read-only.
func (store *Store) AuthorizedTransmittingCommissioningStep(ctx context.Context, indexerID, stepID string) bool {
	_, indexerCanonical, err := canonicalBoundaryIdentity(indexerID)
	if err != nil {
		return false
	}
	_, stepCanonical, err := canonicalBoundaryIdentity(stepID)
	if err != nil {
		return false
	}
	return store.authorizedTransmittingStep(ctx, indexerCanonical, stepCanonical)
}

func (store *Store) authorizedTransmittingStep(ctx context.Context, canonicalIndexer, canonicalStep string) bool {
	// ONE SQL statement / one MVCC snapshot joining the commissioning step,
	// the run/indexer identity, and the indexer_budget_state gate. The
	// authorization is true only when that one read observes the exact
	// indexer identity, the exact step identity, state = transmitting,
	// transmitted_at IS NOT NULL, and commissioning_gate_step_id exactly
	// this step. No transaction is required merely for this read. Defense
	// in depth: the snapshot's attempt-scoped send cell remains the true
	// one-use enforcement.
	var authorized bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM indexer_commissioning_steps s
		JOIN indexer_commissioning_runs r ON r.id=s.run_id
		JOIN indexer_budget_state b ON b.indexer_instance_id=r.indexer_instance_id
		WHERE s.id=$2
		AND r.indexer_instance_id=$1
		AND b.indexer_instance_id=$1
		AND s.state='transmitting'
		AND s.transmitted_at IS NOT NULL
		AND b.commissioning_gate_step_id=s.id
	)`, canonicalIndexer, canonicalStep).Scan(&authorized); err != nil {
		return false
	}
	return authorized
}

// from the database on every call and never caches a value. No row or NULL
// means clear; non-NULL means unresolved; query errors fail closed.
var _ commissioningv2.LiveStepStore = (*Store)(nil)

func (store *Store) CommissioningGate(ctx context.Context, indexerID string) (indexers.CommissioningGateState, bool) {
	parsed, err := uuid.Parse(indexerID)
	if err != nil {
		return indexers.CommissioningGateUnknown, false
	}
	var gate *string
	if err := store.Pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, parsed.String()).Scan(&gate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return indexers.CommissioningGateClear, true
		}
		return indexers.CommissioningGateUnknown, false
	}
	if gate == nil {
		return indexers.CommissioningGateClear, true
	}
	return indexers.CommissioningGateUnresolved, true
}

// LoadAuthoritativeIndexerState loads the complete durable scheduling state
// in ONE statement (one MVCC snapshot) across indexer_budget_state and the
// referenced gate step. No budget row is a definite valid clear zero snapshot,
// not an error; actual query failures remain fail-closed.
func (store *Store) LoadAuthoritativeIndexerState(ctx context.Context, indexerID string) (indexers.AuthoritativeSnapshot, error) {
	parsed, err := uuid.Parse(indexerID)
	if err != nil {
		return indexers.AuthoritativeSnapshot{}, err
	}
	var result indexers.AuthoritativeSnapshot
	var minimumIntervalMS int64
	var next, cooldown, outcomeAt, updated *time.Time
	var lastOutcome *string
	var revision *int64
	var gateStepID *string
	var gateState, gateOutcome *string
	err = store.Pool.QueryRow(ctx, `SELECT b.minimum_interval_ms,b.next_allowed_at,b.cooldown_until,
		b.consecutive_failure,b.last_outcome,b.last_outcome_at,
		b.revision,b.updated_at,b.commissioning_gate_step_id,
		s.state,s.safe_outcome
		FROM indexer_budget_state b
		LEFT JOIN indexer_commissioning_steps s ON s.id=b.commissioning_gate_step_id
		WHERE b.indexer_instance_id=$1`, parsed.String()).Scan(
		&minimumIntervalMS, &next, &cooldown,
		&result.ConsecutiveFailure, &lastOutcome, &outcomeAt,
		&revision, &updated, &gateStepID,
		&gateState, &gateOutcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return indexers.AuthoritativeSnapshot{
			Snapshot: indexers.Snapshot{IndexerID: parsed.String()},
			Gate:     indexers.CommissioningGateClear,
		}, nil
	}
	if err != nil {
		return indexers.AuthoritativeSnapshot{}, err
	}
	result.IndexerID = parsed.String()
	result.MinimumInterval = time.Duration(minimumIntervalMS) * time.Millisecond
	if lastOutcome != nil {
		result.LastOutcome = indexers.Outcome(*lastOutcome)
	}
	if outcomeAt != nil {
		result.LastOutcomeAt = *outcomeAt
	}
	if next != nil {
		result.NextAllowedAt = *next
	}
	if cooldown != nil {
		result.CooldownUntil = *cooldown
	}
	if updated != nil {
		result.UpdatedAt = *updated
	}
	if revision != nil && *revision > 0 {
		result.Snapshot.Revision = uint64(*revision)
	}
	if gateStepID == nil {
		result.Gate = indexers.CommissioningGateClear
	} else {
		result.Gate = indexers.CommissioningGateUnresolved
		result.GateStepID = *gateStepID
	}
	if gateState != nil {
		result.GateStepState = *gateState
	}
	if gateOutcome != nil {
		result.GateStepOutcome = *gateOutcome
	}
	return result, nil
}

// MarkInterruptedAttemptIndeterminate closes a transmitted-but-unclosed
// attempt indeterminate. It locks integration -> run -> step (NO budget lock)
// and NEVER touches indexer_budget_state, so the gate is structurally
// unreachable from this path. AckKnown never influences gate clearance.
func (store *Store) MarkInterruptedAttemptIndeterminate(ctx context.Context, recovery commissioningv2.LiveRecovery, now time.Time) (commissioningv2.LiveStepState, error) {
	indexerParsed, err := uuid.Parse(recovery.IndexerInstanceID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	runParsed, err := uuid.Parse(recovery.RunID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	stepParsed, err := uuid.Parse(recovery.StepID)
	if err != nil {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	defer transaction.Rollback(ctx)
	if _, _, err := lockCommissioningIntegration(ctx, transaction, indexerParsed); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerParsed, runParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	step, err := lockIndexerCommissioningStep(ctx, transaction, runParsed, stepParsed)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if step.State != commissioningv2.StepTransmitting || step.TransmittedAt == nil {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if now.Before(*step.TransmittedAt) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='indeterminate',completed_at=$3,
		executor_lease_owner=NULL,executor_lease_until=NULL,
		safe_outcome='indeterminate',safe_code='indeterminate.attempt'
		WHERE id=$1 AND run_id=$2 AND state='transmitting'`, stepParsed.String(), runParsed.String(), now)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if run.Revision != recovery.ExpectedRunRev {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if !now.After(run.UpdatedAt) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	// Recovery run transition uses the same explicit checks as normal live
	// transitions: the owner_attention move must be a valid forward
	// transition with its approved safe-code pairing.
	if !commissioningv2.ValidRunTransition(run.State, commissioningv2.RunOwnerAttention) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningRunSafeCodeMatches(commissioningv2.RunOwnerAttention, commissioningv2.SafeCodeIndeterminateAttempt) {
		return commissioningv2.LiveStepState{}, ErrInvalidIndexerCommissioning
	}
	command, err = transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='owner_attention',revision=revision+1,
		safe_code='indeterminate.attempt',updated_at=$4,completed_at=$4
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`, runParsed.String(), indexerParsed.String(), int64(recovery.ExpectedRunRev), now)
	if err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	if command.RowsAffected() != 1 {
		return commissioningv2.LiveStepState{}, ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	completedAt := now
	return commissioningv2.LiveStepState{StepID: stepParsed.String(), State: commissioningv2.StepIndeterminate, CompletedAt: &completedAt, SafeOutcome: commissioningv2.OutcomeIndeterminate, RunRevision: recovery.ExpectedRunRev + 1}, nil
}

// LoadAuthorizedStep loads the consistent run-plus-step snapshot for one
// execute action: exact revisions, phase authorization, ordinal, target,
// fingerprint, and planned state.
func (store *Store) LoadAuthorizedStep(ctx context.Context, stepID string) (commissioningv2.LiveAuthorizedStep, error) {
	parsed, err := uuid.Parse(stepID)
	if err != nil {
		return commissioningv2.LiveAuthorizedStep{}, ErrInvalidIndexerCommissioning
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	defer transaction.Rollback(ctx)
	var runID, indexerID, state, phase, targetKind, recipeKey, scope, role, fixtureKey string
	var revision int64
	var ordinal int16
	var structural []byte
	var fixtureDigest []byte
	var runState string
	var plannerVersion, profileVersion int16
	var categoryIDs []int
	var capAuthorized, searchAuthorized any
	_ = capAuthorized
	_ = searchAuthorized
	if err := transaction.QueryRow(ctx, `SELECT step.run_id::text,run.indexer_instance_id::text,step.state,step.phase,
		step.target_kind,COALESCE(step.recipe_key,''),COALESCE(step.scope,''),COALESCE(step.role,''),
		COALESCE(step.fixture_key,''),step.ordinal,step.structural_request_sha256,step.fixture_sha256,
		run.state,run.revision,run.planner_version,run.profile_schema_version,step.category_ids,
		run.caps_authorized_at,run.search_authorized_at
		FROM indexer_commissioning_steps step
		JOIN indexer_commissioning_runs run ON run.id=step.run_id
		WHERE step.id=$1`, parsed.String()).Scan(
		&runID, &indexerID, &state, &phase, &targetKind, &recipeKey, &scope, &role,
		&fixtureKey, &ordinal, &structural, &fixtureDigest,
		&runState, &revision, &plannerVersion, &profileVersion, &categoryIDs,
		&capAuthorized, &searchAuthorized); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commissioningv2.LiveAuthorizedStep{}, ErrNotFound
		}
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	step, err := store.IndexerCommissioningStep(ctx, mustParseBoundaryUUID(runID), parsed)
	if err != nil {
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	_ = step
	_ = structural
	_ = fixtureDigest
	_ = categoryIDs
	_ = scope
	_ = role
	_ = recipeKey
	_ = fixtureKey
	_ = targetKind
	_ = phase
	_ = state
	_ = ordinal
	var fingerprint commissioningv2.SHA256Digest
	if len(structural) == 32 {
		fingerprint, err = commissioningv2.SHA256DigestFromBytes(structural)
		if err != nil {
			return commissioningv2.LiveAuthorizedStep{}, ErrInvalidIndexerCommissioning
		}
	} else {
		return commissioningv2.LiveAuthorizedStep{}, ErrInvalidIndexerCommissioning
	}
	authorized := (phase == "caps" && runState == string(commissioningv2.RunCAPSAuthorized)) ||
		(phase == "search" && runState == string(commissioningv2.RunSearchesAuthorized))
	result := commissioningv2.LiveAuthorizedStep{
		RunID:           runID,
		StepID:          parsed.String(),
		IndexerID:       indexerID,
		RunRevision:     uint64(revision),
		StepState:       commissioningv2.StepState(state),
		Phase:           commissioningv2.StepPhase(phase),
		Authorized:      authorized,
		RunState:        commissioningv2.RunState(runState),
		Ordinal:         uint8(ordinal),
		PlannerVersion:  uint16(plannerVersion),
		ProfileVersion:  uint16(profileVersion),
		Fingerprint:     fingerprint,
		ConfigurationOK: true,
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	return result, nil
}

var _ = bytes.MinRead
