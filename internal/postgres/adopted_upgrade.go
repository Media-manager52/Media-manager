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

type adoptedUpgradeFile struct {
	id, mediaID, rootID                 uuid.UUID
	destinationRelative                 string
	size, modtimeNS                     int64
	authorizedDestination               *string
	authorizedSize, authorizedModtimeNS *int64
}

func (store *Store) planAdoptedUpgradeDisplacements(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID, now time.Time) (int, error) {
	rows, err := transaction.Query(ctx, `SELECT adopted.id::text,adopted.media_id::text,adopted.root_id::text,adopted.destination_relative,adopted.source_size,adopted.source_modtime_ns,
		replacement_auth.destination_relative,replacement_auth.source_size,replacement_auth.source_modtime_ns
		FROM workflows workflow
		JOIN adopted_files adopted ON adopted.media_id=workflow.media_id AND adopted.variant=workflow.variant AND adopted.state='current'
		LEFT JOIN adopted_replacement_authorizations replacement_auth ON replacement_auth.workflow_id=workflow.id AND replacement_auth.adopted_file_id=adopted.id
		WHERE workflow.id=$1 ORDER BY adopted.id FOR UPDATE OF adopted`, workflowID.String())
	if err != nil {
		return 0, err
	}
	var files []adoptedUpgradeFile
	for rows.Next() {
		var file adoptedUpgradeFile
		var idText, mediaText, rootText string
		if err := rows.Scan(&idText, &mediaText, &rootText, &file.destinationRelative, &file.size, &file.modtimeNS, &file.authorizedDestination, &file.authorizedSize, &file.authorizedModtimeNS); err != nil {
			rows.Close()
			return 0, err
		}
		file.id, _ = uuid.Parse(idText)
		file.mediaID, _ = uuid.Parse(mediaText)
		file.rootID, _ = uuid.Parse(rootText)
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(files) == 0 {
		return 0, nil
	}
	cycleID := uuid.NewV7()
	purgeAfter := now.UTC().Add(30 * 24 * time.Hour)
	for _, file := range files {
		if file.authorizedDestination == nil || file.authorizedSize == nil || file.authorizedModtimeNS == nil || *file.authorizedDestination != file.destinationRelative || *file.authorizedSize != file.size || *file.authorizedModtimeNS != file.modtimeNS {
			return 0, errors.New("adopted current file does not match its replacement authorization")
		}
		entryID := uuid.NewV7()
		recycleRelative := filepath.ToSlash(filepath.Join(".media-manager-recycle", entryID.String(), filepath.FromSlash(file.destinationRelative)))
		if _, err := transaction.Exec(ctx, `INSERT INTO recycle_entries(id,cycle_id,library_link_version_id,adopted_file_id,replacement_workflow_id,media_id,root_id,entry_kind,purpose,original_path,recycled_path,expected_target,expected_size,expected_modtime_ns,altmount_source_retained,purge_after,state,confirmation_required,created_at)
			VALUES($1,$2,NULL,$3,$4,$5,$6,'real_file','upgrade_displacement',$7,$8,NULL,$9,$10,false,$11,'planned',true,$12)`, entryID.String(), cycleID.String(), file.id.String(), workflowID.String(), file.mediaID.String(), file.rootID.String(), file.destinationRelative, recycleRelative, file.size, file.modtimeNS, purgeAfter, now.UTC()); err != nil {
			return 0, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE adopted_files SET state='recycle_planned',displaced_at=$2 WHERE id=$1 AND state='current'`, file.id.String(), now.UTC()); err != nil {
			return 0, err
		}
		payload, _ := json.Marshal(map[string]string{"recycle_entry_id": entryID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'recycle_entry',$2,1,'recycle.move_requested',$3,$4,$4)`, uuid.NewV7().String(), entryID.String(), payload, now.UTC()); err != nil {
			return 0, err
		}
	}
	detail, _ := json.Marshal(map[string]any{"adopted_files": len(files), "retention_days": 30, "purge_confirmation_required": true, "new_media_plex_confirmed": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','upgrade.adopted_recycle_planned','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), detail, now.UTC()); err != nil {
		return 0, err
	}
	return len(files), nil
}
