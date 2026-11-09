package commissioningstore

import (
	"bytes"
	"context"
	"errors"
	"github.com/media-manager/media-manager/internal/postgres/dbcore"
	"strings"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
)

var (
	ErrInvalidIndexerCommissioning       = errors.New("invalid indexer commissioning data")
	ErrIndexerCommissioningConfigChanged = errors.New("indexer commissioning configuration changed")
)

type IndexerCommissioningRun struct {
	ID                       uuid.UUID
	IndexerInstanceID        uuid.UUID
	State                    commissioningv2.RunState
	Revision                 uint64
	ProfileSchemaVersion     uint16
	PlannerVersion           uint16
	EndpointConfigSHA256     []byte
	ProviderContract         *newznab.ProviderContractRef
	Fixtures                 commissioningv2.FixtureDocument
	FixtureDocumentSHA256    []byte
	CAPSPlan                 commissioningv2.ProbePlan
	CAPSPlanDocumentSHA256   []byte
	CAPSAuthorizedAt         *time.Time
	CategorySelectionSHA256  []byte
	MovieCategoryIDs         []int
	SeriesCategoryIDs        []int
	SearchPlan               *commissioningv2.ProbePlan
	SearchPlanDocumentSHA256 []byte
	SearchAuthorizedAt       *time.Time
	MaximumAPICalls          uint8
	PlannedAPICalls          uint8
	SafeCode                 commissioningv2.SafeCode
	GeneratedProfileID       *uuid.UUID
	CreatedAt                time.Time
	UpdatedAt                time.Time
	CancelRequestedAt        *time.Time
	StaleAt                  *time.Time
	CompletedAt              *time.Time
	ConfigurationCurrent     bool
	Steps                    []IndexerCommissioningStep
}

type IndexerCommissioningStep struct {
	ID                      uuid.UUID
	RunID                   uuid.UUID
	Ordinal                 uint8
	Phase                   commissioningv2.StepPhase
	Target                  newznab.EvidenceTarget
	Scope                   newznab.SearchScope
	Role                    newznab.RecipeRole
	FixtureKey              string
	FixtureSHA256           []byte
	Request                 commissioningv2.TypedRequest
	StructuralRequestSHA256 []byte
	CategoryIDs             []int
	RequestLimit            uint16
	BudgetFamily            commissioningv2.BudgetFamily
	State                   commissioningv2.StepState
	IntentAt                *time.Time
	TransmittedAt           *time.Time
	CompletedAt             *time.Time
	ExecutorLeaseOwner      string
	ExecutorLeaseUntil      *time.Time
	SafeOutcome             commissioningv2.SafeOutcome
	SafeCode                commissioningv2.SafeCode
	RetryAt                 *time.Time
	RawResultCount          *uint16
	PlausibleIdentityCount  *uint16
	SafeResponseSHA256      []byte
	RawBodySHA256           []byte
	EvidenceKey             string
	Evidence                *commissioningv2.StepEvidenceDocument
}

type IndexerCommissioningStepTransition struct {
	State                  commissioningv2.StepState
	LeaseOwner             string
	LeaseUntil             *time.Time
	SafeOutcome            commissioningv2.SafeOutcome
	SafeCode               commissioningv2.SafeCode
	RetryAt                *time.Time
	RawResultCount         *uint16
	PlausibleIdentityCount *uint16
	SafeResponseSHA256     []byte
	RawBodySHA256          []byte
	Evidence               *commissioningv2.StepEvidenceDocument
}

const indexerCommissioningRunColumns = `run.id::text,run.indexer_instance_id::text,run.state,run.revision,run.profile_schema_version,run.planner_version,run.endpoint_config_sha256,run.provider_contract_key,run.provider_contract_version,run.fixture_document,run.fixture_sha256,run.caps_plan_document,run.caps_plan_sha256,run.caps_authorized_at,run.category_selection_sha256,run.movie_category_ids,run.series_category_ids,run.search_plan_document,run.search_plan_sha256,run.search_authorized_at,run.maximum_api_calls,run.planned_api_calls,COALESCE(run.safe_code,''),run.generated_profile_id::text,run.created_at,run.updated_at,run.cancel_requested_at,run.stale_at,run.completed_at`

const indexerCommissioningStepColumns = `step.id::text,step.run_id::text,step.ordinal,step.phase,step.target_kind,COALESCE(step.recipe_key,''),COALESCE(step.scope,''),COALESCE(step.role,''),COALESCE(step.fixture_key,''),step.fixture_sha256,step.typed_request_document,step.structural_request_sha256,step.category_ids,step.request_limit,step.budget_family,step.state,step.intent_at,step.transmitted_at,step.completed_at,COALESCE(step.executor_lease_owner,''),step.executor_lease_until,COALESCE(step.safe_outcome,''),COALESCE(step.safe_code,''),step.retry_at,step.raw_result_count,step.plausible_identity_count,step.safe_response_sha256,step.raw_body_sha256,COALESCE(step.evidence_key,''),step.evidence_document`

// CreateIndexerCommissioningRun persists only an immutable offline plan. It
// creates no worker, outbox event, profile, authorization, or network request.
func (store *Commissioning) CreateIndexerCommissioningRun(ctx context.Context, indexerID uuid.UUID, fixtures commissioningv2.FixtureDocument, capsPlan commissioningv2.ProbePlan, now time.Time) (IndexerCommissioningRun, error) {
	fixtureDocument, err := commissioningv2.CanonicalFixtureDocument(fixtures)
	if err != nil || indexerID == (uuid.UUID{}) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	planDocument, err := commissioningv2.CanonicalProbePlan(capsPlan)
	if err != nil || capsPlan.Phase != commissioningv2.PlanPhaseCAPS || len(capsPlan.Steps) != 1 {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	fixtureDigest, err := commissioningv2.FixtureDocumentDigest(fixtures)
	if err != nil || fixtureDigest != capsPlan.FixtureSHA256 || commissioningv2.ValidateProbePlanForFixtures(capsPlan, fixtures) != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	requestDocument, err := commissioningv2.CanonicalTypedRequest(capsPlan.Steps[0].Request)
	if err != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, currentDigest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if !bytes.Equal(currentDigest, capsPlan.ConfigurationSHA256.Bytes()) || !commissioningProviderContractAligned(configuration, capsPlan.ProviderContract) {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningConfigChanged
	}
	runID := uuid.NewV7()
	var contractKey, contractVersion any
	if capsPlan.ProviderContract != nil {
		contractKey, contractVersion = capsPlan.ProviderContract.Key, int16(capsPlan.ProviderContract.Version)
	}
	fixtureDocumentDigest := commissioningv2.SumSHA256(fixtureDocument)
	capsPlanDocumentDigest := commissioningv2.SumSHA256(planDocument)
	if _, err := transaction.Exec(ctx, `INSERT INTO indexer_commissioning_runs(
		id,indexer_instance_id,state,revision,profile_schema_version,planner_version,
		endpoint_config_sha256,provider_contract_key,provider_contract_version,
		fixture_document,fixture_sha256,caps_plan_document,caps_plan_sha256,
		maximum_api_calls,planned_api_calls,created_at,updated_at)
		VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,$14)`,
		runID.String(), indexerID.String(), string(commissioningv2.RunAwaitingCAPSAuthorization),
		int16(capsPlan.ProfileSchemaVersion), int16(capsPlan.PlannerVersion), capsPlan.ConfigurationSHA256.Bytes(),
		contractKey, contractVersion, string(fixtureDocument), fixtureDocumentDigest.Bytes(),
		string(planDocument), capsPlanDocumentDigest.Bytes(), int16(capsPlan.MaximumAPICalls), now); err != nil {
		return IndexerCommissioningRun{}, err
	}
	if err := insertIndexerCommissioningStep(ctx, transaction, runID, capsPlan.Steps[0], requestDocument); err != nil {
		return IndexerCommissioningRun{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningRun{}, err
	}
	return store.IndexerCommissioningRun(ctx, runID)
}

func (store *Commissioning) IndexerCommissioningRun(ctx context.Context, runID uuid.UUID) (IndexerCommissioningRun, error) {
	if runID == (uuid.UUID{}) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	defer transaction.Rollback(ctx)
	row := transaction.QueryRow(ctx, `SELECT `+indexerCommissioningRunColumns+`,
		run.endpoint_config_sha256 IS NOT DISTINCT FROM media_manager_newznab_endpoint_config_sha256(integration.config,integration.secret_ref),
		integration.config
		FROM indexer_commissioning_runs run
		JOIN integration_instances integration ON integration.id=run.indexer_instance_id AND integration.kind='newznab'
		WHERE run.id=$1`, runID.String())
	result, configuration, digestCurrent, err := scanIndexerCommissioningRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return IndexerCommissioningRun{}, dbcore.ErrNotFound
	}
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	result.ConfigurationCurrent = digestCurrent && commissioningRunConfigurationCurrent(configuration, result.EndpointConfigSHA256, result)
	result.Steps, err = listIndexerCommissioningSteps(ctx, transaction, result.ID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if validateCommissioningRunSteps(result, result.Steps) != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningRun{}, err
	}
	return result, nil
}

func (store *Commissioning) ListIndexerCommissioningRuns(ctx context.Context, indexerID uuid.UUID) ([]IndexerCommissioningRun, error) {
	if indexerID == (uuid.UUID{}) {
		return nil, ErrInvalidIndexerCommissioning
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, `SELECT `+indexerCommissioningRunColumns+`,
		run.endpoint_config_sha256 IS NOT DISTINCT FROM media_manager_newznab_endpoint_config_sha256(integration.config,integration.secret_ref),
		integration.config
		FROM indexer_commissioning_runs run
		JOIN integration_instances integration ON integration.id=run.indexer_instance_id AND integration.kind='newznab'
		WHERE run.indexer_instance_id=$1 ORDER BY run.created_at,run.id`, indexerID.String())
	if err != nil {
		return nil, err
	}
	result := make([]IndexerCommissioningRun, 0)
	configurations := make([][]byte, 0)
	digestCurrent := make([]bool, 0)
	for rows.Next() {
		run, configuration, current, err := scanIndexerCommissioningRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, run)
		configurations = append(configurations, configuration)
		digestCurrent = append(digestCurrent, current)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range result {
		run := &result[index]
		run.ConfigurationCurrent = digestCurrent[index] && commissioningRunConfigurationCurrent(configurations[index], run.EndpointConfigSHA256, *run)
		run.Steps, err = listIndexerCommissioningSteps(ctx, transaction, run.ID)
		if err != nil || validateCommissioningRunSteps(*run, run.Steps) != nil {
			return nil, ErrInvalidIndexerCommissioning
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (store *Commissioning) AuthorizeIndexerCommissioningCAPS(ctx context.Context, indexerID, runID uuid.UUID, expectedRevision uint64, actor string, now time.Time) (IndexerCommissioningRun, error) {
	return store.authorizeIndexerCommissioningPhase(ctx, indexerID, runID, expectedRevision, actor, true, now)
}

func (store *Commissioning) AuthorizeIndexerCommissioningSearches(ctx context.Context, indexerID, runID uuid.UUID, expectedRevision uint64, actor string, now time.Time) (IndexerCommissioningRun, error) {
	return store.authorizeIndexerCommissioningPhase(ctx, indexerID, runID, expectedRevision, actor, false, now)
}

func (store *Commissioning) authorizeIndexerCommissioningPhase(ctx context.Context, indexerID, runID uuid.UUID, expectedRevision uint64, actor string, caps bool, now time.Time) (IndexerCommissioningRun, error) {
	if indexerID == (uuid.UUID{}) || runID == (uuid.UUID{}) || expectedRevision == 0 || strings.TrimSpace(actor) != "owner" {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerID, runID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if run.Revision != expectedRevision {
		return IndexerCommissioningRun{}, dbcore.ErrConcurrentModification
	}
	if !now.After(run.UpdatedAt) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningRunConfigurationCurrent(configuration, digest, run) {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningConfigChanged
	}
	from, to := commissioningv2.RunAwaitingCAPSAuthorization, commissioningv2.RunCAPSAuthorized
	columnTime, columnActor := "caps_authorized_at", "caps_authorized_by"
	if !caps {
		from, to = commissioningv2.RunAwaitingSearchAuthorization, commissioningv2.RunSearchesAuthorized
		columnTime, columnActor = "search_authorized_at", "search_authorized_by"
	}
	if run.State != from || !commissioningv2.ValidRunTransition(from, to) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if caps {
		if len(run.Steps) != 1 || run.Steps[0].State != commissioningv2.StepPlanned {
			return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
		}
	} else if !commissioningCAPSStepComplete(run) || !commissioningSearchStepsPlanned(run) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state=$4,revision=revision+1,`+columnTime+`=$5,`+columnActor+`='owner',updated_at=$5 WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`, runID.String(), indexerID.String(), int64(expectedRevision), string(to), now)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if command.RowsAffected() != 1 {
		return IndexerCommissioningRun{}, dbcore.ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningRun{}, err
	}
	return store.IndexerCommissioningRun(ctx, runID)
}

// SealIndexerCommissioningSearchPlan freezes the owner-reviewed categories and
// exact offline search plan in one transaction. It starts no execution.
func (store *Commissioning) SealIndexerCommissioningSearchPlan(ctx context.Context, indexerID, runID uuid.UUID, expectedRevision uint64, categories newznab.NewznabCategorySelection, searchPlan commissioningv2.ProbePlan, now time.Time) (IndexerCommissioningRun, error) {
	planDocument, err := commissioningv2.CanonicalProbePlan(searchPlan)
	if err != nil || searchPlan.Phase != commissioningv2.PlanPhaseSearch || searchPlan.CategorySelectionSHA256 == nil || expectedRevision == 0 {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	normalizedCategories, categoryDigest, err := commissioningv2.NormalizeCategorySelection(categories)
	if err != nil || categoryDigest != *searchPlan.CategorySelectionSHA256 {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	requestDocuments := make([][]byte, len(searchPlan.Steps))
	for index, step := range searchPlan.Steps {
		requestDocuments[index], err = commissioningv2.CanonicalTypedRequest(step.Request)
		if err != nil {
			return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
		}
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	configuredCategories, err := commissioningCategorySelection(configuration)
	if err != nil || configuredCategories.ReviewRequired || !equalCommissioningCategories(configuredCategories, normalizedCategories) {
		return IndexerCommissioningRun{}, ErrIndexerCategoryReviewRequired
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerID, runID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if run.Revision != expectedRevision {
		return IndexerCommissioningRun{}, dbcore.ErrConcurrentModification
	}
	if !now.After(run.UpdatedAt) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	capsEvidence := commissioningCAPSEvidence(run)
	if run.State != commissioningv2.RunAwaitingCategoryReview || !bytes.Equal(run.EndpointConfigSHA256, digest) || !commissioningProviderContractAligned(configuration, run.ProviderContract) || !sameCommissioningPlanIdentity(run, searchPlan) || capsEvidence == nil || commissioningv2.ValidateSearchPlanContext(searchPlan, run.Fixtures, *capsEvidence, normalizedCategories) != nil {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningConfigChanged
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state=$4,revision=revision+1,category_selection_sha256=$5,
		movie_category_ids=$6,series_category_ids=$7,
		search_plan_document=$8,search_plan_sha256=$9,
		planned_api_calls=$10,updated_at=$11
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`,
		runID.String(), indexerID.String(), int64(expectedRevision), string(commissioningv2.RunAwaitingSearchAuthorization),
		categoryDigest.Bytes(), normalizedCategories.Movie, normalizedCategories.Series, string(planDocument),
		commissioningv2.SumSHA256(planDocument).Bytes(), int16(searchPlan.PlannedAPICalls), now)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if command.RowsAffected() != 1 {
		return IndexerCommissioningRun{}, dbcore.ErrConcurrentModification
	}
	for index, step := range searchPlan.Steps {
		if err := insertIndexerCommissioningStep(ctx, transaction, runID, step, requestDocuments[index]); err != nil {
			return IndexerCommissioningRun{}, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningRun{}, err
	}
	return store.IndexerCommissioningRun(ctx, runID)
}

// TransitionIndexerCommissioningRun exposes only local lifecycle transitions.
// It intentionally does not expose ready_for_draft -> completed, because draft
// creation and profile linkage are outside this offline milestone.
func (store *Commissioning) TransitionIndexerCommissioningRun(ctx context.Context, indexerID, runID uuid.UUID, expectedRevision uint64, to commissioningv2.RunState, safeCode commissioningv2.SafeCode, now time.Time) (IndexerCommissioningRun, error) {
	if indexerID == (uuid.UUID{}) || runID == (uuid.UUID{}) || expectedRevision == 0 || !commissioningv2.ValidRunState(to) || to == commissioningv2.RunCompleted || !commissioningRunSafeCodeMatches(to, safeCode) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerID, runID)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if run.Revision != expectedRevision {
		return IndexerCommissioningRun{}, dbcore.ErrConcurrentModification
	}
	if !now.After(run.UpdatedAt) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningv2.ValidRunTransition(run.State, to) || !allowedOfflineRunTransition(run.State, to) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if run.State == commissioningv2.RunCAPSAuthorized && to == commissioningv2.RunAwaitingCategoryReview && !commissioningCAPSStepComplete(run) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if run.State == commissioningv2.RunSearchesAuthorized && to == commissioningv2.RunReadyForDraft && !commissioningSearchStepsComplete(run) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if run.State == commissioningv2.RunCancelRequested && to == commissioningv2.RunCancelled && !commissioningAllStepsTerminal(run) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if commissioningv2.TerminalRunState(to) && commissioningHasExecutingStep(run) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if to != commissioningv2.RunStale && to != commissioningv2.RunOwnerAttention && to != commissioningv2.RunSafetyBlocked && to != commissioningv2.RunCancelRequested && to != commissioningv2.RunCancelled && !commissioningRunConfigurationCurrent(configuration, digest, run) {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningConfigChanged
	}
	setCancel := to == commissioningv2.RunCancelRequested
	terminal := commissioningv2.TerminalRunState(to)
	setStale := to == commissioningv2.RunStale
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state=$4,revision=revision+1,safe_code=NULLIF($5,''),updated_at=$6,
		cancel_requested_at=CASE WHEN $7 THEN $6 ELSE cancel_requested_at END,
		stale_at=CASE WHEN $8 THEN $6 ELSE stale_at END,
		completed_at=CASE WHEN $9 THEN $6 ELSE completed_at END
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`, runID.String(), indexerID.String(), int64(expectedRevision), string(to), string(safeCode), now, setCancel, setStale, terminal)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if command.RowsAffected() != 1 {
		return IndexerCommissioningRun{}, dbcore.ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningRun{}, err
	}
	return store.IndexerCommissioningRun(ctx, runID)
}

// TransitionIndexerCommissioningStep persists only an explicitly requested
// local state change. Nothing calls this method from a worker in this
// milestone, and it performs no scheduler or network operation.
func (store *Commissioning) TransitionIndexerCommissioningStep(ctx context.Context, indexerID, runID, stepID uuid.UUID, expectedState commissioningv2.StepState, transition IndexerCommissioningStepTransition, now time.Time) (IndexerCommissioningStep, error) {
	if indexerID == (uuid.UUID{}) || runID == (uuid.UUID{}) || stepID == (uuid.UUID{}) || !commissioningv2.ValidStepState(expectedState) || !commissioningv2.ValidStepState(transition.State) || !commissioningv2.ValidStepTransition(expectedState, transition.State) {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	now = commissioningTime(now)
	transition.LeaseUntil = normalizeCommissioningTimePointer(transition.LeaseUntil)
	transition.RetryAt = normalizeCommissioningTimePointer(transition.RetryAt)
	transition.Evidence = normalizeCommissioningEvidence(transition.Evidence)
	evidenceDocument, err := validateCommissioningStepTransition(transition, now)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	defer transaction.Rollback(ctx)
	configuration, digest, err := lockCommissioningIntegration(ctx, transaction, indexerID)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	run, err := lockIndexerCommissioningRun(ctx, transaction, indexerID, runID)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	step, err := lockIndexerCommissioningStep(ctx, transaction, runID, stepID)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	if step.State != expectedState {
		return IndexerCommissioningStep{}, dbcore.ErrConcurrentModification
	}
	if err := validateCommissioningTransitionForStep(step, transition); err != nil {
		return IndexerCommissioningStep{}, err
	}
	if !commissioningRunConfigurationCurrent(configuration, digest, run) {
		mayCloseTransmittedAttempt := expectedState == commissioningv2.StepTransmitting && commissioningv2.TerminalStepState(transition.State)
		mayCancel := transition.State == commissioningv2.StepCancelled
		mayRecordPreSendStale := transition.State == commissioningv2.StepDeferredBeforeSend && transition.SafeOutcome == commissioningv2.OutcomeConfigurationStale
		if !mayCloseTransmittedAttempt && !mayCancel && !mayRecordPreSendStale {
			return IndexerCommissioningStep{}, ErrIndexerCommissioningConfigChanged
		}
	}
	if transition.State == commissioningv2.StepIntentCommitted || transition.State == commissioningv2.StepTransmitting {
		if step.Phase == commissioningv2.StepPhaseCAPS && run.State != commissioningv2.RunCAPSAuthorized || step.Phase == commissioningv2.StepPhaseSearch && run.State != commissioningv2.RunSearchesAuthorized {
			return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
		}
	}
	if transition.State == commissioningv2.StepTransmitting && (step.ExecutorLeaseUntil == nil || !step.ExecutorLeaseUntil.After(now) || step.ExecutorLeaseOwner == "") {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	if transition.State == commissioningv2.StepIntentCommitted && !commissioningStepIsNext(run, step) {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	if transition.State == commissioningv2.StepCancelled && run.State != commissioningv2.RunCancelRequested {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	if step.IntentAt != nil && now.Before(*step.IntentAt) || step.TransmittedAt != nil && now.Before(*step.TransmittedAt) {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	var leaseOwner any
	var leaseUntil any
	if transition.State == commissioningv2.StepIntentCommitted {
		leaseOwner, leaseUntil = transition.LeaseOwner, transition.LeaseUntil
	} else if transition.State == commissioningv2.StepTransmitting {
		leaseOwner, leaseUntil = step.ExecutorLeaseOwner, step.ExecutorLeaseUntil
	}
	var completedAt any
	if commissioningv2.TerminalStepState(transition.State) {
		completedAt = now
	}
	var intentAt, transmittedAt any
	if transition.State == commissioningv2.StepIntentCommitted {
		intentAt = now
	} else {
		intentAt = step.IntentAt
	}
	if transition.State == commissioningv2.StepTransmitting {
		transmittedAt = now
	} else {
		transmittedAt = step.TransmittedAt
	}
	var rawResultCount, plausibleCount any
	if transition.RawResultCount != nil {
		rawResultCount = int16(*transition.RawResultCount)
	}
	if transition.PlausibleIdentityCount != nil {
		plausibleCount = int16(*transition.PlausibleIdentityCount)
	}
	var safeResponseDigest, rawBodyDigest any
	if len(transition.SafeResponseSHA256) != 0 {
		safeResponseDigest = transition.SafeResponseSHA256
	}
	if len(transition.RawBodySHA256) != 0 {
		rawBodyDigest = transition.RawBodySHA256
	}
	var evidenceKey, evidenceValue any
	if transition.Evidence != nil {
		evidenceKey, evidenceValue = transition.Evidence.Key, string(evidenceDocument)
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_steps SET
		state=$4,intent_at=$5,transmitted_at=$6,completed_at=$7,
		executor_lease_owner=$8,executor_lease_until=$9,
		safe_outcome=NULLIF($10,''),safe_code=NULLIF($11,''),retry_at=$12,
		raw_result_count=$13,plausible_identity_count=$14,
		safe_response_sha256=$15,raw_body_sha256=$16,
		evidence_key=$17,evidence_document=$18
		WHERE id=$1 AND run_id=$2 AND state=$3`, stepID.String(), runID.String(), string(expectedState),
		string(transition.State), intentAt, transmittedAt, completedAt, leaseOwner, leaseUntil,
		string(transition.SafeOutcome), string(transition.SafeCode), transition.RetryAt,
		rawResultCount, plausibleCount, safeResponseDigest, rawBodyDigest, evidenceKey, evidenceValue)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	if command.RowsAffected() != 1 {
		return IndexerCommissioningStep{}, dbcore.ErrConcurrentModification
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningStep{}, err
	}
	return store.IndexerCommissioningStep(ctx, runID, stepID)
}

func (store *Commissioning) IndexerCommissioningStep(ctx context.Context, runID, stepID uuid.UUID) (IndexerCommissioningStep, error) {
	if runID == (uuid.UUID{}) || stepID == (uuid.UUID{}) {
		return IndexerCommissioningStep{}, ErrInvalidIndexerCommissioning
	}
	run, err := store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		return IndexerCommissioningStep{}, err
	}
	for _, step := range run.Steps {
		if step.ID == stepID {
			return step, nil
		}
	}
	return IndexerCommissioningStep{}, dbcore.ErrNotFound
}

type commissioningQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func listIndexerCommissioningSteps(ctx context.Context, queryer commissioningQueryer, runID uuid.UUID) ([]IndexerCommissioningStep, error) {
	rows, err := queryer.Query(ctx, `SELECT `+indexerCommissioningStepColumns+` FROM indexer_commissioning_steps step WHERE step.run_id=$1 ORDER BY step.ordinal`, runID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]IndexerCommissioningStep, 0)
	for rows.Next() {
		step, err := scanIndexerCommissioningStep(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, step)
	}
	return result, rows.Err()
}
