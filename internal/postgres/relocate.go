package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/library"
)

// RelocateWorkflowLinks moves every current Plex-facing link (and verified
// subtitle companion) of a workflow from its library root to another enabled
// root of the same media kind, updates the link and workflow records, and
// records an audit event. The link targets and the read-only AltMount content
// are never touched. Moves are resumable: a rerun completes a partially
// applied change because RelocateSymlink is idempotent.
func (store *Store) RelocateWorkflowLinks(ctx context.Context, workflowID, targetRootID uuid.UUID, now time.Time) (int, error) {
	var mediaKind, oldRootID, fromRoot, state string
	if err := store.Pool.QueryRow(ctx, `SELECT w.root_id::text,r.media_kind,r.destination_root,w.state
		FROM workflows w JOIN library_roots r ON r.id=w.root_id WHERE w.id=$1`, workflowID.String()).Scan(&oldRootID, &mediaKind, &fromRoot, &state); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	switch state {
	case "searching", "candidate_selected", "submitting", "processing", "streaming_ready", "import_complete", "publishing", "plex_scanning":
		return 0, refuse("workflow_in_flight", "workflow has an in-flight acquisition or publication; relocate after it settles", "Wait until the workflow is not acquiring or publishing, then retry.")
	}
	if oldRootID == targetRootID.String() {
		return 0, nil
	}
	var targetKind, toRoot string
	var enabled bool
	if err := store.Pool.QueryRow(ctx, `SELECT media_kind,destination_root,enabled FROM library_roots WHERE id=$1`, targetRootID.String()).Scan(&targetKind, &toRoot, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, refuse("library_root_disabled", "target library root is disabled", "Choose an enabled root from GET /api/v1/settings/library-roots.")
	}
	if targetKind != mediaKind {
		return 0, refuse("library_root_wrong_kind", "target library root does not serve this media kind", "Choose a root with the same media_kind from GET /api/v1/settings/library-roots.")
	}
	type relocateItem struct {
		linkID   string
		relative string
		target   string
	}
	links := []relocateItem{}
	rows, err := store.Pool.Query(ctx, `SELECT l.id::text,l.destination_relative,v.plex_visible_target
		FROM library_links l JOIN library_link_versions v ON v.library_link_id=l.id AND v.state='current'
		WHERE v.workflow_id=$1 AND l.root_id=$2 ORDER BY l.destination_relative`, workflowID.String(), oldRootID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var item relocateItem
		if err := rows.Scan(&item.linkID, &item.relative, &item.target); err != nil {
			rows.Close()
			return 0, err
		}
		links = append(links, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	companions := []relocateItem{}
	rows, err = store.Pool.Query(ctx, `SELECT c.destination_relative,c.plex_visible_target
		FROM library_link_companions c
		JOIN library_link_versions v ON v.id=c.link_version_id AND v.state='current'
		JOIN library_links l ON l.id=v.library_link_id AND l.root_id=$2
		WHERE v.workflow_id=$1 AND c.state='verified' ORDER BY c.destination_relative`, workflowID.String(), oldRootID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var item relocateItem
		if err := rows.Scan(&item.relative, &item.target); err != nil {
			rows.Close()
			return 0, err
		}
		companions = append(companions, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(links) == 0 && len(companions) == 0 {
		return 0, refuse("no_current_links", "workflow has no current links in its library root", "Only workflows with published links can be relocated.")
	}
	moved := 0
	for _, item := range links {
		if err := library.RelocateSymlink(fromRoot, toRoot, item.relative, item.relative, item.target); err != nil {
			return moved, fmt.Errorf("relocate %s: %w", item.relative, err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE library_links SET root_id=$2 WHERE id=$1`, item.linkID, targetRootID.String()); err != nil {
			return moved, err
		}
		moved++
	}
	for _, item := range companions {
		if err := library.RelocateSymlink(fromRoot, toRoot, item.relative, item.relative, item.target); err != nil {
			return moved, fmt.Errorf("relocate subtitle %s: %w", item.relative, err)
		}
		moved++
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET root_id=$2,revision=revision+1,updated_at=$3 WHERE id=$1`, workflowID.String(), targetRootID.String(), now.UTC()); err != nil {
		return moved, err
	}
	detail, _ := json.Marshal(map[string]any{"workflow_id": workflowID.String(), "from_root": oldRootID, "to_root": targetRootID.String(), "links": len(links), "companions": len(companions)})
	if _, err := store.Pool.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','library.links_relocated','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), string(detail), now.UTC()); err != nil {
		return moved, err
	}
	return moved, nil
}
