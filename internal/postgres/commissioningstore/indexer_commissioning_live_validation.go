package commissioningstore

import (
	"bytes"
	"context"
	"errors"
	"github.com/media-manager/media-manager/internal/postgres/dbcore"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"uuid"
)

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
	if classified.Inputs.TargetKind.Kind == newznab.EvidenceTargetSearchRecipe {
		return commissioningv2.ValidateSearchCompletionEvidence(classified)
	}
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
	document, err := commissioningv2.CanonicalStepEvidenceForTarget(*classified.Evidence)
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
		return dbcore.ErrConcurrentModification
	}
	return nil
}

// RequestRunCancellation writes only the run row (integration -> run).
func (store *Commissioning) RequestRunCancellation(ctx context.Context, indexerID, runID string, expectedRevision uint64, now time.Time) (commissioningv2.LiveStepState, error) {
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
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
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
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveStepState{}, err
	}
	return commissioningv2.LiveStepState{State: commissioningv2.StepPlanned, RunRevision: expectedRevision + 1}, nil
}

// FinalizeCancelledRun closes remaining planned/intent steps cancelled after a
// cancellation request. It never touches budget state.
func (store *Commissioning) FinalizeCancelledRun(ctx context.Context, indexerID, runID string, expectedRevision uint64, now time.Time) (commissioningv2.LiveStepState, error) {
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
			return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
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
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
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
func (store *Commissioning) AuthorizedTransmittingCommissioningStep(ctx context.Context, indexerID, stepID string) bool {
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

func (store *Commissioning) authorizedTransmittingStep(ctx context.Context, canonicalIndexer, canonicalStep string) bool {
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
var _ commissioningv2.LiveStepStore = (*Commissioning)(nil)

func (store *Commissioning) CommissioningGate(ctx context.Context, indexerID string) (indexers.CommissioningGateState, bool) {
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
func (store *Commissioning) LoadAuthoritativeIndexerState(ctx context.Context, indexerID string) (indexers.AuthoritativeSnapshot, error) {
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
func (store *Commissioning) MarkInterruptedAttemptIndeterminate(ctx context.Context, recovery commissioningv2.LiveRecovery, now time.Time) (commissioningv2.LiveStepState, error) {
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
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
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
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
	}
	if run.Revision != recovery.ExpectedRunRev {
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
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
		return commissioningv2.LiveStepState{}, dbcore.ErrConcurrentModification
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
func (store *Commissioning) LoadAuthorizedStep(ctx context.Context, stepID string) (commissioningv2.LiveAuthorizedStep, error) {
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
	var secretReference string
	_ = capAuthorized
	_ = searchAuthorized
	if err := transaction.QueryRow(ctx, `SELECT step.run_id::text,run.indexer_instance_id::text,step.state,step.phase,
		step.target_kind,COALESCE(step.recipe_key,''),COALESCE(step.scope,''),COALESCE(step.role,''),
		COALESCE(step.fixture_key,''),step.ordinal,step.structural_request_sha256,step.fixture_sha256,
		run.state,run.revision,run.planner_version,run.profile_schema_version,step.category_ids,
		run.caps_authorized_at,run.search_authorized_at,COALESCE(integration.secret_ref,'')
		FROM indexer_commissioning_steps step
		JOIN indexer_commissioning_runs run ON run.id=step.run_id
		JOIN integration_instances integration ON integration.id=run.indexer_instance_id
		WHERE step.id=$1`, parsed.String()).Scan(
		&runID, &indexerID, &state, &phase, &targetKind, &recipeKey, &scope, &role,
		&fixtureKey, &ordinal, &structural, &fixtureDigest,
		&runState, &revision, &plannerVersion, &profileVersion, &categoryIDs,
		&capAuthorized, &searchAuthorized, &secretReference); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commissioningv2.LiveAuthorizedStep{}, dbcore.ErrNotFound
		}
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	runRecord, err := store.IndexerCommissioningRun(ctx, mustParseBoundaryUUID(runID))
	if err != nil {
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	var stepRecord IndexerCommissioningStep
	foundStep := false
	for _, candidate := range runRecord.Steps {
		if candidate.ID == parsed {
			stepRecord = candidate
			foundStep = true
			break
		}
	}
	if !foundStep {
		return commissioningv2.LiveAuthorizedStep{}, dbcore.ErrNotFound
	}
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
		SecretReference: secretReference,
		Request:         stepRecord.Request,
		Target:          stepRecord.Target,
		Scope:           stepRecord.Scope,
		Role:            stepRecord.Role,
		FixtureKey:      stepRecord.FixtureKey,
		CategoryIDs:     append([]int(nil), stepRecord.CategoryIDs...),
		Fixtures:        runRecord.Fixtures,
	}
	if err := transaction.Commit(ctx); err != nil {
		return commissioningv2.LiveAuthorizedStep{}, err
	}
	return result, nil
}

var _ = bytes.MinRead
