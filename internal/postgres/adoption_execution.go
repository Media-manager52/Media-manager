package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
)

var ErrAdoptionMonitoringUnsupported = errors.New("adoption execution with monitoring or search is not implemented")

type AdoptionExecutionItem struct {
	Item  AdoptionItem
	Root  LibraryRoot
	Media catalog.MediaItem
	Plex  Integration
}

type AdoptionExecutionContext struct {
	Batch          AdoptionBatch
	Items          []AdoptionExecutionItem
	Operations     []AdoptionOperation
	ConfirmedItems map[uuid.UUID]bool
}

type AdoptionOperation struct {
	ID                  uuid.UUID       `json:"id"`
	BatchID             uuid.UUID       `json:"batch_id"`
	ItemID              uuid.UUID       `json:"item_id"`
	Ordinal             int             `json:"ordinal"`
	Kind                string          `json:"kind"`
	SourceRelative      string          `json:"source_relative"`
	DestinationRelative string          `json:"destination_relative"`
	SourceSize          int64           `json:"source_size"`
	SourceModTimeNS     int64           `json:"source_modtime_ns"`
	State               string          `json:"state"`
	LastError           string          `json:"last_error,omitempty"`
	AppliedAt           *time.Time      `json:"applied_at,omitempty"`
	VerifiedAt          *time.Time      `json:"verified_at,omitempty"`
	RolledBackAt        *time.Time      `json:"rolled_back_at,omitempty"`
	MediaInfo           json.RawMessage `json:"media_info,omitempty"`
	MediaInfoSHA256     string          `json:"media_info_sha256,omitempty"`
	MediaInfoRecordedAt *time.Time      `json:"media_info_recorded_at,omitempty"`
}

type AdoptionPlexConfirmation struct {
	ItemID       uuid.UUID
	ObservedPath string
	RatingKey    string
	ProviderGUID string
	ConfirmedAt  time.Time
}

func (store *Store) QueueAdoptionExecution(ctx context.Context, id uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state string
	if err := transaction.QueryRow(ctx, `SELECT state FROM adoption_batches WHERE id=$1 FOR UPDATE`, id.String()).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "approved" {
		return errors.New("adoption plan is not approved for execution")
	}
	var approved, unsupported int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FILTER (WHERE review_state='approved'),count(*) FILTER (WHERE review_state='approved' AND (COALESCE(monitoring_enabled,false) OR COALESCE(search_immediately,false))) FROM adoption_items WHERE batch_id=$1`, id.String()).Scan(&approved, &unsupported); err != nil {
		return err
	}
	if approved < 1 || approved > 25 {
		return fmt.Errorf("adoption execution requires 1 to 25 approved items")
	}
	if unsupported > 0 {
		return ErrAdoptionMonitoringUnsupported
	}
	deadline := now.UTC().Add(15 * time.Minute)
	if _, err := transaction.Exec(ctx, `UPDATE adoption_batches SET state='executing',execution_deadline=$2 WHERE id=$1`, id.String(), deadline); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_items SET execution_state=CASE WHEN review_state='approved' THEN 'queued' ELSE 'skipped' END WHERE batch_id=$1`, id.String()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"batch_id": id.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'adoption_batch',$2,1,'adoption.execute_requested',$3,$4,$4)`, uuid.NewV7().String(), id.String(), payload, now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"approved_items": approved, "confirmation": "explicit", "monitoring_changes": 0, "searches": 0})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','adoption.execution_queued','adoption_batch',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) AdoptionExecutionContext(ctx context.Context, id uuid.UUID) (AdoptionExecutionContext, error) {
	batch, err := store.AdoptionBatch(ctx, id)
	if err != nil {
		return AdoptionExecutionContext{}, err
	}
	if batch.State != "executing" {
		return AdoptionExecutionContext{}, ErrConcurrentModification
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return AdoptionExecutionContext{}, err
	}
	rootByID := map[uuid.UUID]LibraryRoot{}
	for _, root := range roots {
		rootByID[root.ID] = root
	}
	result := AdoptionExecutionContext{Batch: batch, Items: []AdoptionExecutionItem{}, ConfirmedItems: map[uuid.UUID]bool{}}
	for _, item := range batch.Items {
		if item.ReviewState != "approved" || item.MediaID == nil || item.RootID == nil {
			continue
		}
		root, ok := rootByID[*item.RootID]
		if !ok || !root.Enabled || root.PlexInstanceID == nil {
			return AdoptionExecutionContext{}, errors.New("adoption root is no longer available")
		}
		media, err := store.GetMedia(ctx, *item.MediaID)
		if err != nil {
			return AdoptionExecutionContext{}, err
		}
		plexIntegration, err := store.Integration(ctx, *root.PlexInstanceID)
		if err != nil || plexIntegration.Kind != "plex" || !plexIntegration.Enabled {
			return AdoptionExecutionContext{}, errors.New("adoption Plex integration is no longer available")
		}
		result.Items = append(result.Items, AdoptionExecutionItem{Item: item, Root: root, Media: media, Plex: plexIntegration})
	}
	if len(result.Items) == 0 {
		return AdoptionExecutionContext{}, errors.New("adoption execution has no approved items")
	}
	result.Operations, err = store.ListAdoptionOperations(ctx, id)
	if err != nil {
		return AdoptionExecutionContext{}, err
	}
	confirmationRows, err := store.Pool.Query(ctx, `SELECT DISTINCT item_id::text FROM adoption_plex_confirmations WHERE batch_id=$1`, id.String())
	if err != nil {
		return AdoptionExecutionContext{}, err
	}
	defer confirmationRows.Close()
	for confirmationRows.Next() {
		var itemText string
		if err := confirmationRows.Scan(&itemText); err != nil {
			return AdoptionExecutionContext{}, err
		}
		itemID, _ := uuid.Parse(itemText)
		result.ConfirmedItems[itemID] = true
	}
	return result, confirmationRows.Err()
}

func (store *Store) PrepareAdoptionOperations(ctx context.Context, batchID uuid.UUID, operations []AdoptionOperation) ([]AdoptionOperation, error) {
	if len(operations) < 1 || len(operations) > 500 {
		return nil, errors.New("invalid adoption operation count")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback(ctx)
	var state string
	if err := transaction.QueryRow(ctx, `SELECT state FROM adoption_batches WHERE id=$1 FOR UPDATE`, batchID.String()).Scan(&state); err != nil {
		return nil, err
	}
	if state != "executing" {
		return nil, ErrConcurrentModification
	}
	existing, err := queryAdoptionOperations(ctx, transaction, batchID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		if !sameAdoptionOperations(existing, operations) {
			return nil, errors.New("persisted adoption journal differs from retry plan")
		}
		return existing, transaction.Commit(ctx)
	}
	seenOrdinal := map[int]bool{}
	for index := range operations {
		operation := &operations[index]
		if operation.ID == (uuid.UUID{}) {
			operation.ID = uuid.NewV7()
		}
		operation.BatchID = batchID
		operation.SourceRelative = strings.TrimSpace(operation.SourceRelative)
		operation.DestinationRelative = strings.TrimSpace(operation.DestinationRelative)
		if operation.ItemID == (uuid.UUID{}) || operation.Ordinal < 1 || seenOrdinal[operation.Ordinal] || !slices.Contains([]string{"primary", "recognized_companion", "unknown_preserved"}, operation.Kind) || !safeRelative(operation.SourceRelative) || !safeRelative(operation.DestinationRelative) || operation.SourceSize < 0 {
			return nil, errors.New("invalid adoption operation")
		}
		seenOrdinal[operation.Ordinal] = true
		operation.State = "planned"
		if operation.Kind == "unknown_preserved" {
			operation.State = "preserved"
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO adoption_operations(id,batch_id,item_id,ordinal,kind,source_relative,destination_relative,source_size,source_modtime_ns,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, operation.ID.String(), batchID.String(), operation.ItemID.String(), operation.Ordinal, operation.Kind, operation.SourceRelative, operation.DestinationRelative, operation.SourceSize, operation.SourceModTimeNS, operation.State); err != nil {
			return nil, err
		}
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_items SET execution_state='planned' WHERE batch_id=$1 AND review_state='approved'`, batchID.String()); err != nil {
		return nil, err
	}
	return operations, transaction.Commit(ctx)
}

func sameAdoptionOperations(existing, planned []AdoptionOperation) bool {
	if len(existing) != len(planned) {
		return false
	}
	for index := range existing {
		left, right := existing[index], planned[index]
		if left.ItemID != right.ItemID || left.Ordinal != right.Ordinal || left.Kind != right.Kind || left.SourceRelative != right.SourceRelative || left.DestinationRelative != right.DestinationRelative || left.SourceSize != right.SourceSize || left.SourceModTimeNS != right.SourceModTimeNS {
			return false
		}
	}
	return true
}

func (store *Store) ListAdoptionOperations(ctx context.Context, batchID uuid.UUID) ([]AdoptionOperation, error) {
	return queryAdoptionOperations(ctx, store.Pool, batchID)
}

type adoptionOperationQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func queryAdoptionOperations(ctx context.Context, query adoptionOperationQuerier, batchID uuid.UUID) ([]AdoptionOperation, error) {
	rows, err := query.Query(ctx, `SELECT id::text,batch_id::text,item_id::text,ordinal,kind,source_relative,destination_relative,source_size,source_modtime_ns,state,COALESCE(last_error,''),applied_at,verified_at,rolled_back_at,media_info,COALESCE(encode(media_info_sha256,'hex'),''),media_info_recorded_at FROM adoption_operations WHERE batch_id=$1 ORDER BY ordinal`, batchID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AdoptionOperation{}
	for rows.Next() {
		var item AdoptionOperation
		var idText, batchText, itemText string
		var mediaInfo []byte
		if err := rows.Scan(&idText, &batchText, &itemText, &item.Ordinal, &item.Kind, &item.SourceRelative, &item.DestinationRelative, &item.SourceSize, &item.SourceModTimeNS, &item.State, &item.LastError, &item.AppliedAt, &item.VerifiedAt, &item.RolledBackAt, &mediaInfo, &item.MediaInfoSHA256, &item.MediaInfoRecordedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.BatchID, _ = uuid.Parse(batchText)
		item.ItemID, _ = uuid.Parse(itemText)
		if mediaInfo != nil {
			item.MediaInfo = append(json.RawMessage(nil), mediaInfo...)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) RecordAdoptionMediaInfo(ctx context.Context, operationID uuid.UUID, evidence json.RawMessage, now time.Time) error {
	if operationID == (uuid.UUID{}) || len(evidence) < 2 || len(evidence) > 1<<20 || !json.Valid(evidence) {
		return errors.New("invalid adoption media-info evidence")
	}
	var object map[string]any
	if err := json.Unmarshal(evidence, &object); err != nil || object == nil {
		return errors.New("invalid adoption media-info evidence")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, evidence); err != nil {
		return errors.New("invalid adoption media-info evidence")
	}
	digest := sha256.Sum256(compact.Bytes())
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var kind, state string
	var existing []byte
	if err := transaction.QueryRow(ctx, `SELECT kind,state,media_info_sha256 FROM adoption_operations WHERE id=$1 FOR UPDATE`, operationID.String()).Scan(&kind, &state, &existing); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if kind != "primary" || state != "verified" {
		return errors.New("adoption primary file is not verified")
	}
	if existing != nil {
		if !bytes.Equal(existing, digest[:]) {
			return ErrConcurrentModification
		}
		return transaction.Commit(ctx)
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_operations SET media_info=$2,media_info_sha256=$3,media_info_recorded_at=$4 WHERE id=$1`, operationID.String(), compact.Bytes(), digest[:], now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) MarkAdoptionOperation(ctx context.Context, id uuid.UUID, state, detail string, now time.Time) error {
	if !slices.Contains([]string{"applied", "verified", "preserved", "rolled_back", "failed"}, state) {
		return errors.New("invalid adoption operation state")
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > 200 {
		detail = detail[:200]
	}
	command, err := store.Pool.Exec(ctx, `UPDATE adoption_operations SET state=$2,last_error=NULLIF($3,''),applied_at=CASE WHEN $2='applied' THEN $4 ELSE applied_at END,verified_at=CASE WHEN $2='verified' THEN $4 ELSE verified_at END,rolled_back_at=CASE WHEN $2='rolled_back' THEN $4 ELSE rolled_back_at END WHERE id=$1`, id.String(), state, detail, now.UTC())
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (store *Store) QueueAdoptionPlexConfirmation(ctx context.Context, batchID uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var incomplete int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM adoption_operations WHERE batch_id=$1 AND NOT (
		kind='unknown_preserved' AND state='preserved' OR
		kind='recognized_companion' AND state='verified' OR
		kind='primary' AND state='verified' AND media_info IS NOT NULL
	)`, batchID.String()).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete != 0 {
		return errors.New("adoption filesystem operations or media-info evidence are not verified")
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_items SET execution_state='filesystem_verified' WHERE batch_id=$1 AND review_state='approved'`, batchID.String()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"batch_id": batchID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'adoption_batch',$2,1,'adoption.plex_confirm_requested',$3,$4,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), batchID.String(), payload, now.UTC(), now.UTC().Add(5*time.Second)); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) CompleteAdoptionExecution(ctx context.Context, batchID uuid.UUID, confirmations []AdoptionPlexConfirmation, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state string
	if err := transaction.QueryRow(ctx, `SELECT state FROM adoption_batches WHERE id=$1 FOR UPDATE`, batchID.String()).Scan(&state); err != nil {
		return err
	}
	if state == "complete" {
		return transaction.Commit(ctx)
	}
	if state != "executing" {
		return ErrConcurrentModification
	}
	for _, confirmation := range confirmations {
		if confirmation.ItemID == (uuid.UUID{}) || !strings.HasPrefix(confirmation.ObservedPath, "/") || strings.TrimSpace(confirmation.RatingKey) == "" || strings.TrimSpace(confirmation.ProviderGUID) == "" {
			return errors.New("invalid adoption Plex confirmation")
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO adoption_plex_confirmations(id,batch_id,item_id,observed_path,rating_key,provider_guid,confirmed_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(item_id,observed_path) DO NOTHING`, uuid.NewV7().String(), batchID.String(), confirmation.ItemID.String(), confirmation.ObservedPath, confirmation.RatingKey, confirmation.ProviderGUID, confirmation.ConfirmedAt.UTC()); err != nil {
			return err
		}
	}
	var approved, confirmed int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FILTER (WHERE item.review_state='approved'),count(DISTINCT confirmation.item_id) FROM adoption_items item LEFT JOIN adoption_plex_confirmations confirmation ON confirmation.item_id=item.id WHERE item.batch_id=$1`, batchID.String()).Scan(&approved, &confirmed); err != nil {
		return err
	}
	if approved != confirmed {
		return errors.New("not every adopted item has Plex confirmation")
	}
	_, err = transaction.Exec(ctx, `INSERT INTO adopted_files(
		id,adoption_batch_id,adoption_item_id,media_id,root_id,quality_profile_id,variant,
		original_source_root,original_relative,destination_relative,source_size,source_modtime_ns,
		plex_observed_path,plex_item_key,provider_guid,quality_evidence_state,quality_evidence,state,verified_at)
		SELECT item.id,item.batch_id,item.id,item.media_id,item.root_id,item.quality_profile_id,item.variant,
			batch.source_root,item.original_relative,item.proposed_relative,operation.source_size,operation.source_modtime_ns,
			confirmation.observed_path,confirmation.rating_key,confirmation.provider_guid,'media_info',operation.media_info,'current',confirmation.confirmed_at
		FROM adoption_items item
		JOIN adoption_batches batch ON batch.id=item.batch_id
		JOIN LATERAL (
			SELECT source_size,source_modtime_ns,media_info FROM adoption_operations
			WHERE item_id=item.id AND kind='primary' AND state='verified' AND media_info IS NOT NULL
			ORDER BY ordinal LIMIT 1
		) operation ON true
		JOIN LATERAL (
			SELECT observed_path,rating_key,provider_guid,confirmed_at FROM adoption_plex_confirmations
			WHERE item_id=item.id ORDER BY confirmed_at DESC,id DESC LIMIT 1
		) confirmation ON true
		WHERE item.batch_id=$1 AND item.review_state='approved'
		ON CONFLICT(adoption_item_id) DO NOTHING`, batchID.String())
	if err != nil {
		return err
	}
	var represented int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM adopted_files WHERE adoption_batch_id=$1`, batchID.String()).Scan(&represented); err != nil {
		return err
	}
	if represented != approved {
		return errors.New("not every adopted item has durable current-file provenance")
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_items SET execution_state='complete' WHERE batch_id=$1 AND review_state='approved'`, batchID.String()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_batches SET state='complete',completed_at=$2 WHERE id=$1`, batchID.String(), now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"items": approved, "plex_confirmed": confirmed, "current_files_recorded": represented, "quality_evidence": "media_info", "monitoring_changes": 0, "rollback_required": false})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','adoption.execution_completed','adoption_batch',$2,$3,$4)`, uuid.NewV7().String(), batchID.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) RecordAdoptionPlexConfirmation(ctx context.Context, batchID uuid.UUID, confirmation AdoptionPlexConfirmation) error {
	if confirmation.ItemID == (uuid.UUID{}) || !strings.HasPrefix(confirmation.ObservedPath, "/") || strings.TrimSpace(confirmation.RatingKey) == "" || strings.TrimSpace(confirmation.ProviderGUID) == "" {
		return errors.New("invalid adoption Plex confirmation")
	}
	command, err := store.Pool.Exec(ctx, `INSERT INTO adoption_plex_confirmations(id,batch_id,item_id,observed_path,rating_key,provider_guid,confirmed_at) SELECT $1,$2,$3,$4,$5,$6,$7 WHERE EXISTS(SELECT 1 FROM adoption_items WHERE id=$3 AND batch_id=$2 AND review_state='approved') ON CONFLICT(item_id,observed_path) DO NOTHING`, uuid.NewV7().String(), batchID.String(), confirmation.ItemID.String(), confirmation.ObservedPath, confirmation.RatingKey, confirmation.ProviderGUID, confirmation.ConfirmedAt.UTC())
	if err == nil && command.RowsAffected() == 0 {
		var exists bool
		if queryErr := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adoption_plex_confirmations WHERE item_id=$1 AND observed_path=$2)`, confirmation.ItemID.String(), confirmation.ObservedPath).Scan(&exists); queryErr != nil {
			return queryErr
		}
		if !exists {
			return ErrNotFound
		}
	}
	return err
}

func (store *Store) FailAdoptionExecution(ctx context.Context, batchID uuid.UUID, detail string, now time.Time) error {
	detail = strings.TrimSpace(detail)
	if len(detail) > 200 {
		detail = detail[:200]
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `UPDATE adoption_batches SET state='failed',completed_at=$2 WHERE id=$1 AND state='executing'`, batchID.String(), now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_items SET execution_state='failed' WHERE batch_id=$1 AND review_state='approved'`, batchID.String()); err != nil {
		return err
	}
	auditDetail, _ := json.Marshal(map[string]any{"reason": detail, "rollback_attempted": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','adoption.execution_failed','adoption_batch',$2,$3,$4)`, uuid.NewV7().String(), batchID.String(), auditDetail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
