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
	"github.com/media-manager/media-manager/internal/workflows"
)

type RetrievalContext struct {
	Workflow       workflows.Workflow
	Variant        string
	CandidateID    uuid.UUID
	NativeID       string
	ReleaseTitle   string
	Indexer        Integration
	DownloadClient Integration
}
type SubmissionIntent struct {
	JobID           uuid.UUID
	Filename        string
	State           string
	ExternalQueueID *int64
	ShouldSubmit    bool
}

func (store *Store) LoadRetrievalContext(ctx context.Context, workflowID, candidateID uuid.UUID) (RetrievalContext, error) {
	var result RetrievalContext
	var workflowText, clientText, indexerText string
	var revision int64
	err := store.Pool.QueryRow(ctx, `SELECT w.id::text,w.state,w.revision,w.variant,w.download_client_id::text,c.id::text,c.native_key,c.title,c.indexer_instance_id::text FROM workflows w JOIN release_candidates c ON c.workflow_id=w.id WHERE w.id=$1 AND c.id=$2 AND w.selected_release_id=c.id`, workflowID.String(), candidateID.String()).Scan(&workflowText, &result.Workflow.State, &revision, &result.Variant, &clientText, &workflowText, &result.NativeID, &result.ReleaseTitle, &indexerText)
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
	indexerID, _ := uuid.Parse(indexerText)
	result.Indexer, err = store.Integration(ctx, indexerID)
	if err != nil {
		return RetrievalContext{}, err
	}
	result.DownloadClient, err = store.Integration(ctx, result.Workflow.DownloadClientID)
	return result, err
}

func (store *Store) RecordReleaseRetrievalFailure(ctx context.Context, retrieval RetrievalContext, detail string, now time.Time) error {
	if strings.TrimSpace(detail) == "" {
		detail = "selected release NZB could not be retrieved"
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
		VALUES($1,$2,$3,$4,NULL,'release_retrieval',$5,$6)
		ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO NOTHING`, retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), indexerText, nativeKey, detail, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='needs_attention',revision=revision+1,updated_at=$2,attention_code='release_retrieval_failed',attention_detail=$3 WHERE id=$1`, retrieval.Workflow.ID.String(), now.UTC(), detail); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'needs_attention',$4,'worker',$5)`, retrieval.Workflow.ID.String(), revision+1, state, detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) BeginAltMountSubmission(ctx context.Context, retrieval RetrievalContext, now time.Time) (SubmissionIntent, error) {
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
	if generation >= maxSubtitleAcquisitionAttempts {
		var subtitleAttemptsExhausted bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_verifications WHERE workflow_id=$1 AND status='unverified')`, retrieval.Workflow.ID.String()).Scan(&subtitleAttemptsExhausted); err != nil {
			return SubmissionIntent{}, err
		}
		if subtitleAttemptsExhausted {
			return SubmissionIntent{}, errors.New("subtitle acquisition attempt limit reached")
		}
	}
	var current workflows.State
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, retrieval.Workflow.ID.String()).Scan(&current, &revision); err != nil {
		return SubmissionIntent{}, err
	}
	if current != workflows.StateCandidateSelected {
		return SubmissionIntent{}, ErrConcurrentModification
	}
	jobID := uuid.NewV7()
	filename = releaseSlug(retrieval.ReleaseTitle) + "--mm-" + jobID.String() + ".nzb"
	generation++
	if _, err := transaction.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision) VALUES($1,$2,$3,$4,$5,$6,'submitting',$7,1)`, jobID.String(), retrieval.Workflow.ID.String(), retrieval.CandidateID.String(), retrieval.DownloadClient.ID.String(), generation, filename, now.UTC()); err != nil {
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
	if err := transaction.QueryRow(ctx, `UPDATE altmount_jobs SET state=$2,external_queue_id=$3,submitted_at=CASE WHEN $3 IS NULL THEN submitted_at ELSE $4 END,last_reconciled_at=$4,revision=revision+1 WHERE id=$1 RETURNING revision`, jobID.String(), jobState, queueID, now.UTC()).Scan(&jobRevision); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,revision=revision+1,updated_at=$3 WHERE id=$1 AND revision=$4`, workflowID.String(), workflowState, now.UTC(), revision); err != nil {
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
