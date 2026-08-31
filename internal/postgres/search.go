package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

type SearchContext struct {
	Workflow      workflows.Workflow
	Variant       string
	Media         catalog.MediaItem
	PolicyVersion uint64
	Policy        json.RawMessage
	Indexers      []Integration
	Blocks        []ReleaseBlock
	Exclusions    []WorkflowReleaseExclusion
	SubtitleRetry bool
}

type WorkflowReleaseExclusion struct {
	CandidateID       uuid.UUID
	IndexerInstanceID uuid.UUID
	NativeKey         string
	Reason            string
}

func (item WorkflowReleaseExclusion) Matches(indexerID uuid.UUID, nativeKey string) bool {
	return item.IndexerInstanceID == indexerID && item.NativeKey == nativeKey
}

type EvaluatedObservation struct {
	Observation releases.Observation
	Evaluation  releases.Evaluation
}

func (store *Store) LoadSearchContext(ctx context.Context, workflowID uuid.UUID) (SearchContext, error) {
	var result SearchContext
	var workflowText, mediaText, rootText, profileText string
	var selectedText, attemptText, clientText *string
	var workflowRevision, mediaRevision, policyVersion int64
	var year *int16
	var policy []byte
	err := store.Pool.QueryRow(ctx, `SELECT w.id::text,w.media_id::text,w.state,w.root_id::text,w.quality_profile_id::text,w.download_client_id::text,w.selected_release_id::text,w.grab_attempt_id::text,w.revision,w.created_at,w.updated_at,w.variant,m.kind,m.title,m.year,m.revision,m.created_at,m.updated_at,p.policy_version,p.policy FROM workflows w JOIN media_items m ON m.id=w.media_id JOIN quality_profiles p ON p.id=w.quality_profile_id WHERE w.id=$1`, workflowID.String()).Scan(&workflowText, &mediaText, &result.Workflow.State, &rootText, &profileText, &clientText, &selectedText, &attemptText, &workflowRevision, &result.Workflow.CreatedAt, &result.Workflow.UpdatedAt, &result.Variant, &result.Media.Kind, &result.Media.Title, &year, &mediaRevision, &result.Media.CreatedAt, &result.Media.UpdatedAt, &policyVersion, &policy)
	if errors.Is(err, pgx.ErrNoRows) {
		return SearchContext{}, ErrNotFound
	}
	if err != nil {
		return SearchContext{}, err
	}
	result.Workflow.ID, _ = uuid.Parse(workflowText)
	result.Workflow.MediaID, _ = uuid.Parse(mediaText)
	result.Workflow.RootID, _ = uuid.Parse(rootText)
	result.Workflow.QualityProfileID, _ = uuid.Parse(profileText)
	result.Workflow.Revision = uint64(workflowRevision)
	if clientText != nil {
		result.Workflow.DownloadClientID, _ = uuid.Parse(*clientText)
	}
	if selectedText != nil {
		parsed, parseErr := uuid.Parse(*selectedText)
		if parseErr != nil {
			return SearchContext{}, parseErr
		}
		result.Workflow.SelectedRelease = &parsed
	}
	if attemptText != nil {
		parsed, parseErr := uuid.Parse(*attemptText)
		if parseErr != nil {
			return SearchContext{}, parseErr
		}
		result.Workflow.GrabAttemptID = &parsed
	}
	result.Media.ID = result.Workflow.MediaID
	result.Media.Revision = uint64(mediaRevision)
	if year != nil {
		converted := uint16(*year)
		result.Media.Year = &converted
	}
	result.PolicyVersion = uint64(policyVersion)
	result.Policy = append(json.RawMessage(nil), policy...)
	result.Media.Identities, err = store.identities(ctx, result.Media.ID)
	if err != nil {
		return SearchContext{}, err
	}
	all, err := store.ListIntegrations(ctx)
	if err != nil {
		return SearchContext{}, err
	}
	for _, integration := range all {
		var config struct {
			ModeVerified bool `json:"mode_verified"`
		}
		if integration.Kind == "newznab" && integration.Enabled && json.Unmarshal(integration.Config, &config) == nil && config.ModeVerified {
			result.Indexers = append(result.Indexers, integration)
		}
	}
	result.Blocks, err = store.ListActiveReleaseBlocks(ctx, result.Media.ID, result.Variant, time.Now().UTC())
	if err != nil {
		return SearchContext{}, err
	}
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_verifications WHERE workflow_id=$1 AND retry_state='queued')`, result.Workflow.ID.String()).Scan(&result.SubtitleRetry); err != nil {
		return SearchContext{}, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT exclusion.candidate_id::text,exclusion.indexer_instance_id::text,exclusion.native_key,exclusion.reason FROM workflow_release_exclusions exclusion WHERE exclusion.workflow_id=$1 ORDER BY exclusion.created_at`, result.Workflow.ID.String())
	if err != nil {
		return SearchContext{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item WorkflowReleaseExclusion
		var candidateText, indexerText string
		if err := rows.Scan(&candidateText, &indexerText, &item.NativeKey, &item.Reason); err != nil {
			return SearchContext{}, err
		}
		item.CandidateID, _ = uuid.Parse(candidateText)
		item.IndexerInstanceID, _ = uuid.Parse(indexerText)
		result.Exclusions = append(result.Exclusions, item)
	}
	if err := rows.Err(); err != nil {
		return SearchContext{}, err
	}
	return result, nil
}

func (store *Store) PersistSearch(ctx context.Context, search SearchContext, queryFingerprint []byte, evaluated []EvaluatedObservation, now time.Time) (workflows.Workflow, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workflows.Workflow{}, err
	}
	defer transaction.Rollback(ctx)
	runID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',$4,$5,$5)`, runID.String(), search.Workflow.ID.String(), queryFingerprint, int64(search.PolicyVersion), now.UTC()); err != nil {
		return workflows.Workflow{}, err
	}
	observations := make([]releases.Observation, 0, len(evaluated))
	evaluations := make([]releases.Evaluation, 0, len(evaluated))
	candidateByObservation := map[uuid.UUID]uuid.UUID{}
	for _, entry := range evaluated {
		observation := entry.Observation
		rawFields, _ := json.Marshal(observation.Fields)
		facts, _ := json.Marshal(observation.Facts)
		problems, _ := json.Marshal(observation.Problems)
		bodyHash, decodeErr := decodeHex(observation.BodyHash)
		if decodeErr != nil {
			bodyHash = sha256Bytes([]byte(observation.BodyHash))
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, observation.ID.String(), observation.IndexerID.String(), observation.Operation, observation.NativeGUID, observation.Title, bodyHash, string(rawFields), string(facts), observation.ParseVersion, string(problems), observation.ObservedAt); err != nil {
			return workflows.Workflow{}, err
		}
		candidateID := uuid.NewV7()
		var returned string
		err = transaction.QueryRow(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO UPDATE SET search_run_id=EXCLUDED.search_run_id,observation_id=EXCLUDED.observation_id,title=EXCLUDED.title,created_at=EXCLUDED.created_at RETURNING id::text`, candidateID.String(), search.Workflow.ID.String(), runID.String(), observation.ID.String(), observation.IndexerID.String(), observation.NativeGUID, observation.Title, "native:"+observation.NativeGUID, now.UTC()).Scan(&returned)
		if err != nil {
			return workflows.Workflow{}, err
		}
		candidateID, _ = uuid.Parse(returned)
		candidateByObservation[observation.ID] = candidateID
		reasons, _ := json.Marshal(entry.Evaluation.Explanations)
		if _, err := transaction.Exec(ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,$3,$4,$5,$6,'policy',$7) ON CONFLICT(workflow_id,candidate_id,policy_version) DO UPDATE SET verdict=EXCLUDED.verdict,score=EXCLUDED.score,reasons=EXCLUDED.reasons,decided_at=EXCLUDED.decided_at`, search.Workflow.ID.String(), candidateID.String(), int64(search.PolicyVersion), entry.Evaluation.Verdict, entry.Evaluation.Score, string(reasons), now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
		observations = append(observations, observation)
		evaluations = append(evaluations, entry.Evaluation)
	}
	ranked := releases.Rank(observations, evaluations)
	to := workflows.StateWaiting
	var selected *uuid.UUID
	for _, evaluation := range ranked {
		if evaluation.Verdict == releases.VerdictEligible {
			candidate := candidateByObservation[evaluation.ObservationID]
			selected = &candidate
			to = workflows.StateCandidateSelected
			break
		}
		if evaluation.Verdict == releases.VerdictHeld {
			to = workflows.StateHeld
		}
	}
	if selected == nil && search.SubtitleRetry {
		to = workflows.StateNeedsAttention
	}
	var currentState workflows.State
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, search.Workflow.ID.String()).Scan(&currentState, &revision); err != nil {
		return workflows.Workflow{}, err
	}
	if currentState != workflows.StateSearching {
		return workflows.Workflow{}, ErrConcurrentModification
	}
	if _, allowed := map[workflows.State]bool{workflows.StateCandidateSelected: true, workflows.StateWaiting: true, workflows.StateHeld: true, workflows.StateNeedsAttention: true}[to]; !allowed {
		return workflows.Workflow{}, workflows.ErrInvalidTransition
	}
	newRevision := revision + 1
	var selectedValue any
	if selected != nil {
		selectedValue = selected.String()
	}
	var attentionCode, attentionDetail any
	if to == workflows.StateNeedsAttention {
		attentionCode = "subtitle_retry_no_alternate"
		attentionDetail = "The subtitle-unverified release was excluded from this workflow, and the one permitted alternate search found no eligible release."
	}
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,selected_release_id=$3,revision=$4,updated_at=$5,attention_code=$7,attention_detail=$8 WHERE id=$1 AND revision=$6`, search.Workflow.ID.String(), to, selectedValue, newRevision, now.UTC(), revision, attentionCode, attentionDetail); err != nil {
		return workflows.Workflow{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,'targeted search evaluated','policy',$5)`, search.Workflow.ID.String(), newRevision, currentState, to, now.UTC()); err != nil {
		return workflows.Workflow{}, err
	}
	if to == workflows.StateNeedsAttention && search.SubtitleRetry {
		if _, err := transaction.Exec(ctx, `UPDATE subtitle_verifications SET retry_state='no_alternate' WHERE workflow_id=$1 AND retry_state='queued'`, search.Workflow.ID.String()); err != nil {
			return workflows.Workflow{}, err
		}
	}
	if selected != nil {
		if _, err := transaction.Exec(ctx, `INSERT INTO candidate_selections(workflow_id,candidate_id,selected_at,actor,reason) VALUES($1,$2,$3,'policy','highest eligible evidence score') ON CONFLICT(workflow_id) DO UPDATE SET candidate_id=EXCLUDED.candidate_id,selected_at=EXCLUDED.selected_at,actor=EXCLUDED.actor,reason=EXCLUDED.reason`, search.Workflow.ID.String(), selected.String(), now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
		payload, _ := json.Marshal(map[string]string{"workflow_id": search.Workflow.ID.String(), "candidate_id": selected.String()})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'release.retrieve_requested',$4,$5,$5) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), search.Workflow.ID.String(), newRevision, string(payload), now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
	} else if to == workflows.StateWaiting {
		payload, _ := json.Marshal(map[string]string{"workflow_id": search.Workflow.ID.String(), "reason": "no eligible release yet"})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), search.Workflow.ID.String(), newRevision, string(payload), now.UTC(), now.UTC().Add(30*time.Minute)); err != nil {
			return workflows.Workflow{}, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return workflows.Workflow{}, err
	}
	search.Workflow.State = to
	search.Workflow.Revision = uint64(newRevision)
	search.Workflow.SelectedRelease = selected
	search.Workflow.UpdatedAt = now.UTC()
	return search.Workflow, nil
}

func sha256Bytes(value []byte) []byte        { digest := sha256.Sum256(value); return digest[:] }
func decodeHex(value string) ([]byte, error) { return hex.DecodeString(value) }
