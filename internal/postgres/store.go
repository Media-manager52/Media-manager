package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/workflows"
)

type Store struct{ Pool *pgxpool.Pool }

var (
	ErrConcurrentModification = errors.New("concurrent modification")
	ErrNotFound               = errors.New("record not found")
)

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 12
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (store *Store) Close()                         { store.Pool.Close() }
func (store *Store) Ping(ctx context.Context) error { return store.Pool.Ping(ctx) }

func (store *Store) CreateMediaWorkflow(ctx context.Context, item catalog.MediaItem, workflow workflows.Workflow, actor string) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var year any
	if item.Year != nil {
		year = int16(*item.Year)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, item.ID.String(), item.Kind, item.Title, year, int64(item.Revision), item.CreatedAt, item.UpdatedAt); err != nil {
		return err
	}
	for _, identity := range item.Identities {
		if _, err := transaction.Exec(ctx, `INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewV7().String(), item.ID.String(), item.Kind, identity.Namespace, identity.Value, identity.State, identity.Source, item.CreatedAt); err != nil {
			return err
		}
	}
	if err := persistMetadata(ctx, transaction, item.ID, item.Metadata, item.CreatedAt); err != nil {
		return err
	}
	var clientID any
	if workflow.DownloadClientID != (uuid.UUID{}) {
		clientID = workflow.DownloadClientID.String()
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'default',$7,$8,$9)`, workflow.ID.String(), workflow.MediaID.String(), workflow.State, workflow.RootID.String(), workflow.QualityProfileID.String(), clientID, int64(workflow.Revision), workflow.CreatedAt, workflow.UpdatedAt); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'media.created','media',$3,$4,$5)`, uuid.NewV7().String(), actor, item.ID.String(), `{"workflow_created":true}`, item.CreatedAt); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) ListMedia(ctx context.Context, limit, offset int) ([]catalog.MediaItem, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := store.Pool.Query(ctx, `SELECT id::text,kind,title,year,revision,created_at,updated_at FROM media_items WHERE deleted_at IS NULL ORDER BY title,id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []catalog.MediaItem{}
	for rows.Next() {
		var idText string
		var item catalog.MediaItem
		var year *int16
		var revision int64
		if err := rows.Scan(&idText, &item.Kind, &item.Title, &year, &revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ID, err = uuid.Parse(idText)
		if err != nil {
			return nil, err
		}
		item.Revision = uint64(revision)
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		identities, err := store.identities(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Identities = identities
		item.Metadata, err = store.metadata(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *Store) identities(ctx context.Context, mediaID uuid.UUID) ([]catalog.ExternalIdentity, error) {
	rows, err := store.Pool.Query(ctx, `SELECT namespace,value,state,source FROM external_identities WHERE media_id=$1 ORDER BY namespace,value`, mediaID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalog.ExternalIdentity{}
	for rows.Next() {
		var identity catalog.ExternalIdentity
		if err := rows.Scan(&identity.Namespace, &identity.Value, &identity.State, &identity.Source); err != nil {
			return nil, err
		}
		result = append(result, identity)
	}
	return result, rows.Err()
}

func (store *Store) GetWorkflow(ctx context.Context, id uuid.UUID) (workflows.Workflow, error) {
	var workflow workflows.Workflow
	var idText, mediaText, rootText, profileText string
	var clientText *string
	var selectedText, attemptText *string
	var revision int64
	err := store.Pool.QueryRow(ctx, `SELECT id::text,media_id::text,state,root_id::text,quality_profile_id::text,download_client_id::text,selected_release_id::text,grab_attempt_id::text,revision,created_at,updated_at FROM workflows WHERE id=$1`, id.String()).Scan(&idText, &mediaText, &workflow.State, &rootText, &profileText, &clientText, &selectedText, &attemptText, &revision, &workflow.CreatedAt, &workflow.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return workflows.Workflow{}, ErrNotFound
	}
	if err != nil {
		return workflows.Workflow{}, err
	}
	workflow.ID, _ = uuid.Parse(idText)
	workflow.MediaID, _ = uuid.Parse(mediaText)
	workflow.RootID, _ = uuid.Parse(rootText)
	workflow.QualityProfileID, _ = uuid.Parse(profileText)
	if clientText != nil {
		workflow.DownloadClientID, _ = uuid.Parse(*clientText)
	}
	workflow.Revision = uint64(revision)
	if selectedText != nil {
		parsed, parseErr := uuid.Parse(*selectedText)
		if parseErr != nil {
			return workflows.Workflow{}, parseErr
		}
		workflow.SelectedRelease = &parsed
	}
	if attemptText != nil {
		parsed, parseErr := uuid.Parse(*attemptText)
		if parseErr != nil {
			return workflows.Workflow{}, parseErr
		}
		workflow.GrabAttemptID = &parsed
	}
	return workflow, nil
}

func (store *Store) TransitionWorkflow(ctx context.Context, id uuid.UUID, expectedRevision uint64, to workflows.State, reason, actor string, now time.Time) (workflows.Workflow, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workflows.Workflow{}, err
	}
	defer transaction.Rollback(ctx)
	var workflow workflows.Workflow
	var idText, mediaText, rootText, profileText string
	var clientText *string
	var selectedText, attemptText *string
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT id::text,media_id::text,state,root_id::text,quality_profile_id::text,download_client_id::text,selected_release_id::text,grab_attempt_id::text,revision,created_at,updated_at FROM workflows WHERE id=$1 FOR UPDATE`, id.String()).Scan(&idText, &mediaText, &workflow.State, &rootText, &profileText, &clientText, &selectedText, &attemptText, &revision, &workflow.CreatedAt, &workflow.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return workflows.Workflow{}, ErrNotFound
	} else if err != nil {
		return workflows.Workflow{}, err
	}
	if uint64(revision) != expectedRevision {
		return workflows.Workflow{}, ErrConcurrentModification
	}
	workflow.ID, _ = uuid.Parse(idText)
	workflow.MediaID, _ = uuid.Parse(mediaText)
	workflow.RootID, _ = uuid.Parse(rootText)
	workflow.QualityProfileID, _ = uuid.Parse(profileText)
	if clientText != nil {
		workflow.DownloadClientID, _ = uuid.Parse(*clientText)
	}
	workflow.Revision = uint64(revision)
	if selectedText != nil {
		parsed, parseErr := uuid.Parse(*selectedText)
		if parseErr != nil {
			return workflows.Workflow{}, parseErr
		}
		workflow.SelectedRelease = &parsed
	}
	if attemptText != nil {
		parsed, parseErr := uuid.Parse(*attemptText)
		if parseErr != nil {
			return workflows.Workflow{}, parseErr
		}
		workflow.GrabAttemptID = &parsed
	}
	transition, err := workflow.Transition(to, reason, actor, now)
	if err != nil {
		return workflows.Workflow{}, err
	}
	command, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=$3,updated_at=$4,terminal_at=CASE WHEN $2 IN ('plex_confirmed','cancelled') THEN $4 ELSE terminal_at END WHERE id=$1 AND revision=$5`, id.String(), workflow.State, int64(workflow.Revision), workflow.UpdatedAt, revision)
	if err != nil {
		return workflows.Workflow{}, err
	}
	if command.RowsAffected() != 1 {
		return workflows.Workflow{}, ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, id.String(), int64(workflow.Revision), transition.From, transition.To, transition.Reason, transition.Actor, transition.At); err != nil {
		return workflows.Workflow{}, err
	}
	payload, _ := json.Marshal(map[string]any{"from": transition.From, "to": transition.To})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.transitioned',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), id.String(), int64(workflow.Revision), string(payload), now.UTC()); err != nil {
		return workflows.Workflow{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return workflows.Workflow{}, err
	}
	return workflow, nil
}

func (store *Store) LoadIndexerBudget(ctx context.Context, indexerID string) (indexers.Snapshot, error) {
	var snapshot indexers.Snapshot
	var next, cooldown *time.Time
	var lastOutcome *string
	var minimumIntervalMS int64
	err := store.Pool.QueryRow(ctx, `SELECT minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,updated_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID).Scan(&minimumIntervalMS, &next, &cooldown, &snapshot.ConsecutiveFailure, &lastOutcome, &snapshot.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return indexers.Snapshot{IndexerID: indexerID}, nil
	}
	if err != nil {
		return indexers.Snapshot{}, err
	}
	snapshot.IndexerID = indexerID
	snapshot.MinimumInterval = time.Duration(minimumIntervalMS) * time.Millisecond
	if lastOutcome != nil {
		snapshot.LastOutcome = indexers.Outcome(*lastOutcome)
	}
	if next != nil {
		snapshot.NextAllowedAt = *next
	}
	if cooldown != nil {
		snapshot.CooldownUntil = *cooldown
	}
	return snapshot, nil
}

func (store *Store) SaveIndexerBudget(ctx context.Context, snapshot indexers.Snapshot) error {
	minimumIntervalMS := snapshot.MinimumInterval.Milliseconds()
	if minimumIntervalMS < 0 {
		minimumIntervalMS = 0
	}
	_, err := store.Pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at) VALUES($1,$2,$3,$4,$5,$6,1,$7) ON CONFLICT(indexer_instance_id) DO UPDATE SET minimum_interval_ms=EXCLUDED.minimum_interval_ms,next_allowed_at=EXCLUDED.next_allowed_at,cooldown_until=EXCLUDED.cooldown_until,consecutive_failure=EXCLUDED.consecutive_failure,last_outcome=EXCLUDED.last_outcome,revision=indexer_budget_state.revision+1,updated_at=EXCLUDED.updated_at`, snapshot.IndexerID, minimumIntervalMS, nullableTime(snapshot.NextAllowedAt), nullableTime(snapshot.CooldownUntil), snapshot.ConsecutiveFailure, snapshot.LastOutcome, snapshot.UpdatedAt)
	return err
}

func (store *Store) ReserveIndexerRequest(ctx context.Context, indexerID, family string, limit int, window time.Duration, now time.Time) (time.Time, error) {
	if (family != "query" && family != "grab") || limit <= 0 || window <= 0 {
		return time.Time{}, indexers.ErrInvalidBudget
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "indexer-request-budget:"+indexerID); err != nil {
		return time.Time{}, err
	}
	windowStart := now.UTC().Add(-window)
	if _, err := transaction.Exec(ctx, `DELETE FROM indexer_request_reservations WHERE id IN (SELECT id FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND reserved_at <= $2 ORDER BY reserved_at LIMIT 1000)`, indexerID, windowStart); err != nil {
		return time.Time{}, err
	}
	var used int64
	var oldest *time.Time
	if err := transaction.QueryRow(ctx, `SELECT count(*),min(reserved_at) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND request_family=$2 AND reserved_at > $3`, indexerID, family, windowStart).Scan(&used, &oldest); err != nil {
		return time.Time{}, err
	}
	if used >= int64(limit) {
		retryAt := now.UTC().Add(window)
		if oldest != nil {
			retryAt = oldest.UTC().Add(window)
		}
		if err := transaction.Commit(ctx); err != nil {
			return time.Time{}, err
		}
		return retryAt, nil
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,$3,$4)`, uuid.NewV7().String(), indexerID, family, now.UTC()); err != nil {
		return time.Time{}, err
	}
	return time.Time{}, transaction.Commit(ctx)
}

func (store *Store) RecordSeerrDelivery(ctx context.Context, instanceID, requestID, eventID string, payload json.RawMessage, now time.Time) (bool, error) {
	digest := sha256.Sum256(payload)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	deliveryID := uuid.NewV7()
	command, err := transaction.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',NULLIF($3,''),$4,$5,$6,$7) ON CONFLICT DO NOTHING`, deliveryID.String(), instanceID, eventID, requestID, digest[:], string(payload), now.UTC())
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 0 {
		if err := transaction.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	outboxPayload, _ := json.Marshal(map[string]string{"delivery_id": deliveryID.String(), "request_id": requestID})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'seerr_delivery',$2,1,'seerr.reconcile',$3,$4,$4)`, uuid.NewV7().String(), deliveryID.String(), string(outboxPayload), now.UTC()); err != nil {
		return false, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (store *Store) Ready(ctx context.Context) error {
	if err := store.Ping(ctx); err != nil {
		return err
	}
	version, err := SchemaVersion(ctx, store.Pool)
	if err != nil {
		return err
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("schema version %d, expected %d", version, CurrentSchemaVersion)
	}
	return nil
}
