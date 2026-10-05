package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// Shared by the owner read model and mutation so the button describes the same
// exact, pre-submission recovery the transaction permits. Access errors are
// not evidence of a defective payload; every other exclusion stays in force.
const accessRejectedRetryQuery = `FROM workflows w
 JOIN media_items m ON m.id=w.media_id AND m.deleted_at IS NULL
 JOIN release_candidates c ON c.workflow_id=w.id AND c.id=w.selected_release_id
 JOIN integration_instances i ON i.id=c.indexer_instance_id AND i.kind IN ('newznab','oldboys') AND i.enabled AND i.config->'mode_verified'='true'::jsonb
 JOIN quality_profiles p ON p.id=w.quality_profile_id
 JOIN acquisition_rounds r ON r.workflow_id=w.id AND r.state='open' AND r.policy_version=p.policy_version
 JOIN candidate_decisions d ON d.workflow_id=w.id AND d.candidate_id=c.id AND d.policy_version=p.policy_version AND d.verdict='eligible'
 JOIN workflow_release_exclusions e ON e.workflow_id=w.id AND e.candidate_id=c.id AND e.indexer_instance_id=c.indexer_instance_id AND e.native_key=c.native_key
  AND e.failure_kind='release_retrieval' AND (
   (e.retrieval_round_id=r.id AND e.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403'))
   OR (e.retrieval_round_id IS NULL AND e.retrieval_error_code IS NULL AND e.created_at>=r.opened_at AND e.reason IN (
    'selected release NZB access was rejected after one bounded request (newznab_http_401)',
    'selected release NZB access was rejected after one bounded request (newznab_http_403)',
    'selected release NZB could not be retrieved after two bounded requests (newznab_http_401)',
    'selected release NZB could not be retrieved after two bounded requests (newznab_http_403)')))

 WHERE w.state='needs_attention' AND w.terminal_at IS NULL AND w.attention_code IN ('release_retrieval_failed','release_retrieval_limit')
 AND (SELECT count(*) FROM altmount_jobs j WHERE j.round_id=r.id)<r.attempt_limit_snapshot
 AND (SELECT count(*) FROM workflow_release_exclusions x WHERE x.retrieval_round_id=r.id)<4+CASE WHEN e.retrieval_round_id=r.id THEN 1 ELSE 0 END
 AND (SELECT count(DISTINCT j.id) FROM altmount_jobs j JOIN altmount_output_inventories v ON v.job_id=j.id
  WHERE j.workflow_id=w.id AND j.acquisition_outcome IS NOT NULL AND j.acquisition_outcome<>'output_verified')<%d
 AND r.upgrade_baseline_link_versions=(SELECT COALESCE(string_agg(v.id::text,',' ORDER BY v.id),'') FROM workflows published
  JOIN library_link_versions v ON v.workflow_id=published.id AND v.state='current' WHERE published.media_id=w.media_id AND published.variant=w.variant)
 AND NOT EXISTS(SELECT 1 FROM workflow_attempted_releases a JOIN release_candidates attempted ON attempted.id=a.candidate_id
  WHERE a.workflow_id=w.id AND attempted.indexer_instance_id=c.indexer_instance_id AND attempted.native_key=c.native_key)
 AND NOT EXISTS(SELECT 1 FROM altmount_jobs j WHERE j.workflow_id=w.id AND
  (j.candidate_id=c.id OR j.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested')))
 AND NOT EXISTS(SELECT 1 FROM library_link_versions v WHERE v.workflow_id=w.id AND v.state IN ('publishing','verified','current','quarantined'))
 AND NOT EXISTS(SELECT 1 FROM workflow_release_exclusions x WHERE x.retrieval_round_id=r.id AND x.indexer_instance_id=c.indexer_instance_id
  AND x.native_key<>c.native_key AND x.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403'))
 AND NOT EXISTS(SELECT 1 FROM release_blocks b WHERE (b.expires_at IS NULL OR b.expires_at>now()) AND
  ((b.scope='native_release' AND b.indexer_instance_id=c.indexer_instance_id AND b.native_key=c.native_key)
   OR (b.scope='pack_release' AND b.media_id=w.media_id AND b.variant=w.variant AND lower(b.release_title)=lower(c.title))))
 AND NOT EXISTS(SELECT 1 FROM adopted_files a WHERE a.media_id=w.media_id AND a.variant=w.variant AND a.state='current'
  AND NOT EXISTS(SELECT 1 FROM adopted_replacement_authorizations auth WHERE auth.workflow_id=w.id AND auth.adopted_file_id=a.id
   AND auth.destination_relative=a.destination_relative AND auth.source_size=a.source_size AND auth.source_modtime_ns=a.source_modtime_ns))`

func (store *Store) accessRejectedRetryQuery() string {
	return fmt.Sprintf(accessRejectedRetryQuery, store.AcquisitionLimits().RetainedFailedOutputs)
}

// RetryAccessRejectedRelease queues only the owner-reviewed selected release
// inside its existing round. Revision and candidate guards prevent a stale UI
// click or old worker delivery from altering a newer selection. No provider is
// contacted here; the normal retrieval worker retains all pacing and guards.
func (store *Store) RetryAccessRejectedRelease(ctx context.Context, workflowID, candidateID uuid.UUID, expectedRevision uint64, now time.Time) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&revision); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if uint64(revision) != expectedRevision {
		return ErrConcurrentModification
	}
	var roundID, indexerID, nativeKey, errorCode, originalReason string
	var originalCreatedAt time.Time
	err = tx.QueryRow(ctx, `SELECT r.id::text,i.id::text,c.native_key,COALESCE(e.retrieval_error_code,CASE WHEN e.reason LIKE '%(newznab_http_401)' THEN 'newznab_http_401' ELSE 'newznab_http_403' END),e.reason,e.created_at `+store.accessRejectedRetryQuery()+`
  AND w.id=$1 AND c.id=$2 FOR UPDATE OF r,c,e`, workflowID.String(), candidateID.String()).Scan(&roundID, &indexerID, &nativeKey, &errorCode, &originalReason, &originalCreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("this release cannot be retried: recovery requires an eligible, unsubmitted 401/403 rejection in the current open round")
	}
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM workflow_release_exclusions WHERE workflow_id=$1 AND candidate_id=$2 AND (retrieval_round_id=$3 OR retrieval_round_id IS NULL)
  AND failure_kind='release_retrieval'`, workflowID.String(), candidateID.String(), roundID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	newRevision := revision + 1
	if _, err := tx.Exec(ctx, `UPDATE workflows SET state='candidate_selected',revision=$2,grab_attempt_id=NULL,attention_code=NULL,attention_detail=NULL,updated_at=$3 WHERE id=$1`, workflowID.String(), newRevision, now.UTC()); err != nil {
		return err
	}
	const reason = "owner retried the exact access-rejected release after repairing provider access"
	if _, err := tx.Exec(ctx, `INSERT INTO candidate_selections(workflow_id,candidate_id,selected_at,actor,reason) VALUES($1,$2,$3,'owner',$4)
  ON CONFLICT(workflow_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id,selected_at=EXCLUDED.selected_at,actor=EXCLUDED.actor,reason=EXCLUDED.reason`, workflowID.String(), candidateID.String(), now.UTC(), reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at)
  VALUES($1,$2,'needs_attention','candidate_selected',$3,'owner',$4)`, workflowID.String(), newRevision, reason, now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"candidate_id": candidateID.String(), "round_id": roundID, "indexer_id": indexerID, "native_key": nativeKey, "retrieval_error_code": errorCode, "previous_revision": revision, "original_reason": originalReason, "original_created_at": originalCreatedAt})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at)
  VALUES($1,'owner','workflow.access_rejected_release_retried','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), string(detail), now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "candidate_id": candidateID.String()})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
  VALUES($1,'workflow',$2,$3,'release.retrieve_requested',$4,$5,$5)`, uuid.NewV7().String(), workflowID.String(), newRevision, string(payload), now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
