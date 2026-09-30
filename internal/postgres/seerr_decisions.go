package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"uuid"
)

// ErrSeerrRequestNotPending means the request is not waiting for approval in
// Seerr, so an owner decision would not apply.
var ErrSeerrRequestNotPending = errors.New("Seerr request is not waiting for approval")

// SeerrRequestPending reports whether the persisted request is still pending
// approval in Seerr (Seerr request status 1).
func (store *Store) SeerrRequestPending(ctx context.Context, instanceID, requestID string) (bool, error) {
	detail, err := store.SeerrRequest(ctx, instanceID, requestID)
	if err != nil {
		return false, err
	}
	return detail.Request.SeerrState == "1", nil
}

// RecordSeerrOwnerDecision audits an owner decision that Seerr has already
// confirmed and queues the same reconcile a Seerr webhook would. Intake then
// re-reads the request from Seerr, so Media Manager never trusts the decision
// locally: an approved request is admitted only through normal intake.
//
// The stored Seerr state of a still-pending request is set to the status Seerr
// confirmed, so the owner never sees decision buttons for a request Seerr has
// already decided. The Seerr update cursor is left alone, so the re-read still
// replaces the whole snapshot with Seerr's authoritative data.
func (store *Store) RecordSeerrOwnerDecision(ctx context.Context, instanceID, requestID, decision string, confirmedStatus int, now time.Time) error {
	if decision != "approve" && decision != "decline" {
		return errors.New("invalid Seerr decision")
	}
	if (decision == "approve" && confirmedStatus != 2) || (decision == "decline" && confirmedStatus != 3) {
		return errors.New("Seerr status does not match the decision")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	action := "seerr.request_approved_by_owner"
	if decision == "decline" {
		action = "seerr.request_declined_by_owner"
	}
	detail, _ := json.Marshal(map[string]string{"seerr_instance_id": instanceID, "request_id": requestID, "decision": decision})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner',$2,'seerr_request',NULL,$3,$4)`, uuid.NewV7().String(), action, string(detail), now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE seerr_request_snapshots SET state=$3,revision=revision+1 WHERE seerr_instance_id=$1 AND request_id=$2 AND state='1'`, instanceID, requestID, strconv.Itoa(confirmedStatus)); err != nil {
		return err
	}
	deliveryID := uuid.NewV7()
	payload, _ := json.Marshal(map[string]string{"request_id": requestID, "decision": decision, "source": "owner"})
	digest := sha256.Sum256(payload)
	if _, err := transaction.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'poll',$3,$4,$5,$6,$7)`, deliveryID.String(), instanceID, "owner-decision:"+deliveryID.String(), requestID, digest[:], string(payload), now.UTC()); err != nil {
		return err
	}
	outboxPayload, _ := json.Marshal(map[string]string{"delivery_id": deliveryID.String(), "request_id": requestID})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'seerr_delivery',$2,1,'seerr.reconcile',$3,$4,$4)`, uuid.NewV7().String(), deliveryID.String(), string(outboxPayload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
