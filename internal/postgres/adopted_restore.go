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
	"github.com/media-manager/media-manager/internal/catalog"
)

const AdoptedRestoreConfirmationPhrase = "RESTORE RETAINED ADOPTED FILE AFTER PLEX VERIFICATION"

type AdoptedRestoreContext struct {
	Leader      RecycleEntry
	Entries     []RecycleEntry
	Media       catalog.MediaItem
	Root        LibraryRoot
	Integration Integration
}

type AdoptedRestoreConfirmation struct {
	RecycleEntryID uuid.UUID
	ObservedPath   string
	RatingKey      string
	ProviderGUID   string
	ConfirmedAt    time.Time
}

func (store *Store) RequestAdoptedRestore(ctx context.Context, entryID uuid.UUID, confirmation string, now time.Time) error {
	if confirmation != AdoptedRestoreConfirmationPhrase {
		return errors.New("exact adopted-file restore confirmation is required")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var cycleText, mediaText, state, entryKind, purpose string
	var replacementWorkflowValue *string
	if err := transaction.QueryRow(ctx, `SELECT cycle_id::text,media_id::text,replacement_workflow_id::text,state,entry_kind,purpose FROM recycle_entries WHERE id=$1`, entryID.String()).Scan(&cycleText, &mediaText, &replacementWorkflowValue, &state, &entryKind, &purpose); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "retained" || entryKind != "real_file" || purpose != "upgrade_displacement" || replacementWorkflowValue == nil {
		return errors.New("recycle cycle is not a retained adopted upgrade")
	}
	replacementWorkflowText := *replacementWorkflowValue
	var mediaKind catalog.MediaKind
	if err := transaction.QueryRow(ctx, `SELECT kind FROM media_items WHERE id=$1 FOR UPDATE`, mediaText).Scan(&mediaKind); err != nil {
		return err
	}
	var active int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE media_id=$1 AND variant=(SELECT variant FROM workflows WHERE id=$2) AND terminal_at IS NULL`, mediaText, replacementWorkflowText).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return errors.New("media has an active workflow; finish or cancel it before restoring adopted files")
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,adopted_file_id::text,root_id::text,state,entry_kind,purpose,replacement_workflow_id::text FROM recycle_entries WHERE cycle_id=$1 ORDER BY id FOR UPDATE`, cycleText)
	if err != nil {
		return err
	}
	type sibling struct{ id, adoptedID, rootID, state, kind, purpose, replacementWorkflow string }
	var siblings []sibling
	for rows.Next() {
		var item sibling
		if err := rows.Scan(&item.id, &item.adoptedID, &item.rootID, &item.state, &item.kind, &item.purpose, &item.replacementWorkflow); err != nil {
			rows.Close()
			return err
		}
		siblings = append(siblings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(siblings) == 0 {
		return ErrNotFound
	}
	if mediaKind == catalog.KindMovie && len(siblings) != 1 {
		return errors.New("a movie adopted restore must contain exactly one primary file")
	}
	for _, item := range siblings {
		if item.state != "retained" || item.kind != "real_file" || item.purpose != "upgrade_displacement" || item.replacementWorkflow != replacementWorkflowText || item.adoptedID == "" || item.rootID != siblings[0].rootID {
			return errors.New("the complete adopted-file cycle is not restorable")
		}
	}
	var currentLinks, totalLinks int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='current'),count(*) FROM library_link_versions WHERE workflow_id=$1`, replacementWorkflowText).Scan(&currentLinks, &totalLinks); err != nil {
		return err
	}
	if currentLinks == 0 || currentLinks != totalLinks || mediaKind == catalog.KindMovie && currentLinks != 1 {
		return errors.New("the complete Plex-confirmed replacement publication is no longer current")
	}
	deadline := now.UTC().Add(15 * time.Minute)
	for _, item := range siblings {
		if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='restore_planned',restore_revision=1,restore_attempts=0,restore_requested_at=$2,restore_deadline_at=$3,restore_refresh_at=NULL,restore_requested_by='owner' WHERE id=$1`, item.id, now.UTC(), deadline); err != nil {
			return err
		}
		command, err := transaction.Exec(ctx, `UPDATE adopted_files SET state='restore_confirming' WHERE id=$1 AND state='retained'`, item.adoptedID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
		payload, _ := json.Marshal(map[string]string{"recycle_entry_id": item.id})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,5,'recycle.restore_requested',$3,$4,$4)`, uuid.NewV7().String(), item.id, payload, now.UTC()); err != nil {
			return err
		}
	}
	detail, _ := json.Marshal(map[string]any{"entries": len(siblings), "plex_verification_required": true, "replacement_remains_current": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','recycle.adopted_restore_requested','recycle_cycle',$2,$3,$4)`, uuid.NewV7().String(), cycleText, detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) MarkAdoptedRestoreAwaitingPlex(ctx context.Context, entryID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var cycleText string
	if err := transaction.QueryRow(ctx, `SELECT cycle_id::text FROM recycle_entries WHERE id=$1`, entryID.String()).Scan(&cycleText); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,state FROM recycle_entries WHERE cycle_id=$1 ORDER BY id FOR UPDATE`, cycleText)
	if err != nil {
		return err
	}
	state := ""
	for rows.Next() {
		var siblingID, siblingState string
		if err := rows.Scan(&siblingID, &siblingState); err != nil {
			rows.Close()
			return err
		}
		if siblingID == entryID.String() {
			state = siblingState
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if state == "" {
		return ErrNotFound
	}
	if state == "awaiting_confirmation" || state == "restored" || state == "failed" {
		return transaction.Commit(ctx)
	}
	if state != "restore_planned" {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='awaiting_confirmation' WHERE id=$1`, entryID.String()); err != nil {
		return err
	}
	var remaining int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM recycle_entries WHERE cycle_id=$1 AND state='restore_planned'`, cycleText).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		var leaderText string
		var revision int64
		if err := transaction.QueryRow(ctx, `SELECT id::text,restore_revision FROM recycle_entries WHERE cycle_id=$1 ORDER BY id LIMIT 1 FOR UPDATE`, cycleText).Scan(&leaderText, &revision); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"recycle_entry_id": leaderText})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,$3,'plex.adopted_restore_confirm_requested',$4,$5,$5)`, uuid.NewV7().String(), leaderText, revision, payload, now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func (store *Store) LoadAdoptedRestoreContext(ctx context.Context, entryID uuid.UUID) (AdoptedRestoreContext, error) {
	entry, err := store.LoadRecycleEntry(ctx, entryID)
	if err != nil {
		return AdoptedRestoreContext{}, err
	}
	rows, err := store.Pool.Query(ctx, recycleEntrySelect+` WHERE entry.cycle_id=$1 ORDER BY entry.id`, entry.CycleID.String())
	if err != nil {
		return AdoptedRestoreContext{}, err
	}
	entries, err := scanRecycleEntries(rows)
	if err != nil {
		return AdoptedRestoreContext{}, err
	}
	if len(entries) == 0 {
		return AdoptedRestoreContext{}, ErrNotFound
	}
	result := AdoptedRestoreContext{Leader: entries[0], Entries: entries}
	for _, candidate := range entries {
		if candidate.EntryKind != "real_file" || candidate.Purpose != "upgrade_displacement" || candidate.RootID != result.Leader.RootID || candidate.ReplacementWorkflowID == nil || result.Leader.ReplacementWorkflowID == nil || *candidate.ReplacementWorkflowID != *result.Leader.ReplacementWorkflowID {
			return AdoptedRestoreContext{}, ErrConcurrentModification
		}
	}
	result.Media, err = store.GetMedia(ctx, result.Leader.MediaID)
	if err != nil {
		return AdoptedRestoreContext{}, err
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return AdoptedRestoreContext{}, err
	}
	for _, root := range roots {
		if root.ID == result.Leader.RootID {
			result.Root = root
			break
		}
	}
	if result.Root.ID == (uuid.UUID{}) || !result.Root.Enabled || result.Root.PlexInstanceID == nil || result.Root.PlexLibraryID == "" {
		return AdoptedRestoreContext{}, errors.New("adopted restore has no enabled Plex library")
	}
	result.Integration, err = store.Integration(ctx, *result.Root.PlexInstanceID)
	if err != nil {
		return AdoptedRestoreContext{}, err
	}
	return result, nil
}

func (store *Store) MarkAdoptedRestoreRefreshed(ctx context.Context, entryID uuid.UUID, expectedRevision uint64, now, next time.Time) error {
	return store.rescheduleAdoptedRestore(ctx, entryID, expectedRevision, true, now, next)
}

func (store *Store) RescheduleAdoptedRestoreConfirmation(ctx context.Context, entryID uuid.UUID, expectedRevision uint64, now, next time.Time) error {
	return store.rescheduleAdoptedRestore(ctx, entryID, expectedRevision, false, now, next)
}

func (store *Store) rescheduleAdoptedRestore(ctx context.Context, entryID uuid.UUID, expectedRevision uint64, refreshed bool, now, next time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	refreshValue := any(nil)
	attemptIncrement := 1
	if refreshed {
		refreshValue = now.UTC()
		attemptIncrement = 0
	}
	var revision int64
	command := `UPDATE recycle_entries SET restore_revision=restore_revision+1,restore_attempts=restore_attempts+$4,restore_refresh_at=COALESCE(restore_refresh_at,$3) WHERE id=$1 AND state='awaiting_confirmation' AND restore_revision=$2 RETURNING restore_revision`
	if err := transaction.QueryRow(ctx, command, entryID.String(), int64(expectedRevision), refreshValue, attemptIncrement).Scan(&revision); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entryID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,$3,'plex.adopted_restore_confirm_requested',$4,$5,$6)`, uuid.NewV7().String(), entryID.String(), revision, payload, now.UTC(), next.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) FailAdoptedRestore(ctx context.Context, entryID uuid.UUID, detail string, now time.Time) error {
	if len(detail) > 500 {
		detail = detail[:500]
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var cycleText string
	if err := transaction.QueryRow(ctx, `SELECT cycle_id::text FROM recycle_entries WHERE id=$1 FOR UPDATE`, entryID.String()).Scan(&cycleText); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE adopted_files SET state='restore_attention' WHERE id IN (SELECT adopted_file_id FROM recycle_entries WHERE cycle_id=$1) AND state='restore_confirming'`, cycleText); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='failed' WHERE cycle_id=$1 AND state IN ('restore_planned','awaiting_confirmation')`, cycleText); err != nil {
		return err
	}
	audit, _ := json.Marshal(map[string]any{"reason": strings.TrimSpace(detail), "replacement_preserved": true, "owner_attention_required": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','recycle.adopted_restore_needs_attention','recycle_cycle',$2,$3,$4)`, uuid.NewV7().String(), cycleText, audit, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) CompleteAdoptedRestore(ctx context.Context, entryID uuid.UUID, confirmations []AdoptedRestoreConfirmation, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var cycleText, replacementWorkflowText, plexVisibleRoot string
	var restoreDeadline time.Time
	if err := transaction.QueryRow(ctx, `SELECT entry.cycle_id::text,entry.replacement_workflow_id::text,root.plex_visible_root,entry.restore_deadline_at FROM recycle_entries entry JOIN library_roots root ON root.id=entry.root_id WHERE entry.id=$1 FOR UPDATE OF entry`, entryID.String()).Scan(&cycleText, &replacementWorkflowText, &plexVisibleRoot, &restoreDeadline); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !now.UTC().Before(restoreDeadline) {
		return errors.New("adopted restore confirmation arrived after its deadline")
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,adopted_file_id::text,original_path,state FROM recycle_entries WHERE cycle_id=$1 ORDER BY id FOR UPDATE`, cycleText)
	if err != nil {
		return err
	}
	type sibling struct{ id, adoptedID, original, state string }
	var siblings []sibling
	for rows.Next() {
		var item sibling
		if err := rows.Scan(&item.id, &item.adoptedID, &item.original, &item.state); err != nil {
			rows.Close()
			return err
		}
		siblings = append(siblings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	byEntry := make(map[uuid.UUID]AdoptedRestoreConfirmation, len(confirmations))
	for _, confirmation := range confirmations {
		byEntry[confirmation.RecycleEntryID] = confirmation
	}
	if len(siblings) == 0 || len(byEntry) != len(siblings) {
		return errors.New("Plex confirmation count does not match adopted restore cycle")
	}
	for _, item := range siblings {
		if item.state != "awaiting_confirmation" {
			return ErrConcurrentModification
		}
		parsed, _ := uuid.Parse(item.id)
		confirmation, exists := byEntry[parsed]
		expectedPath := filepath.Clean(filepath.Join(plexVisibleRoot, item.original))
		if !exists || filepath.Clean(confirmation.ObservedPath) != expectedPath || strings.TrimSpace(confirmation.RatingKey) == "" || strings.TrimSpace(confirmation.ProviderGUID) == "" {
			return errors.New("Plex omitted an adopted restore path or identity")
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO adopted_restore_confirmations(recycle_entry_id,observed_path,rating_key,provider_guid,confirmed_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(recycle_entry_id) DO NOTHING`, item.id, expectedPath, confirmation.RatingKey, confirmation.ProviderGUID, confirmation.ConfirmedAt.UTC()); err != nil {
			return err
		}
	}
	linkRows, err := transaction.Query(ctx, `SELECT version.id::text,version.generation,link.id::text FROM library_link_versions version JOIN library_links link ON link.id=version.library_link_id WHERE version.workflow_id=$1 AND version.state='current' ORDER BY version.id FOR UPDATE OF version,link`, replacementWorkflowText)
	if err != nil {
		return err
	}
	type currentLink struct {
		versionID, linkID string
		generation        int
	}
	var links []currentLink
	for linkRows.Next() {
		var link currentLink
		if err := linkRows.Scan(&link.versionID, &link.generation, &link.linkID); err != nil {
			linkRows.Close()
			return err
		}
		links = append(links, link)
	}
	if err := linkRows.Err(); err != nil {
		linkRows.Close()
		return err
	}
	linkRows.Close()
	if len(links) == 0 {
		return errors.New("replacement publication is no longer current")
	}
	for _, link := range links {
		if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='displaced',displaced_at=$2 WHERE id=$1`, link.versionID, now.UTC()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_links SET current_version_id=NULL WHERE id=$1 AND current_version_id=$2`, link.linkID, link.versionID); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"version_id": link.versionID})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'link_version',$2,$3,'library.displaced_remove_requested',$4,$5,$5)`, uuid.NewV7().String(), link.versionID, link.generation, payload, now.UTC()); err != nil {
			return err
		}
	}
	for _, item := range siblings {
		parsed, _ := uuid.Parse(item.id)
		confirmation := byEntry[parsed]
		command, err := transaction.Exec(ctx, `UPDATE adopted_files SET state='current',displaced_at=NULL,plex_observed_path=$2,plex_item_key=$3,provider_guid=$4,verified_at=$5 WHERE id=$1 AND state='restore_confirming'`, item.adoptedID, filepath.Clean(confirmation.ObservedPath), confirmation.RatingKey, confirmation.ProviderGUID, confirmation.ConfirmedAt.UTC())
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
	}
	if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='restored' WHERE cycle_id=$1 AND state='awaiting_confirmation'`, cycleText); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entryID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,8,'plex.recycle_refresh_requested',$3,$4,$5)`, uuid.NewV7().String(), entryID.String(), payload, now.UTC(), now.UTC().Add(30*time.Second)); err != nil {
		return err
	}
	audit, _ := json.Marshal(map[string]any{"entries": len(siblings), "replacement_links": len(links), "plex_confirmed": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','recycle.adopted_restore_completed','recycle_cycle',$2,$3,$4)`, uuid.NewV7().String(), cycleText, audit, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
