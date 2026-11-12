package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/releases"
)

// Shared season answers. A season with many missing episodes used to send
// one search per episode to every indexer, although the indexer's answer to
// "season N" holds every episode of it. The first episode workflow of such a
// season asks for the whole season and stores the answer here; the other
// episodes of the season reuse it for a few hours. Every episode still
// evaluates the releases under its own scope, language and quality rules, and
// every reused release becomes that workflow's own candidate exactly as if
// the indexer had returned it to an episode search.

// SeasonResponse is one stored indexer answer to a whole-season search.
type SeasonResponse struct {
	FetchedAt time.Time
	// Complete is false when the indexer had more results than were read.
	// Such an answer is never shared.
	Complete     bool
	Observations []releases.Observation
	// Locators are the download locator references of Observations, in the
	// same order, as the provider returned them.
	Locators []string
}

// OpenEpisodeWorkflows counts the open episode workflows of one season of a
// series.
func (store *Store) OpenEpisodeWorkflows(ctx context.Context, mediaID uuid.UUID, season int) (int, error) {
	var count int
	err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE media_id=$1 AND terminal_at IS NULL AND starts_with(variant,$2)`,
		mediaID.String(), fmt.Sprintf("season:%02d:episode:", season)).Scan(&count)
	return count, err
}

// SharedSeasonResponse returns the stored answer of one indexer to one
// season request when it may serve workflowID: fetched after notBefore,
// after the workflow was created, after the newest upload the newest-uploads
// reader matched to the workflow, after the owner last asked for a search
// of it, and after the workflow's own last recorded search (an answer it has
// already evaluated would only repeat itself; its next search asks the
// indexer). found is false otherwise, and also when an observation it named
// is gone.
func (store *Store) SharedSeasonResponse(ctx context.Context, indexerID uuid.UUID, fingerprint []byte, workflowID uuid.UUID, notBefore time.Time) (SeasonResponse, bool, error) {
	var response SeasonResponse
	var ids []string
	err := store.Pool.QueryRow(ctx, `SELECT response.fetched_at,response.complete,response.observation_ids::text[],response.locator_refs
		FROM season_search_responses response, workflows workflow
		WHERE response.indexer_instance_id=$1 AND response.request_fingerprint=$2 AND workflow.id=$3
		  AND response.fetched_at>$4 AND response.fetched_at>workflow.created_at
		  AND NOT EXISTS(SELECT 1 FROM indexer_feed_matches hit WHERE hit.workflow_id=workflow.id AND hit.seen_at>=response.fetched_at)
		  AND NOT EXISTS(SELECT 1 FROM workflow_transitions transition WHERE transition.workflow_id=workflow.id AND transition.actor='owner' AND transition.to_state='searching' AND transition.occurred_at>=response.fetched_at)
		  AND NOT EXISTS(SELECT 1 FROM search_runs run WHERE run.workflow_id=workflow.id AND run.started_at>=response.fetched_at)`,
		indexerID.String(), fingerprint, workflowID.String(), notBefore.UTC()).Scan(&response.FetchedAt, &response.Complete, &ids, &response.Locators)
	if errors.Is(err, pgx.ErrNoRows) {
		return SeasonResponse{}, false, nil
	}
	if err != nil {
		return SeasonResponse{}, false, err
	}
	if !response.Complete || len(ids) == 0 {
		return response, true, nil
	}
	rows, err := store.Pool.Query(ctx, `SELECT observation.id::text,observation.indexer_instance_id::text,
		observation.operation_key,observation.native_guid,observation.title,
		observation.raw_fields,observation.normalized_facts,observation.body_sha256,
		observation.observed_at,observation.parse_version,observation.problems
		FROM unnest($1::uuid[]) WITH ORDINALITY AS wanted(id, position)
		JOIN release_observations observation ON observation.id=wanted.id
		WHERE observation.indexer_instance_id=$2
		ORDER BY wanted.position`, ids, indexerID.String())
	if err != nil {
		return SeasonResponse{}, false, err
	}
	observations, err := scanStoredObservations(rows)
	if err != nil {
		return SeasonResponse{}, false, err
	}
	if len(observations) != len(ids) || len(response.Locators) != len(ids) {
		// Evidence it named was removed: ask the indexer again.
		return SeasonResponse{}, false, nil
	}
	response.Observations = observations
	return response, true, nil
}

// SaveSeasonResponse stores one indexer's answer to a season request.
// observations are already sanitized and locators are their download locator
// references, in the same order. Every observation is stored as release
// evidence; the returned observations carry their stored IDs. An incomplete
// answer stores no observations: it only remembers that this season is too
// large to share.
func (store *Store) SaveSeasonResponse(ctx context.Context, indexerID uuid.UUID, fingerprint []byte, observations []releases.Observation, locators []string, complete bool, now time.Time) ([]releases.Observation, []string, error) {
	if len(fingerprint) != 32 {
		return nil, nil, errors.New("invalid season request fingerprint")
	}
	if len(locators) != len(observations) {
		return nil, nil, errors.New("season answer locators do not match its observations")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer transaction.Rollback(ctx)
	stored := []releases.Observation{}
	storedLocators := []string{}
	ids := []string{}
	if complete {
		for index, observation := range observations {
			if observation.IndexerID != indexerID {
				continue
			}
			rawFields, _ := json.Marshal(observation.Fields)
			facts, _ := json.Marshal(observation.Facts)
			problems, _ := json.Marshal(observation.Problems)
			bodyHash, decodeErr := decodeHex(observation.BodyHash)
			if decodeErr != nil {
				bodyHash = sha256Bytes([]byte(observation.BodyHash))
			}
			id, err := insertReleaseObservation(ctx, transaction, observation, bodyHash, rawFields, facts, problems)
			if err != nil {
				return nil, nil, err
			}
			observation.ID = id
			stored = append(stored, observation)
			storedLocators = append(storedLocators, locators[index])
			ids = append(ids, id.String())
		}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO season_search_responses(indexer_instance_id,request_fingerprint,fetched_at,complete,observation_ids,locator_refs)
		VALUES($1,$2,$3,$4,$5::uuid[],$6)
		ON CONFLICT(indexer_instance_id,request_fingerprint) DO UPDATE SET fetched_at=EXCLUDED.fetched_at,complete=EXCLUDED.complete,observation_ids=EXCLUDED.observation_ids,locator_refs=EXCLUDED.locator_refs`,
		indexerID.String(), fingerprint, now.UTC(), complete, ids, storedLocators); err != nil {
		return nil, nil, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return stored, storedLocators, nil
}

// DeleteExpiredSeasonResponses removes stored season answers fetched before
// cutoff. They are never shared after their lifetime anyway.
func (store *Store) DeleteExpiredSeasonResponses(ctx context.Context, cutoff time.Time) (int64, error) {
	command, err := store.Pool.Exec(ctx, `DELETE FROM season_search_responses WHERE fetched_at<$1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}
