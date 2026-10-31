package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

// CandidateObservation is the stored evidence and selection state for one
// release candidate. It is used only by the read-only dry-run.
type CandidateObservation struct {
	CandidateID       uuid.UUID
	Observation       releases.Observation
	IndexerKind       string
	Attempted         bool
	Excluded          bool
	Selected          bool
	FirstSeenSequence int64
}

// WorkflowCandidateObservations loads the stored observation linked to each
// candidate. It never contacts a provider and never mutates candidate state.
//
// It deliberately loads only the columns the evaluator reads: identity, title,
// normalized facts, observed_at and parse_version. raw_fields, problems,
// operation_key and body_sha256 are left empty because evaluateCandidateObservation
// does not consume them. If an evaluator rule ever starts reading one of those,
// this loader must load it too, or the dry run silently diverges from the worker.
//
// Facts follow the same shapes ParseObservationFacts accepts: a fact array, or
// the legacy empty object "{}" meaning no facts. Any other shape is an error;
// facts are never guessed.
func (store *Store) WorkflowCandidateObservations(ctx context.Context, workflowID uuid.UUID, limit int) ([]CandidateObservation, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	if err := store.requireWorkflow(ctx, workflowID); err != nil {
		return nil, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT c.id::text,o.id::text,o.indexer_instance_id::text,o.native_guid,o.title,o.normalized_facts,o.observed_at,o.parse_version,i.kind,
		EXISTS(SELECT 1 FROM workflow_attempted_releases a WHERE a.workflow_id=c.workflow_id AND a.candidate_id=c.id),
		EXISTS(SELECT 1 FROM workflow_release_exclusions e WHERE e.workflow_id=c.workflow_id AND e.candidate_id=c.id),
		COALESCE(w.selected_release_id=c.id,false),c.first_seen_sequence
		FROM release_candidates c
		JOIN release_observations o ON o.id=c.observation_id
		JOIN integration_instances i ON i.id=c.indexer_instance_id
		JOIN workflows w ON w.id=c.workflow_id
		WHERE c.workflow_id=$1
		ORDER BY c.last_observed_at DESC,c.id LIMIT $2`, workflowID.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CandidateObservation{}
	for rows.Next() {
		var item CandidateObservation
		var candidateText, observationText, indexerText string
		var facts []byte
		if err := rows.Scan(&candidateText, &observationText, &indexerText, &item.Observation.NativeGUID, &item.Observation.Title, &facts, &item.Observation.ObservedAt, &item.Observation.ParseVersion, &item.IndexerKind, &item.Attempted, &item.Excluded, &item.Selected, &item.FirstSeenSequence); err != nil {
			return nil, err
		}
		item.CandidateID, _ = uuid.Parse(candidateText)
		item.Observation.ID, _ = uuid.Parse(observationText)
		item.Observation.IndexerID, _ = uuid.Parse(indexerText)
		if err := json.Unmarshal(facts, &item.Observation.Facts); err != nil {
			var legacyEmpty map[string]any
			if objectErr := json.Unmarshal(facts, &legacyEmpty); objectErr != nil || len(legacyEmpty) != 0 {
				return nil, fmt.Errorf("candidate %s has invalid stored facts: %w", item.CandidateID, err)
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
