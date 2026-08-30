package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

type RecycleEntry struct {
	ID                     uuid.UUID  `json:"id"`
	CycleID                uuid.UUID  `json:"cycle_id"`
	MediaID                uuid.UUID  `json:"media_id"`
	MediaTitle             string     `json:"media_title"`
	LinkVersionID          *uuid.UUID `json:"library_link_version_id,omitempty"`
	AdoptedFileID          *uuid.UUID `json:"adopted_file_id,omitempty"`
	RootID                 uuid.UUID  `json:"root_id"`
	EntryKind              string     `json:"entry_kind"`
	Purpose                string     `json:"purpose"`
	DestinationRoot        string     `json:"-"`
	OriginalRelative       string     `json:"original_relative"`
	RecycleRelative        string     `json:"recycle_relative"`
	ExpectedTarget         string     `json:"-"`
	ExpectedSize           *int64     `json:"expected_size,omitempty"`
	ExpectedModTimeNS      *int64     `json:"-"`
	AltMountSourceRetained bool       `json:"altmount_source_retained"`
	PurgeAfter             time.Time  `json:"purge_after"`
	State                  string     `json:"state"`
	ConfirmationRequired   bool       `json:"confirmation_required"`
	CreatedAt              time.Time  `json:"created_at"`
}

func (store *Store) PlanMediaRecycle(ctx context.Context, mediaID uuid.UUID, now time.Time) ([]RecycleEntry, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback(ctx)
	var title string
	var deletedAt *time.Time
	if err := transaction.QueryRow(ctx, `SELECT title,deleted_at FROM media_items WHERE id=$1 FOR UPDATE`, mediaID.String()).Scan(&title, &deletedAt); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if deletedAt != nil {
		rows, err := transaction.Query(ctx, recycleEntrySelect+` WHERE entry.media_id=$1 AND entry.cycle_id=(SELECT cycle_id FROM recycle_entries WHERE media_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1) AND entry.state IN ('planned','retained','purge_planned') ORDER BY entry.created_at,entry.id`, mediaID.String())
		if err != nil {
			return nil, err
		}
		entries, err := scanRecycleEntries(rows)
		if err != nil {
			return nil, err
		}
		return entries, transaction.Commit(ctx)
	}
	var active int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE media_id=$1 AND terminal_at IS NULL`, mediaID.String()).Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, errors.New("media has an active workflow; cancel or finish it before deleting")
	}
	var adoptedProtected bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adopted_files WHERE media_id=$1 AND state IN ('current','recycle_planned','retained'))`, mediaID.String()).Scan(&adoptedProtected); err != nil {
		return nil, err
	}
	if adoptedProtected {
		return nil, errors.New("adopted real files and their recycle retention must be resolved before deleting the media item")
	}
	rows, err := transaction.Query(ctx, `SELECT version.id::text,link.id::text,root.id::text,root.destination_root,link.destination_relative,version.plex_visible_target
		FROM library_link_versions version JOIN library_links link ON link.id=version.library_link_id JOIN library_roots root ON root.id=link.root_id JOIN workflows workflow ON workflow.id=version.workflow_id
		WHERE workflow.media_id=$1 AND version.state='current' FOR UPDATE OF version,link`, mediaID.String())
	if err != nil {
		return nil, err
	}
	type currentLink struct{ versionID, linkID, rootID, destinationRoot, relative, target string }
	var links []currentLink
	for rows.Next() {
		var link currentLink
		if err := rows.Scan(&link.versionID, &link.linkID, &link.rootID, &link.destinationRoot, &link.relative, &link.target); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(links) == 0 {
		if _, err := transaction.Exec(ctx, `UPDATE media_items SET deleted_at=$2,revision=revision+1,updated_at=$2 WHERE id=$1`, mediaID.String(), now.UTC()); err != nil {
			return nil, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE external_identities SET state='unverified',source=source || ':unpublished_archived' WHERE media_id=$1 AND state='verified'`, mediaID.String()); err != nil {
			return nil, err
		}
		detail, _ := json.Marshal(map[string]any{"title": title, "entries": 0, "unpublished": true})
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','media.archived_unpublished','media',$2,$3,$4)`, uuid.NewV7().String(), mediaID.String(), string(detail), now.UTC()); err != nil {
			return nil, err
		}
		return []RecycleEntry{}, transaction.Commit(ctx)
	}
	purgeAfter := now.UTC().Add(30 * 24 * time.Hour)
	cycleID := uuid.NewV7()
	entries := make([]RecycleEntry, 0, len(links))
	for _, link := range links {
		entryID := uuid.NewV7()
		versionID, _ := uuid.Parse(link.versionID)
		rootID, _ := uuid.Parse(link.rootID)
		recycleRelative := filepath.Join(".media-manager-recycle", entryID.String(), link.relative)
		if _, err := transaction.Exec(ctx, `INSERT INTO recycle_entries(id,cycle_id,library_link_version_id,media_id,root_id,entry_kind,original_path,recycled_path,expected_target,altmount_source_retained,purge_after,state,confirmation_required,created_at) VALUES($1,$2,$3,$4,$5,'link',$6,$7,$8,true,$9,'planned',false,$10)`, entryID.String(), cycleID.String(), versionID.String(), mediaID.String(), rootID.String(), link.relative, recycleRelative, link.target, purgeAfter, now.UTC()); err != nil {
			return nil, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='displaced',displaced_at=$2 WHERE id=$1`, versionID.String(), now.UTC()); err != nil {
			return nil, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_links SET current_version_id=NULL WHERE id=$1 AND current_version_id=$2`, link.linkID, versionID.String()); err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entryID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,1,'recycle.move_requested',$3,$4,$4)`, uuid.NewV7().String(), entryID.String(), string(payload), now.UTC()); err != nil {
			return nil, err
		}
		entries = append(entries, RecycleEntry{ID: entryID, CycleID: cycleID, MediaID: mediaID, LinkVersionID: &versionID, RootID: rootID, EntryKind: "link", Purpose: "media_delete", DestinationRoot: link.destinationRoot, OriginalRelative: link.relative, RecycleRelative: recycleRelative, ExpectedTarget: link.target, AltMountSourceRetained: true, PurgeAfter: purgeAfter, State: "planned", CreatedAt: now.UTC()})
	}
	if _, err := transaction.Exec(ctx, `UPDATE media_items SET deleted_at=$2,revision=revision+1,updated_at=$2 WHERE id=$1`, mediaID.String(), now.UTC()); err != nil {
		return nil, err
	}
	detail, _ := json.Marshal(map[string]any{"title": title, "entries": len(entries), "purge_after": purgeAfter})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','media.recycle_planned','media',$2,$3,$4)`, uuid.NewV7().String(), mediaID.String(), string(detail), now.UTC()); err != nil {
		return nil, err
	}
	return entries, transaction.Commit(ctx)
}

const recycleEntrySelect = `SELECT entry.id::text,entry.cycle_id::text,entry.media_id::text,media.title,entry.library_link_version_id::text,entry.adopted_file_id::text,entry.root_id::text,entry.entry_kind,entry.purpose,root.destination_root,entry.original_path,COALESCE(entry.recycled_path,''),COALESCE(entry.expected_target,''),entry.expected_size,entry.expected_modtime_ns,entry.altmount_source_retained,entry.purge_after,entry.state,entry.confirmation_required,entry.created_at FROM recycle_entries entry JOIN media_items media ON media.id=entry.media_id JOIN library_roots root ON root.id=entry.root_id`

func (store *Store) LoadRecycleEntry(ctx context.Context, entryID uuid.UUID) (RecycleEntry, error) {
	rows, err := store.Pool.Query(ctx, recycleEntrySelect+` WHERE entry.id=$1`, entryID.String())
	if err != nil {
		return RecycleEntry{}, err
	}
	entries, err := scanRecycleEntries(rows)
	if err != nil {
		return RecycleEntry{}, err
	}
	if len(entries) == 0 {
		return RecycleEntry{}, ErrNotFound
	}
	return entries[0], nil
}

func (store *Store) ListRecycleEntries(ctx context.Context, limit int) ([]RecycleEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := store.Pool.Query(ctx, recycleEntrySelect+` ORDER BY entry.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return scanRecycleEntries(rows)
}

func scanRecycleEntries(rows pgx.Rows) ([]RecycleEntry, error) {
	defer rows.Close()
	result := []RecycleEntry{}
	for rows.Next() {
		var entry RecycleEntry
		var idText, cycleText, mediaText, rootText string
		var versionText, adoptedText *string
		if err := rows.Scan(&idText, &cycleText, &mediaText, &entry.MediaTitle, &versionText, &adoptedText, &rootText, &entry.EntryKind, &entry.Purpose, &entry.DestinationRoot, &entry.OriginalRelative, &entry.RecycleRelative, &entry.ExpectedTarget, &entry.ExpectedSize, &entry.ExpectedModTimeNS, &entry.AltMountSourceRetained, &entry.PurgeAfter, &entry.State, &entry.ConfirmationRequired, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.ID, _ = uuid.Parse(idText)
		entry.CycleID, _ = uuid.Parse(cycleText)
		entry.MediaID, _ = uuid.Parse(mediaText)
		entry.RootID, _ = uuid.Parse(rootText)
		if versionText != nil {
			parsed, _ := uuid.Parse(*versionText)
			entry.LinkVersionID = &parsed
		}
		if adoptedText != nil {
			parsed, _ := uuid.Parse(*adoptedText)
			entry.AdoptedFileID = &parsed
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (store *Store) MarkRecycleRetained(ctx context.Context, entryID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state, entryKind string
	var adoptedText *string
	if err := transaction.QueryRow(ctx, `SELECT state,entry_kind,adopted_file_id::text FROM recycle_entries WHERE id=$1 FOR UPDATE`, entryID.String()).Scan(&state, &entryKind, &adoptedText); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state == "retained" {
		return transaction.Commit(ctx)
	}
	if state != "planned" {
		return ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='retained' WHERE id=$1`, entryID.String()); err != nil {
		return err
	}
	if entryKind == "real_file" {
		if adoptedText == nil {
			return errors.New("real-file recycle entry has no adopted file")
		}
		command, err := transaction.Exec(ctx, `UPDATE adopted_files SET state='retained' WHERE id=$1 AND state='recycle_planned'`, *adoptedText)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
	}
	payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entryID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,4,'plex.recycle_refresh_requested',$3,$4,$4) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), entryID.String(), string(payload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) RequestRecycleRestore(ctx context.Context, entryID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state, mediaText, cycleText, entryKind, purpose string
	if err := transaction.QueryRow(ctx, `SELECT state,media_id::text,cycle_id::text,entry_kind,purpose FROM recycle_entries WHERE id=$1`, entryID.String()).Scan(&state, &mediaText, &cycleText, &entryKind, &purpose); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if entryKind != "link" || purpose != "media_delete" {
		return errors.New("adopted upgrade rollback requires a separately verified workflow")
	}
	if _, err := transaction.Exec(ctx, `SELECT 1 FROM media_items WHERE id=$1 FOR UPDATE`, mediaText); err != nil {
		return err
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,state FROM recycle_entries WHERE media_id=$1 AND cycle_id=$2 ORDER BY id FOR UPDATE`, mediaText, cycleText)
	if err != nil {
		return err
	}
	type sibling struct{ id, state string }
	var siblings []sibling
	for rows.Next() {
		var item sibling
		if err := rows.Scan(&item.id, &item.state); err != nil {
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
	for _, item := range siblings {
		if item.state == "purged" || item.state == "purge_planned" || item.state == "awaiting_confirmation" || item.state == "failed" {
			return errors.New("the complete media item is no longer restorable")
		}
	}
	for _, item := range siblings {
		if item.state == "restored" || item.state == "restore_planned" {
			continue
		}
		if item.state != "retained" {
			return ErrConcurrentModification
		}
		if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='restore_planned' WHERE id=$1 AND state='retained'`, item.id); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"recycle_entry_id": item.id})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,2,'recycle.restore_requested',$3,$4,$4) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), item.id, string(payload), now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func (store *Store) MarkRecycleRestored(ctx context.Context, entry RecycleEntry, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `SELECT 1 FROM media_items WHERE id=$1 FOR UPDATE`, entry.MediaID.String()); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='restored' WHERE id=$1 AND state='restore_planned'`, entry.ID.String())
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var state string
		if err := transaction.QueryRow(ctx, `SELECT state FROM recycle_entries WHERE id=$1`, entry.ID.String()).Scan(&state); err != nil {
			return err
		}
		if state != "restored" {
			return ErrConcurrentModification
		}
	} else {
		if entry.LinkVersionID == nil {
			return errors.New("link recycle entry has no library version")
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_link_versions SET state='current',displaced_at=NULL WHERE id=$1`, entry.LinkVersionID.String()); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `UPDATE library_links SET current_version_id=$1 WHERE id=(SELECT library_link_id FROM library_link_versions WHERE id=$1)`, entry.LinkVersionID.String()); err != nil {
			return err
		}
	}
	var notRestored int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM recycle_entries WHERE media_id=$1 AND cycle_id=$2 AND state<>'restored'`, entry.MediaID.String(), entry.CycleID.String()).Scan(&notRestored); err != nil {
		return err
	}
	if notRestored == 0 {
		if _, err := transaction.Exec(ctx, `UPDATE media_items SET deleted_at=NULL,revision=revision+1,updated_at=$2 WHERE id=$1`, entry.MediaID.String(), now.UTC()); err != nil {
			return err
		}
	}
	payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entry.ID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,5,'plex.recycle_refresh_requested',$3,$4,$4) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), entry.ID.String(), string(payload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) PlexForRecycleEntry(ctx context.Context, entryID uuid.UUID) (RecycleEntry, LibraryRoot, Integration, error) {
	entry, err := store.LoadRecycleEntry(ctx, entryID)
	if err != nil {
		return RecycleEntry{}, LibraryRoot{}, Integration{}, err
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return RecycleEntry{}, LibraryRoot{}, Integration{}, err
	}
	var root LibraryRoot
	for _, candidate := range roots {
		if candidate.ID == entry.RootID {
			root = candidate
			break
		}
	}
	if root.ID == (uuid.UUID{}) || root.PlexInstanceID == nil {
		return RecycleEntry{}, LibraryRoot{}, Integration{}, errors.New("recycle entry has no Plex-enabled library root")
	}
	integrations, err := store.ListIntegrations(ctx)
	if err != nil {
		return RecycleEntry{}, LibraryRoot{}, Integration{}, err
	}
	for _, integration := range integrations {
		if integration.ID == *root.PlexInstanceID {
			return entry, root, integration, nil
		}
	}
	return RecycleEntry{}, LibraryRoot{}, Integration{}, errors.New("recycle entry Plex integration was not found")
}

func (store *Store) PlanDueRecyclePurges(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, `SELECT id::text FROM recycle_entries WHERE state='retained' AND purge_after<=$1 AND confirmation_required=false ORDER BY purge_after,id FOR UPDATE SKIP LOCKED LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, value)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='purge_planned' WHERE id=$1`, id); err != nil {
			return 0, err
		}
		payload, _ := json.Marshal(map[string]string{"recycle_entry_id": id})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,3,'recycle.purge_requested',$3,$4,$4) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), id, string(payload), now.UTC()); err != nil {
			return 0, err
		}
	}
	return len(ids), transaction.Commit(ctx)
}

const RealFilePurgeConfirmation = "PURGE RETAINED ADOPTED FILE"

func (store *Store) RequestRealFilePurge(ctx context.Context, entryID uuid.UUID, confirmation string, now time.Time) error {
	if confirmation != RealFilePurgeConfirmation {
		return errors.New("exact retained real-file purge confirmation is required")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state, entryKind, purpose string
	var purgeAfter time.Time
	var confirmationRequired bool
	if err := transaction.QueryRow(ctx, `SELECT state,entry_kind,purpose,purge_after,confirmation_required FROM recycle_entries WHERE id=$1 FOR UPDATE`, entryID.String()).Scan(&state, &entryKind, &purpose, &purgeAfter, &confirmationRequired); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "retained" || entryKind != "real_file" || purpose != "upgrade_displacement" || !confirmationRequired {
		return errors.New("recycle entry is not an owner-confirmed real-file purge candidate")
	}
	if now.UTC().Before(purgeAfter) {
		return errors.New("the 30-day retained-file period has not elapsed")
	}
	if _, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='purge_planned',confirmed_by='owner',confirmed_at=$2 WHERE id=$1`, entryID.String(), now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entryID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,3,'recycle.purge_requested',$3,$4,$4) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), entryID.String(), payload, now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"retention_elapsed": true, "real_file": true, "confirmation": "exact"})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','recycle.real_file_purge_confirmed','recycle_entry',$2,$3,$4)`, uuid.NewV7().String(), entryID.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) MarkRecyclePurged(ctx context.Context, entryID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var mediaText, cycleText, entryKind, purpose string
	var adoptedText *string
	if err := transaction.QueryRow(ctx, `SELECT media_id::text,cycle_id::text,entry_kind,purpose,adopted_file_id::text FROM recycle_entries WHERE id=$1`, entryID.String()).Scan(&mediaText, &cycleText, &entryKind, &purpose, &adoptedText); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `SELECT 1 FROM media_items WHERE id=$1 FOR UPDATE`, mediaText); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `UPDATE recycle_entries SET state='purged',confirmed_at=COALESCE(confirmed_at,$2),confirmed_by=COALESCE(confirmed_by,'automatic-link-retention') WHERE id=$1 AND state='purge_planned'`, entryID.String(), now.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var state string
		if err := transaction.QueryRow(ctx, `SELECT state FROM recycle_entries WHERE id=$1`, entryID.String()).Scan(&state); err != nil {
			return err
		}
		if state != "purged" {
			return ErrConcurrentModification
		}
	}
	if entryKind == "real_file" {
		if purpose != "upgrade_displacement" || adoptedText == nil {
			return errors.New("invalid real-file purge provenance")
		}
		adoptedCommand, err := transaction.Exec(ctx, `UPDATE adopted_files SET state='purged',purged_at=COALESCE(purged_at,$2) WHERE id=$1 AND state IN ('retained','purged')`, *adoptedText, now.UTC())
		if err != nil {
			return err
		}
		if adoptedCommand.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
	}
	var remaining, restored int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state NOT IN ('purged','restored')),count(*) FILTER (WHERE state='restored') FROM recycle_entries WHERE media_id=$1 AND cycle_id=$2`, mediaText, cycleText).Scan(&remaining, &restored); err != nil {
		return err
	}
	var deletedAt *time.Time
	if err := transaction.QueryRow(ctx, `SELECT deleted_at FROM media_items WHERE id=$1`, mediaText).Scan(&deletedAt); err != nil {
		return err
	}
	if purpose == "media_delete" && remaining == 0 && deletedAt != nil && restored > 0 {
		if _, err := transaction.Exec(ctx, `UPDATE media_items SET deleted_at=NULL,revision=revision+1,updated_at=$2 WHERE id=$1`, mediaText, now.UTC()); err != nil {
			return err
		}
	}
	if purpose == "media_delete" && remaining == 0 && deletedAt != nil && restored == 0 {
		if _, err := transaction.Exec(ctx, `UPDATE external_identities SET state='unverified',source=source || ':recycle_purged' WHERE media_id=$1 AND state='verified'`, mediaText); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','media.identity_released_after_recycle','media',$2,'{"source_files_retained":true}',$3)`, uuid.NewV7().String(), mediaText, now.UTC()); err != nil {
			return err
		}
	}
	if entryKind == "real_file" && command.RowsAffected() == 1 {
		detail, _ := json.Marshal(map[string]any{"retention_elapsed": true, "owner_confirmed": true, "fingerprint_matched": true})
		if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','recycle.real_file_purged','recycle_entry',$2,$3,$4)`, uuid.NewV7().String(), entryID.String(), detail, now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}
