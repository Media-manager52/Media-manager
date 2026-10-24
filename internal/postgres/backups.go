package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

var ErrBackupInProgress = errors.New("a backup is already in progress")
var backupChecksum = regexp.MustCompile(`^[a-f0-9]{64}$`)

type BackupRecord struct {
	ID                uuid.UUID  `json:"id"`
	State             string     `json:"state"`
	Format            string     `json:"format"`
	Filename          string     `json:"filename,omitempty"`
	SizeBytes         *int64     `json:"size_bytes,omitempty"`
	SHA256            string     `json:"sha256,omitempty"`
	SchemaVersion     *int64     `json:"schema_version,omitempty"`
	Attempts          int        `json:"attempts"`
	LastError         string     `json:"last_error,omitempty"`
	OffsiteState      string     `json:"offsite_state,omitempty"`
	OffsiteKey        string     `json:"offsite_key,omitempty"`
	OffsiteError      string     `json:"offsite_error,omitempty"`
	OffsiteUploadedAt *time.Time `json:"offsite_uploaded_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

func (store *Store) CreateBackup(ctx context.Context, now time.Time) (BackupRecord, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return BackupRecord{}, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('media-manager-backup',0))`); err != nil {
		return BackupRecord{}, err
	}
	var active bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM backup_records WHERE state IN ('queued','running'))`).Scan(&active); err != nil {
		return BackupRecord{}, err
	}
	if active {
		return BackupRecord{}, ErrBackupInProgress
	}
	item := BackupRecord{ID: uuid.NewV7(), State: "queued", Format: "postgres_custom_aes256gcm_v1", CreatedAt: now.UTC()}
	if _, err := transaction.Exec(ctx, `INSERT INTO backup_records(id,state,format,created_at) VALUES($1,$2,$3,$4)`, item.ID.String(), item.State, item.Format, item.CreatedAt); err != nil {
		return BackupRecord{}, err
	}
	payload, _ := json.Marshal(map[string]string{"backup_id": item.ID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'backup',$2,1,'backup.create_requested',$3,$4,$4)`, uuid.NewV7().String(), item.ID.String(), payload, item.CreatedAt); err != nil {
		return BackupRecord{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','backup.requested','backup',$2,'{"production_restore":false,"automatic_deletion":false}',$3)`, uuid.NewV7().String(), item.ID.String(), item.CreatedAt); err != nil {
		return BackupRecord{}, err
	}
	return item, transaction.Commit(ctx)
}

func (store *Store) ListBackups(ctx context.Context, limit int) ([]BackupRecord, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := store.Pool.Query(ctx, `SELECT id::text,state,format,COALESCE(filename,''),size_bytes,COALESCE(sha256,''),schema_version,attempts,COALESCE(last_error,''),COALESCE(offsite_state,''),COALESCE(offsite_key,''),COALESCE(offsite_error,''),offsite_uploaded_at,created_at,started_at,completed_at FROM backup_records ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BackupRecord{}
	for rows.Next() {
		item, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) Backup(ctx context.Context, id uuid.UUID) (BackupRecord, error) {
	item, err := scanBackup(store.Pool.QueryRow(ctx, `SELECT id::text,state,format,COALESCE(filename,''),size_bytes,COALESCE(sha256,''),schema_version,attempts,COALESCE(last_error,''),COALESCE(offsite_state,''),COALESCE(offsite_key,''),COALESCE(offsite_error,''),offsite_uploaded_at,created_at,started_at,completed_at FROM backup_records WHERE id=$1`, id.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupRecord{}, ErrNotFound
	}
	return item, err
}

func scanBackup(row rowScanner) (BackupRecord, error) {
	var item BackupRecord
	var idText string
	if err := row.Scan(&idText, &item.State, &item.Format, &item.Filename, &item.SizeBytes, &item.SHA256, &item.SchemaVersion, &item.Attempts, &item.LastError, &item.OffsiteState, &item.OffsiteKey, &item.OffsiteError, &item.OffsiteUploadedAt, &item.CreatedAt, &item.StartedAt, &item.CompletedAt); err != nil {
		return BackupRecord{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	return item, nil
}

func (store *Store) StartBackup(ctx context.Context, id uuid.UUID, now time.Time) error {
	command, err := store.Pool.Exec(ctx, `UPDATE backup_records SET state='running',attempts=attempts+1,started_at=$2,completed_at=NULL,last_error=NULL WHERE id=$1 AND state IN ('queued','running','failed')`, id.String(), now.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) CompleteBackup(ctx context.Context, id uuid.UUID, filename string, size int64, checksum string, schemaVersion int64, now time.Time) error {
	filename, checksum = strings.TrimSpace(filename), strings.TrimSpace(checksum)
	if filename == "" || size < 1 || !backupChecksum.MatchString(checksum) || schemaVersion < 1 {
		return errors.New("invalid completed backup")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	command, err := transaction.Exec(ctx, `UPDATE backup_records SET state='verified',filename=$2,size_bytes=$3,sha256=$4,schema_version=$5,last_error=NULL,completed_at=$6 WHERE id=$1 AND state='running'`, id.String(), filename, size, checksum, schemaVersion, now.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	detail, _ := json.Marshal(map[string]any{"filename": filename, "size_bytes": size, "sha256": checksum, "schema_version": schemaVersion, "restore_verified": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','backup.verified','backup',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// QueueBackupOffsite queues a durable retry of an off-site copy for one
// verified backup. A pending request is not duplicated.
func (store *Store) QueueBackupOffsite(ctx context.Context, id uuid.UUID, now time.Time) (BackupRecord, error) {
	record, err := store.Backup(ctx, id)
	if err != nil {
		return BackupRecord{}, err
	}
	if record.State != "verified" || record.Filename == "" {
		return BackupRecord{}, ErrNotFound
	}
	payload, _ := json.Marshal(map[string]string{"backup_id": record.ID.String()})
	_, err = store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		SELECT $1,'backup',$2,1,'backup.offsite_requested',$3,$4,$4
		WHERE NOT EXISTS(SELECT 1 FROM outbox_events WHERE aggregate_type='backup' AND aggregate_id=$2 AND event_type='backup.offsite_requested' AND completed_at IS NULL)`, uuid.NewV7().String(), record.ID.String(), payload, now.UTC())
	if err != nil {
		return BackupRecord{}, err
	}
	return record, nil
}

// SetBackupOffsite records the outcome of copying a verified backup to the
// configured off-site destination. A failed copy keeps the local verified
// record intact and is retryable.
func (store *Store) SetBackupOffsite(ctx context.Context, id uuid.UUID, state, key, detail string, now time.Time) error {
	state, key, detail = strings.TrimSpace(state), strings.TrimSpace(key), strings.TrimSpace(detail)
	if state != "uploaded" && state != "failed" {
		return errors.New("invalid off-site backup state")
	}
	if state == "uploaded" && key == "" {
		return errors.New("uploaded off-site backup needs an object key")
	}
	if len(detail) > 200 {
		detail = detail[:200]
	}
	var uploaded any
	if state == "uploaded" {
		uploaded = now.UTC()
	}
	command, err := store.Pool.Exec(ctx, `UPDATE backup_records SET offsite_state=$2,offsite_key=NULLIF($3,''),offsite_error=NULLIF($4,''),offsite_uploaded_at=$5 WHERE id=$1 AND state='verified'`, id.String(), state, key, detail, uploaded)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) FailBackup(ctx context.Context, id uuid.UUID, safeDetail string, now time.Time) error {
	safeDetail = strings.TrimSpace(safeDetail)
	if len(safeDetail) > 200 {
		safeDetail = safeDetail[:200]
	}
	_, err := store.Pool.Exec(ctx, `UPDATE backup_records SET state='failed',last_error=NULLIF($2,''),completed_at=$3 WHERE id=$1 AND state <> 'verified'`, id.String(), safeDetail, now.UTC())
	return err
}
