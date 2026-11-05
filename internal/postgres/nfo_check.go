package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/media-manager/media-manager/internal/releases"
)

// NFOCheckContext is what a pre-retrieval NFO language check needs about the
// selected release: its stored provider observation, the open round's frozen
// policy and the title's original language.
type NFOCheckContext struct {
	Observation      releases.Observation
	Policy           json.RawMessage
	OriginalLanguage string
}

// LoadNFOCheckContext returns the selected candidate's observation and the
// open round's policy. ErrNotFound means there is no open round or the
// candidate is no longer selected; the caller then skips the optional check.
func (store *Store) LoadNFOCheckContext(ctx context.Context, workflowID, candidateID uuid.UUID) (NFOCheckContext, error) {
	var result NFOCheckContext
	var policy []byte
	err := store.Pool.QueryRow(ctx, `SELECT round.policy_snapshot,COALESCE((SELECT metadata.data->>'original_language' FROM media_metadata metadata WHERE metadata.media_id=workflow.media_id),'')
		FROM workflows workflow
		JOIN acquisition_rounds round ON round.workflow_id=workflow.id AND round.state='open'
		WHERE workflow.id=$1 AND workflow.selected_release_id=$2`, workflowID.String(), candidateID.String()).Scan(&policy, &result.OriginalLanguage)
	if errors.Is(err, pgx.ErrNoRows) {
		return NFOCheckContext{}, ErrNotFound
	}
	if err != nil {
		return NFOCheckContext{}, err
	}
	result.Policy = policy
	rows, err := store.Pool.Query(ctx, `SELECT observation.id::text,observation.indexer_instance_id::text,
		observation.operation_key,observation.native_guid,observation.title,
		observation.raw_fields,observation.normalized_facts,observation.body_sha256,
		observation.observed_at,observation.parse_version,observation.problems
		FROM release_candidates candidate
		JOIN release_observations observation ON observation.id=candidate.observation_id
		WHERE candidate.workflow_id=$1 AND candidate.id=$2`, workflowID.String(), candidateID.String())
	if err != nil {
		return NFOCheckContext{}, err
	}
	observations, err := scanStoredObservations(rows)
	if err != nil {
		return NFOCheckContext{}, err
	}
	if len(observations) != 1 {
		return NFOCheckContext{}, ErrNotFound
	}
	result.Observation = observations[0]
	return result, nil
}

// NFOCheckRecorded reports whether this selected candidate's NFO was already
// checked. The NFO request takes the provider's pacing slot, so the retrieval
// that follows is usually deferred once; the deferred run must go straight to
// the NZB instead of checking (and pacing) again.
func (store *Store) NFOCheckRecorded(ctx context.Context, candidateID uuid.UUID) (bool, error) {
	var recorded bool
	err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE aggregate_type='release_candidate' AND aggregate_id=$1 AND action='release.nfo_checked')`, candidateID.String()).Scan(&recorded)
	return recorded, err
}

// RecordNFOCheck records the outcome of a pre-retrieval NFO check.
func (store *Store) RecordNFOCheck(ctx context.Context, workflowID, candidateID uuid.UUID, outcome, reason string, now time.Time) error {
	detail, err := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "outcome": outcome, "reason": reason})
	if err != nil {
		return err
	}
	_, err = store.Pool.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','release.nfo_checked','release_candidate',$2,$3,$4)`, uuid.NewV7().String(), candidateID.String(), string(detail), now.UTC())
	return err
}
