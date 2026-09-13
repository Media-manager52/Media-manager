package commissioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// liveNewznabConfig is the owner-configured Newznab integration shape used by
// the Milestone-B owner-authorized CAPS path. It carries no secret value.
type liveNewznabConfig struct {
	BaseURL                 string `json:"base_url"`
	AllowHTTP               bool   `json:"allow_http"`
	MinimumIntervalMS       int64  `json:"minimum_interval_ms"`
	QueryLimit              int    `json:"query_limit"`
	GrabLimit               int    `json:"grab_limit"`
	BudgetWindow            int64  `json:"budget_window_seconds"`
	ProviderContractKey     string `json:"provider_contract_key"`
	ProviderContractVersion uint16 `json:"provider_contract_version"`
}

// LiveCAPSService is the production adapter for exactly one explicitly
// owner-authorized CAPS request. It is the only production constructor that
// wires a commissioningv2 LiveExecutor; every attempt still requires a
// separate, durable owner authorization before any HTTP may occur.
type LiveCAPSService struct {
	Store         *postgres.Store
	Scheduler     *indexers.Scheduler
	ResolveSecret SecretResolver
	// ClientFactory overrides ephemeral client/destination construction. It
	// exists so tests can inject a fake local TLS server (TestLoopback). The
	// production default never permits loopback and never accepts provider
	// data as a destination.
	ClientFactory func(integration postgres.Integration, values liveNewznabConfig, secret string) (*newznab.Client, newznab.CommissioningDestination, error)
	Now           func() time.Time
}

var errLiveCAPSNotConfigured = errors.New("commissioning v2 is not configured")

func (service *LiveCAPSService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func (service *LiveCAPSService) loadedIntegration(ctx context.Context, integrationID uuid.UUID) (postgres.Integration, liveNewznabConfig, error) {
	if service == nil || service.Store == nil || service.Scheduler == nil || service.ResolveSecret == nil {
		return postgres.Integration{}, liveNewznabConfig{}, errLiveCAPSNotConfigured
	}
	integration, err := service.Store.Integration(ctx, integrationID)
	if err != nil {
		return postgres.Integration{}, liveNewznabConfig{}, err
	}
	var values liveNewznabConfig
	if integration.Kind != "newznab" || json.Unmarshal(integration.Config, &values) != nil || values.BaseURL == "" || integration.SecretRef == "" {
		return postgres.Integration{}, liveNewznabConfig{}, errLiveCAPSNotConfigured
	}
	return integration, values, nil
}

// CreateRun builds and persists exactly one CAPS plan/step for the selected
// integration from owner-supplied fixtures. It is offline and sends nothing.
func (service *LiveCAPSService) CreateRun(ctx context.Context, integrationID uuid.UUID, fixtures commissioningv2.FixtureDocument) (postgres.IndexerCommissioningRun, error) {
	_, values, err := service.loadedIntegration(ctx, integrationID)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	digestBytes, err := service.Store.IndexerEndpointConfigurationDigest(ctx, integrationID)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	plan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{
		ConfigurationSHA256:  digest,
		ProviderContract:     liveProviderContract(values),
		Fixtures:             fixtures,
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	return service.Store.CreateIndexerCommissioningRun(ctx, integrationID, fixtures, plan, service.now())
}

// Run returns one consistent run-plus-steps snapshot.
func (service *LiveCAPSService) Run(ctx context.Context, runID uuid.UUID) (postgres.IndexerCommissioningRun, error) {
	if service == nil || service.Store == nil {
		return postgres.IndexerCommissioningRun{}, errLiveCAPSNotConfigured
	}
	return service.Store.IndexerCommissioningRun(ctx, runID)
}

// AuthorizeCAPS durably records the owner's explicit authorization of the
// exact run/plan. It sends nothing and is a one-time transition.
func (service *LiveCAPSService) AuthorizeCAPS(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCommissioningRun, error) {
	if service == nil || service.Store == nil {
		return postgres.IndexerCommissioningRun{}, errLiveCAPSNotConfigured
	}
	return service.Store.AuthorizeIndexerCommissioningCAPS(ctx, integrationID, runID, expectedRevision, "owner", service.now())
}

// AuthorizeSearches durably records the owner's explicit authorization of the
// sealed search plan. It sends nothing and is a one-time transition.
func (service *LiveCAPSService) AuthorizeSearches(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCommissioningRun, error) {
	if service == nil || service.Store == nil {
		return postgres.IndexerCommissioningRun{}, errLiveCAPSNotConfigured
	}
	return service.Store.AuthorizeIndexerCommissioningSearches(ctx, integrationID, runID, expectedRevision, "owner", service.now())
}

// SealSearchPlan freezes the owner-reviewed categories and the exact offline
// search plan (at most the hard four-probe ceiling) for one run. It sends
// nothing and performs no transmission.
func (service *LiveCAPSService) SealSearchPlan(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64, movieCategories, seriesCategories []int, includeMovieFallback, includeSeasonFallback bool) (postgres.IndexerCommissioningRun, error) {
	_, values, err := service.loadedIntegration(ctx, integrationID)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	run, err := service.Store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	if run.Revision != expectedRevision || run.State != commissioningv2.RunAwaitingCategoryReview {
		return postgres.IndexerCommissioningRun{}, postgres.ErrConcurrentModification
	}
	var capsEvidence *commissioningv2.StepEvidenceDocument
	for index := range run.Steps {
		if run.Steps[index].Phase == commissioningv2.StepPhaseCAPS && run.Steps[index].Evidence != nil {
			capsEvidence = run.Steps[index].Evidence
			break
		}
	}
	if capsEvidence == nil {
		return postgres.IndexerCommissioningRun{}, postgres.ErrInvalidIndexerCommissioning
	}
	digestBytes, err := service.Store.IndexerEndpointConfigurationDigest(ctx, integrationID)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	categories := newznab.NewznabCategorySelection{Movie: append([]int(nil), movieCategories...), Series: append([]int(nil), seriesCategories...)}
	plan, err := commissioningv2.BuildSearchPlan(commissioningv2.SearchPlanInput{
		ConfigurationSHA256:  digest,
		ProviderContract:     liveProviderContract(values),
		Fixtures:             run.Fixtures,
		CAPSEvidence:         *capsEvidence,
		Categories:           categories,
		Options:              commissioningv2.SearchProbeOptions{IncludeMovieFallback: includeMovieFallback, IncludeCompleteSeasonFallback: includeSeasonFallback},
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		return postgres.IndexerCommissioningRun{}, fmt.Errorf("build search plan: %w", err)
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > int(commissioningv2.MaximumSearchCalls) {
		return postgres.IndexerCommissioningRun{}, fmt.Errorf("search plan step count %d: %w", len(plan.Steps), commissioningv2.ErrInvalidCommissioningDocument)
	}
	if _, err := service.Store.SealIndexerCommissioningSearchPlan(ctx, integrationID, runID, expectedRevision, categories, plan, service.now()); err != nil {
		return postgres.IndexerCommissioningRun{}, err
	}
	return service.Store.IndexerCommissioningRun(ctx, runID)
}

// FinalizeDraft derives and persists one immutable capability-profile draft
// from the durable run evidence. It is entirely local: it resolves no
// credential, constructs no client, and performs no network work. A replay
// against an already completed run returns the existing linked draft.
func (service *LiveCAPSService) FinalizeDraft(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCommissioningRun, error) {
	if service == nil || service.Store == nil {
		return postgres.IndexerCommissioningRun{}, errLiveCAPSNotConfigured
	}
	return service.Store.FinalizeIndexerCommissioningDraft(ctx, integrationID, runID, expectedRevision, service.now())
}

// Draft reads the immutable draft linked from a completed run for exactly one
// integration. It performs no network work.
func (service *LiveCAPSService) Draft(ctx context.Context, integrationID, runID uuid.UUID) (postgres.IndexerCapabilityProfile, error) {
	if service == nil || service.Store == nil {
		return postgres.IndexerCapabilityProfile{}, errLiveCAPSNotConfigured
	}
	run, err := service.Store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		return postgres.IndexerCapabilityProfile{}, err
	}
	if run.IndexerInstanceID != integrationID || run.GeneratedProfileID == nil {
		return postgres.IndexerCapabilityProfile{}, postgres.ErrNotFound
	}
	return service.Store.IndexerCapabilityProfile(ctx, *run.GeneratedProfileID)
}

// ActivateProfile is the Milestone-E explicit owner activation gate. It is a
// second, separate owner action after draft generation and performs no
// acquisition, retrieval, publication, scheduler, credential, or search work.
//
// It locates the immutable draft linked from the completed run, binds the
// caller's expected profile revision, and delegates every freshness, evidence,
// category, and provider-contract recheck to the existing atomic
// ActivateIndexerCapabilityProfile repository transaction. An already-active
// linked profile is returned unchanged (idempotent, no second audit event).
func (service *LiveCAPSService) ActivateProfile(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCapabilityProfile, error) {
	if service == nil || service.Store == nil {
		return postgres.IndexerCapabilityProfile{}, errLiveCAPSNotConfigured
	}
	if integrationID == (uuid.UUID{}) || runID == (uuid.UUID{}) || expectedRevision == 0 {
		return postgres.IndexerCapabilityProfile{}, postgres.ErrInvalidIndexerCapabilityProfile
	}
	run, err := service.Store.IndexerCommissioningRun(ctx, runID)
	if err != nil {
		return postgres.IndexerCapabilityProfile{}, err
	}
	if run.IndexerInstanceID != integrationID || run.GeneratedProfileID == nil || run.State != commissioningv2.RunCompleted {
		return postgres.IndexerCapabilityProfile{}, postgres.ErrNotFound
	}
	profile, err := service.Store.IndexerCapabilityProfile(ctx, *run.GeneratedProfileID)
	if err != nil {
		return postgres.IndexerCapabilityProfile{}, err
	}
	// Profile revisions are immutable once persisted, so binding the observed
	// revision here lets the repository's own locked lifecycle check enforce
	// the rest of the activation gate atomically.
	if profile.IndexerInstanceID != integrationID || profile.Revision != expectedRevision {
		return postgres.IndexerCapabilityProfile{}, postgres.ErrConcurrentModification
	}
	return service.Store.ActivateIndexerCapabilityProfile(ctx, integrationID, profile.ID, "owner", service.now())
}

// ExecuteStep synchronously performs exactly one owner-authorized step (CAPS
// or one semantic probe) and returns only after it reached a safe durable
// state or failed closed. It never loops to a second step and never resends.
func (service *LiveCAPSService) ExecuteStep(ctx context.Context, integrationID, runID, stepID uuid.UUID) (commissioningv2.LiveExecuteResult, error) {
	integration, values, err := service.loadedIntegration(ctx, integrationID)
	if err != nil {
		return commissioningv2.LiveExecuteResult{}, err
	}
	secret, err := service.ResolveSecret(integration.SecretRef)
	if err != nil || secret == "" {
		return commissioningv2.LiveExecuteResult{}, commissioningv2.ErrLiveExecutorCredential
	}
	budget := indexers.NewznabBudget(values.MinimumIntervalMS, values.QueryLimit, values.GrabLimit, values.BudgetWindow)
	if err := service.Scheduler.Configure(integration.ID.String(), budget); err != nil {
		return commissioningv2.LiveExecuteResult{}, err
	}
	client, destination, err := service.client(integration, values, secret)
	if err != nil {
		return commissioningv2.LiveExecuteResult{}, err
	}
	snapshot, err := service.Store.LoadAuthorizedStep(ctx, stepID.String())
	if err != nil {
		return commissioningv2.LiveExecuteResult{}, err
	}
	if snapshot.RunID != runID.String() || snapshot.IndexerID != integrationID.String() {
		return commissioningv2.LiveExecuteResult{}, postgres.ErrInvalidIndexerCommissioning
	}
	executor, err := commissioningv2.NewLocalLiveExecutor(commissioningv2.LiveExecutorDeps{
		Steps:        service.Store,
		Scheduler:    service.Scheduler,
		Resolver:     commissioningv2.EnvironmentCredentialResolver{Resolve: service.ResolveSecret},
		Client:       client,
		Reservations: service.Store,
		Authorizer:   service.Store,
		Destination:  destination,
		Now:          service.now,
	})
	if err != nil {
		return commissioningv2.LiveExecuteResult{}, err
	}
	command := commissioningv2.LiveExecuteCommand{
		RunID:             snapshot.RunID,
		StepID:            snapshot.StepID,
		ExpectedRunRev:    snapshot.RunRevision,
		ExpectedStepState: commissioningv2.StepPlanned,
		Fingerprint:       snapshot.Fingerprint,
		PlannerVersion:    snapshot.PlannerVersion,
		LeaseOwner:        "owner-caps",
	}
	return executor.Execute(ctx, command)
}

// liveProviderContract maps the optional owner-selected provider contract to
// the plan input. An absent key means no contract (nil), which aligns with an
// integration config that has no provider_contract_key.
func liveProviderContract(values liveNewznabConfig) *newznab.ProviderContractRef {
	key := strings.TrimSpace(values.ProviderContractKey)
	if key == "" {
		return nil
	}
	version := values.ProviderContractVersion
	if version == 0 {
		version = 1
	}
	return &newznab.ProviderContractRef{Key: key, Version: version}
}

func (service *LiveCAPSService) client(integration postgres.Integration, values liveNewznabConfig, secret string) (*newznab.Client, newznab.CommissioningDestination, error) {
	if service.ClientFactory != nil {
		return service.ClientFactory(integration, values, secret)
	}
	timeout := 20 * time.Second
	client, err := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: values.BaseURL, APIKey: secret, AllowHTTP: values.AllowHTTP, Timeout: timeout}, service.Scheduler, nil)
	if err != nil {
		return nil, newznab.CommissioningDestination{}, err
	}
	// Production destination: no injected resolver/dialer, system roots, and
	// TestLoopback permanently false so provider data can never authorize a
	// loopback send.
	return client, newznab.CommissioningDestination{Timeout: timeout}, nil
}
