package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

type SearchContext struct {
	Workflow            workflows.Workflow
	Variant             string
	Media               catalog.MediaItem
	PolicyVersion       uint64
	Policy              json.RawMessage
	CurrentRelease      *releases.Observation
	CurrentLinkVersions string
	Indexers            []Integration
	Blocks              []ReleaseBlock
	Exclusions          []WorkflowReleaseExclusion
	OpenRound           *AcquisitionRound
}

type SearchIndexerOutcome struct {
	IndexerInstanceID uuid.UUID
	State             string
	RetryAt           *time.Time
	Detail            string
}

type SearchPersistenceOptions struct {
	RecoveryRescan   bool
	RescanGeneration int64
	IndexerOutcomes  []SearchIndexerOutcome
}

type WorkflowReleaseExclusion struct {
	CandidateID       uuid.UUID
	IndexerInstanceID uuid.UUID
	NativeKey         string
	FailureKind       string
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
	var currentObservation releases.Observation
	var currentObservationText, currentIndexerText, currentWorkflowText string
	var currentFacts []byte
	err = store.Pool.QueryRow(ctx, `SELECT observation.id::text,observation.indexer_instance_id::text,observation.native_guid,observation.title,observation.normalized_facts,current_workflow.id::text
		FROM library_link_versions version
		JOIN workflows current_workflow ON current_workflow.id=version.workflow_id
		JOIN release_candidates candidate ON candidate.id=current_workflow.selected_release_id
		JOIN release_observations observation ON observation.id=candidate.observation_id
		WHERE current_workflow.media_id=$1 AND current_workflow.variant=$2 AND version.state='current'
		ORDER BY version.verified_at DESC,version.id LIMIT 1`, result.Media.ID.String(), result.Variant).Scan(&currentObservationText, &currentIndexerText, &currentObservation.NativeGUID, &currentObservation.Title, &currentFacts, &currentWorkflowText)
	if err == nil {
		currentObservation.ID, _ = uuid.Parse(currentObservationText)
		currentObservation.IndexerID, _ = uuid.Parse(currentIndexerText)
		if err := json.Unmarshal(currentFacts, &currentObservation.Facts); err != nil {
			var legacyEmpty map[string]any
			if objectErr := json.Unmarshal(currentFacts, &legacyEmpty); objectErr != nil || len(legacyEmpty) != 0 {
				return SearchContext{}, err
			}
		}
		var observedLanguages []string
		languageErr := store.Pool.QueryRow(ctx, `SELECT observed_languages FROM subtitle_verifications WHERE workflow_id=$1 AND status='verified' ORDER BY attempt_generation DESC LIMIT 1`, currentWorkflowText).Scan(&observedLanguages)
		if languageErr != nil && !errors.Is(languageErr, pgx.ErrNoRows) {
			return SearchContext{}, languageErr
		}
		if len(observedLanguages) > 0 {
			currentObservation.Facts = upsertReleaseFact(currentObservation.Facts, releases.Fact{Name: "subtitle_languages", Values: observedLanguages, State: releases.FactObserved})
		}
		result.CurrentRelease = &currentObservation
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SearchContext{}, err
	}
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'')
		FROM workflows published JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
		WHERE published.media_id=$1 AND published.variant=$2`, result.Media.ID.String(), result.Variant).Scan(&result.CurrentLinkVersions); err != nil {
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
	rows, err := store.Pool.Query(ctx, `SELECT exclusion.candidate_id::text,exclusion.indexer_instance_id::text,exclusion.native_key,exclusion.failure_kind,exclusion.reason FROM workflow_release_exclusions exclusion WHERE exclusion.workflow_id=$1 ORDER BY exclusion.created_at`, result.Workflow.ID.String())
	if err != nil {
		return SearchContext{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item WorkflowReleaseExclusion
		var candidateText, indexerText string
		if err := rows.Scan(&candidateText, &indexerText, &item.NativeKey, &item.FailureKind, &item.Reason); err != nil {
			return SearchContext{}, err
		}
		item.CandidateID, _ = uuid.Parse(candidateText)
		item.IndexerInstanceID, _ = uuid.Parse(indexerText)
		result.Exclusions = append(result.Exclusions, item)
	}
	if err := rows.Err(); err != nil {
		return SearchContext{}, err
	}
	var round AcquisitionRound
	var roundText string
	var roundPolicyVersion int64
	err = store.Pool.QueryRow(ctx, `SELECT id::text,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions
		FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, result.Workflow.ID.String()).Scan(&roundText, &round.Ordinal, &round.State, &round.AttemptLimit, &round.NoveltyCutoffSequence, &roundPolicyVersion, &round.Policy, &round.UpgradeBaseline)
	if err == nil {
		round.ID, _ = uuid.Parse(roundText)
		round.PolicyVersion = uint64(roundPolicyVersion)
		result.OpenRound = &round
		result.PolicyVersion = round.PolicyVersion
		result.Policy = append(json.RawMessage(nil), round.Policy...)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SearchContext{}, err
	} else {
		err = store.Pool.QueryRow(ctx, `SELECT round.policy_version,round.policy_snapshot
			FROM workflow_retry_discovery discovery
			JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
			WHERE discovery.workflow_id=$1 AND discovery.state IN ('waiting','ready') AND round.state='exhausted'`, result.Workflow.ID.String()).Scan(&roundPolicyVersion, &result.Policy)
		if err == nil {
			result.PolicyVersion = uint64(roundPolicyVersion)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return SearchContext{}, err
		}
	}
	return result, nil
}

func upsertReleaseFact(facts []releases.Fact, replacement releases.Fact) []releases.Fact {
	result := append([]releases.Fact(nil), facts...)
	for index := range result {
		if strings.EqualFold(strings.TrimSpace(result[index].Name), replacement.Name) {
			result[index] = replacement
			return result
		}
	}
	return append(result, replacement)
}

func (store *Store) PersistSearch(ctx context.Context, search SearchContext, queryFingerprint []byte, evaluated []EvaluatedObservation, options SearchPersistenceOptions, now time.Time) (workflows.Workflow, error) {
	if !validSearchIndexerOutcomes(search.Indexers, options.IndexerOutcomes) {
		return workflows.Workflow{}, errors.New("search result does not cover its exact planned indexer set")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workflows.Workflow{}, err
	}
	defer transaction.Rollback(ctx)
	if options.RecoveryRescan {
		var discoveryState, roundState string
		var persistedGeneration int64
		var workflowState workflows.State
		err := transaction.QueryRow(ctx, `SELECT discovery.state,discovery.rescan_generation,round.state,workflow.state
			FROM workflow_retry_discovery discovery
			JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
			JOIN workflows workflow ON workflow.id=discovery.workflow_id
			WHERE discovery.workflow_id=$1
			AND NOT EXISTS(SELECT 1 FROM altmount_jobs job WHERE job.workflow_id=workflow.id AND job.state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested'))
			FOR UPDATE OF discovery,round,workflow`, search.Workflow.ID.String()).Scan(&discoveryState, &persistedGeneration, &roundState, &workflowState)
		if errors.Is(err, pgx.ErrNoRows) {
			return workflows.Workflow{}, ErrConcurrentModification
		}
		if err != nil {
			return workflows.Workflow{}, err
		}
		if discoveryState != "waiting" || persistedGeneration != options.RescanGeneration || roundState != "exhausted" || workflowState != workflows.StateWaiting {
			return workflows.Workflow{}, ErrConcurrentModification
		}
	} else {
		var roundText, roundBaseline string
		var roundPolicyVersion, currentRevision int64
		var currentState workflows.State
		err := transaction.QueryRow(ctx, `SELECT round.id::text,round.policy_version,round.upgrade_baseline_link_versions,
			workflow.state,workflow.revision
			FROM workflows workflow
			JOIN acquisition_rounds round ON round.workflow_id=workflow.id AND round.state='open'
			WHERE workflow.id=$1 FOR UPDATE OF workflow,round`, search.Workflow.ID.String()).Scan(&roundText, &roundPolicyVersion, &roundBaseline, &currentState, &currentRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			return workflows.Workflow{}, ErrConcurrentModification
		}
		if err != nil {
			return workflows.Workflow{}, err
		}
		if search.OpenRound == nil || roundText != search.OpenRound.ID.String() || uint64(roundPolicyVersion) != search.PolicyVersion || currentState != search.Workflow.State || uint64(currentRevision) != search.Workflow.Revision {
			return workflows.Workflow{}, ErrConcurrentModification
		}
		var currentBaseline string
		if err := transaction.QueryRow(ctx, `SELECT COALESCE(string_agg(version.id::text,',' ORDER BY version.id),'') FROM workflows requested
			LEFT JOIN workflows published ON published.media_id=requested.media_id AND published.variant=requested.variant
			LEFT JOIN library_link_versions version ON version.workflow_id=published.id AND version.state='current'
			WHERE requested.id=$1`, search.Workflow.ID.String()).Scan(&currentBaseline); err != nil {
			return workflows.Workflow{}, err
		}
		if roundBaseline != currentBaseline {
			return workflows.Workflow{}, ErrConcurrentModification
		}
	}
	runID := uuid.NewV7()
	mode, runState := "targeted", "partial"
	if searchRunComplete(options.IndexerOutcomes) {
		runState = "complete"
	}
	if options.RecoveryRescan {
		mode = "recovery"
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7)`, runID.String(), search.Workflow.ID.String(), mode, queryFingerprint, runState, int64(search.PolicyVersion), now.UTC()); err != nil {
		return workflows.Workflow{}, err
	}
	for _, outcome := range options.IndexerOutcomes {
		detail := strings.TrimSpace(outcome.Detail)
		if len(detail) > 300 {
			detail = detail[:300]
		}
		var completedAt any
		if outcome.State != "planned" {
			completedAt = now.UTC()
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO search_run_indexers(search_run_id,indexer_instance_id,state,retry_at,detail,completed_at) VALUES($1,$2,$3,$4,NULLIF($5,''),$6)`, runID.String(), outcome.IndexerInstanceID.String(), outcome.State, outcome.RetryAt, detail, completedAt); err != nil {
			return workflows.Workflow{}, err
		}
	}
	observations := make([]releases.Observation, 0, len(evaluated))
	evaluations := make([]releases.Evaluation, 0, len(evaluated))
	candidateByObservation := map[uuid.UUID]uuid.UUID{}
	candidateSequenceByObservation := map[uuid.UUID]int64{}
	for _, entry := range evaluated {
		observation := newznab.SanitizeObservation(entry.Observation)
		evaluation := newznab.SanitizeEvaluation(entry.Evaluation)
		sourceObservationID := observation.ID
		persistedObservationID := observation.ID
		rawFields, _ := json.Marshal(observation.Fields)
		facts, _ := json.Marshal(observation.Facts)
		problems, _ := json.Marshal(observation.Problems)
		bodyHash, decodeErr := decodeHex(observation.BodyHash)
		if decodeErr != nil {
			bodyHash = sha256Bytes([]byte(observation.BodyHash))
		}
		var unchangedObservationText string
		reuseErr := transaction.QueryRow(ctx, `SELECT previous.id::text
			FROM release_candidates candidate JOIN release_observations previous ON previous.id=candidate.observation_id
			WHERE candidate.workflow_id=$1 AND candidate.indexer_instance_id=$2 AND candidate.native_key=$3
			AND previous.operation_key=$4 AND previous.title=$5 AND previous.raw_fields=$6::jsonb
			AND previous.normalized_facts=$7::jsonb AND previous.parse_version=$8 AND previous.problems=$9::jsonb`, search.Workflow.ID.String(), observation.IndexerID.String(), observation.NativeGUID, observation.Operation, observation.Title, string(rawFields), string(facts), observation.ParseVersion, string(problems)).Scan(&unchangedObservationText)
		if reuseErr == nil {
			persistedObservationID, _ = uuid.Parse(unchangedObservationText)
		} else if errors.Is(reuseErr, pgx.ErrNoRows) {
			if _, err := transaction.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, observation.ID.String(), observation.IndexerID.String(), observation.Operation, observation.NativeGUID, observation.Title, bodyHash, string(rawFields), string(facts), observation.ParseVersion, string(problems), observation.ObservedAt); err != nil {
				return workflows.Workflow{}, err
			}
		} else {
			return workflows.Workflow{}, reuseErr
		}
		candidateID := uuid.NewV7()
		var returned string
		var firstSeenSequence int64
		locatorReference := newznab.DownloadLocatorReference(observation.DownloadURL, observation.NativeGUID)
		err = transaction.QueryRow(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at,first_observed_at,last_observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$9)
			ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO UPDATE SET search_run_id=EXCLUDED.search_run_id,observation_id=EXCLUDED.observation_id,title=EXCLUDED.title,download_locator_ref=EXCLUDED.download_locator_ref,last_observed_at=EXCLUDED.last_observed_at,observation_count=release_candidates.observation_count+1
			RETURNING id::text,first_seen_sequence`, candidateID.String(), search.Workflow.ID.String(), runID.String(), persistedObservationID.String(), observation.IndexerID.String(), observation.NativeGUID, observation.Title, locatorReference, now.UTC()).Scan(&returned, &firstSeenSequence)
		if err != nil {
			return workflows.Workflow{}, err
		}
		candidateID, _ = uuid.Parse(returned)
		candidateByObservation[sourceObservationID] = candidateID
		candidateSequenceByObservation[sourceObservationID] = firstSeenSequence
		reasons, _ := json.Marshal(evaluation.Explanations)
		if _, err := transaction.Exec(ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,$3,$4,$5,$6,'policy',$7) ON CONFLICT(workflow_id,candidate_id,policy_version) DO UPDATE SET verdict=EXCLUDED.verdict,score=EXCLUDED.score,reasons=EXCLUDED.reasons,decided_at=EXCLUDED.decided_at`, search.Workflow.ID.String(), candidateID.String(), int64(search.PolicyVersion), evaluation.Verdict, evaluation.Score, string(reasons), now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
		observations = append(observations, observation)
		evaluations = append(evaluations, evaluation)
	}
	ranked := releases.Rank(observations, evaluations)
	if options.RecoveryRescan {
		if err := store.persistRecoveryDiscovery(ctx, transaction, search, ranked, candidateByObservation, candidateSequenceByObservation, options, now); err != nil {
			return workflows.Workflow{}, err
		}
		if err := transaction.Commit(ctx); err != nil {
			return workflows.Workflow{}, err
		}
		return search.Workflow, nil
	}
	to := workflows.StateWaiting
	var selected *uuid.UUID
	for _, evaluation := range ranked {
		if evaluation.Verdict == releases.VerdictEligible {
			candidate := candidateByObservation[evaluation.ObservationID]
			var alreadyAttempted bool
			if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_attempted_releases WHERE workflow_id=$1 AND candidate_id=$2)`, search.Workflow.ID.String(), candidate.String()).Scan(&alreadyAttempted); err != nil {
				return workflows.Workflow{}, err
			}
			if alreadyAttempted {
				continue
			}
			if search.OpenRound != nil && search.OpenRound.Ordinal > 1 && candidateSequenceByObservation[evaluation.ObservationID] <= search.OpenRound.NoveltyCutoffSequence {
				continue
			}
			selected = &candidate
			to = workflows.StateCandidateSelected
			break
		}
		if evaluation.Verdict == releases.VerdictHeld {
			to = workflows.StateHeld
		}
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
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state=$2,selected_release_id=$3,revision=$4,updated_at=$5,attention_code=$7,attention_detail=$8 WHERE id=$1 AND revision=$6`, search.Workflow.ID.String(), to, selectedValue, newRevision, now.UTC(), revision, attentionCode, attentionDetail); err != nil {
		return workflows.Workflow{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,$4,'targeted search evaluated','policy',$5)`, search.Workflow.ID.String(), newRevision, currentState, to, now.UTC()); err != nil {
		return workflows.Workflow{}, err
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
		nextSearchAt := now.UTC().Add(30 * time.Minute)
		var priorSubmissions int
		if err := transaction.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, search.Workflow.ID.String()).Scan(&priorSubmissions); err != nil {
			return workflows.Workflow{}, err
		}
		if priorSubmissions > 0 {
			nextSearchAt = retryDiscoveryAt(search.Workflow.ID, int64(newRevision), 0, now)
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), search.Workflow.ID.String(), newRevision, string(payload), now.UTC(), nextSearchAt); err != nil {
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

func (store *Store) persistRecoveryDiscovery(ctx context.Context, transaction pgx.Tx, search SearchContext, ranked []releases.Evaluation, candidates map[uuid.UUID]uuid.UUID, sequences map[uuid.UUID]int64, options SearchPersistenceOptions, now time.Time) error {
	var exhaustedRoundText, state string
	var generation, cutoff, completeNoNew int64
	err := transaction.QueryRow(ctx, `SELECT discovery.exhausted_round_id::text,discovery.state,discovery.rescan_generation,round.novelty_cutoff_sequence,discovery.complete_no_new_scans
		FROM workflow_retry_discovery discovery
		JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
		WHERE discovery.workflow_id=$1 FOR UPDATE OF discovery,round`, search.Workflow.ID.String()).Scan(&exhaustedRoundText, &state, &generation, &cutoff, &completeNoNew)
	if errors.Is(err, pgx.ErrNoRows) || state != "waiting" || generation != options.RescanGeneration {
		return nil
	}
	if err != nil {
		return err
	}
	if search.Workflow.State != workflows.StateWaiting {
		return ErrConcurrentModification
	}
	var readyCandidate *uuid.UUID
	var readySequence int64
	for _, evaluation := range ranked {
		if evaluation.Verdict != releases.VerdictEligible {
			continue
		}
		candidate := candidates[evaluation.ObservationID]
		sequence := sequences[evaluation.ObservationID]
		if sequence <= cutoff {
			continue
		}
		var disqualified bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM workflow_attempted_releases attempted WHERE attempted.workflow_id=$1 AND attempted.candidate_id=$2
			UNION ALL
			SELECT 1 FROM workflow_release_exclusions exclusion JOIN release_candidates candidate ON candidate.workflow_id=exclusion.workflow_id AND candidate.indexer_instance_id=exclusion.indexer_instance_id AND candidate.native_key=exclusion.native_key WHERE exclusion.workflow_id=$1 AND candidate.id=$2
		)`, search.Workflow.ID.String(), candidate.String()).Scan(&disqualified); err != nil {
			return err
		}
		if disqualified {
			continue
		}
		readyCandidate, readySequence = &candidate, sequence
		break
	}
	if readyCandidate != nil {
		command, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET state='ready',next_rescan_at=NULL,last_scan_at=$2,last_scan_complete=$3,complete_no_new_scans=0,pending_indexer_ids=NULL,ready_candidate_id=$4,ready_first_seen_sequence=$5,updated_at=$2
			WHERE workflow_id=$1 AND state='waiting' AND rescan_generation=$6`, search.Workflow.ID.String(), now.UTC(), searchRunComplete(options.IndexerOutcomes), readyCandidate.String(), readySequence, generation)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrConcurrentModification
		}
		return nil
	}
	complete := searchRunComplete(options.IndexerOutcomes)
	pendingIndexerIDs := []string{}
	for _, outcome := range options.IndexerOutcomes {
		if outcome.State != "succeeded" {
			pendingIndexerIDs = append(pendingIndexerIDs, outcome.IndexerInstanceID.String())
		}
	}
	if complete {
		completeNoNew++
		pendingIndexerIDs = nil
	}
	nextGeneration := generation + 1
	next := retryDiscoveryAt(search.Workflow.ID, nextGeneration, int(completeNoNew), now)
	if !complete {
		var earliestRetryAt *time.Time
		for _, outcome := range options.IndexerOutcomes {
			if outcome.State == "succeeded" || outcome.RetryAt == nil || !outcome.RetryAt.After(now) {
				continue
			}
			scheduled := outcome.RetryAt.UTC()
			if earliestRetryAt == nil || scheduled.Before(*earliestRetryAt) {
				earliestRetryAt = &scheduled
			}
		}
		if earliestRetryAt != nil {
			next = *earliestRetryAt
		}
	}
	command, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET rescan_generation=$2,next_rescan_at=$3,last_scan_at=$4,last_scan_complete=$5,complete_no_new_scans=$6,pending_indexer_ids=$7,updated_at=$4
		WHERE workflow_id=$1 AND state='waiting' AND rescan_generation=$8`, search.Workflow.ID.String(), nextGeneration, next.UTC(), now.UTC(), complete, completeNoNew, pendingIndexerIDs, generation)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConcurrentModification
	}
	payload, _ := json.Marshal(map[string]any{"workflow_id": search.Workflow.ID.String(), "rescan_generation": nextGeneration})
	_, err = transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow_retry_discovery',$2,$3,'workflow.discovery_rescan_requested',$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), search.Workflow.ID.String(), nextGeneration, payload, now.UTC(), next.UTC())
	return err
}

func searchRunComplete(outcomes []SearchIndexerOutcome) bool {
	if len(outcomes) == 0 {
		return false
	}
	for _, outcome := range outcomes {
		if outcome.State != "succeeded" {
			return false
		}
	}
	return true
}

func validSearchIndexerOutcomes(indexers []Integration, outcomes []SearchIndexerOutcome) bool {
	if len(indexers) == 0 || len(indexers) != len(outcomes) {
		return false
	}
	planned := make(map[uuid.UUID]bool, len(indexers))
	for _, integration := range indexers {
		if integration.ID == (uuid.UUID{}) || planned[integration.ID] {
			return false
		}
		planned[integration.ID] = true
	}
	seen := make(map[uuid.UUID]bool, len(outcomes))
	for _, outcome := range outcomes {
		if !planned[outcome.IndexerInstanceID] || seen[outcome.IndexerInstanceID] {
			return false
		}
		switch outcome.State {
		case "succeeded", "deferred", "failed":
		default:
			return false
		}
		seen[outcome.IndexerInstanceID] = true
	}
	return true
}

func sha256Bytes(value []byte) []byte        { digest := sha256.Sum256(value); return digest[:] }
func decodeHex(value string) ([]byte, error) { return hex.DecodeString(value) }
