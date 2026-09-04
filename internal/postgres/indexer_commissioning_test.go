package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"uuid"
)

func TestIndexerCommissioningRepositoryOfflineLifecycle(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(123 * time.Nanosecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := commissioningRepositoryFixtures()
	capsPlan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{
		ConfigurationSHA256:  digest,
		Fixtures:             fixtures,
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, capsPlan, now)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != commissioningv2.RunAwaitingCAPSAuthorization || run.Revision != 1 || !run.ConfigurationCurrent || len(run.Steps) != 1 || run.Steps[0].State != commissioningv2.StepPlanned {
		t.Fatalf("created run=%#v steps=%#v", run, run.Steps)
	}
	if run.CreatedAt.Nanosecond()%1000 != 0 || run.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("repository did not normalize run timestamps to PostgreSQL precision: created=%s updated=%s", run.CreatedAt, run.UpdatedAt)
	}
	if _, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, capsPlan, now); err == nil {
		t.Fatal("created a second nonterminal run")
	}
	if _, err := store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunStale, commissioningv2.SafeCode("provider_token"), now.Add(time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("accepted arbitrary regex-safe run code: %v", err)
	}
	if _, err := store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunStale, commissioningv2.SafeCodeCredentialEcho, now.Add(time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("accepted a run code for the wrong state: %v", err)
	}

	if _, err := store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", run.UpdatedAt); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("equal-time run mutation replaced existing state: %v", err)
	}
	if _, err := store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", run.UpdatedAt.Add(-time.Microsecond)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("backward-time run mutation replaced existing state: %v", err)
	}
	run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Second))
	if err != nil || run.State != commissioningv2.RunCAPSAuthorized || run.CAPSAuthorizedAt == nil {
		t.Fatalf("authorize CAPS run=%#v err=%v", run, err)
	}
	if _, err := store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunAwaitingCategoryReview, "", now.Add(2*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("advanced without CAPS evidence: %v", err)
	}

	capsEvidence := commissioningRepositoryCAPSEvidence(now.Add(4 * time.Second))
	capsStep := run.Steps[0]
	capsStep = transitionCommissioningStepToTransmission(t, ctx, store, indexerID, run.ID, capsStep, now.Add(2*time.Second))
	zero := uint16(0)
	capsStep, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, capsStep.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
		State:                  commissioningv2.StepCompleted,
		SafeOutcome:            commissioningv2.OutcomeCAPSObserved,
		RawResultCount:         &zero,
		PlausibleIdentityCount: &zero,
		SafeResponseSHA256:     commissioningv2.SumSHA256([]byte("safe CAPS projection")).Bytes(),
		RawBodySHA256:          capsEvidence.BodySHA256.Bytes(),
		Evidence:               &capsEvidence,
	}, now.Add(4*time.Second))
	if err != nil || capsStep.Evidence == nil {
		t.Fatalf("complete CAPS step=%#v err=%v", capsStep, err)
	}
	if capsStep.CompletedAt == nil || capsStep.Evidence.ObservedAt.Nanosecond()%1000 != 0 || !capsStep.Evidence.ObservedAt.Equal(*capsStep.CompletedAt) {
		t.Fatalf("non-microsecond evidence did not round-trip consistently: completed=%v observed=%s", capsStep.CompletedAt, capsStep.Evidence.ObservedAt)
	}
	capsEvidence = *capsStep.Evidence

	run, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunAwaitingCategoryReview, "", now.Add(5*time.Second))
	if err != nil || run.State != commissioningv2.RunAwaitingCategoryReview {
		t.Fatalf("await category review run=%#v err=%v", run, err)
	}
	categories := newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}}
	searchPlan, err := commissioningv2.BuildSearchPlan(commissioningv2.SearchPlanInput{
		ConfigurationSHA256: digest,
		Fixtures:            fixtures,
		CAPSEvidence:        capsEvidence,
		Categories:          categories,
		Options: commissioningv2.SearchProbeOptions{
			IncludeMovieFallback:          true,
			IncludeCompleteSeasonFallback: true,
		},
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.SealIndexerCommissioningSearchPlan(ctx, indexerID, run.ID, run.Revision, categories, searchPlan, now.Add(6*time.Second))
	if err != nil || run.State != commissioningv2.RunAwaitingSearchAuthorization || run.SearchPlan == nil || len(run.Steps) != int(searchPlan.PlannedAPICalls) {
		t.Fatalf("seal search plan run=%#v err=%v", run, err)
	}

	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{movie_category_ids}','[2010]'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	stale, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil || stale.ConfigurationCurrent {
		t.Fatalf("category edit did not stale read model: current=%v err=%v", stale.ConfigurationCurrent, err)
	}
	if _, err := store.AuthorizeIndexerCommissioningSearches(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(7*time.Second)); !errors.Is(err, ErrIndexerCommissioningConfigChanged) {
		t.Fatalf("authorized stale category plan: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{movie_category_ids}','[2000]'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningSearches(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(8*time.Second))
	if err != nil || run.State != commissioningv2.RunSearchesAuthorized {
		t.Fatalf("authorize searches run=%#v err=%v", run, err)
	}
	if _, err := store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunReadyForDraft, "", now.Add(9*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("advanced with planned search steps: %v", err)
	}

	for index := 1; index < len(run.Steps); index++ {
		if index == 1 && len(run.Steps) > 2 {
			leaseUntil := now.Add(time.Minute)
			if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, run.Steps[2].ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{
				State:      commissioningv2.StepIntentCommitted,
				LeaseOwner: "worker.out_of_order",
				LeaseUntil: &leaseUntil,
			}, now.Add(10*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
				t.Fatalf("started a later commissioning step before ordinal 2: %v", err)
			}
		}
		step := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, run.ID, run.Steps[index], now.Add(time.Duration(10+index*3)*time.Second))
		evidenceKey := "evidence." + strings.Repeat(string("abcdef0123456789"[index%16]), 32)
		bodyDigest := commissioningv2.SumSHA256([]byte(evidenceKey))
		evidence := commissioningv2.StepEvidenceDocument{
			SchemaVersion:          commissioningv2.DocumentSchemaVersion,
			Key:                    evidenceKey,
			Kind:                   newznab.EvidenceKindControlledProbe,
			Target:                 step.Target,
			ObservedAt:             now.Add(time.Duration(12+index*3) * time.Second),
			SafeOutcome:            commissioningv2.OutcomeNoResults,
			RawResultCount:         0,
			PlausibleIdentityCount: 0,
			BodySHA256:             bodyDigest,
		}
		if index == 1 {
			if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
				State:                  commissioningv2.StepCompleted,
				SafeOutcome:            commissioningv2.OutcomeNoResults,
				SafeCode:               commissioningv2.SafeCodeOwnerCancelled,
				RawResultCount:         &zero,
				PlausibleIdentityCount: &zero,
				RawBodySHA256:          bodyDigest.Bytes(),
				Evidence:               &evidence,
			}, evidence.ObservedAt); !errors.Is(err, ErrInvalidIndexerCommissioning) {
				t.Fatalf("accepted a step code for the wrong outcome: %v", err)
			}
			if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
				State:         commissioningv2.StepCompleted,
				SafeOutcome:   commissioningv2.OutcomeNoResults,
				RawBodySHA256: bodyDigest.Bytes(),
				Evidence:      &evidence,
			}, evidence.ObservedAt); !errors.Is(err, ErrInvalidIndexerCommissioning) {
				t.Fatalf("completed search evidence without explicit counts: %v", err)
			}
		}
		if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
			State:                  commissioningv2.StepCompleted,
			SafeOutcome:            commissioningv2.OutcomeNoResults,
			RawResultCount:         &zero,
			PlausibleIdentityCount: &zero,
			RawBodySHA256:          bodyDigest.Bytes(),
			Evidence:               &evidence,
		}, evidence.ObservedAt); err != nil {
			t.Fatalf("complete search step %d: %v", index, err)
		}
	}
	run, err = store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunReadyForDraft, "", now.Add(30*time.Second))
	if err != nil || run.State != commissioningv2.RunReadyForDraft {
		t.Fatalf("ready for draft run=%#v err=%v", run, err)
	}
	if _, err := store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunCompleted, "", now.Add(31*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("offline foundation exposed completion: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET typed_request_document='{}'::jsonb WHERE id=$1`, run.Steps[0].ID.String()); err == nil {
		t.Fatal("database changed immutable step request")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET evidence_document=jsonb_set(evidence_document,'{safe_outcome}','"contradicted"'::jsonb) WHERE run_id=$1 AND evidence_key IS NOT NULL`, run.ID.String()); err == nil {
		t.Fatal("database changed immutable completed-step evidence")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET secret_ref='COMMISSIONING_REPOSITORY_OTHER_KEY' WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	staleAccount, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil || staleAccount.ConfigurationCurrent {
		t.Fatalf("secret-reference edit did not stale run: current=%v err=%v", staleAccount.ConfigurationCurrent, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET secret_ref='COMMISSIONING_REPOSITORY_KEY',config=jsonb_set(config,'{provider_contract_key}','"nzbfinder"'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	staleContract, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil || staleContract.ConfigurationCurrent {
		t.Fatalf("provider-contract edit did not stale run: current=%v err=%v", staleContract.ConfigurationCurrent, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=config-'provider_contract_key' WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_commissioning_runs WHERE id=$1`, run.ID.String()); err == nil {
		t.Fatal("database deleted commissioning history")
	}
}

func TestIndexerCommissioningStopStatesAndIndeterminateAttempt(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)

	create := func(offset time.Duration) IndexerCommissioningRun {
		digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
		if err != nil {
			t.Fatal(err)
		}
		fixtures := commissioningRepositoryFixtures()
		plan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion})
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, plan, now.Add(offset))
		if err != nil {
			t.Fatal(err)
		}
		return run
	}

	indeterminate := create(0)
	indeterminate, err := store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, indeterminate.ID, indeterminate.Revision, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	step := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, indeterminate.ID, indeterminate.Steps[0], now.Add(2*time.Second))
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET secret_ref='COMMISSIONING_REPOSITORY_CHANGED_KEY' WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	step, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, indeterminate.ID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
		State:       commissioningv2.StepIndeterminate,
		SafeOutcome: commissioningv2.OutcomeIndeterminate,
	}, now.Add(4*time.Second))
	if err != nil || step.State != commissioningv2.StepIndeterminate {
		t.Fatalf("post-send config drift prevented indeterminate closure: step=%#v err=%v", step, err)
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, indeterminate.ID, step.ID, commissioningv2.StepIndeterminate, IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: commissioningv2.OutcomeCAPSObserved}, now.Add(5*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("terminal indeterminate attempt was resent/replaced: %v", err)
	}
	indeterminate, err = store.IndexerCommissioningRun(ctx, indeterminate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionIndexerCommissioningRun(ctx, indexerID, indeterminate.ID, indeterminate.Revision, commissioningv2.RunStale, commissioningv2.SafeCodeConfigurationChanged, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET secret_ref='COMMISSIONING_REPOSITORY_KEY' WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}

	cancelled := create(7 * time.Second)
	cancelled, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, cancelled.ID, cancelled.Revision, commissioningv2.RunCancelRequested, commissioningv2.SafeCodeOwnerCancelled, now.Add(8*time.Second))
	if err != nil || cancelled.CancelRequestedAt == nil {
		t.Fatalf("cancel request=%#v err=%v", cancelled, err)
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, cancelled.ID, cancelled.Steps[0].ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{State: commissioningv2.StepCancelled, SafeOutcome: commissioningv2.OutcomeCancelled}, now.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	cancelled, err = store.IndexerCommissioningRun(ctx, cancelled.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, cancelled.ID, cancelled.Revision, commissioningv2.RunCancelled, commissioningv2.SafeCodeOwnerCancelled, now.Add(10*time.Second))
	if err != nil || cancelled.State != commissioningv2.RunCancelled || cancelled.CancelRequestedAt == nil || cancelled.CompletedAt == nil {
		t.Fatalf("cancelled run=%#v err=%v", cancelled, err)
	}

	stale := create(11 * time.Second)
	stale, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, stale.ID, stale.Revision, commissioningv2.RunStale, commissioningv2.SafeCodeConfigurationChanged, now.Add(12*time.Second))
	if err != nil || stale.State != commissioningv2.RunStale || stale.StaleAt == nil {
		t.Fatalf("stale run=%#v err=%v", stale, err)
	}
	safety := create(13 * time.Second)
	safety, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, safety.ID, safety.Revision, commissioningv2.RunSafetyBlocked, commissioningv2.SafeCodeCredentialEcho, now.Add(14*time.Second))
	if err != nil || safety.State != commissioningv2.RunSafetyBlocked || safety.CompletedAt == nil {
		t.Fatalf("safety-blocked run=%#v err=%v", safety, err)
	}

	crashedBeforeSend := create(15 * time.Second)
	crashedBeforeSend, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, crashedBeforeSend.ID, crashedBeforeSend.Revision, "owner", now.Add(16*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil := now.Add(18 * time.Second)
	intent, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, crashedBeforeSend.ID, crashedBeforeSend.Steps[0].ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{
		State: commissioningv2.StepIntentCommitted, LeaseOwner: "worker.crashed", LeaseUntil: &leaseUntil,
	}, now.Add(17*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, crashedBeforeSend.ID, intent.ID, commissioningv2.StepIntentCommitted, IndexerCommissioningStepTransition{State: commissioningv2.StepTransmitting}, now.Add(19*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
		t.Fatalf("expired pre-send intent resumed transmission: %v", err)
	}
	crashedBeforeSend, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, crashedBeforeSend.ID, crashedBeforeSend.Revision, commissioningv2.RunCancelRequested, commissioningv2.SafeCodeOwnerCancelled, now.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, crashedBeforeSend.ID, intent.ID, commissioningv2.StepIntentCommitted, IndexerCommissioningStepTransition{
		State: commissioningv2.StepCancelled, SafeOutcome: commissioningv2.OutcomeCancelled,
	}, now.Add(21*time.Second)); err != nil {
		t.Fatalf("cancel expired pre-send intent: %v", err)
	}
	crashedBeforeSend, err = store.IndexerCommissioningRun(ctx, crashedBeforeSend.ID)
	if err != nil {
		t.Fatal(err)
	}
	crashedBeforeSend, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, crashedBeforeSend.ID, crashedBeforeSend.Revision, commissioningv2.RunCancelled, commissioningv2.SafeCodeOwnerCancelled, now.Add(22*time.Second))
	if err != nil || crashedBeforeSend.State != commissioningv2.RunCancelled {
		t.Fatalf("cancel pre-send crash run=%#v err=%v", crashedBeforeSend, err)
	}

	malformedEndpoint := create(23 * time.Second)
	malformedEndpoint, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, malformedEndpoint.ID, malformedEndpoint.Revision, "owner", now.Add(24*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	malformedStep := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, malformedEndpoint.ID, malformedEndpoint.Steps[0], now.Add(25*time.Second))
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{base_url}','"malformed endpoint"'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	malformedStep, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, malformedEndpoint.ID, malformedStep.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
		State: commissioningv2.StepIndeterminate, SafeOutcome: commissioningv2.OutcomeIndeterminate,
	}, now.Add(27*time.Second))
	if err != nil || malformedStep.State != commissioningv2.StepIndeterminate {
		t.Fatalf("malformed endpoint prevented transmitted-attempt closure: step=%#v err=%v", malformedStep, err)
	}
	malformedEndpoint, err = store.IndexerCommissioningRun(ctx, malformedEndpoint.ID)
	if err != nil || malformedEndpoint.ConfigurationCurrent {
		t.Fatalf("malformed endpoint was not reported stale: current=%v err=%v", malformedEndpoint.ConfigurationCurrent, err)
	}
	malformedEndpoint, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, malformedEndpoint.ID, malformedEndpoint.Revision, commissioningv2.RunStale, commissioningv2.SafeCodeConfigurationChanged, now.Add(28*time.Second))
	if err != nil || malformedEndpoint.State != commissioningv2.RunStale {
		t.Fatalf("malformed endpoint prevented stale closure: run=%#v err=%v", malformedEndpoint, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{base_url}','"https://repository.invalid/api"'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name        string
		outcome     commissioningv2.SafeOutcome
		continuable bool
	}{
		{name: "no results permits the next independent probe", outcome: commissioningv2.OutcomeNoResults, continuable: true},
		{name: "authentication failure blocks the next probe", outcome: commissioningv2.OutcomeAuthenticationFailure},
		{name: "safety failure blocks the next probe", outcome: commissioningv2.OutcomeSafetyCredentialEcho},
	} {
		t.Run(test.name, func(t *testing.T) {
			progressionIndexerID := insertCommissioningIndexer(t, ctx, store, now)
			progressionRun, progressionCategories, progressionPlan := commissioningRepositoryAwaitingCategoryReview(t, ctx, store, progressionIndexerID, now.Add(40*time.Second))
			progressionRun = sealAndAuthorizeCommissioningSearchPlan(t, ctx, store, progressionIndexerID, progressionRun, progressionCategories, progressionPlan, now.Add(46*time.Second))
			first := transitionCommissioningStepToTransmission(t, ctx, store, progressionIndexerID, progressionRun.ID, progressionRun.Steps[1], now.Add(48*time.Second))
			transition := IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: test.outcome}
			if test.outcome == commissioningv2.OutcomeNoResults {
				zero := uint16(0)
				bodyDigest := commissioningv2.SumSHA256([]byte(first.ID.String()))
				evidence := commissioningv2.StepEvidenceDocument{
					SchemaVersion: commissioningv2.DocumentSchemaVersion,
					Key:           newCommissioningEvidenceKey(), Kind: newznab.EvidenceKindControlledProbe,
					Target: first.Target, ObservedAt: now.Add(50 * time.Second),
					SafeOutcome: commissioningv2.OutcomeNoResults, BodySHA256: bodyDigest,
				}
				transition.RawResultCount = &zero
				transition.PlausibleIdentityCount = &zero
				transition.RawBodySHA256 = bodyDigest.Bytes()
				transition.Evidence = &evidence
			}
			if _, err := store.TransitionIndexerCommissioningStep(ctx, progressionIndexerID, progressionRun.ID, first.ID, commissioningv2.StepTransmitting, transition, now.Add(50*time.Second)); err != nil {
				t.Fatalf("complete first search step: %v", err)
			}
			progressionRun, err = store.IndexerCommissioningRun(ctx, progressionRun.ID)
			if err != nil {
				t.Fatal(err)
			}
			next := progressionRun.Steps[2]
			leaseUntil := now.Add(2 * time.Minute)
			if test.continuable {
				if _, err := store.TransitionIndexerCommissioningStep(ctx, progressionIndexerID, progressionRun.ID, next.ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{
					State: commissioningv2.StepIntentCommitted, LeaseOwner: "worker.allowed", LeaseUntil: &leaseUntil,
				}, now.Add(51*time.Second)); err != nil {
					t.Fatalf("continuable outcome blocked the next probe: %v", err)
				}
				return
			}
			if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET
				state='intent_committed',intent_at=$2,executor_lease_owner='worker.sql-bypass',executor_lease_until=$3
				WHERE id=$1`, next.ID.String(), now.Add(51*time.Second), leaseUntil); err == nil {
				t.Fatal("database allowed the next probe after a stop-required outcome")
			}
			if _, err := store.TransitionIndexerCommissioningStep(ctx, progressionIndexerID, progressionRun.ID, next.ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{
				State: commissioningv2.StepIntentCommitted, LeaseOwner: "worker.blocked", LeaseUntil: &leaseUntil,
			}, now.Add(51*time.Second)); !errors.Is(err, ErrInvalidIndexerCommissioning) {
				t.Fatalf("repository allowed the next probe after a stop-required outcome: %v", err)
			}
		})
	}
}

func TestIndexerCommissioningTransmittedSearchCanCloseAfterCategoryDrift(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := commissioningRepositoryFixtures()
	fixtures.CompleteSeason = nil
	capsPlan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{
		ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, capsPlan, now)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	capsStep := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, run.ID, run.Steps[0], now.Add(2*time.Second))
	capsEvidence := commissioningRepositoryCAPSEvidence(now.Add(4 * time.Second))
	zero := uint16(0)
	capsStep, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, capsStep.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
		State: commissioningv2.StepCompleted, SafeOutcome: commissioningv2.OutcomeCAPSObserved,
		RawResultCount: &zero, PlausibleIdentityCount: &zero,
		RawBodySHA256: capsEvidence.BodySHA256.Bytes(), Evidence: &capsEvidence,
	}, now.Add(4*time.Second))
	if err != nil || capsStep.Evidence == nil {
		t.Fatalf("complete CAPS step=%#v err=%v", capsStep, err)
	}
	capsEvidence = *capsStep.Evidence
	run, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunAwaitingCategoryReview, "", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	categories := newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}}
	searchPlan, err := commissioningv2.BuildSearchPlan(commissioningv2.SearchPlanInput{
		ConfigurationSHA256: digest, Fixtures: fixtures, CAPSEvidence: capsEvidence, Categories: categories,
		Options: commissioningv2.SearchProbeOptions{IncludeMovieFallback: true}, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchPlan.Steps) != 2 {
		t.Fatalf("expected exact movie and fallback steps, got %d", len(searchPlan.Steps))
	}
	run, err = store.SealIndexerCommissioningSearchPlan(ctx, indexerID, run.ID, run.Revision, categories, searchPlan, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningSearches(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	searchStep := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, run.ID, run.Steps[1], now.Add(8*time.Second))
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{movie_category_ids}','[2010]'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	searchBodyDigest := commissioningv2.SumSHA256([]byte("category drift response"))
	searchEvidence := commissioningv2.StepEvidenceDocument{
		SchemaVersion: commissioningv2.DocumentSchemaVersion,
		Key:           "evidence.22222222222222222222222222222222",
		Kind:          newznab.EvidenceKindControlledProbe,
		Target:        searchStep.Target,
		ObservedAt:    now.Add(10 * time.Second),
		SafeOutcome:   commissioningv2.OutcomeNoResults,
		BodySHA256:    searchBodyDigest,
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, searchStep.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
		State: commissioningv2.StepCompleted, SafeOutcome: commissioningv2.OutcomeNoResults,
		RawResultCount: &zero, PlausibleIdentityCount: &zero,
		RawBodySHA256: searchBodyDigest.Bytes(), Evidence: &searchEvidence,
	}, searchEvidence.ObservedAt); err != nil {
		t.Fatalf("post-send category drift prevented safe evidence persistence: %v", err)
	}
	run, err = store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil || run.ConfigurationCurrent {
		t.Fatalf("category drift not visible after safe completion: current=%v err=%v", run.ConfigurationCurrent, err)
	}
	leaseUntil := now.Add(time.Minute)
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, run.Steps[2].ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{
		State: commissioningv2.StepIntentCommitted, LeaseOwner: "worker.blocked", LeaseUntil: &leaseUntil,
	}, now.Add(11*time.Second)); !errors.Is(err, ErrIndexerCommissioningConfigChanged) {
		t.Fatalf("category drift allowed a later request intent: %v", err)
	}
	run, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunStale, commissioningv2.SafeCodeConfigurationChanged, now.Add(12*time.Second))
	if err != nil || run.State != commissioningv2.RunStale {
		t.Fatalf("stale closure after post-send category drift: run=%#v err=%v", run, err)
	}
}

func TestIndexerCommissioningRunCreationSerializesPerInstance(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := commissioningv2.SHA256DigestFromBytes(digestBytes)
	fixtures := commissioningRepositoryFixtures()
	plan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, createErr := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, plan, now)
			errorsSeen <- createErr
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	successes := 0
	for createErr := range errorsSeen {
		if createErr == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent run creations succeeded=%d", successes)
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_commissioning_runs WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted runs=%d err=%v", count, err)
	}
	runs, err := store.ListIndexerCommissioningRuns(ctx, indexerID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list runs=%d err=%v", len(runs), err)
	}
	start = make(chan struct{})
	errorsSeen = make(chan error, 2)
	wait = sync.WaitGroup{}
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, authorizeErr := store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, runs[0].ID, runs[0].Revision, "owner", now.Add(time.Second))
			errorsSeen <- authorizeErr
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	successes = 0
	conflicts := 0
	for authorizeErr := range errorsSeen {
		if authorizeErr == nil {
			successes++
		} else if errors.Is(authorizeErr, ErrConcurrentModification) {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent authorizations successes=%d conflicts=%d", successes, conflicts)
	}

	t.Run("create rolls back the run when CAPS step insertion fails", func(t *testing.T) {
		rollbackIndexerID := insertCommissioningIndexer(t, ctx, store, now.Add(2*time.Second))
		rollbackDigestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, rollbackIndexerID)
		if err != nil {
			t.Fatal(err)
		}
		rollbackDigest, err := commissioningv2.SHA256DigestFromBytes(rollbackDigestBytes)
		if err != nil {
			t.Fatal(err)
		}
		rollbackPlan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{
			ConfigurationSHA256: rollbackDigest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
		})
		if err != nil {
			t.Fatal(err)
		}
		var schema string
		if err := store.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal(err)
		}
		functionName := pgx.Identifier{schema, "test_fail_commissioning_caps_step_insert"}.Sanitize()
		triggerName := pgx.Identifier{"test_fail_commissioning_caps_step_insert"}.Sanitize()
		stepTable := pgx.Identifier{schema, "indexer_commissioning_steps"}.Sanitize()
		if _, err := store.Pool.Exec(ctx, `CREATE FUNCTION `+functionName+`() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$ BEGIN RAISE EXCEPTION 'forced CAPS step failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, "CREATE TRIGGER "+triggerName+" BEFORE INSERT ON "+stepTable+" FOR EACH ROW EXECUTE FUNCTION "+functionName+"()"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON `+stepTable)
			_, _ = store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`()`)
		})
		if _, err := store.CreateIndexerCommissioningRun(ctx, rollbackIndexerID, fixtures, rollbackPlan, now.Add(3*time.Second)); err == nil {
			t.Fatal("run creation succeeded despite forced CAPS step insertion failure")
		}
		var runCount, stepCount int
		if err := store.Pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM indexer_commissioning_runs WHERE indexer_instance_id=$1),
			(SELECT count(*) FROM indexer_commissioning_steps step JOIN indexer_commissioning_runs run ON run.id=step.run_id WHERE run.indexer_instance_id=$1)`, rollbackIndexerID.String()).Scan(&runCount, &stepCount); err != nil {
			t.Fatal(err)
		}
		if runCount != 0 || stepCount != 0 {
			t.Fatalf("failed creation left durable rows: runs=%d steps=%d", runCount, stepCount)
		}
		if _, err := store.Pool.Exec(ctx, `DROP TRIGGER `+triggerName+` ON `+stepTable); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, "DROP FUNCTION "+functionName+"()"); err != nil {
			t.Fatal(err)
		}
		created, err := store.CreateIndexerCommissioningRun(ctx, rollbackIndexerID, fixtures, rollbackPlan, now.Add(4*time.Second))
		if err != nil {
			t.Fatalf("valid creation after rollback: %v", err)
		}
		if created.State != commissioningv2.RunAwaitingCAPSAuthorization || created.Revision != 1 || !created.ConfigurationCurrent || len(created.Steps) != 1 {
			t.Fatalf("creation after rollback=%#v", created)
		}
		step := created.Steps[0]
		if step.RunID != created.ID || step.Ordinal != 1 || step.Phase != commissioningv2.StepPhaseCAPS || step.Target != (newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}) || step.State != commissioningv2.StepPlanned {
			t.Fatalf("CAPS step after rollback=%#v", step)
		}
		if err := store.Pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM indexer_commissioning_runs WHERE indexer_instance_id=$1),
			(SELECT count(*) FROM indexer_commissioning_steps step JOIN indexer_commissioning_runs run ON run.id=step.run_id WHERE run.indexer_instance_id=$1)`, rollbackIndexerID.String()).Scan(&runCount, &stepCount); err != nil {
			t.Fatal(err)
		}
		if runCount != 1 || stepCount != 1 {
			t.Fatalf("valid creation after rollback: runs=%d steps=%d", runCount, stepCount)
		}
	})

	for _, list := range []bool{false, true} {
		name := "single run load uses one database snapshot"
		if list {
			name = "run list load uses one database snapshot"
		}
		t.Run(name, func(t *testing.T) {
			snapshotStore, snapshotContext := isolatedCommissioningStore(t)
			snapshotNow := time.Now().UTC().Truncate(time.Microsecond)
			snapshotIndexerID := insertCommissioningIndexer(t, snapshotContext, snapshotStore, snapshotNow)
			snapshotRun, categories, searchPlan := commissioningRepositoryAwaitingCategoryReview(t, snapshotContext, snapshotStore, snapshotIndexerID, snapshotNow)

			writer, err := snapshotStore.Pool.Begin(snapshotContext)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback(snapshotContext)
			if _, err := writer.Exec(snapshotContext, `LOCK TABLE indexer_commissioning_steps IN ACCESS EXCLUSIVE MODE`); err != nil {
				t.Fatal(err)
			}

			type readResult struct {
				run IndexerCommissioningRun
				err error
			}
			read := make(chan readResult, 1)
			go func() {
				if !list {
					loaded, loadErr := snapshotStore.IndexerCommissioningRun(snapshotContext, snapshotRun.ID)
					read <- readResult{run: loaded, err: loadErr}
					return
				}
				runs, loadErr := snapshotStore.ListIndexerCommissioningRuns(snapshotContext, snapshotIndexerID)
				if loadErr != nil {
					read <- readResult{err: loadErr}
					return
				}
				for _, loaded := range runs {
					if loaded.ID == snapshotRun.ID {
						read <- readResult{run: loaded}
						return
					}
				}
				read <- readResult{err: ErrNotFound}
			}()
			waitForCommissioningStepReadLock(t, snapshotContext, snapshotStore)
			sealCommissioningSearchPlanInTransaction(t, snapshotContext, writer, snapshotIndexerID, snapshotRun, categories, searchPlan, snapshotNow.Add(6*time.Second))
			if err := writer.Commit(snapshotContext); err != nil {
				t.Fatal(err)
			}

			select {
			case loaded := <-read:
				if loaded.err != nil {
					t.Fatalf("load split one logical run across database snapshots: %v", loaded.err)
				}
				if loaded.run.State != commissioningv2.RunAwaitingCategoryReview || loaded.run.SearchPlan != nil || len(loaded.run.Steps) != 1 {
					t.Fatalf("load returned mixed snapshot: state=%s search_plan=%v steps=%d", loaded.run.State, loaded.run.SearchPlan != nil, len(loaded.run.Steps))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for coordinated commissioning read")
			}
		})
	}
}

func waitForCommissioningStepReadLock(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := store.Pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_catalog.pg_stat_activity
			WHERE pid <> pg_catalog.pg_backend_pid()
			AND datname = pg_catalog.current_database()
			AND wait_event_type = 'Lock'
			AND query LIKE '%FROM indexer_commissioning_steps step%'
		)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("commissioning reader did not reach the coordinated step-table lock")
}

func sealCommissioningSearchPlanInTransaction(t *testing.T, ctx context.Context, transaction pgx.Tx, indexerID uuid.UUID, run IndexerCommissioningRun, categories newznab.NewznabCategorySelection, plan commissioningv2.ProbePlan, now time.Time) {
	t.Helper()
	planDocument, err := commissioningv2.CanonicalProbePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	normalizedCategories, categoryDigest, err := commissioningv2.NormalizeCategorySelection(categories)
	if err != nil {
		t.Fatal(err)
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state=$4,revision=revision+1,category_selection_sha256=$5,
		movie_category_ids=$6,series_category_ids=$7,
		search_plan_document=$8,search_plan_sha256=$9,
		planned_api_calls=$10,updated_at=$11
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`,
		run.ID.String(), indexerID.String(), int64(run.Revision), string(commissioningv2.RunAwaitingSearchAuthorization),
		categoryDigest.Bytes(), normalizedCategories.Movie, normalizedCategories.Series, string(planDocument),
		commissioningv2.SumSHA256(planDocument).Bytes(), int16(plan.PlannedAPICalls), commissioningTime(now))
	if err != nil {
		t.Fatal(err)
	}
	if command.RowsAffected() != 1 {
		t.Fatalf("sealed rows=%d", command.RowsAffected())
	}
	for _, step := range plan.Steps {
		requestDocument, err := commissioningv2.CanonicalTypedRequest(step.Request)
		if err != nil {
			t.Fatal(err)
		}
		if err := insertIndexerCommissioningStep(ctx, transaction, run.ID, step, requestDocument); err != nil {
			t.Fatal(err)
		}
	}
}

func transitionCommissioningStepToTransmission(t *testing.T, ctx context.Context, store *Store, indexerID, runID uuid.UUID, step IndexerCommissioningStep, now time.Time) IndexerCommissioningStep {
	t.Helper()
	leaseUntil := now.Add(time.Minute)
	updated, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, runID, step.ID, commissioningv2.StepPlanned, IndexerCommissioningStepTransition{
		State:      commissioningv2.StepIntentCommitted,
		LeaseOwner: "worker.test",
		LeaseUntil: &leaseUntil,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, runID, step.ID, commissioningv2.StepIntentCommitted, IndexerCommissioningStepTransition{State: commissioningv2.StepTransmitting}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func commissioningRepositoryAwaitingCategoryReview(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID, now time.Time) (IndexerCommissioningRun, newznab.NewznabCategorySelection, commissioningv2.ProbePlan) {
	t.Helper()
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := commissioningRepositoryFixtures()
	capsPlan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{
		ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, capsPlan, now)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	capsStep := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, run.ID, run.Steps[0], now.Add(2*time.Second))
	capsEvidence := commissioningRepositoryCAPSEvidence(now.Add(4 * time.Second))
	capsEvidence.Key = newCommissioningEvidenceKey()
	zero := uint16(0)
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, capsStep.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
		State:                  commissioningv2.StepCompleted,
		SafeOutcome:            commissioningv2.OutcomeCAPSObserved,
		RawResultCount:         &zero,
		PlausibleIdentityCount: &zero,
		RawBodySHA256:          capsEvidence.BodySHA256.Bytes(),
		Evidence:               &capsEvidence,
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err = store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunAwaitingCategoryReview, "", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	categories := newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}}
	searchPlan, err := commissioningv2.BuildSearchPlan(commissioningv2.SearchPlanInput{
		ConfigurationSHA256: digest,
		Fixtures:            fixtures,
		CAPSEvidence:        capsEvidence,
		Categories:          categories,
		Options: commissioningv2.SearchProbeOptions{
			IncludeMovieFallback:          true,
			IncludeCompleteSeasonFallback: true,
		},
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run, categories, searchPlan
}

func sealAndAuthorizeCommissioningSearchPlan(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID, run IndexerCommissioningRun, categories newznab.NewznabCategorySelection, plan commissioningv2.ProbePlan, now time.Time) IndexerCommissioningRun {
	t.Helper()
	updated, err := store.SealIndexerCommissioningSearchPlan(ctx, indexerID, run.ID, run.Revision, categories, plan, now)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = store.AuthorizeIndexerCommissioningSearches(ctx, indexerID, run.ID, updated.Revision, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func newCommissioningEvidenceKey() string {
	return "evidence." + strings.ReplaceAll(uuid.New().String(), "-", "")
}

func commissioningRepositoryFixtures() commissioningv2.FixtureDocument {
	movieYear, seasonYear := uint16(2026), uint16(2022)
	return commissioningv2.FixtureDocument{
		SchemaVersion: commissioningv2.DocumentSchemaVersion,
		Movie: &commissioningv2.MovieFixture{
			Key: "cfx_00000000000000000000000000000001", CanonicalTitle: "Commissioning Movie", Year: &movieYear,
			Identities: []commissioningv2.VerifiedIdentity{{Namespace: commissioningv2.IdentityIMDb, Value: "tt1234567"}, {Namespace: commissioningv2.IdentityTMDb, Value: "123"}},
			Source:     commissioningv2.FixtureExplicitOwner,
		},
		CompleteSeason: &commissioningv2.CompleteSeasonFixture{
			Key: "cfx_00000000000000000000000000000002", CanonicalTitle: "Commissioning Series", Season: 2, Year: &seasonYear,
			Identities: []commissioningv2.VerifiedIdentity{{Namespace: commissioningv2.IdentityTVDb, Value: "456"}},
			Source:     commissioningv2.FixtureExplicitOwner,
		},
	}
}

func commissioningRepositoryCAPSEvidence(observedAt time.Time) commissioningv2.StepEvidenceDocument {
	bodyDigest := commissioningv2.SumSHA256([]byte("synthetic CAPS response"))
	caps := newznab.CAPSRecord{
		BodySHA256: bodyDigest.String(), ObservedAt: observedAt, DefaultLimit: 20, MaximumLimit: 100,
		Categories: []newznab.Category{{ID: 2000, Name: "Movies"}, {ID: 5000, Name: "TV"}},
		ModeObservations: []newznab.CAPSModeObservation{
			{Mode: newznab.SearchModeGeneral, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery}},
			{Mode: newznab.SearchModeMovie, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q", "imdbid", "tmdbid", "extended"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery, newznab.WireIMDbID, newznab.WireTMDbID, newznab.WireExtended}},
			{Mode: newznab.SearchModeTV, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q", "tvdbid", "season", "extended"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery, newznab.WireTVDbID, newznab.WireSeason, newznab.WireExtended}},
		},
	}
	return commissioningv2.StepEvidenceDocument{
		SchemaVersion: commissioningv2.DocumentSchemaVersion,
		Key:           "evidence.00000000000000000000000000000001",
		Kind:          newznab.EvidenceKindControlledProbe,
		Target:        newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		ObservedAt:    observedAt,
		SafeOutcome:   commissioningv2.OutcomeCAPSObserved,
		BodySHA256:    bodyDigest,
		CAPS:          &caps,
	}
}

func insertCommissioningIndexer(t *testing.T, ctx context.Context, store *Store, now time.Time) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	name := "Commissioning repository fixture " + id.String()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,'COMMISSIONING_REPOSITORY_KEY',$4,$4)`, id.String(), name, `{"base_url":"https://repository.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func isolatedCommissioningStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	schema := "mm_indexer_commissioning_repository_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	return store, ctx
}
