package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

// HeldCandidateObservations returns the stored observations behind candidates
// whose latest decision is still held. A search can re-evaluate these against
// a newly available frozen episode mapping without contacting any provider.
func (store *Store) HeldCandidateObservations(ctx context.Context, workflowID uuid.UUID) ([]releases.Observation, error) {
	rows, err := store.Pool.Query(ctx, `SELECT observation.id::text,observation.indexer_instance_id::text,
		observation.operation_key,observation.native_guid,observation.title,
		observation.raw_fields,observation.normalized_facts,observation.body_sha256,
		observation.observed_at,observation.parse_version,observation.problems
		FROM release_candidates candidate
		JOIN release_observations observation ON observation.id=candidate.observation_id
		JOIN LATERAL (
			SELECT verdict FROM candidate_decisions decision
			WHERE decision.workflow_id=candidate.workflow_id AND decision.candidate_id=candidate.id
			ORDER BY decision.policy_version DESC,decision.decided_at DESC LIMIT 1
		) latest ON true
		WHERE candidate.workflow_id=$1 AND latest.verdict='held'
		ORDER BY candidate.last_observed_at DESC,candidate.id`, workflowID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []releases.Observation{}
	for rows.Next() {
		var observation releases.Observation
		var idText, indexerText string
		var bodyHash []byte
		var fields, facts, problems []byte
		var observedAt time.Time
		if err := rows.Scan(&idText, &indexerText, &observation.Operation, &observation.NativeGUID,
			&observation.Title, &fields, &facts, &bodyHash, &observedAt,
			&observation.ParseVersion, &problems); err != nil {
			return nil, err
		}
		observation.ID, _ = uuid.Parse(idText)
		observation.IndexerID, _ = uuid.Parse(indexerText)
		observation.BodyHash = hex.EncodeToString(bodyHash)
		observation.ObservedAt = observedAt
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
