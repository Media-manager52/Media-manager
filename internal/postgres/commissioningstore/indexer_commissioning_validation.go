package commissioningstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/media-manager/media-manager/internal/postgres/dbcore"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
)

type commissioningScanner interface {
	Scan(...any) error
}

func lockIndexerCommissioningStep(ctx context.Context, transaction pgx.Tx, runID, stepID uuid.UUID) (IndexerCommissioningStep, error) {
	step, err := scanIndexerCommissioningStep(transaction.QueryRow(ctx, `SELECT `+indexerCommissioningStepColumns+` FROM indexer_commissioning_steps step WHERE step.run_id=$1 AND step.id=$2 FOR UPDATE`, runID.String(), stepID.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return IndexerCommissioningStep{}, dbcore.ErrNotFound
	}
	return step, err
}

func validateCommissioningStepTransition(transition IndexerCommissioningStepTransition, now time.Time) ([]byte, error) {
	if !commissioningStepSafeCodeMatches(transition.SafeOutcome, transition.SafeCode) || len(transition.SafeResponseSHA256) != 0 && len(transition.SafeResponseSHA256) != 32 || len(transition.RawBodySHA256) != 0 && len(transition.RawBodySHA256) != 32 {
		return nil, ErrInvalidIndexerCommissioning
	}
	if transition.State == commissioningv2.StepIntentCommitted {
		if !capabilitySafeKeyPattern.MatchString(transition.LeaseOwner) || transition.LeaseUntil == nil || !transition.LeaseUntil.After(now) || transition.SafeOutcome != "" || transition.SafeCode != "" || transition.RetryAt != nil || transition.RawResultCount != nil || transition.PlausibleIdentityCount != nil || len(transition.SafeResponseSHA256) != 0 || len(transition.RawBodySHA256) != 0 || transition.Evidence != nil {
			return nil, ErrInvalidIndexerCommissioning
		}
		return nil, nil
	}
	if transition.LeaseOwner != "" || transition.LeaseUntil != nil {
		return nil, ErrInvalidIndexerCommissioning
	}
	if transition.State == commissioningv2.StepTransmitting {
		if transition.SafeOutcome != "" || transition.SafeCode != "" || transition.RetryAt != nil || transition.RawResultCount != nil || transition.PlausibleIdentityCount != nil || len(transition.SafeResponseSHA256) != 0 || len(transition.RawBodySHA256) != 0 || transition.Evidence != nil {
			return nil, ErrInvalidIndexerCommissioning
		}
		return nil, nil
	}
	if !commissioningv2.TerminalStepState(transition.State) || !commissioningv2.ValidSafeOutcome(transition.SafeOutcome) {
		return nil, ErrInvalidIndexerCommissioning
	}
	if transition.RetryAt != nil && transition.State != commissioningv2.StepDeferredBeforeSend {
		return nil, ErrInvalidIndexerCommissioning
	}
	if (transition.RawResultCount == nil) != (transition.PlausibleIdentityCount == nil) {
		return nil, ErrInvalidIndexerCommissioning
	}
	if transition.RawResultCount != nil && *transition.PlausibleIdentityCount > *transition.RawResultCount {
		return nil, ErrInvalidIndexerCommissioning
	}
	switch transition.State {
	case commissioningv2.StepCancelled:
		if transition.SafeOutcome != commissioningv2.OutcomeCancelled {
			return nil, ErrInvalidIndexerCommissioning
		}
	case commissioningv2.StepIndeterminate:
		if transition.SafeOutcome != commissioningv2.OutcomeIndeterminate {
			return nil, ErrInvalidIndexerCommissioning
		}
	case commissioningv2.StepDeferredBeforeSend:
		if !commissioningDeferredOutcome(transition.SafeOutcome) {
			return nil, ErrInvalidIndexerCommissioning
		}
	case commissioningv2.StepCompleted:
		if transition.SafeOutcome == commissioningv2.OutcomeCancelled || transition.SafeOutcome == commissioningv2.OutcomeIndeterminate {
			return nil, ErrInvalidIndexerCommissioning
		}
	}
	if commissioningSafetyOutcome(transition.SafeOutcome) && (len(transition.SafeResponseSHA256) != 0 || len(transition.RawBodySHA256) != 0 || transition.Evidence != nil) {
		return nil, ErrInvalidIndexerCommissioning
	}
	if transition.Evidence == nil {
		if commissioningDurableEvidenceOutcome(transition.SafeOutcome) || len(transition.RawBodySHA256) != 0 {
			return nil, ErrInvalidIndexerCommissioning
		}
		return nil, nil
	}
	if transition.RawResultCount == nil || transition.PlausibleIdentityCount == nil {
		return nil, ErrInvalidIndexerCommissioning
	}
	evidence, err := commissioningv2.CanonicalStepEvidence(*transition.Evidence)
	if err != nil || transition.Evidence.SafeOutcome != transition.SafeOutcome || !bytes.Equal(transition.RawBodySHA256, transition.Evidence.BodySHA256.Bytes()) || transition.Evidence.ObservedAt.After(now) {
		return nil, ErrInvalidIndexerCommissioning
	}
	if transition.Evidence.Target.Kind == newznab.EvidenceTargetCAPS && (transition.Evidence.CAPS == nil || !transition.Evidence.CAPS.ObservedAt.Equal(transition.Evidence.ObservedAt)) {
		return nil, ErrInvalidIndexerCommissioning
	}
	return evidence, nil
}

func validateCommissioningTransitionForStep(step IndexerCommissioningStep, transition IndexerCommissioningStepTransition) error {
	if transition.RawResultCount != nil && *transition.RawResultCount > step.RequestLimit {
		return ErrInvalidIndexerCommissioning
	}
	if transition.Evidence != nil {
		evidence := transition.Evidence
		if evidence.Target != step.Target || evidence.RawResultCount != optionalUint16(transition.RawResultCount) || evidence.PlausibleIdentityCount != optionalUint16(transition.PlausibleIdentityCount) || step.TransmittedAt == nil || evidence.ObservedAt.Before(*step.TransmittedAt) {
			return ErrInvalidIndexerCommissioning
		}
	}
	if step.Phase == commissioningv2.StepPhaseCAPS {
		if transition.RawResultCount != nil && *transition.RawResultCount != 0 || transition.PlausibleIdentityCount != nil && *transition.PlausibleIdentityCount != 0 || transition.SafeOutcome == commissioningv2.OutcomeRecipeVerified || transition.SafeOutcome == commissioningv2.OutcomeNoResults || transition.SafeOutcome == commissioningv2.OutcomeNoPlausibleMatch {
			return ErrInvalidIndexerCommissioning
		}
	} else if transition.SafeOutcome == commissioningv2.OutcomeCAPSObserved {
		return ErrInvalidIndexerCommissioning
	}
	return nil
}

func commissioningDeferredOutcome(outcome commissioningv2.SafeOutcome) bool {
	switch outcome {
	case commissioningv2.OutcomeCredentialUnavailable, commissioningv2.OutcomeQuotaExhausted, commissioningv2.OutcomeRateLimited, commissioningv2.OutcomeRetryAfterDeferred, commissioningv2.OutcomeConfigurationStale:
		return true
	default:
		return false
	}
}

func commissioningDurableEvidenceOutcome(outcome commissioningv2.SafeOutcome) bool {
	switch outcome {
	case commissioningv2.OutcomeCAPSObserved, commissioningv2.OutcomeRecipeVerified, commissioningv2.OutcomeNoResults, commissioningv2.OutcomeNoPlausibleMatch, commissioningv2.OutcomeUnsupported, commissioningv2.OutcomePermissionRestricted, commissioningv2.OutcomeContradicted:
		return true
	default:
		return false
	}
}

func commissioningSafetyOutcome(outcome commissioningv2.SafeOutcome) bool {
	switch outcome {
	case commissioningv2.OutcomeSafetyCredentialEcho, commissioningv2.OutcomeSafetyUnsafeRedirect, commissioningv2.OutcomeSafetyUnsafeText, commissioningv2.OutcomeSafetyBodyLimit, commissioningv2.OutcomeSafetyXMLLimit:
		return true
	default:
		return false
	}
}

func scanIndexerCommissioningRun(row commissioningScanner) (IndexerCommissioningRun, []byte, bool, error) {
	var result IndexerCommissioningRun
	var idText, indexerText string
	var revision int64
	var profileVersion, plannerVersion, maximumCalls, plannedCalls int16
	var contractKey *string
	var contractVersion *int16
	var fixtureDocument, capsPlanDocument, searchPlanDocument, configuration []byte
	var configDigestMatches bool
	var generatedProfileText *string
	if err := row.Scan(
		&idText, &indexerText, &result.State, &revision, &profileVersion, &plannerVersion,
		&result.EndpointConfigSHA256, &contractKey, &contractVersion,
		&fixtureDocument, &result.FixtureDocumentSHA256, &capsPlanDocument, &result.CAPSPlanDocumentSHA256,
		&result.CAPSAuthorizedAt, &result.CategorySelectionSHA256, &result.MovieCategoryIDs, &result.SeriesCategoryIDs,
		&searchPlanDocument, &result.SearchPlanDocumentSHA256, &result.SearchAuthorizedAt,
		&maximumCalls, &plannedCalls, &result.SafeCode, &generatedProfileText,
		&result.CreatedAt, &result.UpdatedAt, &result.CancelRequestedAt, &result.StaleAt, &result.CompletedAt,
		&configDigestMatches, &configuration,
	); err != nil {
		return IndexerCommissioningRun{}, nil, false, err
	}
	parsedID, err := uuid.Parse(idText)
	if err != nil {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	parsedIndexerID, err := uuid.Parse(indexerText)
	if err != nil {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	fixtures, err := commissioningv2.DecodeFixtureDocument(fixtureDocument)
	if err != nil {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	capsPlan, err := commissioningv2.DecodeProbePlan(capsPlanDocument)
	if err != nil || capsPlan.Phase != commissioningv2.PlanPhaseCAPS {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	var searchPlan *commissioningv2.ProbePlan
	if len(searchPlanDocument) > 0 {
		decoded, decodeErr := commissioningv2.DecodeProbePlan(searchPlanDocument)
		if decodeErr != nil || decoded.Phase != commissioningv2.PlanPhaseSearch {
			return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
		}
		searchPlan = &decoded
	}
	if revision <= 0 || profileVersion <= 0 || plannerVersion <= 0 || maximumCalls <= 0 || plannedCalls <= 0 || !commissioningv2.ValidRunState(result.State) {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	result.ID, result.IndexerInstanceID = parsedID, parsedIndexerID
	result.Revision = uint64(revision)
	result.ProfileSchemaVersion, result.PlannerVersion = uint16(profileVersion), uint16(plannerVersion)
	result.MaximumAPICalls, result.PlannedAPICalls = uint8(maximumCalls), uint8(plannedCalls)
	result.Fixtures, result.CAPSPlan, result.SearchPlan = fixtures, capsPlan, searchPlan
	canonicalFixture, fixtureErr := commissioningv2.CanonicalFixtureDocument(fixtures)
	canonicalCAPSPlan, capsPlanErr := commissioningv2.CanonicalProbePlan(capsPlan)
	if fixtureErr != nil || capsPlanErr != nil || !bytes.Equal(result.FixtureDocumentSHA256, commissioningv2.SumSHA256(canonicalFixture).Bytes()) || !bytes.Equal(result.CAPSPlanDocumentSHA256, commissioningv2.SumSHA256(canonicalCAPSPlan).Bytes()) {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	if searchPlan != nil {
		canonicalSearchPlan, canonicalErr := commissioningv2.CanonicalProbePlan(*searchPlan)
		if canonicalErr != nil || !bytes.Equal(result.SearchPlanDocumentSHA256, commissioningv2.SumSHA256(canonicalSearchPlan).Bytes()) {
			return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
		}
	}
	if (contractKey == nil) != (contractVersion == nil) {
		return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
	}
	if contractKey != nil {
		result.ProviderContract = &newznab.ProviderContractRef{Key: *contractKey, Version: uint16(*contractVersion)}
	}
	if generatedProfileText != nil {
		parsed, parseErr := uuid.Parse(*generatedProfileText)
		if parseErr != nil {
			return IndexerCommissioningRun{}, nil, false, ErrInvalidIndexerCommissioning
		}
		result.GeneratedProfileID = &parsed
	}
	if err := validateLoadedCommissioningRun(result); err != nil {
		return IndexerCommissioningRun{}, nil, false, err
	}
	return result, configuration, configDigestMatches, nil
}

func scanIndexerCommissioningStep(row commissioningScanner) (IndexerCommissioningStep, error) {
	var result IndexerCommissioningStep
	var idText, runText, targetKind string
	var requestDocument []byte
	var ordinal, requestLimit int16
	var fixtureDigest, structuralDigest, safeResponseDigest, rawBodyDigest []byte
	var rawResultCount, plausibleCount *int16
	var evidenceDocument []byte
	if err := row.Scan(&idText, &runText, &ordinal, &result.Phase, &targetKind, &result.Target.RecipeKey,
		&result.Scope, &result.Role, &result.FixtureKey, &fixtureDigest, &requestDocument,
		&structuralDigest, &result.CategoryIDs, &requestLimit, &result.BudgetFamily, &result.State,
		&result.IntentAt, &result.TransmittedAt, &result.CompletedAt, &result.ExecutorLeaseOwner,
		&result.ExecutorLeaseUntil, &result.SafeOutcome, &result.SafeCode, &result.RetryAt,
		&rawResultCount, &plausibleCount, &safeResponseDigest, &rawBodyDigest, &result.EvidenceKey,
		&evidenceDocument); err != nil {
		return IndexerCommissioningStep{}, err
	}
	result.Target.Kind = newznab.EvidenceTargetKind(targetKind)
	var err error
	result.ID, err = uuid.Parse(idText)
	if err != nil {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	result.RunID, err = uuid.Parse(runText)
	if err != nil {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	result.Ordinal, result.RequestLimit = uint8(ordinal), uint16(requestLimit)
	result.FixtureSHA256, result.StructuralRequestSHA256 = append([]byte(nil), fixtureDigest...), append([]byte(nil), structuralDigest...)
	result.SafeResponseSHA256, result.RawBodySHA256 = append([]byte(nil), safeResponseDigest...), append([]byte(nil), rawBodyDigest...)
	request, err := commissioningv2.DecodeTypedRequest(requestDocument)
	if err != nil {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	result.Request = request
	if rawResultCount != nil {
		value := uint16(*rawResultCount)
		result.RawResultCount = &value
	}
	if plausibleCount != nil {
		value := uint16(*plausibleCount)
		result.PlausibleIdentityCount = &value
	}
	if len(evidenceDocument) > 0 {
		evidence, evidenceErr := commissioningv2.DecodeStepEvidence(evidenceDocument)
		if evidenceErr != nil {
			return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
		}
		result.Evidence = &evidence
	}
	if err := validateLoadedCommissioningStep(result); err != nil {
		return IndexerCommissioningStep{}, err
	}
	return result, nil
}

func insertIndexerCommissioningStep(ctx context.Context, transaction pgx.Tx, runID uuid.UUID, step commissioningv2.ProbeStep, requestDocument []byte) error {
	var recipeKey, scope, role, fixtureKey any
	var fixtureDigest any
	categoryIDs := step.CategoryIDs
	if categoryIDs == nil {
		categoryIDs = []int{}
	}
	if step.Phase == commissioningv2.StepPhaseSearch {
		recipeKey, scope, role, fixtureKey = step.Target.RecipeKey, string(step.Scope), string(step.Role), step.FixtureKey
		fixtureDigest = step.FixtureSHA256.Bytes()
	}
	_, err := transaction.Exec(ctx, `INSERT INTO indexer_commissioning_steps(
		id,run_id,ordinal,phase,target_kind,recipe_key,scope,role,fixture_key,fixture_sha256,
		typed_request_document,structural_request_sha256,category_ids,request_limit,budget_family,state)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'planned')`,
		uuid.NewV7().String(), runID.String(), int16(step.Ordinal), string(step.Phase), string(step.Target.Kind),
		recipeKey, scope, role, fixtureKey, fixtureDigest, string(requestDocument), step.StructuralSHA256.Bytes(),
		categoryIDs, int16(step.Request.RequestLimit), string(commissioningv2.BudgetQuery))
	return err
}

func lockCommissioningIntegration(ctx context.Context, transaction pgx.Tx, indexerID uuid.UUID) ([]byte, []byte, error) {
	var kind string
	var configuration, digest []byte
	if err := transaction.QueryRow(ctx, `SELECT kind,config,media_manager_newznab_endpoint_config_sha256(config,secret_ref) FROM integration_instances WHERE id=$1 FOR UPDATE`, indexerID.String()).Scan(&kind, &configuration, &digest); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, dbcore.ErrNotFound
	} else if err != nil {
		return nil, nil, err
	}
	// A malformed current configuration intentionally yields a NULL digest.
	// Retain the locked Newznab row so a transmitted attempt can be closed and
	// the run marked stale; every path that could begin a request separately
	// requires the exact 32-byte snapshot to remain current.
	if kind != "newznab" || len(digest) != 0 && len(digest) != 32 {
		return nil, nil, ErrInvalidIndexerCommissioning
	}
	return configuration, append([]byte(nil), digest...), nil
}

func lockIndexerCommissioningRun(ctx context.Context, transaction pgx.Tx, indexerID, runID uuid.UUID) (IndexerCommissioningRun, error) {
	row := transaction.QueryRow(ctx, `SELECT `+indexerCommissioningRunColumns+`,
		true,'{}'::jsonb
		FROM indexer_commissioning_runs run WHERE run.id=$1 AND run.indexer_instance_id=$2 FOR UPDATE`, runID.String(), indexerID.String())
	run, _, _, err := scanIndexerCommissioningRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return IndexerCommissioningRun{}, dbcore.ErrNotFound
	}
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	run.Steps, err = listIndexerCommissioningSteps(ctx, transaction, run.ID)
	if err != nil || validateCommissioningRunSteps(run, run.Steps) != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	return run, nil
}

func validateLoadedCommissioningRun(run IndexerCommissioningRun) error {
	if run.ID == (uuid.UUID{}) || run.IndexerInstanceID == (uuid.UUID{}) || run.Revision == 0 || run.ProfileSchemaVersion != run.CAPSPlan.ProfileSchemaVersion || run.PlannerVersion != run.CAPSPlan.PlannerVersion || run.MaximumAPICalls != run.CAPSPlan.MaximumAPICalls || len(run.EndpointConfigSHA256) != 32 || !bytes.Equal(run.EndpointConfigSHA256, run.CAPSPlan.ConfigurationSHA256.Bytes()) {
		return ErrInvalidIndexerCommissioning
	}
	if !commissioningRunSafeCodeMatches(run.State, run.SafeCode) || commissioningv2.TerminalRunState(run.State) != (run.CompletedAt != nil) || (run.State == commissioningv2.RunStale) != (run.StaleAt != nil) || (run.State == commissioningv2.RunCompleted) != (run.GeneratedProfileID != nil) {
		return ErrInvalidIndexerCommissioning
	}
	requiresCancellationTime := run.State == commissioningv2.RunCancelRequested || run.State == commissioningv2.RunCancelled
	allowsCancellationTime := requiresCancellationTime || run.State == commissioningv2.RunOwnerAttention || run.State == commissioningv2.RunStale || run.State == commissioningv2.RunSafetyBlocked
	if requiresCancellationTime && run.CancelRequestedAt == nil || !allowsCancellationTime && run.CancelRequestedAt != nil {
		return ErrInvalidIndexerCommissioning
	}
	if run.State == commissioningv2.RunAwaitingCAPSAuthorization && run.CAPSAuthorizedAt != nil || run.State != commissioningv2.RunAwaitingCAPSAuthorization && run.State != commissioningv2.RunOwnerAttention && run.State != commissioningv2.RunCancelRequested && run.State != commissioningv2.RunCancelled && run.State != commissioningv2.RunStale && run.State != commissioningv2.RunSafetyBlocked && run.CAPSAuthorizedAt == nil {
		return ErrInvalidIndexerCommissioning
	}
	if (run.State == commissioningv2.RunSearchesAuthorized || run.State == commissioningv2.RunReadyForDraft || run.State == commissioningv2.RunCompleted) && run.SearchAuthorizedAt == nil {
		return ErrInvalidIndexerCommissioning
	}
	if run.CreatedAt.IsZero() || run.UpdatedAt.Before(run.CreatedAt) || !commissioningTimeWithin(run.CAPSAuthorizedAt, run.CreatedAt, run.UpdatedAt) || !commissioningTimeWithin(run.SearchAuthorizedAt, run.CreatedAt, run.UpdatedAt) || !commissioningTimeWithin(run.CancelRequestedAt, run.CreatedAt, run.UpdatedAt) || !commissioningTimeWithin(run.StaleAt, run.CreatedAt, run.UpdatedAt) || !commissioningTimeWithin(run.CompletedAt, run.CreatedAt, run.UpdatedAt) || run.CAPSAuthorizedAt != nil && run.SearchAuthorizedAt != nil && run.SearchAuthorizedAt.Before(*run.CAPSAuthorizedAt) {
		return ErrInvalidIndexerCommissioning
	}
	fixtureDigest, err := commissioningv2.FixtureDocumentDigest(run.Fixtures)
	if err != nil || fixtureDigest != run.CAPSPlan.FixtureSHA256 || !sameProviderContract(run.ProviderContract, run.CAPSPlan.ProviderContract) || commissioningv2.ValidateProbePlanForFixtures(run.CAPSPlan, run.Fixtures) != nil {
		return ErrInvalidIndexerCommissioning
	}
	if run.SearchPlan == nil {
		if run.PlannedAPICalls != 1 || len(run.CategorySelectionSHA256) != 0 || run.MovieCategoryIDs != nil || run.SeriesCategoryIDs != nil {
			return ErrInvalidIndexerCommissioning
		}
		return nil
	}
	plan := *run.SearchPlan
	if !sameCommissioningPlanIdentity(run, plan) || commissioningv2.ValidateProbePlanForFixtures(plan, run.Fixtures) != nil || plan.CategorySelectionSHA256 == nil || !bytes.Equal(run.CategorySelectionSHA256, plan.CategorySelectionSHA256.Bytes()) || run.PlannedAPICalls != plan.PlannedAPICalls {
		return ErrInvalidIndexerCommissioning
	}
	selection := newznab.NewznabCategorySelection{Movie: run.MovieCategoryIDs, Series: run.SeriesCategoryIDs, ReviewRequired: false}
	_, digest, err := commissioningv2.NormalizeCategorySelection(selection)
	if err != nil || !bytes.Equal(run.CategorySelectionSHA256, digest.Bytes()) {
		return ErrInvalidIndexerCommissioning
	}
	return nil
}

func commissioningTimeWithin(value *time.Time, lower, upper time.Time) bool {
	return value == nil || !value.Before(lower) && !value.After(upper)
}

func validateLoadedCommissioningStep(step IndexerCommissioningStep) error {
	if step.ID == (uuid.UUID{}) || step.RunID == (uuid.UUID{}) || !commissioningv2.ValidStepState(step.State) || !commissioningv2.ValidStepPhase(step.Phase) || step.BudgetFamily != commissioningv2.BudgetQuery || len(step.StructuralRequestSHA256) != 32 || step.Ordinal == 0 || step.Ordinal > commissioningv2.MaximumAPICalls || step.RequestLimit != step.Request.RequestLimit {
		return ErrInvalidIndexerCommissioning
	}
	// Reason, priority and CAPS basis live in the immutable plan rather than the
	// normalized step columns, so strict plan validation supplies those checks.
	if step.Phase == commissioningv2.StepPhaseCAPS {
		if step.Target != (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}) || step.RequestLimit != 0 || len(step.CategoryIDs) != 0 {
			return ErrInvalidIndexerCommissioning
		}
	} else if step.Target.Kind != newznab.EvidenceTargetSearchRecipe || step.Target.RecipeKey == "" || len(step.FixtureSHA256) != 32 {
		return ErrInvalidIndexerCommissioning
	}
	if step.SafeOutcome != "" && !commissioningv2.ValidSafeOutcome(step.SafeOutcome) {
		return ErrInvalidIndexerCommissioning
	}
	if !commissioningStepSafeCodeMatches(step.SafeOutcome, step.SafeCode) {
		return ErrInvalidIndexerCommissioning
	}
	terminal := commissioningv2.TerminalStepState(step.State)
	if terminal != (step.CompletedAt != nil) || terminal != (step.SafeOutcome != "") {
		return ErrInvalidIndexerCommissioning
	}
	if !terminal && (step.SafeCode != "" || step.RetryAt != nil || step.RawResultCount != nil || step.PlausibleIdentityCount != nil || len(step.SafeResponseSHA256) != 0 || len(step.RawBodySHA256) != 0 || step.EvidenceKey != "" || step.Evidence != nil) {
		return ErrInvalidIndexerCommissioning
	}
	if step.RetryAt != nil && step.State != commissioningv2.StepDeferredBeforeSend {
		return ErrInvalidIndexerCommissioning
	}
	hasLease := step.ExecutorLeaseOwner != "" || step.ExecutorLeaseUntil != nil
	if hasLease != (step.ExecutorLeaseOwner != "" && step.ExecutorLeaseUntil != nil) || hasLease != (step.State == commissioningv2.StepIntentCommitted || step.State == commissioningv2.StepTransmitting) {
		return ErrInvalidIndexerCommissioning
	}
	switch step.State {
	case commissioningv2.StepPlanned:
		if step.IntentAt != nil || step.TransmittedAt != nil {
			return ErrInvalidIndexerCommissioning
		}
	case commissioningv2.StepIntentCommitted, commissioningv2.StepDeferredBeforeSend:
		if step.IntentAt == nil || step.TransmittedAt != nil {
			return ErrInvalidIndexerCommissioning
		}
	case commissioningv2.StepTransmitting, commissioningv2.StepCompleted, commissioningv2.StepIndeterminate:
		if step.IntentAt == nil || step.TransmittedAt == nil {
			return ErrInvalidIndexerCommissioning
		}
	case commissioningv2.StepCancelled:
		if step.TransmittedAt != nil || step.SafeOutcome != commissioningv2.OutcomeCancelled {
			return ErrInvalidIndexerCommissioning
		}
	}
	if step.IntentAt != nil && step.TransmittedAt != nil && step.TransmittedAt.Before(*step.IntentAt) || step.IntentAt != nil && step.CompletedAt != nil && step.CompletedAt.Before(*step.IntentAt) || step.TransmittedAt != nil && step.CompletedAt != nil && step.CompletedAt.Before(*step.TransmittedAt) {
		return ErrInvalidIndexerCommissioning
	}
	if step.ExecutorLeaseUntil != nil && (step.IntentAt == nil || !step.ExecutorLeaseUntil.After(*step.IntentAt) || step.TransmittedAt != nil && !step.ExecutorLeaseUntil.After(*step.TransmittedAt)) {
		return ErrInvalidIndexerCommissioning
	}
	if step.State == commissioningv2.StepIndeterminate && step.SafeOutcome != commissioningv2.OutcomeIndeterminate {
		return ErrInvalidIndexerCommissioning
	}
	if step.State == commissioningv2.StepDeferredBeforeSend && !commissioningDeferredOutcome(step.SafeOutcome) {
		return ErrInvalidIndexerCommissioning
	}
	if commissioningSafetyOutcome(step.SafeOutcome) && (len(step.SafeResponseSHA256) != 0 || len(step.RawBodySHA256) != 0 || step.Evidence != nil) {
		return ErrInvalidIndexerCommissioning
	}
	if step.Evidence == nil {
		if step.EvidenceKey != "" || len(step.RawBodySHA256) != 0 || commissioningDurableEvidenceOutcome(step.SafeOutcome) {
			return ErrInvalidIndexerCommissioning
		}
	} else {
		if step.RawResultCount == nil || step.PlausibleIdentityCount == nil {
			return ErrInvalidIndexerCommissioning
		}
		evidence := *step.Evidence
		if !evidence.ObservedAt.Equal(evidence.ObservedAt.Truncate(time.Microsecond)) || evidence.Key != step.EvidenceKey || evidence.Target != step.Target || evidence.SafeOutcome != step.SafeOutcome || evidence.RawResultCount != optionalUint16(step.RawResultCount) || evidence.PlausibleIdentityCount != optionalUint16(step.PlausibleIdentityCount) || !bytes.Equal(step.RawBodySHA256, evidence.BodySHA256.Bytes()) || step.TransmittedAt == nil || step.CompletedAt == nil || evidence.ObservedAt.Before(*step.TransmittedAt) || evidence.ObservedAt.After(*step.CompletedAt) {
			return ErrInvalidIndexerCommissioning
		}
		if evidence.Target.Kind == newznab.EvidenceTargetCAPS && (evidence.CAPS == nil || !evidence.CAPS.ObservedAt.Equal(evidence.ObservedAt)) {
			return ErrInvalidIndexerCommissioning
		}
	}
	return nil
}

func optionalUint16(value *uint16) uint16 {
	if value == nil {
		return 0
	}
	return *value
}

func sameCommissioningPlanIdentity(run IndexerCommissioningRun, plan commissioningv2.ProbePlan) bool {
	return run.ProfileSchemaVersion == plan.ProfileSchemaVersion &&
		run.PlannerVersion == plan.PlannerVersion &&
		bytes.Equal(run.EndpointConfigSHA256, plan.ConfigurationSHA256.Bytes()) &&
		run.CAPSPlan.FixtureSHA256 == plan.FixtureSHA256 &&
		sameProviderContract(run.ProviderContract, plan.ProviderContract)
}

func commissioningCAPSEvidence(run IndexerCommissioningRun) *commissioningv2.StepEvidenceDocument {
	if len(run.Steps) != 0 && run.Steps[0].Target == (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}) {
		return run.Steps[0].Evidence
	}
	return nil
}

func commissioningCAPSStepComplete(run IndexerCommissioningRun) bool {
	if len(run.Steps) == 0 {
		return false
	}
	step := run.Steps[0]
	return step.State == commissioningv2.StepCompleted && step.SafeOutcome == commissioningv2.OutcomeCAPSObserved && step.Evidence != nil && commissioningv2.ValidateStepEvidence(*step.Evidence) == nil
}

func commissioningSearchStepsPlanned(run IndexerCommissioningRun) bool {
	if run.SearchPlan == nil || len(run.Steps) <= 1 {
		return false
	}
	for _, step := range run.Steps[1:] {
		if step.Phase != commissioningv2.StepPhaseSearch || step.State != commissioningv2.StepPlanned {
			return false
		}
	}
	return true
}

func commissioningSearchStepsComplete(run IndexerCommissioningRun) bool {
	if !commissioningCAPSStepComplete(run) || run.SearchPlan == nil || len(run.Steps) <= 1 {
		return false
	}
	for _, step := range run.Steps[1:] {
		if step.Phase != commissioningv2.StepPhaseSearch || step.State != commissioningv2.StepCompleted || step.Evidence == nil || !commissioningDurableEvidenceOutcome(step.SafeOutcome) || commissioningv2.ValidateStepEvidence(*step.Evidence) != nil {
			return false
		}
	}
	return true
}

func commissioningHasExecutingStep(run IndexerCommissioningRun) bool {
	for _, step := range run.Steps {
		if step.State == commissioningv2.StepIntentCommitted || step.State == commissioningv2.StepTransmitting {
			return true
		}
	}
	return false
}

func commissioningAllStepsTerminal(run IndexerCommissioningRun) bool {
	if len(run.Steps) == 0 {
		return false
	}
	for _, step := range run.Steps {
		if !commissioningv2.TerminalStepState(step.State) {
			return false
		}
	}
	return true
}

func commissioningStepIsNext(run IndexerCommissioningRun, candidate IndexerCommissioningStep) bool {
	found := false
	for _, step := range run.Steps {
		if step.ID == candidate.ID {
			found = true
			continue
		}
		if step.Ordinal < candidate.Ordinal && !commissioningStepPermitsNext(step) {
			return false
		}
		if step.Ordinal > candidate.Ordinal && step.State != commissioningv2.StepPlanned {
			return false
		}
	}
	return found
}

func commissioningStepPermitsNext(step IndexerCommissioningStep) bool {
	if step.State != commissioningv2.StepCompleted || step.Evidence == nil || commissioningv2.ValidateStepEvidence(*step.Evidence) != nil {
		return false
	}
	if step.Phase == commissioningv2.StepPhaseCAPS {
		return step.SafeOutcome == commissioningv2.OutcomeCAPSObserved
	}
	return step.Phase == commissioningv2.StepPhaseSearch && commissioningDurableEvidenceOutcome(step.SafeOutcome)
}

func validateCommissioningRunSteps(run IndexerCommissioningRun, steps []IndexerCommissioningStep) error {
	expected := append([]commissioningv2.ProbeStep(nil), run.CAPSPlan.Steps...)
	if run.SearchPlan != nil {
		expected = append(expected, run.SearchPlan.Steps...)
	}
	if len(steps) != len(expected) || len(expected) != int(run.PlannedAPICalls) {
		return ErrInvalidIndexerCommissioning
	}
	if run.SearchPlan != nil {
		capsEvidence := commissioningCAPSEvidence(IndexerCommissioningRun{Steps: steps})
		selection := newznab.NewznabCategorySelection{Movie: run.MovieCategoryIDs, Series: run.SeriesCategoryIDs, ReviewRequired: false}
		if capsEvidence == nil || commissioningv2.ValidateSearchPlanContext(*run.SearchPlan, run.Fixtures, *capsEvidence, selection) != nil {
			return ErrInvalidIndexerCommissioning
		}
	}
	for index := range expected {
		actual, planned := steps[index], expected[index]
		actualRequest, actualErr := commissioningv2.CanonicalTypedRequest(actual.Request)
		plannedRequest, plannedErr := commissioningv2.CanonicalTypedRequest(planned.Request)
		if actualErr != nil || plannedErr != nil || actual.Ordinal != planned.Ordinal || actual.Phase != planned.Phase || actual.Target != planned.Target || actual.Scope != planned.Scope || actual.Role != planned.Role || actual.FixtureKey != planned.FixtureKey || !bytes.Equal(actual.FixtureSHA256, optionalCommissioningDigestBytes(planned.FixtureSHA256)) || !bytes.Equal(actualRequest, plannedRequest) || !bytes.Equal(actual.StructuralRequestSHA256, planned.StructuralSHA256.Bytes()) || !equalIntSlices(actual.CategoryIDs, planned.CategoryIDs) || actual.RequestLimit != planned.Request.RequestLimit || actual.BudgetFamily != commissioningv2.BudgetQuery {
			return ErrInvalidIndexerCommissioning
		}
	}
	return nil
}

func optionalCommissioningDigestBytes(value *commissioningv2.SHA256Digest) []byte {
	if value == nil {
		return nil
	}
	return value.Bytes()
}

func equalIntSlices(left, right []int) bool {
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

func sameProviderContract(left, right *newznab.ProviderContractRef) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func commissioningProviderContractAligned(configuration []byte, contract *newznab.ProviderContractRef) bool {
	state, err := capabilityConfigurationState(configuration)
	return err == nil && providerContractAligned(state, contract)
}

func commissioningRunConfigurationCurrent(configuration, endpointDigest []byte, run IndexerCommissioningRun) bool {
	if !bytes.Equal(run.EndpointConfigSHA256, endpointDigest) || !commissioningProviderContractAligned(configuration, run.ProviderContract) {
		return false
	}
	if len(run.CategorySelectionSHA256) == 0 {
		return true
	}
	selection, err := commissioningCategorySelection(configuration)
	if err != nil || selection.ReviewRequired {
		return false
	}
	normalized, digest, err := commissioningv2.NormalizeCategorySelection(selection)
	return err == nil && bytes.Equal(run.CategorySelectionSHA256, digest.Bytes()) && equalIntSlices(run.MovieCategoryIDs, normalized.Movie) && equalIntSlices(run.SeriesCategoryIDs, normalized.Series)
}

func commissioningCategorySelection(configuration []byte) (newznab.NewznabCategorySelection, error) {
	var value struct {
		Movie          []int `json:"movie_category_ids"`
		Series         []int `json:"series_category_ids"`
		ReviewRequired *bool `json:"category_selection_review_required"`
	}
	if json.Unmarshal(configuration, &value) != nil || value.ReviewRequired == nil {
		return newznab.NewznabCategorySelection{}, ErrInvalidIndexerCommissioning
	}
	return newznab.NewznabCategorySelection{Movie: value.Movie, Series: value.Series, ReviewRequired: *value.ReviewRequired}, nil
}

func equalCommissioningCategories(left, right newznab.NewznabCategorySelection) bool {
	if left.ReviewRequired != right.ReviewRequired || len(left.Movie) != len(right.Movie) || len(left.Series) != len(right.Series) {
		return false
	}
	for index := range left.Movie {
		if left.Movie[index] != right.Movie[index] {
			return false
		}
	}
	for index := range left.Series {
		if left.Series[index] != right.Series[index] {
			return false
		}
	}
	return true
}

func allowedOfflineRunTransition(from, to commissioningv2.RunState) bool {
	if to == commissioningv2.RunOwnerAttention || to == commissioningv2.RunStale || to == commissioningv2.RunSafetyBlocked || to == commissioningv2.RunCancelRequested || from == commissioningv2.RunCancelRequested && to == commissioningv2.RunCancelled {
		return true
	}
	return from == commissioningv2.RunCAPSAuthorized && to == commissioningv2.RunAwaitingCategoryReview || from == commissioningv2.RunSearchesAuthorized && to == commissioningv2.RunReadyForDraft
}

func commissioningRunSafeCodeMatches(state commissioningv2.RunState, code commissioningv2.SafeCode) bool {
	if code == "" {
		return true
	}
	if !commissioningv2.ValidSafeCode(code) {
		return false
	}
	switch code {
	case commissioningv2.SafeCodeConfigurationChanged:
		return state == commissioningv2.RunStale
	case commissioningv2.SafeCodeOwnerCancelled:
		return state == commissioningv2.RunCancelRequested || state == commissioningv2.RunCancelled
	case commissioningv2.SafeCodeCredentialEcho:
		return state == commissioningv2.RunSafetyBlocked
	case commissioningv2.SafeCodeIndeterminateAttempt:
		return state == commissioningv2.RunOwnerAttention
	default:
		return false
	}
}

func commissioningStepSafeCodeMatches(outcome commissioningv2.SafeOutcome, code commissioningv2.SafeCode) bool {
	if code == "" {
		return true
	}
	if !commissioningv2.ValidSafeCode(code) {
		return false
	}
	switch code {
	case commissioningv2.SafeCodeConfigurationChanged:
		return outcome == commissioningv2.OutcomeConfigurationStale
	case commissioningv2.SafeCodeOwnerCancelled:
		return outcome == commissioningv2.OutcomeCancelled
	case commissioningv2.SafeCodeCredentialEcho:
		return outcome == commissioningv2.OutcomeSafetyCredentialEcho
	case commissioningv2.SafeCodeIndeterminateAttempt:
		return outcome == commissioningv2.OutcomeIndeterminate
	default:
		return false
	}
}

func commissioningTime(value time.Time) time.Time {
	if value.IsZero() {
		value = time.Now()
	}
	return value.UTC().Truncate(time.Microsecond)
}

func normalizeCommissioningEvidence(value *commissioningv2.StepEvidenceDocument) *commissioningv2.StepEvidenceDocument {
	if value == nil {
		return nil
	}
	normalized := *value
	normalized.ObservedAt = normalized.ObservedAt.UTC().Truncate(time.Microsecond)
	if normalized.CAPS != nil {
		caps := *normalized.CAPS
		caps.ObservedAt = caps.ObservedAt.UTC().Truncate(time.Microsecond)
		normalized.CAPS = &caps
	}
	return &normalized
}

func normalizeCommissioningTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC().Truncate(time.Microsecond)
	return &normalized
}
