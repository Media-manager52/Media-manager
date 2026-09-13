package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"uuid"
)

// draftSearchEvidence builds a closed search-recipe evidence document for the
// offline repository path. recipe_verified requires every raw result to be
// plausible on this path, so callers pass raw == plausible.
func draftSearchEvidence(step IndexerCommissioningStep, outcome commissioningv2.SafeOutcome, raw, plausible uint16, key string) commissioningv2.StepEvidenceDocument {
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	if step.TransmittedAt != nil {
		observedAt = step.TransmittedAt.Add(time.Second).UTC().Truncate(time.Microsecond)
	}
	proof := commissioningv2.SearchProof{
		SchemaVersion:          commissioningv2.DocumentSchemaVersion,
		Scope:                  string(step.Scope),
		ProviderOutcome:        "none",
		IdentityMatch:          "observed",
		TitleMatch:             "exact",
		YearMatch:              "not_required",
		SeasonMatch:            "not_required",
		CompletePackBasis:      "none",
		ValidItemCount:         raw,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
	}
	if step.Scope == newznab.SearchScopeMovie {
		proof.YearMatch = "exact"
	} else {
		proof.SeasonMatch = "exact"
		proof.SeasonRequested = 2
		proof.CompletePackBasis = "provider_field"
	}
	return commissioningv2.StepEvidenceDocument{
		SchemaVersion:          commissioningv2.DocumentSchemaVersion,
		Key:                    key,
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 step.Target,
		ObservedAt:             observedAt,
		SafeOutcome:            outcome,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
		BodySHA256:             commissioningv2.SumSHA256([]byte(key)),
		Search:                 &proof,
	}
}

// draftCompletedCommissioningRun drives the full repository lifecycle to the
// point where every search step has durable accepted evidence but the run has
// not yet been finalized. The movie primary verifies, the season primary
// returns no results, and both fallbacks pass their own probe.
func draftCompletedCommissioningRun(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID, now time.Time) IndexerCommissioningRun {
	t.Helper()
	run, categories, searchPlan := commissioningRepositoryAwaitingCategoryReview(t, ctx, store, indexerID, now)
	run = sealAndAuthorizeCommissioningSearchPlan(t, ctx, store, indexerID, run, categories, searchPlan, now.Add(10*time.Second))
	for index := 1; index < len(run.Steps); index++ {
		step := transitionCommissioningStepToTransmission(t, ctx, store, indexerID, run.ID, run.Steps[index], now.Add(time.Duration(20+index*3)*time.Second))
		var outcome commissioningv2.SafeOutcome
		var raw, plausible uint16
		switch {
		case step.Scope == newznab.SearchScopeMovie:
			outcome, raw, plausible = commissioningv2.OutcomeRecipeVerified, 1, 1
		case step.Role == newznab.RecipePrimary:
			outcome, raw, plausible = commissioningv2.OutcomeNoResults, 0, 0
		default:
			outcome, raw, plausible = commissioningv2.OutcomeRecipeVerified, 1, 1
		}
		key := newCommissioningEvidenceKey()
		evidence := draftSearchEvidence(step, outcome, raw, plausible, key)
		rawCount, plausibleCount := raw, plausible
		if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{
			State:                  commissioningv2.StepCompleted,
			SafeOutcome:            outcome,
			RawResultCount:         &rawCount,
			PlausibleIdentityCount: &plausibleCount,
			RawBodySHA256:          evidence.BodySHA256.Bytes(),
			Evidence:               &evidence,
		}, now.Add(time.Duration(40+index*3)*time.Second)); err != nil {
			t.Fatalf("complete search step %d: %v", index, err)
		}
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != commissioningv2.RunSearchesAuthorized {
		t.Fatalf("run state=%s", reloaded.State)
	}
	return reloaded
}

func draftProfileForRun(t *testing.T, ctx context.Context, store *Store, run IndexerCommissioningRun) IndexerCapabilityProfile {
	t.Helper()
	if run.GeneratedProfileID == nil {
		t.Fatal("run has no generated profile")
	}
	profile, err := store.IndexerCapabilityProfile(ctx, *run.GeneratedProfileID)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestFinalizeIndexerCommissioningDraftCreatesImmutableLinkedDraft(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	run := draftCompletedCommissioningRun(t, ctx, store, indexerID, now)

	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, run.ID, run.Revision+1, now.Add(time.Minute)); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("wrong revision error=%v", err)
	}
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, run.ID, run.Revision, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	finalized, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalized.State != commissioningv2.RunCompleted || finalized.GeneratedProfileID == nil || finalized.CompletedAt == nil {
		t.Fatalf("finalized run=%#v", finalized)
	}
	profile := draftProfileForRun(t, ctx, store, finalized)
	if profile.LifecycleState != "draft" || profile.Revision != 1 || profile.ActivatedAt != nil {
		t.Fatalf("profile=%#v", profile)
	}
	if !bytes.Equal(profile.EndpointConfigSHA256, run.EndpointConfigSHA256) {
		t.Fatal("draft is not bound to the run configuration digest")
	}
	document, err := newznab.DecodeCapabilityProfile(profile.Document)
	if err != nil {
		t.Fatal(err)
	}
	if document.CAPS.MaximumLimit != 100 || document.CAPS.DefaultLimit != 20 || len(document.CAPS.ModeObservations) != 3 || len(document.CAPS.Categories) != 2 {
		t.Fatalf("CAPS claims not preserved: %+v", document.CAPS)
	}
	verifiedPrimary := map[newznab.SearchScope]bool{}
	for _, recipe := range document.SearchRecipes {
		if recipe.Role == newznab.RecipePrimary && recipe.Verification.State == newznab.VerificationVerified {
			verifiedPrimary[recipe.Scope] = true
		}
	}
	if !verifiedPrimary[newznab.SearchScopeMovie] {
		t.Fatal("movie primary did not verify")
	}
	for _, recipe := range document.SearchRecipes {
		if recipe.Scope == newznab.SearchScopeCompleteSeason && recipe.Verification.State != newznab.VerificationUntested {
			t.Fatalf("season must remain unknown without a verified primary: %+v", recipe)
		}
	}
	// The linked draft is immutable: the profile trigger forbids rewriting the
	// document, and the run link cannot move.
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_capability_profiles SET profile_document=jsonb_set(profile_document,'{caps}','{}'::jsonb) WHERE id=$1`, profile.ID.String()); err == nil {
		t.Fatal("draft document was mutable")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET generated_profile_id=$2 WHERE id=$1`, run.ID.String(), uuid.NewV7().String()); err == nil {
		t.Fatal("generated profile link was mutable")
	}

	// Replay against the already completed run must not append a second
	// revision, even when the caller replays with the original revision.
	replayed, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, run.ID, run.Revision, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.GeneratedProfileID == nil || *replayed.GeneratedProfileID != *finalized.GeneratedProfileID {
		t.Fatal("replay produced a different draft")
	}
	var profiles int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 {
		t.Fatalf("replay appended drafts: profiles=%d", profiles)
	}
}

func TestFinalizeIndexerCommissioningDraftRequiresCompleteAcceptedEvidence(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	run, categories, searchPlan := commissioningRepositoryAwaitingCategoryReview(t, ctx, store, indexerID, now)
	run = sealAndAuthorizeCommissioningSearchPlan(t, ctx, store, indexerID, run, categories, searchPlan, now.Add(10*time.Second))
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, run.ID, run.Revision, now.Add(time.Minute)); !errors.Is(err, ErrIndexerCommissioningDraftUnavailable) {
		t.Fatalf("finalized a run without accepted evidence: %v", err)
	}
	var profiles int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 {
		t.Fatalf("unavailable run produced a draft: profiles=%d", profiles)
	}
}

func TestFinalizeIndexerCommissioningDraftRejectsStaleConfiguration(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	run := draftCompletedCommissioningRun(t, ctx, store, indexerID, now)
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{movie_category_ids}','[2010]'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, run.ID, run.Revision, now.Add(time.Minute)); !errors.Is(err, ErrIndexerCommissioningConfigChanged) {
		t.Fatalf("stale configuration error=%v", err)
	}
	var profiles int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 {
		t.Fatalf("stale run produced a draft: profiles=%d", profiles)
	}
}

func TestFinalizeIndexerCommissioningDraftRejectsDifferentIndexer(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	otherIndexerID := insertCommissioningIndexer(t, ctx, store, now)
	run := draftCompletedCommissioningRun(t, ctx, store, indexerID, now)
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, otherIndexerID, run.ID, run.Revision, now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-indexer finalization error=%v", err)
	}
}

func TestCapabilityProfileDraftNeverContainsProviderSecrets(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)
	run := draftCompletedCommissioningRun(t, ctx, store, indexerID, now)
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, run.ID, run.Revision, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	finalized, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile := draftProfileForRun(t, ctx, store, finalized)
	document := bytes.ToLower(profile.Document)
	for _, forbidden := range []string{"api_key", "apikey", "authorization", "cookie", "http://", "https://", "secret_ref", "COMMISSIONING_REPOSITORY_KEY"} {
		if bytes.Contains(document, []byte(strings.ToLower(forbidden))) {
			t.Fatalf("draft document contains forbidden material %q: %s", forbidden, profile.Document)
		}
	}
}
