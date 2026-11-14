package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/media-manager/media-manager/internal/workflows"
)

// A release can turn out broken after it was published but before Plex
// confirmed it: AltMount finds its Usenet articles missing and drops the
// file, so the Plex confirmation times out and every retry is futile. The
// published links are unconfirmed (never current), so a repair, which
// replaces a confirmed file, does not apply. Instead the release is rejected
// like any other release failure: its links are removed, the exact release
// and package are excluded, and the round tries the next release.

// ErrPublicationNotRejectable means the workflow is not waiting on a
// timed-out Plex confirmation or a failed publication of its current attempt.
var ErrPublicationNotRejectable = errors.New("only a release whose Plex confirmation timed out or whose publication failed can be rejected")

const (
	// BrokenPublicationCode is recorded when AltMount reported the published
	// file corrupted, or it went missing, before Plex confirmed it.
	BrokenPublicationCode = "broken_before_plex"
	// OwnerRejectedPublicationCode is recorded when the owner rejected the
	// published release (for example a wrong title) before Plex confirmed it.
	OwnerRejectedPublicationCode = "owner_rejected_publication"
	// RepairReleaseRejected is the repair outcome of a broken file whose
	// publication was never confirmed: its release was rejected instead.
	RepairReleaseRejected = "release_rejected"
)

// RejectedPublication reports a rejected release.
type RejectedPublication struct {
	LinksRemoved   int  `json:"links_removed"`
	RetryQueued    bool `json:"retry_queued"`
	RoundExhausted bool `json:"round_exhausted"`
}

// RejectPublishedRelease rejects the release of a workflow whose Plex
// confirmation timed out (or whose publication failed): the attempt's
// unconfirmed links are displaced and removed from the library, the exact
// release and package are excluded, and the workflow waits for the round's
// next release exactly like after any other release failure. Confirmed
// (current) links of earlier releases are never touched.
func (store *Store) RejectPublishedRelease(ctx context.Context, workflowID uuid.UUID, actor, failureKind, failureCode, detail string, now time.Time) (RejectedPublication, error) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "the published release was rejected before Plex confirmed it"
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return RejectedPublication{}, err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var attentionCode string
	var terminal bool
	var attemptText *string
	err = transaction.QueryRow(ctx, `SELECT state,COALESCE(attention_code,''),terminal_at IS NOT NULL,grab_attempt_id::text FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &attentionCode, &terminal, &attemptText)
	if errors.Is(err, pgx.ErrNoRows) {
		return RejectedPublication{}, ErrNotFound
	}
	if err != nil {
		return RejectedPublication{}, err
	}
	if terminal || attemptText == nil || state != workflows.StateNeedsAttention || (attentionCode != "plex_timeout" && attentionCode != "publication_failed") {
		return RejectedPublication{}, ErrPublicationNotRejectable
	}
	var jobRevision int64
	var candidateText string
	err = transaction.QueryRow(ctx, `SELECT revision,candidate_id::text FROM altmount_jobs WHERE id=$1 AND workflow_id=$2 AND round_id IS NOT NULL`, *attemptText, workflowID.String()).Scan(&jobRevision, &candidateText)
	if errors.Is(err, pgx.ErrNoRows) {
		// A job without an acquisition round predates rounds; the owner decides.
		return RejectedPublication{}, ErrPublicationNotRejectable
	}
	if err != nil {
		return RejectedPublication{}, err
	}
	jobID, parseJob := uuid.Parse(*attemptText)
	candidateID, parseCandidate := uuid.Parse(candidateText)
	if parseJob != nil || parseCandidate != nil {
		return RejectedPublication{}, errors.New("rejected publication has invalid identifiers")
	}

	if _, err := transaction.Exec(ctx, `UPDATE library_link_companions companion SET state='displaced',displaced_at=$3
		FROM library_link_versions version
		WHERE companion.link_version_id=version.id AND version.workflow_id=$1 AND version.altmount_job_id=$2
		  AND version.state IN ('planned','publishing','verified','quarantined') AND companion.state IN ('planned','publishing','verified','quarantined')`, workflowID.String(), jobID.String(), now.UTC()); err != nil {
		return RejectedPublication{}, err
	}
	rows, err := transaction.Query(ctx, `UPDATE library_link_versions SET state='displaced',displaced_at=$3
		WHERE workflow_id=$1 AND altmount_job_id=$2 AND state IN ('planned','publishing','verified','quarantined')
		RETURNING id::text,generation`, workflowID.String(), jobID.String(), now.UTC())
	if err != nil {
		return RejectedPublication{}, err
	}
	type displacedVersion struct {
		id         string
		generation int64
	}
	displaced := []displacedVersion{}
	for rows.Next() {
		var version displacedVersion
		if err := rows.Scan(&version.id, &version.generation); err != nil {
			rows.Close()
			return RejectedPublication{}, err
		}
		displaced = append(displaced, version)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RejectedPublication{}, err
	}
	for _, version := range displaced {
		if _, err := transaction.Exec(ctx, `UPDATE library_links SET current_version_id=NULL WHERE current_version_id=$1`, version.id); err != nil {
			return RejectedPublication{}, err
		}
		// The removal checks that each link still points at this release
		// before it deletes it, then asks Plex to refresh the folder.
		payload, _ := json.Marshal(map[string]string{"version_id": version.id})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'link_version',$2,$3,'library.displaced_remove_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), version.id, version.generation, string(payload), now.UTC()); err != nil {
			return RejectedPublication{}, err
		}
	}

	job := AltMountJobContext{JobID: jobID, Workflow: workflows.Workflow{ID: workflowID}, CandidateID: candidateID, JobRevision: uint64(jobRevision)}
	outcome, err := store.recordAttributableAcquisitionFailure(ctx, transaction, job, nil, failureKind, failureCode, detail, now)
	if errors.Is(err, pgx.ErrNoRows) {
		return RejectedPublication{}, ErrPublicationNotRejectable
	}
	if err != nil {
		return RejectedPublication{}, err
	}
	auditDetail, _ := json.Marshal(map[string]any{"altmount_job_id": jobID.String(), "candidate_id": candidateID.String(), "attention_code": attentionCode, "failure_code": failureCode, "links_removed": len(displaced), "reason": detail})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'workflow.published_release_rejected','workflow',$3,$4,$5)`, uuid.NewV7().String(), actor, workflowID.String(), string(auditDetail), now.UTC()); err != nil {
		return RejectedPublication{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return RejectedPublication{}, err
	}
	return RejectedPublication{LinksRemoved: len(displaced), RetryQueued: outcome.RetryQueued, RoundExhausted: outcome.RoundExhausted}, nil
}

// rejectUnconfirmedBrokenFile rejects the release behind an AltMount file that
// AltMount reported corrupted, or that went missing, while its publication
// waits on the owner after a Plex timeout or a failed publication. It returns
// RepairUnmanaged when the file backs no such publication.
func (store *Store) rejectUnconfirmedBrokenFile(ctx context.Context, altMountID uuid.UUID, filePath, reason string, now time.Time) (string, error) {
	var workflowText string
	err := store.Pool.QueryRow(ctx, `SELECT workflow.id::text
		FROM altmount_output_inventories inventory
		JOIN altmount_jobs job ON job.id=inventory.job_id AND job.altmount_instance_id=$2
		JOIN altmount_output_assets asset ON asset.inventory_id=inventory.id AND inventory.root_relative || '/' || asset.relative_path=$1
		JOIN library_link_versions version ON version.source_asset_id=asset.id AND version.altmount_job_id=job.id AND version.state IN ('planned','publishing','verified','quarantined')
		JOIN workflows workflow ON workflow.id=version.workflow_id AND workflow.grab_attempt_id=job.id AND workflow.terminal_at IS NULL
		  AND workflow.state='needs_attention' AND workflow.attention_code IN ('plex_timeout','publication_failed')
		WHERE $1 LIKE inventory.root_relative || '/%'
		ORDER BY workflow.id LIMIT 1`, filePath, altMountID.String()).Scan(&workflowText)
	if errors.Is(err, pgx.ErrNoRows) {
		return RepairUnmanaged, nil
	}
	if err != nil {
		return "", err
	}
	workflowID, err := uuid.Parse(workflowText)
	if err != nil {
		return "", errors.New("broken publication has an invalid workflow")
	}
	detail := "AltMount reports missing Usenet articles in the published file before Plex confirmed it; the release is excluded and another release is tried"
	if reason == RepairReasonMissingFile {
		detail = "AltMount no longer has the published file (its Usenet articles went missing) before Plex confirmed it; the release is excluded and another release is tried"
	}
	_, err = store.RejectPublishedRelease(ctx, workflowID, "worker", "release_incomplete", BrokenPublicationCode, detail, now)
	if errors.Is(err, ErrPublicationNotRejectable) {
		return RepairUnmanaged, nil
	}
	if err != nil {
		return "", err
	}
	return RepairReleaseRejected, nil
}
