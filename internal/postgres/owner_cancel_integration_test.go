package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestCancelWorkflowForOwnerAllowsSettledAttentionWork proves the owner can
// cancel a workflow that needs attention once its AltMount jobs have settled,
// that the cancellation leaves the job and its retained output untouched, and
// that work with an active job or with retrieval under way is still refused.
func TestCancelWorkflowForOwnerAllowsSettledAttentionWork(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()

	setState := func(workflowID uuid.UUID, state, code string) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state=$2,attention_code=NULLIF($3,''),revision=revision+1,updated_at=now() WHERE id=$1`, workflowID.String(), state, code); err != nil {
			t.Fatal(err)
		}
	}
	setJob := func(jobID uuid.UUID, state string) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state=$2 WHERE id=$1`, jobID.String(), state); err != nil {
			t.Fatal(err)
		}
	}
	stateOf := func(workflowID uuid.UUID) (string, bool) {
		t.Helper()
		var state string
		var terminal bool
		if err := store.Pool.QueryRow(ctx, `SELECT state,terminal_at IS NOT NULL FROM workflows WHERE id=$1`, workflowID.String()).Scan(&state, &terminal); err != nil {
			t.Fatal(err)
		}
		return state, terminal
	}

	// Attention work whose only AltMount job failed and settled: cancellable.
	_, settled := fixture.movieWorkflow(t, "Owner Cancel Settled", "590001")
	settledJob, _, _ := seedSubtitleAttempt(t, ctx, store, settled, fixture.altMount.ID, fixture.indexer.ID, 1, "owner-cancel-settled")
	setJob(settledJob.JobID, "needs_attention")
	setState(settled, "needs_attention", "seerr_request_revoked")
	var assetsBefore int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_output_assets asset JOIN altmount_output_inventories inventory ON inventory.id=asset.inventory_id WHERE inventory.job_id=$1`, settledJob.JobID.String()).Scan(&assetsBefore); err != nil || assetsBefore == 0 {
		t.Fatalf("retained assets before=%d err=%v, want a retained output", assetsBefore, err)
	}
	cancelled, err := store.CancelWorkflowForOwner(ctx, settled, now)
	if err != nil {
		t.Fatalf("cancel settled attention workflow: %v", err)
	}
	if cancelled.State != "cancelled" {
		t.Fatalf("cancelled state=%q", cancelled.State)
	}
	if state, terminal := stateOf(settled); state != "cancelled" || !terminal {
		t.Fatalf("settled workflow state=%q terminal=%v, want cancelled terminal", state, terminal)
	}
	var jobState string
	var assetsAfter, openRounds int
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM altmount_jobs WHERE id=$1`, settledJob.JobID.String()).Scan(&jobState); err != nil || jobState != "needs_attention" {
		t.Fatalf("settled job state=%q err=%v, want it untouched", jobState, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_output_assets asset JOIN altmount_output_inventories inventory ON inventory.id=asset.inventory_id WHERE inventory.job_id=$1`, settledJob.JobID.String()).Scan(&assetsAfter); err != nil || assetsAfter != assetsBefore {
		t.Fatalf("retained assets after=%d err=%v, want %d untouched", assetsAfter, err, assetsBefore)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, settled.String()).Scan(&openRounds); err != nil || openRounds != 0 {
		t.Fatalf("open rounds after cancel=%d err=%v, want 0", openRounds, err)
	}
	var reason, actor string
	if err := store.Pool.QueryRow(ctx, `SELECT reason,actor FROM workflow_transitions WHERE workflow_id=$1 AND to_state='cancelled'`, settled.String()).Scan(&reason, &actor); err != nil || actor != "owner" || reason == "" {
		t.Fatalf("cancel transition reason=%q actor=%q err=%v", reason, actor, err)
	}

	// Attention work with a job AltMount is still processing: refused, unchanged.
	_, active := fixture.movieWorkflow(t, "Owner Cancel Active Job", "590002")
	activeJob, _, _ := seedSubtitleAttempt(t, ctx, store, active, fixture.altMount.ID, fixture.indexer.ID, 1, "owner-cancel-active")
	setJob(activeJob.JobID, "processing")
	setState(active, "needs_attention", "altmount_job_attention")
	if _, err := store.CancelWorkflowForOwner(ctx, active, now); !errors.Is(err, ErrWorkflowAltMountJobActive) {
		t.Fatalf("cancel with an active job err=%v, want ErrWorkflowAltMountJobActive", err)
	}
	if state, terminal := stateOf(active); state != "needs_attention" || terminal {
		t.Fatalf("active-job workflow state=%q terminal=%v, want unchanged", state, terminal)
	}

	// Work that is verifying output is never owner-cancellable.
	_, verifying := fixture.movieWorkflow(t, "Owner Cancel Verifying", "590003")
	setState(verifying, "verifying", "")
	if _, err := store.CancelWorkflowForOwner(ctx, verifying, now); !errors.Is(err, ErrWorkflowNotCancellable) {
		t.Fatalf("cancel verifying err=%v, want ErrWorkflowNotCancellable", err)
	}

	// Pre-retrieval work stays cancellable exactly as before.
	_, waiting := fixture.movieWorkflow(t, "Owner Cancel Waiting", "590004")
	setState(waiting, "waiting", "")
	if _, err := store.CancelWorkflowForOwner(ctx, waiting, now); err != nil {
		t.Fatalf("cancel waiting workflow: %v", err)
	}
	if state, _ := stateOf(waiting); state != "cancelled" {
		t.Fatalf("waiting workflow state=%q, want cancelled", state)
	}

	if _, err := store.CancelWorkflowForOwner(ctx, uuid.NewV7(), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel unknown workflow err=%v, want ErrNotFound", err)
	}
}
