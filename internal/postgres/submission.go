package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/workflows"
)

type RetrievalContext struct {
	Workflow           workflows.Workflow
	Variant            string
	CandidateID        uuid.UUID
	NativeID           string
	DownloadLocatorRef string
	ReleaseTitle       string
	Indexer            Integration
	DownloadClient     Integration
}
type SubmissionIntent struct {
	JobID            uuid.UUID
	Filename         string
	State            string
	ExternalQueueID  *int64
	ShouldSubmit     bool
	DuplicatePackage bool
	NextCandidateID  *uuid.UUID
}

const maxDuplicatePackageSkips = 4

func (store *Store) LoadRetrievalContext(ctx context.Context, workflowID, candidateID uuid.UUID) (RetrievalContext, error) {
	var result RetrievalContext
	var workflowText, clientText, indexerText string
	var attemptText *string
	var revision int64
	err := store.Pool.QueryRow(ctx, `SELECT w.id::text,w.state,w.revision,w.variant,w.download_client_id::text,w.grab_attempt_id::text,c.id::text,c.native_key,c.download_locator_ref,c.title,c.indexer_instance_id::text FROM workflows w JOIN release_candidates c ON c.workflow_id=w.id WHERE w.id=$1 AND c.id=$2 AND w.selected_release_id=c.id`, workflowID.String(), candidateID.String()).Scan(&workflowText, &result.Workflow.State, &revision, &result.Variant, &clientText, &attemptText, &workflowText, &result.NativeID, &result.DownloadLocatorRef, &result.ReleaseTitle, &indexerText)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetrievalContext{}, ErrNotFound
	}
	if err != nil {
		return RetrievalContext{}, err
	}
	result.Workflow.ID = workflowID
	result.Workflow.Revision = uint64(revision)
	result.CandidateID = candidateID
	result.Workflow.DownloadClientID, _ = uuid.Parse(clientText)
	if attemptText != nil {
		parsed, parseErr := uuid.Parse(*attemptText)
		if parseErr != nil {
			return RetrievalContext{}, parseErr
		}
		result.Workflow.GrabAttemptID = &parsed
	}
	indexerID, _ := uuid.Parse(indexerText)
	result.Indexer, err = store.Integration(ctx, indexerID)
	if err != nil {
		return RetrievalContext{}, err
	}
	result.DownloadClient, err = store.Integration(ctx, result.Workflow.DownloadClientID)
	return result, err
}

// RecordPreSubmissionRejection records a release whose NZB package was
// rejected by the pre-flight screen before any AltMount submission intent
// existed. No job row and no round slot are consumed: the exact release is
// excluded and the next candidate is searched immediately, so zip/tar/ISO
// packages can never spend one of a round's two download attempts.
func (store *Store) RecordPreSubmissionRejection(ctx context.Context, retrieval RetrievalContext, failureCode, detail string, now time.Time) error {
	if strings.TrimSpace(detail) == "" {
		detail = "release package rejected before AltMount submission"
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var indexerText, nativeKey string
	err = transaction.QueryRow(ctx, `SELECT workflow.state,workflow.revision,candidate.indexer_instance_id::text,candidate.native_key
		FROM workflows workflow
		JOIN release_candidates candidate ON candidate.workflow_id=workflow.id
		WHERE workflow.id=$1 AND candidate.id=$2 AND workflow.selected_release_id=candidate.id
		FOR UPDATE OF workflow`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String()).Scan(&state, &revision, &indexerText, &nativeKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != workflows.StateCandidateSelected || uint64(revision) != retrieval.Workflow.Revision {
		return transaction.Commit(ctx)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at)
		VALUES($1,$2,$3,$4,NULL,$5,$6,$7)
		ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO NOTHING`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), indexerText, nativeKey, failureCode, detail, now.UTC()); err != nil {
		return err
	}
	newRevision := revision + 1
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,revision=$2,updated_at=$3,attention_code=NULL,attention_detail=$4 WHERE id=$1 AND revision=$5`, retrieval.Workflow.ID.String(), newRevision, now.UTC(), detail, revision); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'waiting',$4,'worker',$5)`, retrieval.Workflow.ID.String(), newRevision, state, detail, now.UTC()); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": retrieval.Workflow.ID.String(), "reason": "release package rejected before submission; exact release excluded without spending a round slot"})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$5)`, uuid.NewV7().String(), retrieval.Workflow.ID.String(), newRevision, string(payload), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) BeginAltMountSubmission(ctx context.Context, retrieval RetrievalContext, packageIdentity [sha256.Size]byte, now time.Time) (SubmissionIntent, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SubmissionIntent{}, err
	}
	defer transaction.Rollback(ctx)
	var existingID, existingCandidate, filename, state string
	var queueID *int64
	var generation int
	err = transaction.QueryRow(ctx, `SELECT id::text,candidate_id::text,attempt_generation,upload_name,state,external_queue_id FROM altmount_jobs WHERE workflow_id=$1 ORDER BY attempt_generation DESC LIMIT 1 FOR UPDATE`, retrieval.Workflow.ID.String()).Scan(&existingID, &existingCandidate, &generation, &filename, &state, &queueID)
	if err == nil {
		parsed, _ := uuid.Parse(existingID)
		if existingCandidate == retrieval.CandidateID.String() && state == "import_complete" {
			var current workflows.State
			var revision int64
			if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, retrieval.Workflow.ID.String()).Scan(&current, &revision); err != nil {
				return SubmissionIntent{}, err
			}
			if current != workflows.StateCandidateSelected {
				return SubmissionIntent{}, ErrConcurrentModification
			}
			if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='import_complete',grab_attempt_id=$2,revision=revision+1,updated_at=$3 WHERE id=$1`, retrieval.Workflow.ID.String(), parsed.String(), now.UTC()); err != nil {
				return SubmissionIntent{}, err
			}
			if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'import_complete','resumed verified AltMount import','worker',$4)`, retrieval.Workflow.ID.String(), revision+1, current, now.UTC()); err != nil {
				return SubmissionIntent{}, err
			}
			payload, _ := json.Marshal(map[string]string{"workflow_id": retrieval.Workflow.ID.String(), "job_id": parsed.String()})
			if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'library.publish_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), retrieval.Workflow.ID.String(), revision+1, string(payload), now.UTC()); err != nil {
				return SubmissionIntent{}, err
			}
			if commitErr := transaction.Commit(ctx); commitErr != nil {
				return SubmissionIntent{}, commitErr
			}
			return SubmissionIntent{JobID: parsed, Filename: filename, State: state, ExternalQueueID: queueID, ShouldSubmit: false}, nil
		}
		terminal := state == "failed" || state == "cancelled" || state == "removed" || state == "needs_attention" || state == "import_complete"
		if !terminal {
			if commitErr := transaction.Commit(ctx); commitErr != nil {
				return SubmissionIntent{}, commitErr
			}
			return SubmissionIntent{JobID: parsed, Filename: filename, State: state, ExternalQueueID: queueID, ShouldSubmit: false}, nil
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SubmissionIntent{}, err
	}
	var current workflows.State
	var revision int64
	var selectedCandidate *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,selected_release_id::text FROM workflows WHERE id=$1 FOR UPDATE`, retrieval.Workflow.ID.String()).Scan(&current, &revision, &selectedCandidate); err != nil {
		return SubmissionIntent{}, err
	}
	if current != workflows.StateCandidateSelected || uint64(revision) != retrieval.Workflow.Revision || selectedCandidate == nil || *selectedCandidate != retrieval.CandidateID.String() {
		return SubmissionIntent{}, ErrConcurrentModification
	}
	round, err := store.ensureOpenAcquisitionRound(ctx, transaction, retrieval.Workflow.ID, now)
	if err != nil {
		return SubmissionIntent{}, err
	}
	var currentBaseline string
	err = transaction.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'') FROM workflows requested
		LEFT JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
		LEFT JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
		WHERE requested.id=$1`, retrieval.Workflow.ID.String()).Scan(&currentBaseline)
	if err != nil {
		return SubmissionIntent{}, err
	}
	if round.UpgradeBaseline != currentBaseline {
		return SubmissionIntent{}, errors.New("current publication changed after the acquisition round opened; candidate must be re-evaluated")
	}
	var attemptedCandidate bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_attempted_releases WHERE workflow_id=$1 AND candidate_id=$2)`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String()).Scan(&attemptedCandidate); err != nil {
		return SubmissionIntent{}, err
	}
	if attemptedCandidate {
		return SubmissionIntent{}, errors.New("exact release identity was already submitted in this workflow")
	}
	var roundSlot int
	if err := transaction.QueryRow(ctx, `SELECT count(*)+1 FROM altmount_jobs WHERE round_id=$1`, round.ID.String()).Scan(&roundSlot); err != nil {
		return SubmissionIntent{}, err
	}
	if roundSlot > round.AttemptLimit {
		return SubmissionIntent{}, errors.New("acquisition round attempt limit reached")
	}
	var retainedOutputs int
	if err := transaction.QueryRow(ctx, `SELECT count(DISTINCT job.id) FROM altmount_jobs job
		JOIN altmount_output_inventories inventory ON inventory.job_id=job.id
		WHERE job.workflow_id=$1 AND job.acquisition_outcome IS NOT NULL AND job.acquisition_outcome <> 'output_verified'`, retrieval.Workflow.ID.String()).Scan(&retainedOutputs); err != nil {
		return SubmissionIntent{}, err
	}
	if retainedOutputs >= store.AcquisitionLimits().RetainedFailedOutputs {
		return SubmissionIntent{}, errors.New("retained failed-output limit reached; owner storage review is required before another submission")
	}
	var priorJobText string
	err = transaction.QueryRow(ctx, `SELECT first_job_id::text FROM workflow_submitted_packages WHERE workflow_id=$1 AND package_sha256=$2`, retrieval.Workflow.ID.String(), packageIdentity[:]).Scan(&priorJobText)
	if err == nil {
		if _, parseErr := uuid.Parse(priorJobText); parseErr != nil {
			return SubmissionIntent{}, parseErr
		}
		return recordDuplicatePackage(ctx, transaction, retrieval, current, revision, now)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SubmissionIntent{}, err
	}
	jobID := uuid.NewV7()
	filename = releaseSlug(retrieval.ReleaseTitle) + "--mm-" + jobID.String() + ".nzb"
	generation++
	if _, err := transaction.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,nzb_package_sha256,round_id,round_slot) VALUES($1,$2,$3,$4,$5,$6,'submitting',$7,1,$8,$9,$10)`, jobID.String(), retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), retrieval.DownloadClient.ID.String(), generation, filename, now.UTC(), packageIdentity[:], round.ID.String(), roundSlot); err != nil {
		return SubmissionIntent{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_attempted_releases(workflow_id,candidate_id,first_job_id,created_at) VALUES($1,$2,$3,$4)`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), jobID.String(), now.UTC()); err != nil {
		return SubmissionIntent{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_submitted_packages(workflow_id,package_sha256,first_job_id,created_at) VALUES($1,$2,$3,$4)`, retrieval.Workflow.ID.String(), packageIdentity[:], jobID.String(), now.UTC()); err != nil {
		return SubmissionIntent{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='submitting',grab_attempt_id=$2,revision=revision+1,updated_at=$3 WHERE id=$1 AND revision=$4`, retrieval.Workflow.ID.String(), jobID.String(), now.UTC(), revision); err != nil {
		return SubmissionIntent{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'submitting','selected NZB validated','worker',$4)`, retrieval.Workflow.ID.String(), revision+1, current, now.UTC()); err != nil {
		return SubmissionIntent{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return SubmissionIntent{}, err
	}
	return SubmissionIntent{JobID: jobID, Filename: filename, State: "submitting", ShouldSubmit: true}, nil
}

func recordDuplicatePackage(ctx context.Context, transaction pgx.Tx, retrieval RetrievalContext, current workflows.State, revision int64, now time.Time) (SubmissionIntent, error) {
	const detail = "exact NZB package was already submitted in this workflow"
	var indexerText, nativeKey string
	if err := transaction.QueryRow(ctx, `SELECT indexer_instance_id::text,native_key FROM release_candidates WHERE workflow_id=$1 AND id=$2`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String()).Scan(&indexerText, &nativeKey); err != nil {
		return SubmissionIntent{}, err
	}
	command, err := transaction.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at)
		VALUES($1,$2,$3,$4,NULL,'duplicate_package',$5,$6)
		ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO NOTHING`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), indexerText, nativeKey, detail, now.UTC())
	if err != nil {
		return SubmissionIntent{}, err
	}
	if command.RowsAffected() != 1 {
		return SubmissionIntent{}, ErrConcurrentModification
	}
	var skipped int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind='duplicate_package'`, retrieval.Workflow.ID.String()).Scan(&skipped); err != nil {
		return SubmissionIntent{}, err
	}
	var nextCandidateText string
	if skipped <= maxDuplicatePackageSkips {
		err = transaction.QueryRow(ctx, `SELECT candidate.id::text
			FROM release_candidates candidate
			JOIN candidate_decisions decision ON decision.workflow_id=candidate.workflow_id AND decision.candidate_id=candidate.id
			JOIN workflows workflow ON workflow.id=candidate.workflow_id
			JOIN acquisition_rounds round ON round.workflow_id=workflow.id AND round.state='open' AND round.policy_version=decision.policy_version
			WHERE candidate.workflow_id=$1 AND decision.verdict='eligible'
			  AND NOT EXISTS (SELECT 1 FROM workflow_attempted_releases attempted WHERE attempted.workflow_id=candidate.workflow_id AND attempted.candidate_id=candidate.id)
			AND NOT EXISTS (SELECT 1 FROM workflow_release_exclusions denied WHERE denied.retrieval_round_id=round.id AND denied.indexer_instance_id=candidate.indexer_instance_id AND denied.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403'))
			  AND NOT EXISTS (
			    SELECT 1 FROM workflow_release_exclusions exclusion
			    WHERE exclusion.workflow_id=candidate.workflow_id
			      AND exclusion.indexer_instance_id=candidate.indexer_instance_id
			      AND exclusion.native_key=candidate.native_key
			  )`+" AND NOT "+knownSubtitleFailureSQL("candidate")+`
			ORDER BY (candidate.first_seen_sequence>round.novelty_cutoff_sequence) DESC,decision.score DESC,candidate.indexer_instance_id,candidate.native_key
			LIMIT 1`, retrieval.Workflow.ID.String()).Scan(&nextCandidateText)
	}
	if err == nil && nextCandidateText != "" {
		nextCandidateID, parseErr := uuid.Parse(nextCandidateText)
		if parseErr != nil {
			return SubmissionIntent{}, parseErr
		}
		newRevision := revision + 1
		if _, err := transaction.Exec(ctx, `UPDATE workflows SET selected_release_id=$2,grab_attempt_id=NULL,revision=$3,updated_at=$4,attention_code=NULL,attention_detail=NULL WHERE id=$1 AND revision=$5`, retrieval.Workflow.ID.String(), nextCandidateID.String(), newRevision, now.UTC(), revision); err != nil {
			return SubmissionIntent{}, err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO candidate_selections(workflow_id,candidate_id,selected_at,actor,reason) VALUES($1,$2,$3,'policy','next eligible candidate after exact duplicate package') ON CONFLICT(workflow_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id,selected_at=EXCLUDED.selected_at,actor=EXCLUDED.actor,reason=EXCLUDED.reason`, retrieval.Workflow.ID.String(), nextCandidateID.String(), now.UTC()); err != nil {
			return SubmissionIntent{}, err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$3,$4,'worker',$5)`, retrieval.Workflow.ID.String(), newRevision, current, detail+"; next eligible candidate selected", now.UTC()); err != nil {
			return SubmissionIntent{}, err
		}
		payload, _ := json.Marshal(map[string]string{"workflow_id": retrieval.Workflow.ID.String(), "candidate_id": nextCandidateID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'release.retrieve_requested',$4,$5,$5)`, uuid.NewV7().String(), retrieval.Workflow.ID.String(), newRevision, string(payload), now.UTC()); err != nil {
			return SubmissionIntent{}, err
		}
		if err := transaction.Commit(ctx); err != nil {
			return SubmissionIntent{}, err
		}
		return SubmissionIntent{State: string(current), ShouldSubmit: false, DuplicatePackage: true, NextCandidateID: &nextCandidateID}, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SubmissionIntent{}, err
	}
	attentionCode := "duplicate_package_no_alternate"
	attentionDetail := "The next eligible release resolved to an exact package already submitted in this workflow, and no unexcluded eligible candidate remains."
	if skipped > maxDuplicatePackageSkips {
		attentionCode = "duplicate_package_skip_limit"
		attentionDetail = "The bounded exact-package duplicate skip limit was reached; owner review is required."
	}
	newRevision := revision + 1
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',selected_release_id=NULL,grab_attempt_id=NULL,revision=$2,updated_at=$3,attention_code=$4,attention_detail=$5 WHERE id=$1 AND revision=$6`, retrieval.Workflow.ID.String(), newRevision, now.UTC(), attentionCode, attentionDetail, revision); err != nil {
		return SubmissionIntent{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET retry_state='no_alternate' WHERE workflow_id=$1 AND retry_state='queued'`, retrieval.Workflow.ID.String()); err != nil {
		return SubmissionIntent{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention',$4,'worker',$5)`, retrieval.Workflow.ID.String(), newRevision, current, attentionDetail, now.UTC()); err != nil {
		return SubmissionIntent{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return SubmissionIntent{}, err
	}
	return SubmissionIntent{State: "needs_attention", ShouldSubmit: false, DuplicatePackage: true}, nil
}

func (store *Store) MarkAltMountSubmitted(ctx context.Context, workflowID, jobID uuid.UUID, queueID int64, now time.Time) error {
	return store.finishSubmission(ctx, workflowID, jobID, &queueID, "queued", workflows.StateQueued, "AltMount accepted the NZB", now)
}
func (store *Store) MarkAltMountUnknown(ctx context.Context, workflowID, jobID uuid.UUID, now time.Time) error {
	return store.finishSubmission(ctx, workflowID, jobID, nil, "transport_unknown", workflows.StateTransportUnknown, "AltMount upload result is ambiguous", now)
}
func (store *Store) MarkAltMountFailed(ctx context.Context, workflowID, jobID uuid.UUID, now time.Time) error {
	return store.finishSubmission(ctx, workflowID, jobID, nil, "failed", workflows.StateNeedsAttention, "AltMount definitively rejected the NZB", now)
}

func (store *Store) finishSubmission(ctx context.Context, workflowID, jobID uuid.UUID, queueID *int64, jobState string, workflowState workflows.State, reason string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var current workflows.State
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&current, &revision); err != nil {
		return err
	}
	if current != workflows.StateSubmitting {
		return ErrConcurrentModification
	}
	var jobRevision int64
	if err := transaction.QueryRow(ctx, `UPDATE altmount_jobs SET state=$2,external_queue_id=$3::bigint,submitted_at=CASE WHEN $3::bigint IS NULL THEN submitted_at ELSE $4 END,last_reconciled_at=$4,revision=revision+1,
		error_code=CASE WHEN $2='failed' THEN 'altmount_submission_rejected' ELSE error_code END,
		error_detail=CASE WHEN $2='failed' THEN $5 ELSE error_detail END,
		acquisition_outcome=CASE WHEN $2='failed' THEN 'execution_failed' ELSE acquisition_outcome END,
		acquisition_closed_at=CASE WHEN $2='failed' THEN $4 ELSE acquisition_closed_at END
		WHERE id=$1 RETURNING revision`, jobID.String(), jobState, queueID, now.UTC(), reason).Scan(&jobRevision); err != nil {
		return err
	}
	attentionCode := ""
	if jobState == "failed" {
		attentionCode = "altmount_submission_rejected"
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=revision+1,updated_at=$3,attention_code=NULLIF($5,''),attention_detail=CASE WHEN $5='' THEN attention_detail ELSE $6 END WHERE id=$1 AND revision=$4`, workflowID.String(), workflowState, now.UTC(), revision, attentionCode, reason); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,$5,'worker',$6)`, workflowID.String(), revision+1, current, workflowState, reason, now.UTC()); err != nil {
		return err
	}
	if workflowState == workflows.StateQueued || workflowState == workflows.StateTransportUnknown {
		payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "job_id": jobID.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'altmount_job',$2,$3,'altmount.reconcile_requested',$4,$5,$5)`, uuid.NewV7().String(), jobID.String(), jobRevision, string(payload), now.UTC()); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

var slugToken = regexp.MustCompile(`[^a-z0-9]+`)

func releaseSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugToken.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if len(value) > 80 {
		value = value[:80]
	}
	if value == "" {
		return "release"
	}
	return value
}

// SelectCrossPostWithGrabBudget moves a selection whose indexer has exhausted
// its rolling grab budget to the same release (identical title) posted on
// another enabled, commissioned indexer that still has grab budget, when that
// candidate is eligible under the open round's policy and was never attempted
// or excluded. The exact release is unchanged, so its subtitle evidence and
// the post-download verification still apply; only the source indexer moves.
// It returns false, changing nothing, when the selection is stale or no such
// cross-post exists.
func (store *Store) SelectCrossPostWithGrabBudget(ctx context.Context, retrieval RetrievalContext, now time.Time) (bool, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	var selected *string
	if err := transaction.QueryRow(ctx, `SELECT state,revision,selected_release_id::text FROM workflows WHERE id=$1 FOR UPDATE`, retrieval.Workflow.ID.String()).Scan(&state, &revision, &selected); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if state != workflows.StateCandidateSelected || uint64(revision) != retrieval.Workflow.Revision || selected == nil || *selected != retrieval.CandidateID.String() {
		return false, nil
	}
	var nextText, indexerName string
	err = transaction.QueryRow(ctx, `SELECT candidate.id::text,integration.name
		FROM release_candidates current
		JOIN release_candidates candidate ON candidate.workflow_id=current.workflow_id AND candidate.id<>current.id
		  AND candidate.indexer_instance_id<>current.indexer_instance_id AND lower(candidate.title)=lower(current.title)
		JOIN acquisition_rounds round ON round.workflow_id=candidate.workflow_id AND round.state='open'
		JOIN candidate_decisions decision ON decision.workflow_id=candidate.workflow_id AND decision.candidate_id=candidate.id
		  AND decision.policy_version=round.policy_version AND decision.verdict='eligible'
		JOIN integration_instances integration ON integration.id=candidate.indexer_instance_id
		  AND integration.kind IN ('newznab','oldboys') AND integration.enabled AND integration.config->'mode_verified'='true'::jsonb
		WHERE current.workflow_id=$1 AND current.id=$2
		  AND NOT EXISTS (SELECT 1 FROM workflow_attempted_releases attempted WHERE attempted.workflow_id=candidate.workflow_id AND attempted.candidate_id=candidate.id)
			AND NOT EXISTS (SELECT 1 FROM workflow_release_exclusions denied WHERE denied.retrieval_round_id=round.id AND denied.indexer_instance_id=candidate.indexer_instance_id AND denied.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403'))
		  AND NOT EXISTS (SELECT 1 FROM workflow_release_exclusions exclusion WHERE exclusion.workflow_id=candidate.workflow_id
		    AND exclusion.indexer_instance_id=candidate.indexer_instance_id AND exclusion.native_key=candidate.native_key)
		  AND (SELECT count(*) FROM indexer_request_reservations reservation
		       WHERE reservation.indexer_instance_id=candidate.indexer_instance_id AND reservation.request_family='grab'
		         AND reservation.reserved_at > $3::timestamptz - make_interval(secs => COALESCE(NULLIF(integration.config->>'budget_window_seconds','')::bigint, 86400)))
		      < COALESCE(NULLIF(NULLIF(integration.config->>'grab_limit',''),'0')::int, $4)`+" AND NOT "+knownSubtitleFailureSQL("candidate")+`
		ORDER BY decision.score DESC,candidate.indexer_instance_id,candidate.native_key
		LIMIT 1`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), now.UTC(), indexers.ConservativeGrabLimit).Scan(&nextText, &indexerName)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	newRevision := revision + 1
	reason := "selected indexer's rolling grab budget is exhausted; the same release is selected on " + indexerName
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET selected_release_id=$2,revision=$3,updated_at=$4 WHERE id=$1 AND revision=$5`, retrieval.Workflow.ID.String(), nextText, newRevision, now.UTC(), revision); err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO candidate_selections(workflow_id,candidate_id,selected_at,actor,reason) VALUES($1,$2,$3,'policy',$4) ON CONFLICT(workflow_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id,selected_at=EXCLUDED.selected_at,actor=EXCLUDED.actor,reason=EXCLUDED.reason`, retrieval.Workflow.ID.String(), nextText, now.UTC(), reason); err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,'candidate_selected','candidate_selected',$3,'worker',$4)`, retrieval.Workflow.ID.String(), newRevision, reason, now.UTC()); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]string{"workflow_id": retrieval.Workflow.ID.String(), "candidate_id": nextText})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'release.retrieve_requested',$4,$5,$5)`, uuid.NewV7().String(), retrieval.Workflow.ID.String(), newRevision, string(payload), now.UTC()); err != nil {
		return false, err
	}
	return true, transaction.Commit(ctx)
}
