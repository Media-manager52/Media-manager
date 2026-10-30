package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/releases"
	"uuid"
)

// ErrSearchPartialRetryInvalid means the durable completion state no longer
// matches the immutable candidate evidence it claims to represent. Callers
// must discard the state and perform a full search rather than guessing at
// provenance.
var ErrSearchPartialRetryInvalid = errors.New("search partial retry evidence is no longer valid")

const searchPartialRetryLifetime = 7 * 24 * time.Hour

// SearchCandidateEvidence identifies one candidate and the exact immutable
// observation that the successful provider result produced. The observation
// ID is deliberately retained in addition to the candidate ID: a candidate's
// current observation pointer can move when held evidence is re-scored.
type SearchCandidateEvidence struct {
	CandidateID   uuid.UUID `json:"candidate_id"`
	ObservationID uuid.UUID `json:"observation_id"`
}

type SearchPartialRetry struct {
	ID                uuid.UUID
	WorkflowID        uuid.UUID
	RoundID           uuid.UUID
	Mode              string
	IntentFingerprint []byte
	PlannedIndexerIDs []uuid.UUID
	CompletedIndexers map[uuid.UUID][]SearchCandidateEvidence
	ExpiresAt         time.Time
}

type partialEvidenceJSON struct {
	CandidateID   string `json:"candidate_id"`
	ObservationID string `json:"observation_id"`
}

func decodePartialEvidence(raw []byte) (map[uuid.UUID][]SearchCandidateEvidence, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[uuid.UUID][]SearchCandidateEvidence{}, nil
	}
	var encoded map[string][]partialEvidenceJSON
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, ErrSearchPartialRetryInvalid
	}
	result := make(map[uuid.UUID][]SearchCandidateEvidence, len(encoded))
	for key, values := range encoded {
		indexerID, err := uuid.Parse(key)
		if err != nil {
			return nil, ErrSearchPartialRetryInvalid
		}
		evidence := make([]SearchCandidateEvidence, 0, len(values))
		for _, value := range values {
			candidateID, candidateErr := uuid.Parse(value.CandidateID)
			observationID, observationErr := uuid.Parse(value.ObservationID)
			if candidateErr != nil || observationErr != nil || candidateID == (uuid.UUID{}) || observationID == (uuid.UUID{}) {
				return nil, ErrSearchPartialRetryInvalid
			}
			evidence = append(evidence, SearchCandidateEvidence{CandidateID: candidateID, ObservationID: observationID})
		}
		result[indexerID] = evidence
	}
	return result, nil
}

func encodePartialEvidence(completed map[uuid.UUID][]SearchCandidateEvidence) ([]byte, error) {
	encoded := make(map[string][]partialEvidenceJSON, len(completed))
	for indexerID, evidence := range completed {
		values := make([]partialEvidenceJSON, 0, len(evidence))
		for _, item := range evidence {
			values = append(values, partialEvidenceJSON{CandidateID: item.CandidateID.String(), ObservationID: item.ObservationID.String()})
		}
		encoded[indexerID.String()] = values
	}
	return json.Marshal(encoded)
}

// LoadSearchPartialRetry loads state only while it is unexpired. Expired rows
// are left for the bounded cleanup performed by PersistSearch; they are never
// authoritative again.
func (store *Store) LoadSearchPartialRetry(ctx context.Context, workflowID, roundID uuid.UUID, mode string, intentFingerprint []byte, now time.Time) (SearchPartialRetry, error) {
	if workflowID == (uuid.UUID{}) || roundID == (uuid.UUID{}) || (mode != "targeted" && mode != "recovery") || len(intentFingerprint) != 32 {
		return SearchPartialRetry{}, ErrNotFound
	}
	var result SearchPartialRetry
	var idText string
	var planned []string
	var completedRaw []byte
	var expires time.Time
	err := store.Pool.QueryRow(ctx, `SELECT id::text,planned_indexer_ids::text[],completed_indexers,expires_at
		FROM search_partial_retries
		WHERE workflow_id=$1 AND round_id=$2 AND mode=$3 AND intent_fingerprint=$4 AND expires_at > $5`,
		workflowID.String(), roundID.String(), mode, intentFingerprint, now.UTC()).Scan(&idText, &planned, &completedRaw, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return SearchPartialRetry{}, ErrNotFound
	}
	if err != nil {
		return SearchPartialRetry{}, err
	}
	result.ID, _ = uuid.Parse(idText)
	result.WorkflowID = workflowID
	result.RoundID = roundID
	result.Mode = mode
	result.IntentFingerprint = append([]byte(nil), intentFingerprint...)
	result.ExpiresAt = expires
	result.PlannedIndexerIDs = make([]uuid.UUID, 0, len(planned))
	for _, value := range planned {
		id, parseErr := uuid.Parse(value)
		if parseErr != nil || id == (uuid.UUID{}) {
			return SearchPartialRetry{}, ErrSearchPartialRetryInvalid
		}
		result.PlannedIndexerIDs = append(result.PlannedIndexerIDs, id)
	}
	result.CompletedIndexers, err = decodePartialEvidence(completedRaw)
	if err != nil {
		return SearchPartialRetry{}, err
	}
	return result, nil
}

// DeleteSearchPartialRetry invalidates every intent for one workflow round and
// mode. A changed intent or a full search is deliberately terminal for the
// old retry chain.
func (store *Store) DeleteSearchPartialRetry(ctx context.Context, workflowID, roundID uuid.UUID, mode string) error {
	if workflowID == (uuid.UUID{}) || roundID == (uuid.UUID{}) || (mode != "targeted" && mode != "recovery") {
		return nil
	}
	_, err := store.Pool.Exec(ctx, `DELETE FROM search_partial_retries WHERE workflow_id=$1 AND round_id=$2 AND mode=$3`, workflowID.String(), roundID.String(), mode)
	return err
}

// LoadSearchPartialObservations validates that every stored candidate still
// points at the exact observation recorded at completion, then returns those
// immutable observations for re-evaluation. A moved pointer or missing
// candidate invalidates the entire chain; using either the moved pointer or
// the old observation without validation would misrepresent provenance.
func (store *Store) LoadSearchPartialObservations(ctx context.Context, retry SearchPartialRetry) ([]releases.Observation, error) {
	return store.loadSearchPartialObservations(ctx, nil, retry)
}

func (store *Store) loadSearchPartialObservations(ctx context.Context, transaction pgx.Tx, retry SearchPartialRetry) ([]releases.Observation, error) {
	byCandidate := map[uuid.UUID]SearchCandidateEvidence{}
	for _, evidence := range retry.CompletedIndexers {
		for _, item := range evidence {
			if item.CandidateID == (uuid.UUID{}) || item.ObservationID == (uuid.UUID{}) {
				return nil, ErrSearchPartialRetryInvalid
			}
			if prior, exists := byCandidate[item.CandidateID]; exists && prior != item {
				return nil, ErrSearchPartialRetryInvalid
			}
			byCandidate[item.CandidateID] = item
		}
	}
	if len(byCandidate) == 0 {
		return []releases.Observation{}, nil
	}
	candidateIDs := make([]uuid.UUID, 0, len(byCandidate))
	for candidateID := range byCandidate {
		candidateIDs = append(candidateIDs, candidateID)
	}
	sort.Slice(candidateIDs, func(i, j int) bool { return candidateIDs[i].String() < candidateIDs[j].String() })
	query := `SELECT candidate.id::text,candidate.indexer_instance_id::text,candidate.observation_id::text
		FROM release_candidates candidate
		WHERE candidate.workflow_id=$1 AND candidate.id=ANY($2::uuid[])
		ORDER BY candidate.id`
	if transaction != nil {
		query += ` FOR UPDATE`
	}
	rows, err := func() (pgx.Rows, error) {
		if transaction != nil {
			return transaction.Query(ctx, query, retry.WorkflowID.String(), candidateIDs)
		}
		return store.Pool.Query(ctx, query, retry.WorkflowID.String(), candidateIDs)
	}()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[uuid.UUID]SearchCandidateEvidence{}
	foundIndexer := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var candidateText, indexerText, observationText string
		if err := rows.Scan(&candidateText, &indexerText, &observationText); err != nil {
			return nil, err
		}
		candidateID, candidateErr := uuid.Parse(candidateText)
		indexerID, indexerErr := uuid.Parse(indexerText)
		observationID, observationErr := uuid.Parse(observationText)
		if candidateErr != nil || indexerErr != nil || observationErr != nil {
			return nil, ErrSearchPartialRetryInvalid
		}
		found[candidateID] = SearchCandidateEvidence{CandidateID: candidateID, ObservationID: observationID}
		foundIndexer[candidateID] = indexerID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for candidateID, expected := range byCandidate {
		actual, exists := found[candidateID]
		if !exists || actual.ObservationID != expected.ObservationID {
			return nil, ErrSearchPartialRetryInvalid
		}
		var expectedIndexer uuid.UUID
		for indexerID, evidence := range retry.CompletedIndexers {
			for _, item := range evidence {
				if item.CandidateID == candidateID {
					expectedIndexer = indexerID
					break
				}
			}
		}
		if expectedIndexer == (uuid.UUID{}) || foundIndexer[candidateID] != expectedIndexer {
			return nil, ErrSearchPartialRetryInvalid
		}
	}
	observationIDs := make([]uuid.UUID, 0, len(byCandidate))
	for _, evidence := range byCandidate {
		observationIDs = append(observationIDs, evidence.ObservationID)
	}
	sort.Slice(observationIDs, func(i, j int) bool { return observationIDs[i].String() < observationIDs[j].String() })
	observationRows, err := func() (pgx.Rows, error) {
		if transaction != nil {
			return transaction.Query(ctx, `SELECT id::text,indexer_instance_id::text,operation_key,native_guid,title,raw_fields,normalized_facts,body_sha256,observed_at,parse_version,problems FROM release_observations WHERE id=ANY($1::uuid[])`, observationIDs)
		}
		return store.Pool.Query(ctx, `SELECT id::text,indexer_instance_id::text,operation_key,native_guid,title,raw_fields,normalized_facts,body_sha256,observed_at,parse_version,problems FROM release_observations WHERE id=ANY($1::uuid[])`, observationIDs)
	}()
	if err != nil {
		return nil, err
	}
	defer observationRows.Close()
	result := make([]releases.Observation, 0, len(byCandidate))
	observationIndexers := make(map[uuid.UUID]uuid.UUID, len(byCandidate))
	for observationRows.Next() {
		var observation releases.Observation
		var idText, indexerText string
		var fields, facts, bodyHash, problems []byte
		if err := observationRows.Scan(&idText, &indexerText, &observation.Operation, &observation.NativeGUID, &observation.Title, &fields, &facts, &bodyHash, &observation.ObservedAt, &observation.ParseVersion, &problems); err != nil {
			return nil, err
		}
		observation.ID, _ = uuid.Parse(idText)
		observation.IndexerID, _ = uuid.Parse(indexerText)
		observationIndexers[observation.ID] = observation.IndexerID
		observation.BodyHash = hex.EncodeToString(bodyHash)
		if err := json.Unmarshal(fields, &observation.Fields); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(facts, &observation.Facts); err != nil {
			return nil, err
		}
		if len(problems) > 0 {
			if err := json.Unmarshal(problems, &observation.Problems); err != nil {
				return nil, err
			}
		}
		result = append(result, observation)
	}
	if err := observationRows.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(byCandidate) {
		return nil, ErrSearchPartialRetryInvalid
	}
	for _, evidence := range byCandidate {
		if observationIndexers[evidence.ObservationID] != foundIndexer[evidence.CandidateID] {
			return nil, ErrSearchPartialRetryInvalid
		}
	}
	return result, nil
}

// validateSearchPartialEvidence re-checks provenance inside the persistence
// transaction. It locks the candidate rows so a concurrent held re-score cannot
// move the observation pointer between validation and the reused-evidence
// upsert.
func (store *Store) validateSearchPartialEvidence(ctx context.Context, transaction pgx.Tx, retry SearchPartialRetry) error {
	_, err := store.loadSearchPartialObservations(ctx, transaction, retry)
	return err
}

func sameIndexerIDSet(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[uuid.UUID]bool, len(left))
	for _, id := range left {
		values[id] = true
	}
	for _, id := range right {
		if !values[id] {
			return false
		}
	}
	return true
}

func cloneSearchPartialEvidence(input map[uuid.UUID][]SearchCandidateEvidence) map[uuid.UUID][]SearchCandidateEvidence {
	result := make(map[uuid.UUID][]SearchCandidateEvidence, len(input))
	for indexerID, evidence := range input {
		result[indexerID] = append([]SearchCandidateEvidence(nil), evidence...)
	}
	return result
}

// persistSearchPartialRetryState updates completion state in the same
// transaction as the search run, candidate evidence and outbox event. A
// completed or invalid chain deletes the state so a later discovery cycle
// queries every provider again. `expires_at` is fixed for the lifetime of an
// intent and is only reset when an expired state is replaced.
func persistSearchPartialRetryState(ctx context.Context, transaction pgx.Tx, retry SearchPartialRetry, completed map[uuid.UUID][]SearchCandidateEvidence, active bool, now time.Time) error {
	if retry.WorkflowID == (uuid.UUID{}) || retry.RoundID == (uuid.UUID{}) || (retry.Mode != "targeted" && retry.Mode != "recovery") || len(retry.IntentFingerprint) != 32 {
		return errors.New("invalid search partial retry state")
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM search_partial_retries WHERE workflow_id=$1 AND expires_at <= $2`, retry.WorkflowID.String(), now.UTC()); err != nil {
		return err
	}
	if !active {
		_, err := transaction.Exec(ctx, `DELETE FROM search_partial_retries WHERE workflow_id=$1 AND round_id=$2 AND mode=$3`, retry.WorkflowID.String(), retry.RoundID.String(), retry.Mode)
		return err
	}
	completedJSON, err := encodePartialEvidence(completed)
	if err != nil {
		return err
	}
	planned := make([]string, 0, len(retry.PlannedIndexerIDs))
	for _, id := range retry.PlannedIndexerIDs {
		planned = append(planned, id.String())
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM search_partial_retries WHERE workflow_id=$1 AND round_id=$2 AND mode=$3 AND intent_fingerprint<>$4`, retry.WorkflowID.String(), retry.RoundID.String(), retry.Mode, retry.IntentFingerprint); err != nil {
		return err
	}
	expires := now.UTC().Add(searchPartialRetryLifetime)
	_, err = transaction.Exec(ctx, `INSERT INTO search_partial_retries(id,workflow_id,round_id,mode,intent_fingerprint,planned_indexer_ids,completed_indexers,created_at,updated_at,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)
		ON CONFLICT(workflow_id,round_id,mode,intent_fingerprint) DO UPDATE SET
			planned_indexer_ids=EXCLUDED.planned_indexer_ids,
			completed_indexers=EXCLUDED.completed_indexers,
			updated_at=EXCLUDED.updated_at,
			expires_at=CASE WHEN search_partial_retries.expires_at <= EXCLUDED.updated_at THEN EXCLUDED.expires_at ELSE search_partial_retries.expires_at END`,
		uuid.NewV7().String(), retry.WorkflowID.String(), retry.RoundID.String(), retry.Mode, retry.IntentFingerprint, planned, string(completedJSON), now.UTC(), expires)
	return err
}

func appendUniqueCandidateEvidence(evidence []SearchCandidateEvidence, item SearchCandidateEvidence) []SearchCandidateEvidence {
	for _, existing := range evidence {
		if existing.CandidateID == item.CandidateID && existing.ObservationID == item.ObservationID {
			return evidence
		}
	}
	return append(evidence, item)
}

// DeleteExpiredSearchPartialRetries physically bounds abandoned retry state.
// Expiry is already authoritative for reads; this periodic cleanup keeps the
// small table from retaining rows for workflows that are never searched again.
func (store *Store) DeleteExpiredSearchPartialRetries(ctx context.Context, now time.Time) error {
	_, err := store.Pool.Exec(ctx, `DELETE FROM search_partial_retries WHERE expires_at <= $1`, now.UTC())
	return err
}
