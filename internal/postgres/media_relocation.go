package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

const (
	relocationLease = 5 * time.Minute
	// The recovery event fires only after a crashed run's lease has expired.
	relocationRecoveryDelay = relocationLease + time.Minute
	relocationRetryDelay    = 15 * time.Minute
)

type relocationFolder struct{ root, relative string }

func (store *Store) checkpointRelocation() error {
	if store.relocateCheckpoint == nil {
		return nil
	}
	return store.relocateCheckpoint()
}

// planMediaRelocation records every workflow and Plex folder of a move, plus
// the recovery event, in one transaction before any link changes.
func (store *Store) planMediaRelocation(ctx context.Context, mediaID, targetRootID uuid.UUID, move []mediaWorkflowRow, folders []relocationFolder, now time.Time) (uuid.UUID, error) {
	id := uuid.NewV7()
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return id, err
	}
	defer transaction.Rollback(ctx)
	command, err := transaction.Exec(ctx, `INSERT INTO media_relocations(id,media_id,target_root_id,state,created_at,updated_at) VALUES($1,$2,$3,'running',$4,$4) ON CONFLICT (media_id) WHERE state='running' DO NOTHING`, id.String(), mediaID.String(), targetRootID.String(), now.UTC())
	if err != nil {
		return id, err
	}
	if command.RowsAffected() != 1 {
		return id, refuse("relocation_in_progress", "this title is already being moved", "Wait a moment and check the title again.")
	}
	ordinal := 0
	for _, row := range move {
		ordinal++
		if _, err := transaction.Exec(ctx, `INSERT INTO media_relocation_steps(relocation_id,ordinal,kind,workflow_id) VALUES($1,$2,'workflow',$3)`, id.String(), ordinal, row.id.String()); err != nil {
			return id, err
		}
	}
	for _, folder := range folders {
		ordinal++
		if _, err := transaction.Exec(ctx, `INSERT INTO media_relocation_steps(relocation_id,ordinal,kind,root_id,relative_path) VALUES($1,$2,'plex_folder',$3,$4)`, id.String(), ordinal, folder.root, folder.relative); err != nil {
			return id, err
		}
	}
	payload, _ := json.Marshal(map[string]string{"relocation_id": id.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'media_relocation',$2,1,'media.relocation_resume_requested',$3,$4,$5)`, uuid.NewV7().String(), id.String(), payload, now.UTC(), now.UTC().Add(relocationRecoveryDelay)); err != nil {
		return id, err
	}
	return id, transaction.Commit(ctx)
}

// claimRelocation takes or renews the run lease, so a recovery event and an
// owner retry can never work on the same move at once.
func (store *Store) claimRelocation(ctx context.Context, id uuid.UUID, renew bool) (bool, error) {
	guard := `AND (lease_until IS NULL OR lease_until<now())`
	if renew {
		guard = ""
	}
	command, err := store.Pool.Exec(ctx, `UPDATE media_relocations SET lease_until=now()+make_interval(secs=>$2) WHERE id=$1 AND state='running' `+guard, id.String(), relocationLease.Seconds())
	return command.RowsAffected() == 1, err
}

func (store *Store) releaseRelocation(ctx context.Context, id uuid.UUID, failure error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	detail := ""
	if failure != nil {
		detail = "move_failed"
		var refusal ActionRefusal
		if errors.As(failure, &refusal) {
			detail = refusal.Code
		}
	}
	_, _ = store.Pool.Exec(ctx, `UPDATE media_relocations SET lease_until=NULL,last_error=NULLIF($2,''),updated_at=now() WHERE id=$1 AND state='running'`, id.String(), detail)
}

// runMediaRelocation works through the unfinished steps of a stored plan. A
// workflow step is safe to repeat, so an interruption anywhere only means the
// next run starts at the first step not recorded as finished.
func (store *Store) runMediaRelocation(ctx context.Context, id uuid.UUID, now time.Time) (result MediaRelocation, err error) {
	var mediaText, targetText, state string
	if err := store.Pool.QueryRow(ctx, `SELECT media_id::text,target_root_id::text,state FROM media_relocations WHERE id=$1`, id.String()).Scan(&mediaText, &targetText, &state); errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	if result.MediaID, err = uuid.Parse(mediaText); err != nil {
		return result, err
	}
	if result.RootID, err = uuid.Parse(targetText); err != nil {
		return result, err
	}
	if state == "complete" {
		return store.relocationTotals(ctx, id, result)
	}
	claimed, err := store.claimRelocation(ctx, id, false)
	if err != nil {
		return result, err
	}
	if !claimed {
		return result, refuse("relocation_in_progress", "this title is already being moved", "Wait a moment and check the title again.")
	}
	defer func() { store.releaseRelocation(ctx, id, err) }()
	pending, err := store.pendingRelocationWorkflows(ctx, id)
	if err != nil {
		return result, err
	}
	for _, step := range pending {
		if _, err := store.claimRelocation(ctx, id, true); err != nil {
			return result, err
		}
		_, moveErr := store.RelocateWorkflowLinks(ctx, step.workflow, result.RootID, now)
		var refusal ActionRefusal
		// A title removed during the move leaves nothing to carry over.
		if moveErr != nil && !(errors.As(moveErr, &refusal) && refusal.Code == "no_current_links") {
			return result, moveErr
		}
		if err := store.checkpointRelocation(); err != nil {
			return result, err
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE media_relocation_steps SET completed_at=$3 WHERE relocation_id=$1 AND ordinal=$2`, id.String(), step.ordinal, now.UTC()); err != nil {
			return result, err
		}
		if err := store.checkpointRelocation(); err != nil {
			return result, err
		}
	}
	if err := store.finishMediaRelocation(ctx, id, now); err != nil {
		return result, err
	}
	return store.relocationTotals(ctx, id, result)
}

type relocationWorkflowStep struct {
	ordinal  int
	workflow uuid.UUID
}

func (store *Store) pendingRelocationWorkflows(ctx context.Context, id uuid.UUID) ([]relocationWorkflowStep, error) {
	rows, err := store.Pool.Query(ctx, `SELECT ordinal,workflow_id::text FROM media_relocation_steps WHERE relocation_id=$1 AND kind='workflow' AND completed_at IS NULL ORDER BY ordinal`, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []relocationWorkflowStep
	for rows.Next() {
		var step relocationWorkflowStep
		var workflowText string
		if err := rows.Scan(&step.ordinal, &workflowText); err != nil {
			return nil, err
		}
		if step.workflow, err = uuid.Parse(workflowText); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

// finishMediaRelocation queues every Plex refresh and closes the move in one
// transaction, so Plex is asked exactly once and only after all links moved.
func (store *Store) finishMediaRelocation(ctx context.Context, id uuid.UUID, now time.Time) error {
	if err := store.checkpointRelocation(); err != nil {
		return err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state string
	if err := transaction.QueryRow(ctx, `SELECT state FROM media_relocations WHERE id=$1 FOR UPDATE`, id.String()).Scan(&state); err != nil {
		return err
	}
	if state == "complete" {
		return nil
	}
	rows, err := transaction.Query(ctx, `SELECT ordinal,root_id::text,relative_path FROM media_relocation_steps WHERE relocation_id=$1 AND kind='plex_folder' AND completed_at IS NULL ORDER BY ordinal`, id.String())
	if err != nil {
		return err
	}
	type folderStep struct {
		ordinal        int
		root, relative string
	}
	var folders []folderStep
	for rows.Next() {
		var step folderStep
		if err := rows.Scan(&step.ordinal, &step.root, &step.relative); err != nil {
			rows.Close()
			return err
		}
		folders = append(folders, step)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, folder := range folders {
		payload, _ := json.Marshal(map[string]string{"root_id": folder.root, "relative": folder.relative})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'plex_folder',$2,1,'plex.folder_refresh_requested',$3,$4,$4)`, uuid.NewV7().String(), uuid.NewV7().String(), payload, now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE media_relocation_steps SET completed_at=$3 WHERE relocation_id=$1 AND ordinal=$2`, id.String(), folder.ordinal, now.UTC()); err != nil {
			return err
		}
	}
	if _, err := transaction.Exec(ctx, `UPDATE media_relocations SET state='complete',lease_until=NULL,last_error=NULL,updated_at=$2,completed_at=$2 WHERE id=$1`, id.String(), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// relocationTotals counts from the records, so a resumed move reports what
// actually moved rather than what the last attempt happened to touch.
func (store *Store) relocationTotals(ctx context.Context, id uuid.UUID, result MediaRelocation) (MediaRelocation, error) {
	err := store.Pool.QueryRow(ctx, `WITH moved AS (SELECT workflow_id FROM media_relocation_steps WHERE relocation_id=$1 AND kind='workflow')
		SELECT (SELECT count(*) FROM moved),
		(SELECT count(*) FROM library_links l JOIN library_link_versions v ON v.library_link_id=l.id AND v.state='current' WHERE v.workflow_id IN (SELECT workflow_id FROM moved) AND l.root_id=$2)
		+(SELECT count(*) FROM library_link_companions c JOIN library_link_versions v ON v.id=c.link_version_id AND v.state='current' JOIN library_links l ON l.id=v.library_link_id AND l.root_id=$2 WHERE v.workflow_id IN (SELECT workflow_id FROM moved) AND c.state IN ('verified','current'))`, id.String(), result.RootID.String()).Scan(&result.Workflows, &result.Moved)
	return result, err
}

// relocationPending keeps the recovery event alive until the move is finished.
type relocationPending struct{ until time.Time }

func (relocationPending) Error() string          { return "media relocation is not finished" }
func (relocationPending) SafeCode() string       { return "relocation_pending" }
func (err relocationPending) RetryAt() time.Time { return err.until }

// ResumeMediaRelocation finishes a move whose first run did not complete, for
// example because the process restarted. A finished move does nothing.
func (store *Store) ResumeMediaRelocation(ctx context.Context, event OutboxEvent) error {
	var body struct {
		RelocationID string `json:"relocation_id"`
	}
	if err := json.Unmarshal(event.Payload, &body); err != nil {
		return err
	}
	id, err := uuid.Parse(body.RelocationID)
	if err != nil {
		return err
	}
	if _, err := store.runMediaRelocation(ctx, id, time.Now().UTC()); err != nil {
		return relocationPending{until: time.Now().Add(relocationRetryDelay)}
	}
	return nil
}
