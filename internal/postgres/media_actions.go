package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

// MediaRelocation reports a title-wide move to another library root.
type MediaRelocation struct {
	MediaID   uuid.UUID `json:"media_id"`
	RootID    uuid.UUID `json:"root_id"`
	Workflows int       `json:"workflows"`
	Moved     int       `json:"moved"`
}

// inFlightStates are acquiring or publishing: links must not move under them
// and they cannot be cancelled.
var inFlightStates = map[string]bool{
	"searching": true, "candidate_selected": true, "submitting": true, "queued": true, "processing": true,
	"streaming_ready": true, "import_complete": true, "verifying": true, "publishing": true, "plex_scanning": true,
}

type mediaWorkflowRow struct {
	id       uuid.UUID
	rootID   string
	edition  string
	state    string
	terminal bool
	links    []string // destination_relative of current links in the workflow's root
}

func (store *Store) mediaWorkflowRows(ctx context.Context, mediaID uuid.UUID) ([]mediaWorkflowRow, error) {
	rows, err := store.Pool.Query(ctx, `SELECT w.id::text,w.root_id::text,r.edition,w.state,w.terminal_at IS NOT NULL,
		COALESCE((SELECT array_agg(l.destination_relative ORDER BY l.destination_relative) FROM library_links l
		 JOIN library_link_versions v ON v.library_link_id=l.id AND v.state='current'
		 WHERE v.workflow_id=w.id AND l.root_id=w.root_id),'{}')
		FROM workflows w JOIN library_roots r ON r.id=w.root_id WHERE w.media_id=$1 ORDER BY w.created_at,w.id`, mediaID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []mediaWorkflowRow
	for rows.Next() {
		var row mediaWorkflowRow
		var idText string
		if err := rows.Scan(&idText, &row.rootID, &row.edition, &row.state, &row.terminal, &row.links); err != nil {
			return nil, err
		}
		if row.id, err = uuid.Parse(idText); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// RelocateMedia moves a whole title (every season of a series) to another
// enabled library root of the same kind and edition. Nothing moves when any
// part of the title is acquiring or publishing. After the moves it queues a
// Plex refresh of the title's folder in the old and the new root, so Plex
// drops the old entry and finds the new one. Link targets and AltMount
// content are never touched.
func (store *Store) RelocateMedia(ctx context.Context, mediaID, targetRootID uuid.UUID, now time.Time) (MediaRelocation, error) {
	result := MediaRelocation{MediaID: mediaID, RootID: targetRootID}
	var deleted bool
	if err := store.Pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM media_items WHERE id=$1`, mediaID.String()).Scan(&deleted); errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	if deleted {
		return result, refuse("media_removed", "this title was removed from the library", "Restore it from the recycle bin first.")
	}
	var targetEdition string
	var enabled bool
	if err := store.Pool.QueryRow(ctx, `SELECT edition,enabled FROM library_roots WHERE id=$1`, targetRootID.String()).Scan(&targetEdition, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	if !enabled {
		return result, refuse("library_root_disabled", "target library root is disabled", "Choose an enabled root from GET /api/v1/settings/library-roots.")
	}
	all, err := store.mediaWorkflowRows(ctx, mediaID)
	if err != nil {
		return result, err
	}
	var move []mediaWorkflowRow
	for _, row := range all {
		if row.state == string(workflows.StateCancelled) || row.rootID == targetRootID.String() || row.edition != targetEdition {
			continue
		}
		if row.terminal && len(row.links) == 0 {
			continue
		}
		if inFlightStates[row.state] {
			return result, refuse("workflow_in_flight", "part of this title is downloading or being published; move it after that settles", "Wait until no workflow of the media is acquiring or publishing, then retry.")
		}
		move = append(move, row)
	}
	if len(move) == 0 {
		return result, refuse("nothing_to_move", "nothing of this title is in another library root of the same edition", "Choose another root, or check the media's workflows.")
	}
	type folder struct{ root, relative string }
	seen := map[folder]bool{}
	var folders []folder
	addFolder := func(root, relative string) {
		top := strings.Split(filepath.ToSlash(relative), "/")[0]
		if top == "" || top == "." || top == ".." {
			return
		}
		key := folder{root, top}
		if !seen[key] {
			seen[key] = true
			folders = append(folders, key)
		}
	}
	for _, row := range move {
		moved, err := store.RelocateWorkflowLinks(ctx, row.id, targetRootID, now)
		result.Moved += moved
		if err != nil {
			return result, err
		}
		result.Workflows++
		for _, relative := range row.links {
			addFolder(row.rootID, relative)
			addFolder(targetRootID.String(), relative)
		}
	}
	for _, item := range folders {
		payload, _ := json.Marshal(map[string]string{"root_id": item.root, "relative": item.relative})
		if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'plex_folder',$2,1,'plex.folder_refresh_requested',$3,$4,$4)`, uuid.NewV7().String(), uuid.NewV7().String(), string(payload), now.UTC()); err != nil {
			return result, err
		}
	}
	return result, nil
}

// PlexFolder is a folder below a library root that Plex should rescan.
type PlexFolder struct {
	Root        LibraryRoot
	Integration Integration
	Relative    string
}

// PlexForFolderRefresh resolves a plex.folder_refresh_requested payload. ok is
// false when the root has no Plex library, so there is nothing to refresh.
func (store *Store) PlexForFolderRefresh(ctx context.Context, payload []byte) (PlexFolder, bool, error) {
	var body struct {
		RootID   string `json:"root_id"`
		Relative string `json:"relative"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return PlexFolder{}, false, err
	}
	rootID, err := uuid.Parse(body.RootID)
	if err != nil {
		return PlexFolder{}, false, err
	}
	clean := filepath.Clean(body.Relative)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return PlexFolder{}, false, errors.New("folder refresh path is unsafe")
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return PlexFolder{}, false, err
	}
	for _, root := range roots {
		if root.ID != rootID {
			continue
		}
		if root.PlexInstanceID == nil || root.PlexLibraryID == "" || root.PlexVisibleRoot == "" {
			return PlexFolder{}, false, nil
		}
		integrations, err := store.ListIntegrations(ctx)
		if err != nil {
			return PlexFolder{}, false, err
		}
		for _, integration := range integrations {
			if integration.ID == *root.PlexInstanceID {
				return PlexFolder{Root: root, Integration: integration, Relative: clean}, true, nil
			}
		}
		return PlexFolder{}, false, nil
	}
	return PlexFolder{}, false, nil
}

// CancelOpenWorkOfMedia cancels every open workflow of a title before it is
// deleted: searches, waiting rounds and the monitoring of new episodes. It
// cancels nothing when any of them is downloading or publishing.
func (store *Store) CancelOpenWorkOfMedia(ctx context.Context, mediaID uuid.UUID, now time.Time) (int, error) {
	rows, err := store.Pool.Query(ctx, `SELECT w.id::text,w.state,
		EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=w.id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
		FROM workflows w WHERE w.media_id=$1 AND w.terminal_at IS NULL ORDER BY w.created_at,w.id`, mediaID.String())
	if err != nil {
		return 0, err
	}
	type open struct {
		id        uuid.UUID
		state     workflows.State
		activeJob bool
	}
	var items []open
	for rows.Next() {
		var idText, state string
		var item open
		if err := rows.Scan(&idText, &state, &item.activeJob); err != nil {
			rows.Close()
			return 0, err
		}
		if item.id, err = uuid.Parse(idText); err != nil {
			rows.Close()
			return 0, err
		}
		item.state = workflows.State(state)
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range items {
		switch item.state {
		case workflows.StateMonitored, workflows.StateWaiting, workflows.StateSearching, workflows.StateCandidateSelected, workflows.StateHeld, workflows.StateBlocked:
		case workflows.StateNeedsAttention:
			if item.activeJob {
				return 0, refuse("workflow_in_flight", "part of this title has an active AltMount job; delete it after that settles", "Wait until no workflow of the media has an active AltMount job, then retry.")
			}
		default:
			return 0, refuse("workflow_in_flight", "part of this title is downloading or being published; delete it after that settles", "Wait until no workflow of the media is acquiring or publishing, then retry.")
		}
	}
	cancelled := 0
	for _, item := range items {
		if _, err := store.CancelWorkflowForOwner(ctx, item.id, now); err != nil {
			return cancelled, err
		}
		cancelled++
	}
	return cancelled, nil
}
