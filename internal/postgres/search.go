package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
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
	// SearchRoundID is the open round for targeted searches or the exhausted
	// discovery round for recovery. It scopes durable partial-retry evidence.
	SearchRoundID uuid.UUID
	// Complete searches rotate query titles; partial coverage retries keep the
	// same title instead of skipping an alias before every provider saw it.
	CompleteSearches uint64
	EpisodeNumbering packs.EpisodeNumbering
	// ProfilePolicyVersion is the quality profile's current version. It can
	// differ from PolicyVersion, which follows the open round's snapshot.
	ProfilePolicyVersion uint64
	// RepairBrokenTitles are release titles whose published files lost their
	// Usenet articles; this workflow acquires their replacement (AltMount
	// repair). Search rejects these exact titles and does not require the
	// replacement to improve on the broken publication.
	RepairBrokenTitles []string
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
	// PartialRetry is the durable chain for this search. It is nil for direct
	// repository callers that do not have a search round/intent.
	PartialRetry *SearchPartialRetry
	// ReusedIndexerIDs marks completed providers whose evidence was loaded
	// instead of queried during this attempt.
	ReusedIndexerIDs map[uuid.UUID]bool
}

type WorkflowReleaseExclusion struct {
	CandidateID        uuid.UUID
	IndexerInstanceID  uuid.UUID
	NativeKey          string
	FailureKind        string
	Reason             string
	ReleaseTitle       string
	SubtitleUnverified bool
}

func (item WorkflowReleaseExclusion) Matches(indexerID uuid.UUID, nativeKey string) bool {
	return item.IndexerInstanceID == indexerID && item.NativeKey == nativeKey
}

// A completed subtitle inspection also applies to exact-title cross-posts.
// Retrieval and infrastructure failures retain their provider/native scope.
func (item WorkflowReleaseExclusion) MatchesTitle(title string) bool {
	return (item.FailureKind == "subtitle_verification" || item.FailureKind == "movie_primary_ambiguous") && item.SubtitleUnverified &&
		strings.TrimSpace(item.ReleaseTitle) != "" && strings.EqualFold(strings.TrimSpace(item.ReleaseTitle), strings.TrimSpace(title))
}

type EvaluatedObservation struct {
	Observation releases.Observation
	Evaluation  releases.Evaluation
	// ProviderResult is true only for observations returned by a provider in
	// this execution. Held-candidate re-evaluations must never be recorded as
	// fresh provider coverage.
	ProviderResult bool
	// Reused is true for evidence carried from an earlier successful provider
	// in the same partial-retry chain. Reused evidence must not move the
	// candidate's observation pointer or inflate its observation count.
	Reused bool
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
	result.ProfilePolicyVersion = uint64(policyVersion)
	result.Policy = append(json.RawMessage(nil), policy...)
	result.Media.Identities, err = store.identities(ctx, result.Media.ID)
	if err != nil {
		return SearchContext{}, err
	}
	result.Media.Metadata, err = store.metadata(ctx, result.Media.ID)
	if err != nil {
		return SearchContext{}, err
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM search_runs WHERE workflow_id=$1 AND state='complete'`, workflowID.String()).Scan(&result.CompleteSearches); err != nil {
		return SearchContext{}, err
	}
	if result.Media.Kind == catalog.KindSeries {
		result.EpisodeNumbering, err = store.WorkflowEpisodeNumbering(ctx, workflowID)
		if err != nil {
			return SearchContext{}, err
		}
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
		var observedLanguages, audioLanguages []string
		languageErr := store.Pool.QueryRow(ctx, `SELECT observed_languages,
			COALESCE((SELECT array_agg(DISTINCT track->>'canonical_language') FROM jsonb_array_elements(COALESCE(evidence->'videos','[]'::jsonb)) video,
				jsonb_array_elements(COALESCE(video->'media_info'->'audio_tracks','[]'::jsonb)) track WHERE COALESCE(track->>'canonical_language','')<>''),'{}')
			FROM subtitle_verifications WHERE workflow_id=$1 AND status='verified' ORDER BY attempt_generation DESC LIMIT 1`, currentWorkflowText).Scan(&observedLanguages, &audioLanguages)
		if languageErr != nil && !errors.Is(languageErr, pgx.ErrNoRows) {
			return SearchContext{}, languageErr
		}
		if languageErr == nil {
			// Verified output inspection is the truth about the published
			// release, including "no subtitles": it replaces any title-inferred
			// language so a newly required language is judged as missing.
			if len(observedLanguages) == 0 {
				observedLanguages = []string{releases.NoSubtitleLanguageObserved}
			}
			currentObservation.Facts = upsertReleaseFact(currentObservation.Facts, releases.Fact{Name: "subtitle_languages", Values: observedLanguages, State: releases.FactObserved})
			if len(audioLanguages) > 0 {
				currentObservation.Facts = upsertReleaseFact(currentObservation.Facts, releases.Fact{Name: "audio_languages", Values: audioLanguages, State: releases.FactObserved})
			}
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
		if (integration.Kind == "newznab" || integration.Kind == "oldboys" && OldBoysMediaAllowed(result.Media)) && integration.Enabled && json.Unmarshal(integration.Config, &config) == nil && config.ModeVerified {
			result.Indexers = append(result.Indexers, integration)
		}
	}
	result.Blocks, err = store.ListActiveReleaseBlocks(ctx, result.Media.ID, result.Variant, time.Now().UTC())
	if err != nil {
		return SearchContext{}, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT exclusion.candidate_id::text,exclusion.indexer_instance_id::text,exclusion.native_key,exclusion.failure_kind,exclusion.reason,candidate.title,COALESCE(verification.status='unverified',false)
		FROM workflow_release_exclusions exclusion JOIN release_candidates candidate ON candidate.id=exclusion.candidate_id AND candidate.workflow_id=exclusion.workflow_id
		LEFT JOIN subtitle_verifications verification ON
		 (verification.id=exclusion.verification_id OR (exclusion.failure_kind='movie_primary_ambiguous' AND verification.candidate_id=candidate.id))
		 AND verification.workflow_id=exclusion.workflow_id
		WHERE exclusion.workflow_id=$1 ORDER BY exclusion.created_at`, result.Workflow.ID.String())
	if err != nil {
		return SearchContext{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item WorkflowReleaseExclusion
		var candidateText, indexerText string
		if err := rows.Scan(&candidateText, &indexerText, &item.NativeKey, &item.FailureKind, &item.Reason, &item.ReleaseTitle, &item.SubtitleUnverified); err != nil {
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
		result.SearchRoundID = round.ID
		result.PolicyVersion = round.PolicyVersion
		result.Policy = append(json.RawMessage(nil), round.Policy...)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SearchContext{}, err
	} else {
		var discoveryRoundText string
		err = store.Pool.QueryRow(ctx, `SELECT round.id::text,round.policy_version,round.policy_snapshot
			FROM workflow_retry_discovery discovery
			JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
			WHERE discovery.workflow_id=$1 AND discovery.state IN ('waiting','ready') AND round.state='exhausted'`, result.Workflow.ID.String()).Scan(&discoveryRoundText, &roundPolicyVersion, &result.Policy)
		if err == nil {
			result.SearchRoundID, _ = uuid.Parse(discoveryRoundText)
			result.PolicyVersion = uint64(roundPolicyVersion)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return SearchContext{}, err
		}
	}
	if result.RepairBrokenTitles, err = store.RepairBrokenReleaseTitles(ctx, result.Workflow.ID); err != nil {
		return SearchContext{}, err
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

// insertReleaseObservation stores one immutable observation snapshot. The
// caller normally supplies a fresh UUID, but a held candidate can be re-scored
// after its frozen episode mapping changes: its existing observation ID then
// belongs to the previous facts. Never overwrite that evidence and never retry
// the same primary key. If the supplied ID is taken, allocate a new version and
// let the candidate point at it. The unchanged-content path above already
// reuses the candidate's current observation, so repeating a search with the
// same facts stays idempotent.
func insertReleaseObservation(ctx context.Context, transaction pgx.Tx, observation releases.Observation, bodyHash, rawFields, facts, problems []byte) (uuid.UUID, error) {
	for attempt := 0; attempt < 3; attempt++ {
		id := observation.ID
		if attempt > 0 {
			id = uuid.NewV7()
		}
		command, err := transaction.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (id) DO NOTHING`, id.String(), observation.IndexerID.String(), observation.Operation, observation.NativeGUID, observation.Title, bodyHash, string(rawFields), string(facts), observation.ParseVersion, string(problems), observation.ObservedAt)
		if err != nil {
			return uuid.UUID{}, err
		}
		if command.RowsAffected() == 1 {
			return id, nil
		}
	}
	return uuid.UUID{}, errors.New("could not allocate a new release observation version")
}

func (store *Store) PersistSearch(ctx context.Context, search SearchContext, queryFingerprint []byte, evaluated []EvaluatedObservation, options SearchPersistenceOptions, now time.Time) (workflows.Workflow, error) {
	if !validSearchIndexerOutcomes(search.Indexers, options.IndexerOutcomes) {
		return workflows.Workflow{}, errors.New("search result does not cover its exact planned indexer set")
	}
	if options.PartialRetry != nil {
		planned := make([]uuid.UUID, 0, len(search.Indexers))
		for _, integration := range search.Indexers {
			planned = append(planned, integration.ID)
		}
		if options.PartialRetry.WorkflowID != search.Workflow.ID || options.PartialRetry.RoundID != search.SearchRoundID || !sameIndexerIDSet(options.PartialRetry.PlannedIndexerIDs, planned) {
			return workflows.Workflow{}, ErrSearchPartialRetryInvalid
		}
		plannedSet := make(map[uuid.UUID]bool, len(planned))
		for _, id := range planned {
			plannedSet[id] = true
		}
		for id := range options.PartialRetry.CompletedIndexers {
			if !plannedSet[id] {
				return workflows.Workflow{}, ErrSearchPartialRetryInvalid
			}
		}
		for id := range options.ReusedIndexerIDs {
			if !plannedSet[id] {
				return workflows.Workflow{}, ErrSearchPartialRetryInvalid
			}
		}
		expectedMode := "targeted"
		if options.RecoveryRescan {
			expectedMode = "recovery"
		}
		if options.PartialRetry.Mode != expectedMode {
			return workflows.Workflow{}, ErrSearchPartialRetryInvalid
		}
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workflows.Workflow{}, err
	}
	defer transaction.Rollback(ctx)
	// Native results are admissible only for the exact commission/credential
	// snapshot that searched. Lock credentials before integrations, matching
	// credential replacement and commissioning lock order.
	for _, integration := range search.Indexers {
		if integration.Kind == "oldboys" {
			var stamp time.Time
			if err := transaction.QueryRow(ctx, `SELECT updated_at FROM oldboys_credentials WHERE singleton=true FOR SHARE`).Scan(&stamp); err != nil {
				return workflows.Workflow{}, err
			}
			var enabled bool
			var config []byte
			if err := transaction.QueryRow(ctx, `SELECT enabled,config FROM integration_instances WHERE id=$1 AND kind='oldboys' FOR SHARE`, integration.ID.String()).Scan(&enabled, &config); err != nil {
				return workflows.Workflow{}, err
			}
			var expected, actual map[string]any
			if json.Unmarshal(integration.Config, &expected) != nil || json.Unmarshal(config, &actual) != nil {
				return workflows.Workflow{}, ErrConcurrentModification
			}
			version, _ := actual["credentials_updated_at"].(string)
			configuredAt, parseErr := time.Parse(time.RFC3339Nano, version)
			if !enabled || parseErr != nil || !stamp.Equal(configuredAt) || !reflect.DeepEqual(expected, actual) {
				return workflows.Workflow{}, ErrConcurrentModification
			}
		}
	}
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
	if options.PartialRetry != nil && len(options.PartialRetry.CompletedIndexers) > 0 {
		if err := store.validateSearchPartialEvidence(ctx, transaction, *options.PartialRetry); err != nil {
			return workflows.Workflow{}, err
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
	freshEvidence := map[uuid.UUID][]SearchCandidateEvidence{}
	for _, entry := range evaluated {
		observation := newznab.SanitizeObservation(entry.Observation)
		evaluation := newznab.SanitizeEvaluation(entry.Evaluation)
		sourceObservationID := observation.ID
		rawFields, _ := json.Marshal(observation.Fields)
		facts, _ := json.Marshal(observation.Facts)
		problems, _ := json.Marshal(observation.Problems)
		bodyHash, decodeErr := decodeHex(observation.BodyHash)
		if decodeErr != nil {
			bodyHash = sha256Bytes([]byte(observation.BodyHash))
		}
		var persistedObservationID uuid.UUID
		reusedID, reused, reuseErr := store.reusableObservation(ctx, transaction, search.Workflow.ID, observation.IndexerID, observation.NativeGUID, observation)
		if reuseErr != nil {
			return workflows.Workflow{}, reuseErr
		}
		if reused {
			persistedObservationID = reusedID
		} else {
			persistedObservationID, err = insertReleaseObservation(ctx, transaction, observation, bodyHash, rawFields, facts, problems)
			if err != nil {
				return workflows.Workflow{}, err
			}
		}
		candidateID := uuid.NewV7()
		var returned string
		var firstSeenSequence int64
		locatorReference := newznab.DownloadLocatorReference(observation.DownloadURL, observation.NativeGUID)
		err = transaction.QueryRow(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at,first_observed_at,last_observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$9)
			ON CONFLICT(workflow_id,indexer_instance_id,native_key) DO UPDATE SET
				search_run_id=CASE WHEN $10 THEN release_candidates.search_run_id ELSE EXCLUDED.search_run_id END,
				observation_id=CASE WHEN $10 THEN release_candidates.observation_id ELSE EXCLUDED.observation_id END,
				title=CASE WHEN $10 THEN release_candidates.title ELSE EXCLUDED.title END,
				download_locator_ref=CASE WHEN $10 THEN release_candidates.download_locator_ref ELSE EXCLUDED.download_locator_ref END,
				last_observed_at=CASE WHEN $10 THEN release_candidates.last_observed_at ELSE GREATEST(release_candidates.last_observed_at,EXCLUDED.last_observed_at) END,
				observation_count=CASE WHEN $10 THEN release_candidates.observation_count ELSE release_candidates.observation_count+1 END
			RETURNING id::text,first_seen_sequence`, candidateID.String(), search.Workflow.ID.String(), runID.String(), persistedObservationID.String(), observation.IndexerID.String(), observation.NativeGUID, observation.Title, locatorReference, now.UTC(), entry.Reused).Scan(&returned, &firstSeenSequence)
		if err != nil {
			return workflows.Workflow{}, err
		}
		candidateID, _ = uuid.Parse(returned)
		candidateByObservation[sourceObservationID] = candidateID
		candidateSequenceByObservation[sourceObservationID] = firstSeenSequence
		if entry.ProviderResult {
			freshEvidence[observation.IndexerID] = appendUniqueCandidateEvidence(freshEvidence[observation.IndexerID], SearchCandidateEvidence{CandidateID: candidateID, ObservationID: persistedObservationID})
		}
		reasons, _ := json.Marshal(evaluation.Explanations)
		if _, err := transaction.Exec(ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,$3,$4,$5,$6,'policy',$7) ON CONFLICT(workflow_id,candidate_id,policy_version) DO UPDATE SET verdict=EXCLUDED.verdict,score=EXCLUDED.score,reasons=EXCLUDED.reasons,decided_at=EXCLUDED.decided_at`, search.Workflow.ID.String(), candidateID.String(), int64(search.PolicyVersion), evaluation.Verdict, evaluation.Score, string(reasons), now.UTC()); err != nil {
			return workflows.Workflow{}, err
		}
		observations = append(observations, observation)
		evaluations = append(evaluations, evaluation)
	}
	nextCompleted := map[uuid.UUID][]SearchCandidateEvidence{}
	if options.PartialRetry != nil {
		nextCompleted = cloneSearchPartialEvidence(options.PartialRetry.CompletedIndexers)
		for _, outcome := range options.IndexerOutcomes {
			if outcome.State != "succeeded" || options.ReusedIndexerIDs[outcome.IndexerInstanceID] {
				continue
			}
			nextCompleted[outcome.IndexerInstanceID] = append([]SearchCandidateEvidence(nil), freshEvidence[outcome.IndexerInstanceID]...)
		}
	}
	ranked := releases.Rank(observations, evaluations)
	subtitlePolicy, err := subtitles.ParsePolicy(search.Policy)
	if err != nil {
		return workflows.Workflow{}, err
	}
	if slices.Contains(subtitlePolicy.Required, "da") || slices.Contains(subtitlePolicy.RequiredAudio, "da") {
		ranked = releases.RankForDanishSubtitles(observations, evaluations)
	}
	if options.RecoveryRescan {
		partialNext, err := store.persistRecoveryDiscovery(ctx, transaction, search, ranked, candidateByObservation, candidateSequenceByObservation, options, now)
		if err != nil {
			return workflows.Workflow{}, err
		}
		if options.PartialRetry != nil {
			if err := persistSearchPartialRetryState(ctx, transaction, *options.PartialRetry, nextCompleted, partialNext, now); err != nil {
				return workflows.Workflow{}, err
			}
		}
		if err := transaction.Commit(ctx); err != nil {
			return workflows.Workflow{}, err
		}
		return search.Workflow, nil
	}
	selectedEvaluation, to, selectionErr := selectRankedCandidate(ranked,
		func(evaluation releases.Evaluation) (bool, error) {
			candidate := candidateByObservation[evaluation.ObservationID]
			var alreadyAttempted bool
			if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_attempted_releases WHERE workflow_id=$1 AND candidate_id=$2)
				OR EXISTS(SELECT 1 FROM workflow_release_exclusions e JOIN acquisition_rounds r ON r.id=e.retrieval_round_id AND r.state='open'
				 JOIN release_candidates c ON c.id=$2 AND c.workflow_id=$1
				 WHERE e.workflow_id=$1 AND e.indexer_instance_id=c.indexer_instance_id AND e.retrieval_error_code IN ('newznab_http_401','newznab_http_403','oldboys_http_401','oldboys_http_403')) OR EXISTS(SELECT 1 FROM release_candidates c WHERE c.workflow_id=$1 AND c.id=$2 AND `+knownSubtitleFailureSQL("c")+`)`, search.Workflow.ID.String(), candidate.String()).Scan(&alreadyAttempted); err != nil {
				return false, err
			}
			return alreadyAttempted, nil
		},
		func(evaluation releases.Evaluation) bool {
			return search.OpenRound == nil || search.OpenRound.Ordinal <= 1 || candidateSequenceByObservation[evaluation.ObservationID] > search.OpenRound.NoveltyCutoffSequence
		})
	if selectionErr != nil {
		return workflows.Workflow{}, selectionErr
	}
	// A deferred provider may still return a better release. Keep eligible
	// evidence for the continuation ranking instead of committing a selection
	// that could be beaten when that provider is retried. Failed providers do
	// not hold a ready candidate indefinitely; their failure remains recorded
	// and retryable, while the current eligible evidence can still be selected.
	if selectedEvaluation != nil && hasDeferredIndexerOutcome(options.IndexerOutcomes) {
		selectedEvaluation = nil
		to = workflows.StateWaiting
	}
	var selected *uuid.UUID
	if selectedEvaluation != nil {
		candidate := candidateByObservation[selectedEvaluation.ObservationID]
		selected = &candidate
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
	if to == workflows.StateHeld {
		// Held means at least one candidate was observed but none satisfied
		// the required release evidence. That is a recoverable, paced no-
		// selection outcome, not a terminal failure, so it schedules the same
		// bounded retry as waiting and records why the owner is seeing held.
		attentionDetail = "Releases were observed but none satisfied the required release evidence; a paced retry is scheduled."
	}
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
	} else if to == workflows.StateWaiting || to == workflows.StateHeld {
		retryReason := "no eligible release yet"
		if to == workflows.StateHeld {
			retryReason = "observed releases were held by required evidence; retrying"
		}
		payload, _ := json.Marshal(map[string]string{"workflow_id": search.Workflow.ID.String(), "reason": retryReason})
		var roundRuns int
		if err := transaction.QueryRow(ctx, `SELECT count(*) FROM search_runs run JOIN acquisition_rounds round ON round.workflow_id=run.workflow_id AND round.state='open'
			WHERE run.workflow_id=$1 AND run.mode='targeted' AND run.state='complete' AND run.started_at>=round.opened_at`, search.Workflow.ID.String()).Scan(&roundRuns); err != nil {
			return workflows.Workflow{}, err
		}
		nextSearchAt := now.UTC().Add(noSelectionRetryDelay(roundRuns))
		var priorSubmissions int
		if err := transaction.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, search.Workflow.ID.String()).Scan(&priorSubmissions); err != nil {
			return workflows.Workflow{}, err
		}
		if priorSubmissions > 0 {
			nextSearchAt = retryDiscoveryAt(search.Workflow.ID, int64(newRevision), 0, now)
		}
		if !searchRunComplete(options.IndexerOutcomes) {
			retryReason = "Search incomplete: some indexers were deferred or failed; retrying."
			nextSearchAt = incompleteSearchRetryAt(options.IndexerOutcomes, now)
			payload, _ = json.Marshal(map[string]any{"workflow_id": search.Workflow.ID.String(), "reason": retryReason, "partial_retry": true})
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), search.Workflow.ID.String(), newRevision, string(payload), now.UTC(), nextSearchAt); err != nil {
			return workflows.Workflow{}, err
		}
	}
	if options.PartialRetry != nil {
		partialNext := selected == nil && (to == workflows.StateWaiting || to == workflows.StateHeld) && !searchRunComplete(options.IndexerOutcomes)
		if err := persistSearchPartialRetryState(ctx, transaction, *options.PartialRetry, nextCompleted, partialNext, now); err != nil {
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

const (
	noSelectionRetryBase = 30 * time.Minute
	noSelectionRetryCap  = 12 * time.Hour
)

// Retry incomplete coverage without counting it as evidence of absence or
// increasing the empty-search backoff. Keep a 30-minute floor because this
// path is also used by direct repository callers without durable partial state.
func incompleteSearchRetryAt(outcomes []SearchIndexerOutcome, now time.Time) time.Time {
	floor := now.UTC().Add(noSelectionRetryBase)
	var earliest *time.Time
	for _, outcome := range outcomes {
		if outcome.State == "succeeded" {
			continue
		}
		retry := floor
		if outcome.RetryAt != nil && outcome.RetryAt.After(retry) {
			retry = outcome.RetryAt.UTC()
		}
		if earliest == nil || retry.Before(*earliest) {
			earliest = &retry
		}
	}
	if earliest != nil {
		return *earliest
	}
	return floor
}

// noSelectionRetryDelay paces the next targeted search after runs in the
// current round that selected nothing: 30 minutes after the first, doubling
// per run up to 12 hours. Every search queries every indexer, and a flat
// 30-minute retry for titles with no eligible release spent the indexers'
// rolling query budgets on repeat empty searches.
func noSelectionRetryDelay(roundRuns int) time.Duration {
	delay := noSelectionRetryBase
	for run := 1; run < roundRuns && delay < noSelectionRetryCap; run++ {
		delay *= 2
	}
	if delay > noSelectionRetryCap {
		delay = noSelectionRetryCap
	}
	return delay
}

func (store *Store) persistRecoveryDiscovery(ctx context.Context, transaction pgx.Tx, search SearchContext, ranked []releases.Evaluation, candidates map[uuid.UUID]uuid.UUID, sequences map[uuid.UUID]int64, options SearchPersistenceOptions, now time.Time) (bool, error) {
	var exhaustedRoundText, state string
	var generation, cutoff, completeNoNew int64
	err := transaction.QueryRow(ctx, `SELECT discovery.exhausted_round_id::text,discovery.state,discovery.rescan_generation,round.novelty_cutoff_sequence,discovery.complete_no_new_scans
		FROM workflow_retry_discovery discovery
		JOIN acquisition_rounds round ON round.id=discovery.exhausted_round_id AND round.workflow_id=discovery.workflow_id
		WHERE discovery.workflow_id=$1 FOR UPDATE OF discovery,round`, search.Workflow.ID.String()).Scan(&exhaustedRoundText, &state, &generation, &cutoff, &completeNoNew)
	if errors.Is(err, pgx.ErrNoRows) || state != "waiting" || generation != options.RescanGeneration {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if search.Workflow.State != workflows.StateWaiting {
		return false, ErrConcurrentModification
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
			return false, err
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
			return false, err
		}
		if command.RowsAffected() != 1 {
			return false, ErrConcurrentModification
		}
		return false, nil
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
		return false, err
	}
	if command.RowsAffected() != 1 {
		return false, ErrConcurrentModification
	}
	payload, _ := json.Marshal(map[string]any{"workflow_id": search.Workflow.ID.String(), "rescan_generation": nextGeneration, "partial_retry": !complete})
	_, err = transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'workflow_retry_discovery',$2,$3,'workflow.discovery_rescan_requested',$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), search.Workflow.ID.String(), nextGeneration, payload, now.UTC(), next.UTC())
	return !complete, err
}

func hasDeferredIndexerOutcome(outcomes []SearchIndexerOutcome) bool {
	for _, outcome := range outcomes {
		if outcome.State == "deferred" {
			return true
		}
	}
	return false
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

// selectRankedCandidate chooses the release a search should act on and the
// workflow state it should land in. Ranking puts eligible releases first, so
// the first two passes consider them in score order:
//
//  1. eligible, never attempted, and first observed after the round's novelty
//     cutoff (a reopened round waits for new posts first);
//  2. eligible and never attempted, even when it predates the cutoff, because
//     attempted and excluded releases are already disqualified and stranding a
//     known eligible candidate only parks the workflow as held forever.
//
// With no selectable candidate the search lands on held when any candidate
// failed required evidence, and waiting otherwise.
func selectRankedCandidate(
	ranked []releases.Evaluation,
	attempted func(releases.Evaluation) (bool, error),
	novel func(releases.Evaluation) bool,
) (*releases.Evaluation, workflows.State, error) {
	for pass := 0; pass < 2; pass++ {
		for i := range ranked {
			if ranked[i].Verdict != releases.VerdictEligible {
				continue
			}
			alreadyAttempted, err := attempted(ranked[i])
			if err != nil {
				return nil, workflows.StateWaiting, err
			}
			if alreadyAttempted {
				continue
			}
			if pass == 0 && !novel(ranked[i]) {
				continue
			}
			return &ranked[i], workflows.StateCandidateSelected, nil
		}
	}
	for i := range ranked {
		if ranked[i].Verdict == releases.VerdictHeld {
			return nil, workflows.StateHeld, nil
		}
	}
	return nil, workflows.StateWaiting, nil
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
