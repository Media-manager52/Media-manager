package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

const maxRoundRetrievalFailures = 4

type retrievalAdmission struct {
	roundID  string
	policy   json.RawMessage
	failures int
	code     string
	detail   string
}

// inspectRetrievalAdmission locks the exact selection, then checks the gates
// that can be known before spending a provider grab. It never opens a round.
func (store *Store) inspectRetrievalAdmission(ctx context.Context, tx pgx.Tx, retrieval RetrievalContext) (retrievalAdmission, error) {
	var result retrievalAdmission
	var state, selected string
	var revision, policyVersion int64
	var blocked, accessDenied, subtitleFailed bool
	var media catalog.MediaItem
	var year *int16
	var title string
	var metadata []byte
	if err := tx.QueryRow(ctx, `SELECT w.state,w.revision,COALESCE(w.selected_release_id::text,''),p.policy_version,
		EXISTS(SELECT 1 FROM release_blocks b WHERE (b.expires_at IS NULL OR b.expires_at>now()) AND
		 ((b.scope='native_release' AND b.indexer_instance_id=c.indexer_instance_id AND b.native_key=c.native_key)
		 OR (b.scope='pack_release' AND b.media_id=w.media_id AND b.variant=w.variant AND lower(b.release_title)=lower(c.title)))),
		EXISTS(SELECT 1 FROM workflow_release_exclusions e JOIN acquisition_rounds r ON r.id=e.retrieval_round_id AND r.state='open'
		 WHERE e.workflow_id=w.id AND e.indexer_instance_id=c.indexer_instance_id AND e.retrieval_error_code IN ('newznab_http_401','newznab_http_403')), `+knownSubtitleFailureSQL("c")+`,m.kind,m.title,m.year,c.title,COALESCE(metadata.data,'null'::jsonb)
		FROM workflows w JOIN quality_profiles p ON p.id=w.quality_profile_id JOIN release_candidates c ON c.id=w.selected_release_id
		JOIN media_items m ON m.id=w.media_id LEFT JOIN media_metadata metadata ON metadata.media_id=m.id
		WHERE w.id=$1 FOR UPDATE OF w`, retrieval.Workflow.ID.String()).Scan(&state, &revision, &selected, &policyVersion, &blocked, &accessDenied, &subtitleFailed, &media.Kind, &media.Title, &year, &title, &metadata); err != nil {
		return result, err
	}
	if state != string(workflows.StateCandidateSelected) || uint64(revision) != retrieval.Workflow.Revision || selected != retrieval.CandidateID.String() {
		return result, ErrConcurrentModification
	}
	if blocked || accessDenied {
		result.code, result.detail = "release_retrieval_source_blocked", "The selected release is blocked or its provider rejected access in this round; no further NZB was requested."
		return result, nil
	}
	if subtitleFailed {
		result.code, result.detail = "release_subtitle_already_failed", "An exact-title release already failed bounded subtitle inspection in this workflow; no duplicate NZB was requested."
		return result, nil
	}
	if media.Kind == catalog.KindMovie && year != nil {
		if err := json.Unmarshal(metadata, &media.Metadata); err != nil {
			return result, err
		}
		if releases.ConflictingMovieYear(title, uint16(*year), media.SavedTitles()) {
			result.code, result.detail = "release_movie_year_conflict", "The selected release has an explicit movie year conflicting with the requested title; no NZB was requested."
			return result, nil
		}
	}
	var roundPolicy int64
	var limit, used, retained int
	var baseline, currentBaseline string
	err := tx.QueryRow(ctx, `SELECT r.id::text,r.policy_version,r.policy_snapshot,r.attempt_limit_snapshot,r.upgrade_baseline_link_versions,
		(SELECT count(*) FROM altmount_jobs j WHERE j.round_id=r.id),
		(SELECT count(*) FROM workflow_release_exclusions e WHERE e.retrieval_round_id=r.id),
		(SELECT count(DISTINCT j.id) FROM altmount_jobs j JOIN altmount_output_inventories i ON i.job_id=j.id
		 WHERE j.workflow_id=r.workflow_id AND j.acquisition_outcome IS NOT NULL AND j.acquisition_outcome<>'output_verified'),
		(SELECT COALESCE(string_agg(v.id::text,',' ORDER BY v.id),'') FROM workflows requested
		 JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
		 JOIN library_link_versions v ON v.workflow_id=published.id AND v.state='current' WHERE requested.id=r.workflow_id)
		FROM acquisition_rounds r WHERE r.workflow_id=$1 AND r.state='open' FOR UPDATE OF r`, retrieval.Workflow.ID.String()).Scan(
		&result.roundID, &roundPolicy, &result.policy, &limit, &baseline, &used, &result.failures, &retained, &currentBaseline)
	if errors.Is(err, pgx.ErrNoRows) {
		result.code, result.detail = "release_retrieval_round_unavailable", "NZB retrieval requires an existing open acquisition round; owner review is required."
		return result, nil
	}
	if err != nil {
		return result, err
	}
	switch {
	case roundPolicy != policyVersion || baseline != currentBaseline:
		result.code, result.detail = "release_retrieval_policy_changed", "The policy or current publication changed after this round opened; review the candidate before retrieving another NZB."
	case used >= limit:
		result.code, result.detail = "release_retrieval_round_full", "The open acquisition round has no submission slots left; no further NZB was requested."
	case retained >= store.AcquisitionLimits().RetainedFailedOutputs:
		result.code, result.detail = "retained_failed_output_limit", fmt.Sprintf("%d failed outputs are retained (limit %d); owner storage review is required before another NZB request.", retained, store.AcquisitionLimits().RetainedFailedOutputs)
	case result.failures >= maxRoundRetrievalFailures:
		result.code, result.detail = "release_retrieval_limit", "Four candidates failed NZB retrieval in this round; owner review is required. Submission slots were not consumed by these failures."
	}
	return result, nil
}

func recordRetrievalAttention(ctx context.Context, tx pgx.Tx, retrieval RetrievalContext, code, detail string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE workflows SET state='needs_attention',revision=revision+1,updated_at=$2,attention_code=$3,attention_detail=$4 WHERE id=$1`, retrieval.Workflow.ID.String(), now.UTC(), code, detail); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at)
		VALUES($1,$2,'candidate_selected','needs_attention',$3,'worker',$4)`, retrieval.Workflow.ID.String(), int64(retrieval.Workflow.Revision)+1, detail, now.UTC())
	return err
}

// PrepareReleaseRetrieval stops inadmissible work before any provider request.
// Repeating it at the per-hop guard also covers changes during pacing/redirects.
func (store *Store) PrepareReleaseRetrieval(ctx context.Context, retrieval RetrievalContext, now time.Time) (bool, error) {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	admission, err := store.inspectRetrievalAdmission(ctx, tx, retrieval)
	if errors.Is(err, ErrConcurrentModification) || errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if admission.code != "" {
		if err := recordRetrievalAttention(ctx, tx, retrieval, admission.code, admission.detail, now); err != nil {
			return false, err
		}
	}
	return admission.code == "", tx.Commit(ctx)
}

// RecordReleaseRetrievalFailure excludes one exact provider/native identity,
// then selects a known alternative within the same round. Retrieval failures
// have their own durable cap and never consume an AltMount submission slot.
func (store *Store) RecordReleaseRetrievalFailure(ctx context.Context, retrieval RetrievalContext, errorCode, detail string, now time.Time) error {
	if strings.TrimSpace(detail) == "" {
		detail = "selected release NZB could not be retrieved"
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	admission, err := store.inspectRetrievalAdmission(ctx, tx, retrieval)
	if errors.Is(err, ErrConcurrentModification) || errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if admission.code != "" {
		if err := recordRetrievalAttention(ctx, tx, retrieval, admission.code, admission.detail, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if errorCode == "" {
		errorCode = "release_retrieval_failed"
	}
	command, err := tx.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at,retrieval_round_id,retrieval_error_code)
		SELECT c.workflow_id,c.id,c.indexer_instance_id,c.native_key,NULL,'release_retrieval',$3,$4,$5,$6
		FROM release_candidates c WHERE c.workflow_id=$1 AND c.id=$2
		ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO NOTHING`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), detail, now.UTC(), admission.roundID, errorCode)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	admission.failures++
	var next string
	if admission.failures < maxRoundRetrievalFailures {
		policy, err := subtitles.ParsePolicy(admission.policy)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT c.id::text FROM release_candidates c
			JOIN workflows w ON w.id=c.workflow_id
			JOIN acquisition_rounds r ON r.workflow_id=c.workflow_id AND r.id=$2
			JOIN candidate_decisions d ON d.workflow_id=c.workflow_id AND d.candidate_id=c.id AND d.policy_version=r.policy_version AND d.verdict='eligible'
			JOIN release_observations o ON o.id=c.observation_id
			JOIN integration_instances i ON i.id=c.indexer_instance_id AND i.enabled AND i.kind='newznab' AND i.config->'mode_verified'='true'::jsonb
			WHERE c.workflow_id=$1
			AND NOT EXISTS(SELECT 1 FROM release_blocks b WHERE (b.expires_at IS NULL OR b.expires_at>$5) AND
			 ((b.scope='native_release' AND b.indexer_instance_id=c.indexer_instance_id AND b.native_key=c.native_key)
			 OR (b.scope='pack_release' AND b.media_id=w.media_id AND b.variant=w.variant AND lower(b.release_title)=lower(c.title))))
			AND NOT EXISTS(SELECT 1 FROM workflow_attempted_releases a WHERE a.workflow_id=c.workflow_id AND a.candidate_id=c.id)
			AND NOT EXISTS(SELECT 1 FROM workflow_release_exclusions e WHERE e.workflow_id=c.workflow_id AND e.indexer_instance_id=c.indexer_instance_id AND e.native_key=c.native_key)
			AND NOT EXISTS(SELECT 1 FROM workflow_release_exclusions e WHERE e.retrieval_round_id=r.id AND e.indexer_instance_id=c.indexer_instance_id AND e.retrieval_error_code IN ('newznab_http_401','newznab_http_403'))`+" AND NOT "+knownSubtitleFailureSQL("c")+`
			ORDER BY
			($3 AND EXISTS(SELECT 1 FROM jsonb_array_elements(o.normalized_facts) f WHERE f->>'name'='subtitle_languages' AND f->>'state' IN ('observed','inferred') AND f->'values' @> '["da"]'::jsonb)) DESC,
			($3 AND o.title ~ $4) DESC,
			(EXISTS(SELECT 1 FROM workflow_release_exclusions e JOIN release_candidates failed ON failed.id=e.candidate_id
			 WHERE e.workflow_id=c.workflow_id AND e.failure_kind='subtitle_verification' AND lower(failed.title)=lower(c.title))) ASC,
			(c.first_seen_sequence>r.novelty_cutoff_sequence) DESC,d.score DESC,c.indexer_instance_id,c.native_key
			LIMIT 1`, retrieval.Workflow.ID.String(), admission.roundID, slices.Contains(policy.Required, "da"), releases.DanishSubtitleHintPattern, now.UTC()).Scan(&next)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if next == "" {
		code := "release_retrieval_failed"
		reason := detail + "; no eligible, untried candidate remains on a commissioned provider without an access rejection in this round."
		if admission.failures >= maxRoundRetrievalFailures {
			code = "release_retrieval_limit"
			reason = detail + "; four candidates failed NZB retrieval in this round. Owner review is required; submission slots remain separate."
		}
		if err := recordRetrievalAttention(ctx, tx, retrieval, code, reason, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	revision := int64(retrieval.Workflow.Revision) + 1
	reason := fmt.Sprintf("NZB retrieval failed (%s, candidate %d/%d); selected a known eligible alternative in the same round. No submission slot consumed.", errorCode, admission.failures, maxRoundRetrievalFailures)
	if _, err := tx.Exec(ctx, `UPDATE workflows SET selected_release_id=$2,grab_attempt_id=NULL,revision=$3,updated_at=$4,attention_code=NULL,attention_detail=NULL WHERE id=$1`, retrieval.Workflow.ID.String(), next, revision, now.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO candidate_selections(workflow_id,candidate_id,selected_at,actor,reason) VALUES($1,$2,$3,'policy',$4)
		ON CONFLICT(workflow_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id,selected_at=EXCLUDED.selected_at,actor=EXCLUDED.actor,reason=EXCLUDED.reason`, retrieval.Workflow.ID.String(), next, now.UTC(), reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'candidate_selected','candidate_selected',$3,'worker',$4)`, retrieval.Workflow.ID.String(), revision, reason, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": retrieval.Workflow.ID.String(), "candidate_id": next})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow',$2,$3,'release.retrieve_requested',$4,$5,$5)`, uuid.NewV7().String(), retrieval.Workflow.ID.String(), revision, string(payload), now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
