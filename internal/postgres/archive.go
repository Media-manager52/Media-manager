package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

var (
	// ErrMediaHasPublishedFiles refuses an archive that would strand a
	// legitimate current publication.
	ErrMediaHasPublishedFiles = errors.New("media has current published files; archive is refused")
	// ErrMediaHasInFlightWork refuses an archive while acquisition or
	// publication work is still active or cannot be cancelled by the state
	// machine.
	ErrMediaHasInFlightWork = errors.New("media has in-flight work that cannot be archived safely")
)

// MediaArchiveResult reports the durable effect of an archive operation. It
// never includes deleted files or AltMount sources, because ArchiveMedia never
// touches them.
type MediaArchiveResult struct {
	MediaID              uuid.UUID   `json:"media_id"`
	Title                string      `json:"title"`
	AlreadyArchived      bool        `json:"already_archived"`
	CancelledWorkflows   int         `json:"cancelled_workflows"`
	CancelledWorkflowIDs []uuid.UUID `json:"cancelled_workflow_ids"`
}

// ArchiveMedia retires an owner-confirmed unwanted media record (for example
// confirmed test/acceptance residue) from the active catalog without deleting
// any file, AltMount source, workflow history, or audit row.
//
// It refuses to touch media that has current published files or in-flight
// acquisition/publication work, so it can never unlink or strand legitimate
// media. Cancellation uses the domain state machine, so a workflow that cannot
// legally transition to cancelled also refuses the archive. The media row is
// soft-deleted through the existing removed/archived catalog state, which hides
// it from the normal Library/Wanted/Active Work views while every durable row
// stays inspectable.
func (store *Store) ArchiveMedia(ctx context.Context, mediaID uuid.UUID, now time.Time) (MediaArchiveResult, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return MediaArchiveResult{}, err
	}
	defer transaction.Rollback(ctx)

	var title string
	var deletedAt *time.Time
	if err := transaction.QueryRow(ctx, `SELECT title,deleted_at FROM media_items WHERE id=$1 FOR UPDATE`, mediaID.String()).Scan(&title, &deletedAt); errors.Is(err, pgx.ErrNoRows) {
		return MediaArchiveResult{}, ErrNotFound
	} else if err != nil {
		return MediaArchiveResult{}, err
	}
	result := MediaArchiveResult{MediaID: mediaID, Title: title, CancelledWorkflowIDs: []uuid.UUID{}}
	if deletedAt != nil {
		result.AlreadyArchived = true
		return result, transaction.Commit(ctx)
	}

	var published int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM library_link_versions version JOIN workflows workflow ON workflow.id=version.workflow_id WHERE workflow.media_id=$1 AND version.state='current'`, mediaID.String()).Scan(&published); err != nil {
		return MediaArchiveResult{}, err
	}
	if published > 0 {
		return MediaArchiveResult{}, ErrMediaHasPublishedFiles
	}
	var adopted int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM adopted_files WHERE media_id=$1 AND state='current'`, mediaID.String()).Scan(&adopted); err != nil {
		return MediaArchiveResult{}, err
	}
	if adopted > 0 {
		return MediaArchiveResult{}, ErrMediaHasPublishedFiles
	}
	var inFlight int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs job JOIN workflows workflow ON workflow.id=job.workflow_id WHERE workflow.media_id=$1 AND workflow.terminal_at IS NULL AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested')`, mediaID.String()).Scan(&inFlight); err != nil {
		return MediaArchiveResult{}, err
	}
	if inFlight > 0 {
		return MediaArchiveResult{}, ErrMediaHasInFlightWork
	}

	rows, err := transaction.Query(ctx, `SELECT id::text,state,revision FROM workflows WHERE media_id=$1 AND terminal_at IS NULL ORDER BY id FOR UPDATE`, mediaID.String())
	if err != nil {
		return MediaArchiveResult{}, err
	}
	type activeWorkflow struct {
		id       uuid.UUID
		state    workflows.State
		revision int64
	}
	var active []activeWorkflow
	for rows.Next() {
		var idText string
		var item activeWorkflow
		if err := rows.Scan(&idText, &item.state, &item.revision); err != nil {
			rows.Close()
			return MediaArchiveResult{}, err
		}
		item.id, _ = uuid.Parse(idText)
		active = append(active, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MediaArchiveResult{}, err
	}
	rows.Close()

	// Reuse the workflow state machine instead of duplicating its transition
	// rules: a state that cannot legally reach cancelled refuses the archive.
	for _, item := range active {
		candidate := workflows.Workflow{State: item.state}
		if _, err := candidate.Transition(workflows.StateCancelled, "owner archived media", "owner", now); err != nil {
			return MediaArchiveResult{}, ErrMediaHasInFlightWork
		}
	}
	for _, item := range active {
		newRevision := item.revision + 1
		command, err := transaction.Exec(ctx, `UPDATE workflows SET state='cancelled',revision=$2,updated_at=$3,terminal_at=$3,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$4`, item.id.String(), newRevision, now.UTC(), item.revision)
		if err != nil {
			return MediaArchiveResult{}, err
		}
		if command.RowsAffected() != 1 {
			return MediaArchiveResult{}, ErrConcurrentModification
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'cancelled','owner archived media','owner',$4)`, item.id.String(), newRevision, item.state, now.UTC()); err != nil {
			return MediaArchiveResult{}, err
		}
		result.CancelledWorkflowIDs = append(result.CancelledWorkflowIDs, item.id)
	}
	result.CancelledWorkflows = len(active)

	if _, err := transaction.Exec(ctx, `UPDATE media_items SET deleted_at=$2,revision=revision+1,updated_at=$2 WHERE id=$1`, mediaID.String(), now.UTC()); err != nil {
		return MediaArchiveResult{}, err
	}
	detail, err := json.Marshal(map[string]any{"title": title, "cancelled_workflows": len(active)})
	if err != nil {
		return MediaArchiveResult{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','media.archived','media',$2,$3,$4)`, uuid.NewV7().String(), mediaID.String(), string(detail), now.UTC()); err != nil {
		return MediaArchiveResult{}, err
	}
	return result, transaction.Commit(ctx)
}
