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
)

type OutboxEvent struct {
	ID                uuid.UUID
	AggregateType     string
	AggregateID       uuid.UUID
	AggregateRevision uint64
	Type              string
	Payload           json.RawMessage
	Attempts          int
	OccurredAt        time.Time
	LastError         string
}

func (store *Store) ClaimOutbox(ctx context.Context, owner string, limit int, lease time.Duration) ([]OutboxEvent, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	if lease <= 0 {
		lease = time.Minute
	}
	rows, err := store.Pool.Query(ctx, `WITH picked AS (
  SELECT id FROM outbox_events
  WHERE completed_at IS NULL AND available_at <= now() AND (lease_until IS NULL OR lease_until < now())
  ORDER BY CASE WHEN event_type='workflow.discovery_rescan_requested' THEN 1 ELSE 0 END,occurred_at,id FOR UPDATE SKIP LOCKED LIMIT $1
)
UPDATE outbox_events AS event
SET lease_owner=$2,lease_until=now()+($3 * interval '1 second'),attempts=attempts+1
FROM picked WHERE event.id=picked.id
RETURNING event.id::text,event.aggregate_type,event.aggregate_id::text,event.aggregate_revision,event.event_type,event.payload,event.attempts,event.occurred_at,COALESCE(event.last_error,'')`, limit, owner, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		var idText, aggregateText string
		var revision int64
		var payload []byte
		if err := rows.Scan(&idText, &event.AggregateType, &aggregateText, &revision, &event.Type, &payload, &event.Attempts, &event.OccurredAt, &event.LastError); err != nil {
			return nil, err
		}
		event.ID, err = uuid.Parse(idText)
		if err != nil {
			return nil, err
		}
		event.AggregateID, err = uuid.Parse(aggregateText)
		if err != nil {
			return nil, err
		}
		event.AggregateRevision = uint64(revision)
		event.Payload = append(json.RawMessage(nil), payload...)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (store *Store) CompleteOutbox(ctx context.Context, id uuid.UUID, owner string) error {
	command, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=now(),lease_owner=NULL,lease_until=NULL,last_error=NULL WHERE id=$1 AND lease_owner=$2 AND completed_at IS NULL`, id.String(), owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

func (store *Store) FailOutbox(ctx context.Context, id uuid.UUID, owner, detail string, retryAt time.Time) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	command, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=$3,lease_owner=NULL,lease_until=NULL,last_error=$4 WHERE id=$1 AND lease_owner=$2 AND completed_at IS NULL`, id.String(), owner, retryAt.UTC(), detail)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

func (store *Store) DeferOutbox(ctx context.Context, id uuid.UUID, owner, detail string, retryAt time.Time) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	command, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=$3,attempts=GREATEST(attempts-1,0),lease_owner=NULL,lease_until=NULL,last_error=$4 WHERE id=$1 AND lease_owner=$2 AND completed_at IS NULL`, id.String(), owner, retryAt.UTC(), detail)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

func (store *Store) RenewOutboxLease(ctx context.Context, id uuid.UUID, owner string, lease time.Duration) error {
	if lease <= 0 {
		lease = time.Minute
	}
	command, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET lease_until=now()+($3 * interval '1 second') WHERE id=$1 AND lease_owner=$2 AND completed_at IS NULL`, id.String(), owner, lease.Seconds())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	return nil
}

func (store *Store) MarkSeerrDelivery(ctx context.Context, deliveryID uuid.UUID, outcome, detail string) error {
	command, err := store.Pool.Exec(ctx, `UPDATE seerr_deliveries SET processed_at=now(),outcome=$2,error_detail=NULLIF($3,'') WHERE id=$1 AND processed_at IS NULL`, deliveryID.String(), outcome, detail)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var exists bool
		if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seerr_deliveries WHERE id=$1 AND processed_at IS NOT NULL)`, deliveryID.String()).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (store *Store) RequeueSeerrNeedsConfiguration(ctx context.Context, now time.Time) (int, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, `SELECT id::text,request_id FROM seerr_deliveries WHERE outcome='needs_configuration' AND processed_at IS NOT NULL ORDER BY received_at,id FOR UPDATE`)
	if err != nil {
		return 0, err
	}
	type delivery struct{ id, requestID string }
	var deliveries []delivery
	for rows.Next() {
		var item delivery
		if err := rows.Scan(&item.id, &item.requestID); err != nil {
			rows.Close()
			return 0, err
		}
		deliveries = append(deliveries, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range deliveries {
		// Mark the delivery as requeued (not just unprocessed): intake accepts
		// this specifically so an unchanged Seerr snapshot is re-evaluated after
		// the owner fixes configuration, instead of being discarded as stale.
		if _, err := transaction.Exec(ctx, `UPDATE seerr_deliveries SET processed_at=NULL,outcome='requeued',error_detail=NULL WHERE id=$1`, item.id); err != nil {
			return 0, err
		}
		var revision int64
		if err := transaction.QueryRow(ctx, `SELECT COALESCE(max(aggregate_revision),0)+1 FROM outbox_events WHERE aggregate_type='seerr_delivery' AND aggregate_id=$1 AND event_type='seerr.reconcile'`, item.id).Scan(&revision); err != nil {
			return 0, err
		}
		payload, _ := json.Marshal(map[string]string{"delivery_id": item.id, "request_id": item.requestID})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'seerr_delivery',$2,$3,'seerr.reconcile',$4,$5,$5)`, uuid.NewV7().String(), item.id, revision, string(payload), now.UTC()); err != nil {
			return 0, err
		}
	}
	return len(deliveries), transaction.Commit(ctx)
}

func (store *Store) QueueActiveSeerrPolls(ctx context.Context, instanceID string, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, `SELECT snapshot.request_id
		FROM seerr_request_snapshots snapshot
		WHERE snapshot.seerr_instance_id=$1 AND snapshot.state='2'
		AND EXISTS(
			SELECT 1 FROM seerr_request_workflows provenance
			JOIN workflows workflow ON workflow.id=provenance.workflow_id
			WHERE provenance.seerr_instance_id=snapshot.seerr_instance_id
			AND provenance.request_id=snapshot.request_id AND workflow.terminal_at IS NULL
		)
		AND NOT EXISTS(
			SELECT 1 FROM seerr_deliveries delivery
			WHERE delivery.seerr_instance_id=snapshot.seerr_instance_id
			AND delivery.request_id=snapshot.request_id AND delivery.processed_at IS NULL
		)
		ORDER BY snapshot.reconciled_at,snapshot.request_id
		LIMIT $2 FOR UPDATE OF snapshot SKIP LOCKED`, instanceID, limit)
	if err != nil {
		return 0, err
	}
	var requestIDs []string
	for rows.Next() {
		var requestID string
		if err := rows.Scan(&requestID); err != nil {
			rows.Close()
			return 0, err
		}
		requestIDs = append(requestIDs, requestID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	queued := 0
	bucket := now.UTC().Truncate(5 * time.Minute).Unix()
	for _, requestID := range requestIDs {
		deliveryID := uuid.NewV7()
		eventID := fmt.Sprintf("poll:%s:%d", requestID, bucket)
		payload := []byte(`{}`)
		digest := sha256.Sum256(payload)
		command, err := transaction.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'poll',$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, deliveryID.String(), instanceID, eventID, requestID, digest[:], string(payload), now.UTC())
		if err != nil {
			return 0, err
		}
		if command.RowsAffected() == 0 {
			continue
		}
		outboxPayload, _ := json.Marshal(map[string]string{"delivery_id": deliveryID.String(), "request_id": requestID})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'seerr_delivery',$2,1,'seerr.reconcile',$3,$4,$4)`, uuid.NewV7().String(), deliveryID.String(), string(outboxPayload), now.UTC()); err != nil {
			return 0, err
		}
		queued++
	}
	return queued, transaction.Commit(ctx)
}

func (store *Store) OutboxPending(ctx context.Context) (int64, error) {
	var count int64
	err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE completed_at IS NULL`).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return count, err
}
