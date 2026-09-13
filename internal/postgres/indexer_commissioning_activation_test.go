package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"uuid"
)

func countCapabilityActivationAudits(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='newznab.capability_profile_activated'`, indexerID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func countCommissioningRunSteps(t *testing.T, ctx context.Context, store *Store, runID uuid.UUID) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_commissioning_steps WHERE run_id=$1`, runID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestIndexerCommissioningDraftActivationRechecksAndIsIdempotent proves the
// Milestone-E activation path against a draft produced by the real
// commissioning repository: the existing atomic activation transaction still
// rechecks endpoint, category-review, provider-contract, and evidence
// bindings, is idempotent for an already-active profile, preserves
// supersession, enables nothing, and starts no step or transmission.
func TestIndexerCommissioningDraftActivationRechecksAndIsIdempotent(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := insertCommissioningIndexer(t, ctx, store, now)

	firstRun := draftCompletedCommissioningRun(t, ctx, store, indexerID, now)
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, firstRun.ID, firstRun.Revision, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	firstFinalized, err := store.IndexerCommissioningRun(ctx, firstRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstDraft := draftProfileForRun(t, ctx, store, firstFinalized)
	if firstDraft.LifecycleState != "draft" {
		t.Fatalf("run did not produce a draft: %#v", firstDraft)
	}
	stepsBefore := countCommissioningRunSteps(t, ctx, store, firstRun.ID)
	auditsBefore := countCapabilityActivationAudits(t, ctx, store, indexerID)

	active, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, firstDraft.ID, "owner", now.Add(2*time.Minute))
	if err != nil || active.ID != firstDraft.ID || active.LifecycleState != "active" || active.ActivatedAt == nil {
		t.Fatalf("activate run draft=%#v err=%v", active, err)
	}
	if got := countCapabilityActivationAudits(t, ctx, store, indexerID); got != auditsBefore+1 {
		t.Fatalf("activation audit events=%d, want %d", got, auditsBefore+1)
	}
	if got := countCommissioningRunSteps(t, ctx, store, firstRun.ID); got != stepsBefore {
		t.Fatalf("activation changed commissioning steps: %d -> %d", stepsBefore, got)
	}
	var enabled bool
	if err := store.Pool.QueryRow(ctx, `SELECT enabled FROM integration_instances WHERE id=$1`, indexerID.String()).Scan(&enabled); err != nil || enabled {
		t.Fatalf("activation changed integration enabled=%v err=%v", enabled, err)
	}

	// A second explicit activation of the same profile is idempotent: no new
	// audit event and no second side effect.
	replayed, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, firstDraft.ID, "owner", now.Add(3*time.Minute))
	if err != nil || replayed.ID != firstDraft.ID || replayed.LifecycleState != "active" {
		t.Fatalf("replay activation=%#v err=%v", replayed, err)
	}
	if got := countCapabilityActivationAudits(t, ctx, store, indexerID); got != auditsBefore+1 {
		t.Fatalf("idempotent activation wrote extra audit events: %d", got)
	}

	// A later reviewed draft supersedes the current active profile.
	secondRun := draftCompletedCommissioningRun(t, ctx, store, indexerID, now.Add(20*time.Minute))
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, indexerID, secondRun.ID, secondRun.Revision, now.Add(21*time.Minute)); err != nil {
		t.Fatal(err)
	}
	secondFinalized, err := store.IndexerCommissioningRun(ctx, secondRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondDraft := draftProfileForRun(t, ctx, store, secondFinalized)
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, secondDraft.ID, "owner", now.Add(22*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reloadedFirst, err := store.IndexerCapabilityProfile(ctx, firstDraft.ID)
	if err != nil || reloadedFirst.LifecycleState != "superseded" || reloadedFirst.SupersededAt == nil {
		t.Fatalf("superseded first=%#v err=%v", reloadedFirst, err)
	}
	activeNow, err := store.ActiveIndexerCapabilityProfile(ctx, indexerID)
	if err != nil || activeNow.ID != secondDraft.ID {
		t.Fatalf("active profile=%#v err=%v", activeNow, err)
	}

	// Freshness and category rechecks on an otherwise activatable draft.
	digest, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	recheckDraft, err := store.CreateIndexerCapabilityProfile(ctx, indexerID, activatableCapabilityProfileDocument(t), digest, now.Add(23*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{category_selection_review_required}','true'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, recheckDraft.ID, "owner", now.Add(24*time.Minute)); !errors.Is(err, ErrIndexerCategoryReviewRequired) {
		t.Fatalf("category review recheck=%v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{category_selection_review_required}','false'::jsonb,true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{provider_contract_key}',to_jsonb('nzbfinder'::text),true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, recheckDraft.ID, "owner", now.Add(25*time.Minute)); !errors.Is(err, ErrIndexerProviderContractMismatch) {
		t.Fatalf("provider contract recheck=%v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=config-'provider_contract_key' WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{base_url}',to_jsonb('https://changed.invalid/api'::text),true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, recheckDraft.ID, "owner", now.Add(26*time.Minute)); !errors.Is(err, ErrIndexerEndpointConfigurationChanged) {
		t.Fatalf("endpoint recheck=%v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{base_url}',to_jsonb('https://repository.invalid/api'::text),true) WHERE id=$1`, indexerID.String()); err != nil {
		t.Fatal(err)
	}

	// Evidence recheck: a structurally valid draft with no verified primary
	// recipe is not activatable, and the failed attempt writes no audit event.
	evidenceDraft, err := store.CreateIndexerCapabilityProfile(ctx, indexerID, untestedCapabilityProfileDocument(t), digest, now.Add(27*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, indexerID, evidenceDraft.ID, "owner", now.Add(28*time.Minute)); !errors.Is(err, ErrInvalidIndexerCapabilityProfile) {
		t.Fatalf("evidence recheck=%v", err)
	}
	if got := countCapabilityActivationAudits(t, ctx, store, indexerID); got != auditsBefore+2 {
		t.Fatalf("failed activation wrote an audit event: events=%d want %d", got, auditsBefore+2)
	}
}

func untestedCapabilityProfileDocument(t *testing.T) json.RawMessage {
	t.Helper()
	profile := newznab.CapabilityProfile{
		SchemaVersion: newznab.CapabilityProfileSchemaVersion,
		SearchRecipes: []newznab.SearchRecipe{{
			Key:   "movie-imdb",
			Scope: newznab.SearchScopeMovie,
			Mode:  newznab.SearchModeMovie,
			Query: newznab.QuerySpec{Style: newznab.QueryTitle},
			Bindings: []newznab.WireBinding{{
				Parameter: newznab.WireIMDbID,
				Source:    newznab.BindingIMDbNumeric,
			}},
			Extended:      true,
			Categories:    newznab.CategorySpec{Set: newznab.CategorySelectedMovie},
			VerifiedLimit: 50,
			Role:          newznab.RecipePrimary,
			Priority:      1,
			Verification:  newznab.RecipeVerification{State: newznab.VerificationUntested},
		}},
		Operations:  []newznab.OperationCapability{},
		MappingRefs: []newznab.MappingRef{{Key: newznab.MappingNewznabSearchV1, Version: 1}},
	}
	if err := newznab.ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("untested test profile: %v", err)
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
