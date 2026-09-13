package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"uuid"
)

// ErrIndexerCommissioningDraftUnavailable means the run is not in a state that
// can produce a draft: either the exact accepted search evidence is not yet
// complete, or the run is neither ready_for_draft nor searches_authorized.
var ErrIndexerCommissioningDraftUnavailable = errors.New("indexer commissioning run is not ready for draft generation")

// FinalizeIndexerCommissioningDraft is the Milestone-D offline derivation
// boundary. In one transaction it:
//
//  1. locks the integration, run, and steps in the fixed order;
//  2. proves the run still matches the exact configuration, provider contract,
//     and frozen category selection it was authorized under;
//  3. derives an immutable capability-profile document from the durable CAPS
//     and search evidence only;
//  4. appends one new draft profile revision; and
//  5. links it from the run and closes the run as completed.
//
// It performs no network work, resolves no credential, and never activates,
// enables, or mutates an integration. A duplicate request against an already
// completed run returns the existing linked draft without appending a second
// revision. A stale or mismatched configuration is rejected before any draft
// is written.
func (store *Store) FinalizeIndexerCommissioningDraft(ctx context.Context, indexerID, runID uuid.UUID, expectedRevision uint64, now time.Time) (IndexerCommissioningRun, error) {
	if indexerID == (uuid.UUID{}) || runID == (uuid.UUID{}) || expectedRevision == 0 {
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
	// Replay/idempotency: an already completed run is linked to exactly one
	// immutable draft. Return that state without allocating a new revision.
	if run.State == commissioningv2.RunCompleted {
		if run.GeneratedProfileID == nil {
			return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
		}
		if err := verifyLinkedCommissioningDraft(ctx, transaction, indexerID, *run.GeneratedProfileID, run.EndpointConfigSHA256); err != nil {
			return IndexerCommissioningRun{}, err
		}
		return run, nil
	}
	if run.Revision != expectedRevision {
		return IndexerCommissioningRun{}, ErrConcurrentModification
	}
	if run.State != commissioningv2.RunSearchesAuthorized && run.State != commissioningv2.RunReadyForDraft {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningDraftUnavailable
	}
	if !commissioningSearchStepsComplete(run) {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningDraftUnavailable
	}
	if !now.After(run.UpdatedAt) {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	if !commissioningRunConfigurationCurrent(configuration, digest, run) {
		return IndexerCommissioningRun{}, ErrIndexerCommissioningConfigChanged
	}
	// The 0034 run trigger requires the ready_for_draft -> completed edge to
	// allocate the profile link, and every run UPDATE must strictly advance
	// both revision and updated_at. Advance through ready_for_draft first when
	// the last search step closed without a separate review transition, then
	// use a strictly later timestamp for the completion write.
	completionTime := now
	if run.State == commissioningv2.RunSearchesAuthorized {
		command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='ready_for_draft',revision=revision+1,updated_at=$4
			WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`, runID.String(), indexerID.String(), int64(run.Revision), now)
		if err != nil {
			return IndexerCommissioningRun{}, err
		}
		if command.RowsAffected() != 1 {
			return IndexerCommissioningRun{}, ErrConcurrentModification
		}
		run.State = commissioningv2.RunReadyForDraft
		run.Revision++
		completionTime = now.Add(time.Microsecond)
	}
	input, err := commissioningDraftInput(run)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	profile, _, err := commissioningv2.BuildCapabilityProfileDraft(input)
	if err != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	canonical, err := json.Marshal(profile)
	if err != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	// Re-decode so the stored document is exactly the closed profile model and
	// cannot carry a field the validator does not own.
	if _, err := newznab.DecodeCapabilityProfile(canonical); err != nil {
		return IndexerCommissioningRun{}, ErrInvalidIndexerCommissioning
	}
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revision); err != nil {
		return IndexerCommissioningRun{}, err
	}
	profileID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO indexer_capability_profiles(id,indexer_instance_id,revision,schema_version,lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,created_at)
		VALUES($1,$2,$3,$4,'draft',$5,sha256(convert_to($5::jsonb::text,'UTF8')),$6,$7)`,
		profileID.String(), indexerID.String(), revision, profile.SchemaVersion, string(canonical), run.EndpointConfigSHA256, completionTime); err != nil {
		return IndexerCommissioningRun{}, err
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='completed',generated_profile_id=$4,revision=revision+1,updated_at=$5,completed_at=$5
		WHERE id=$1 AND indexer_instance_id=$2 AND revision=$3`, runID.String(), indexerID.String(), int64(run.Revision), profileID.String(), completionTime)
	if err != nil {
		return IndexerCommissioningRun{}, err
	}
	if command.RowsAffected() != 1 {
		return IndexerCommissioningRun{}, ErrConcurrentModification
	}
	detail, _ := json.Marshal(map[string]any{"profile_id": profileID.String(), "revision": revision, "schema_version": profile.SchemaVersion, "run_id": runID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','newznab.commissioning_draft_finalized','integration',$2,$3,$4)`, uuid.NewV7().String(), indexerID.String(), string(detail), completionTime); err != nil {
		return IndexerCommissioningRun{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCommissioningRun{}, err
	}
	return store.IndexerCommissioningRun(ctx, runID)
}

// FinalizeReadyCommissioningDraft is the automatic normal-completion entry
// point. It invokes the existing local draft-finalization transaction exactly
// when the run is durably ready, and is a no-op when it is not. It reads only
// authoritative durable state and performs no network, credential, or
// scheduler work. A caller treats any error as best-effort: the semantic
// evidence is already committed, and the owner-triggered FinalizeDraft path
// remains the idempotent local retry.
func (store *Store) FinalizeReadyCommissioningDraft(ctx context.Context, indexerID, runID string, expectedRevision uint64, now time.Time) (bool, error) {
	parsedIndexer, err := uuid.Parse(indexerID)
	if err != nil {
		return false, ErrInvalidIndexerCommissioning
	}
	parsedRun, err := uuid.Parse(runID)
	if err != nil {
		return false, ErrInvalidIndexerCommissioning
	}
	if expectedRevision == 0 {
		return false, ErrInvalidIndexerCommissioning
	}
	if _, err := store.FinalizeIndexerCommissioningDraft(ctx, parsedIndexer, parsedRun, expectedRevision, now); err != nil {
		if errors.Is(err, ErrIndexerCommissioningDraftUnavailable) || errors.Is(err, ErrConcurrentModification) {
			// Not all authorized search steps are terminal safe yet, or a
			// concurrent completion owns the transition. Leave the run as-is
			// for the next completion or the owner-triggered retry.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// commissioningDraftInput converts the durable run and its accepted steps into
// the pure builder input. It reads no provider data and makes no request.
func commissioningDraftInput(run IndexerCommissioningRun) (commissioningv2.DraftInput, error) {
	capsEvidence := commissioningCAPSEvidence(run)
	if capsEvidence == nil || run.SearchPlan == nil || len(run.Steps) != len(run.SearchPlan.Steps)+1 {
		return commissioningv2.DraftInput{}, ErrInvalidIndexerCommissioning
	}
	endpoint, err := commissioningv2.SHA256DigestFromBytes(run.EndpointConfigSHA256)
	if err != nil {
		return commissioningv2.DraftInput{}, ErrInvalidIndexerCommissioning
	}
	capsPlan, err := commissioningv2.SHA256DigestFromBytes(run.CAPSPlanDocumentSHA256)
	if err != nil {
		return commissioningv2.DraftInput{}, ErrInvalidIndexerCommissioning
	}
	searchPlan, err := commissioningv2.SHA256DigestFromBytes(run.SearchPlanDocumentSHA256)
	if err != nil {
		return commissioningv2.DraftInput{}, ErrInvalidIndexerCommissioning
	}
	var categoryDigest commissioningv2.SHA256Digest
	if run.SearchPlan.CategorySelectionSHA256 != nil {
		categoryDigest = *run.SearchPlan.CategorySelectionSHA256
	}
	input := commissioningv2.DraftInput{
		IndexerInstanceID:       run.IndexerInstanceID.String(),
		RunID:                   run.ID.String(),
		EndpointConfigSHA256:    endpoint,
		ProviderContract:        run.ProviderContract,
		ProfileSchemaVersion:    run.ProfileSchemaVersion,
		PlannerVersion:          run.PlannerVersion,
		CategorySelectionSHA256: categoryDigest,
		FixtureSHA256:           run.CAPSPlan.FixtureSHA256,
		CAPSPlanSHA256:          capsPlan,
		SearchPlanSHA256:        searchPlan,
		CAPSEvidence:            *capsEvidence,
		SearchSteps:             make([]commissioningv2.DraftSearchStep, len(run.SearchPlan.Steps)),
	}
	for index := range run.SearchPlan.Steps {
		input.SearchSteps[index] = commissioningv2.DraftSearchStep{Plan: run.SearchPlan.Steps[index], Evidence: run.Steps[index+1].Evidence}
	}
	return input, nil
}

// verifyLinkedCommissioningDraft re-reads the linked draft on replay and proves
// it belongs to the same indexer and the same endpoint/account digest as the
// run. A mismatched link fails closed instead of silently returning a draft
// produced for a different configuration.
func verifyLinkedCommissioningDraft(ctx context.Context, transaction pgx.Tx, indexerID, profileID uuid.UUID, expectedDigest []byte) error {
	var linkedIndexer string
	var endpointDigest []byte
	if err := transaction.QueryRow(ctx, `SELECT indexer_instance_id::text,endpoint_config_sha256 FROM indexer_capability_profiles WHERE id=$1`, profileID.String()).Scan(&linkedIndexer, &endpointDigest); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidIndexerCommissioning
	} else if err != nil {
		return err
	}
	if linkedIndexer != indexerID.String() || !bytes.Equal(endpointDigest, expectedDigest) {
		return ErrInvalidIndexerCommissioning
	}
	return nil
}
