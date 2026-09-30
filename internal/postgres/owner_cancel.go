package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

var (
	// ErrWorkflowNotCancellable reports a workflow whose retrieval, output
	// verification or publication is under way, so the owner cannot cancel it.
	ErrWorkflowNotCancellable = errors.New("workflow can no longer be cancelled safely")
	// ErrWorkflowAltMountJobActive reports a workflow that needs attention but
	// still has an AltMount job in flight; that job must settle first.
	ErrWorkflowAltMountJobActive = errors.New("an AltMount job for this workflow is still active; resolve it before cancelling")
)

// CancelWorkflowForOwner cancels a workflow at the owner's request. Work that
// has not retrieved anything can always be cancelled. A workflow that needs
// attention can be cancelled once none of its AltMount jobs is active; its
// jobs, retained output and exclusions are left untouched and stay
// owner-visible. Cancellation closes the open round and pauses retry
// discovery exactly like every other cancellation.
func (store *Store) CancelWorkflowForOwner(ctx context.Context, id uuid.UUID, now time.Time) (workflows.Workflow, error) {
	var state string
	var revision int64
	var activeJob bool
	err := store.Pool.QueryRow(ctx, `SELECT workflow.state,workflow.revision,
		EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=workflow.id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		FROM workflows workflow WHERE workflow.id=$1`, id.String()).Scan(&state, &revision, &activeJob)
	if errors.Is(err, pgx.ErrNoRows) {
		return workflows.Workflow{}, ErrNotFound
	}
	if err != nil {
		return workflows.Workflow{}, err
	}
	reason := "owner cancelled before retrieval"
	switch workflows.State(state) {
	case workflows.StateMonitored, workflows.StateWaiting, workflows.StateSearching, workflows.StateCandidateSelected, workflows.StateHeld, workflows.StateBlocked:
	case workflows.StateNeedsAttention:
		if activeJob {
			return workflows.Workflow{}, ErrWorkflowAltMountJobActive
		}
		reason = "owner cancelled after reviewing the attention item"
	default:
		return workflows.Workflow{}, ErrWorkflowNotCancellable
	}
	// The revision guard makes the check above binding: any state change or
	// new AltMount submission in between bumps the revision and fails here.
	return store.TransitionWorkflow(ctx, id, uint64(revision), workflows.StateCancelled, reason, "owner", now)
}
