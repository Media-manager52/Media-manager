package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Release observations are immutable evidence. Before observation reuse was
// added, every repeated search stored a full new copy of each provider result
// even when nothing about the release had changed; one release can have tens
// of thousands of copies that differ only in when they were seen. Those copies
// make backups and deploys slow without carrying any additional evidence.
//
// PruneRepeatedObservations removes only such repeats. An observation is
// removed when every one of these holds:
//
//   - an earlier observation of the same provider release (indexer and native
//     GUID) has exactly the same content: title, operation, parse version, raw
//     fields, problems, and normalized facts ignoring each fact's observed_at
//     timestamp (that earlier copy is always kept);
//   - no release candidate points at it;
//   - no partial-search retry evidence names it;
//   - no shared season search answer names it;
//   - it was observed before the cutoff.
//
// So the first appearance of every distinct piece of evidence stays, and so
// does every observation anything refers to (invariant S06: re-scored evidence
// is a different content and is never a repeat). Every removed row is passed to
// Archive inside the deleting transaction, and the transaction commits only
// after archiving succeeded, so nothing is removed without a copy.

// ObservationPruneOptions controls one pruning pass.
type ObservationPruneOptions struct {
	// ObservedBefore protects every observation at or after this instant.
	ObservedBefore time.Time
	// ReleasesPerBatch bounds how many provider releases one transaction
	// covers.
	ReleasesPerBatch int
	// Apply deletes. Without it the pass only counts what it would delete.
	Apply bool
	// Archive receives each batch's rows before their deletion commits and
	// must return only once they are durably stored. It is required when
	// Apply is set.
	Archive func([]ArchivedObservation) error
	// IndexerInstanceID, when set, limits the pass to one indexer's
	// releases, so separate passes can run in parallel without overlapping.
	IndexerInstanceID string
	// Progress, when set, is called after every batch.
	Progress func(ObservationPruneProgress)
}

// ObservationPruneProgress counts a pass so far.
type ObservationPruneProgress struct {
	Releases  int64 `json:"releases"`
	Repeats   int64 `json:"repeats"`
	Deleted   int64 `json:"deleted"`
	Batches   int64 `json:"batches"`
	Completed bool  `json:"completed"`
}

// ArchivedObservation is one removed row, with every column.
type ArchivedObservation struct {
	ID                string          `json:"id"`
	IndexerInstanceID string          `json:"indexer_instance_id"`
	OperationKey      string          `json:"operation_key"`
	NativeGUID        string          `json:"native_guid"`
	Title             string          `json:"title"`
	BodySHA256        []byte          `json:"body_sha256"`
	RawFields         json.RawMessage `json:"raw_fields"`
	NormalizedFacts   json.RawMessage `json:"normalized_facts"`
	ParseVersion      string          `json:"parse_version"`
	Problems          json.RawMessage `json:"problems"`
	ObservedAt        time.Time       `json:"observed_at"`
}

const repeatedObservationsCTE = `
WITH batch AS (
	SELECT * FROM unnest($1::uuid[], $2::text[]) AS release(indexer_instance_id, native_guid)
),
protected AS (
	SELECT (evidence->>'observation_id')::uuid AS id
	FROM search_partial_retries retry,
		jsonb_each(CASE WHEN jsonb_typeof(retry.completed_indexers)='object' THEN retry.completed_indexers ELSE '{}'::jsonb END) AS indexer(key, list),
		jsonb_array_elements(CASE WHEN jsonb_typeof(indexer.list)='array' THEN indexer.list ELSE '[]'::jsonb END) AS evidence
	WHERE evidence->>'observation_id' ~ '^[0-9a-fA-F-]{36}$'
	UNION ALL
	SELECT unnest(response.observation_ids) FROM season_search_responses response
	UNION ALL
	SELECT (evidence->>'observation_id')::uuid
	FROM search_recent_answers answer,
		jsonb_each(answer.completed_indexers) AS indexer(key, list),
		jsonb_array_elements(CASE WHEN jsonb_typeof(indexer.list)='array' THEN indexer.list ELSE '[]'::jsonb END) AS evidence
	WHERE evidence->>'observation_id' ~ '^[0-9a-fA-F-]{36}$'
),
versions AS (
	SELECT observation.id, observation.indexer_instance_id, observation.native_guid, observation.observed_at,
		sha256(convert_to(jsonb_build_array(
			observation.title, observation.operation_key, observation.parse_version,
			observation.raw_fields, observation.problems,
			CASE WHEN jsonb_typeof(observation.normalized_facts)='array' THEN
				(SELECT COALESCE(jsonb_agg(CASE WHEN jsonb_typeof(fact)='object' THEN fact - 'observed_at' ELSE fact END ORDER BY position), '[]'::jsonb)
				 FROM jsonb_array_elements(observation.normalized_facts) WITH ORDINALITY AS facts(fact, position))
			ELSE observation.normalized_facts END
		)::text, 'UTF8')) AS content
	FROM release_observations observation
	JOIN batch ON batch.indexer_instance_id=observation.indexer_instance_id AND batch.native_guid=observation.native_guid
),
repeats AS (
	SELECT id FROM (
		SELECT id, observed_at,
			row_number() OVER (PARTITION BY indexer_instance_id, native_guid, content ORDER BY observed_at, id) AS position
		FROM versions
	) ranked
	WHERE position > 1 AND observed_at < $3
		AND NOT EXISTS (SELECT 1 FROM release_candidates candidate WHERE candidate.observation_id=ranked.id)
		AND NOT EXISTS (SELECT 1 FROM protected WHERE protected.id=ranked.id)
)`

// PruneRepeatedObservations walks every provider release once, in index order,
// and removes (or, without Apply, counts) its repeated observations.
func (store *Store) PruneRepeatedObservations(ctx context.Context, options ObservationPruneOptions) (ObservationPruneProgress, error) {
	var progress ObservationPruneProgress
	if options.ObservedBefore.IsZero() {
		return progress, errors.New("observation pruning needs a cutoff")
	}
	if options.ReleasesPerBatch < 1 || options.ReleasesPerBatch > 5000 {
		return progress, errors.New("releases per batch must be between 1 and 5000")
	}
	if options.Apply && options.Archive == nil {
		return progress, errors.New("observation pruning refuses to delete without an archive")
	}
	var afterIndexer, afterGUID string
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		indexers, guids, err := store.nextObservationReleases(ctx, options.IndexerInstanceID, first, afterIndexer, afterGUID, options.ReleasesPerBatch)
		if err != nil {
			return progress, err
		}
		if len(indexers) == 0 {
			progress.Completed = true
			return progress, nil
		}
		repeats, deleted, err := store.pruneObservationBatch(ctx, indexers, guids, options)
		if err != nil {
			return progress, fmt.Errorf("prune releases after %s/%s: %w", afterIndexer, afterGUID, err)
		}
		first = false
		afterIndexer, afterGUID = indexers[len(indexers)-1], guids[len(guids)-1]
		progress.Releases += int64(len(indexers))
		progress.Repeats += repeats
		progress.Deleted += deleted
		progress.Batches++
		if options.Progress != nil {
			options.Progress(progress)
		}
	}
}

// nextObservationReleases returns the next provider releases in index order.
// The columns stay uncast: casting the indexer ID to text would sort by an
// expression the release_observations_native index cannot supply, turning a
// millisecond index walk into a scan of the whole indexer.
func (store *Store) nextObservationReleases(ctx context.Context, onlyIndexer string, first bool, afterIndexer, afterGUID string, limit int) ([]string, []string, error) {
	query := `SELECT DISTINCT indexer_instance_id, native_guid FROM release_observations ORDER BY 1, 2 LIMIT $1`
	args := []any{limit}
	switch {
	case onlyIndexer != "" && first:
		query = `SELECT DISTINCT indexer_instance_id, native_guid FROM release_observations
			WHERE indexer_instance_id=$2::uuid ORDER BY 1, 2 LIMIT $1`
		args = append(args, onlyIndexer)
	case onlyIndexer != "":
		query = `SELECT DISTINCT indexer_instance_id, native_guid FROM release_observations
			WHERE indexer_instance_id=$2::uuid AND native_guid > $3 ORDER BY 1, 2 LIMIT $1`
		args = append(args, onlyIndexer, afterGUID)
	case !first:
		query = `SELECT DISTINCT indexer_instance_id, native_guid FROM release_observations
			WHERE (indexer_instance_id, native_guid) > ($2::uuid, $3) ORDER BY 1, 2 LIMIT $1`
		args = append(args, afterIndexer, afterGUID)
	}
	rows, err := store.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var indexers, guids []string
	for rows.Next() {
		var indexer, guid string
		if err := rows.Scan(&indexer, &guid); err != nil {
			return nil, nil, err
		}
		indexers = append(indexers, indexer)
		guids = append(guids, guid)
	}
	return indexers, guids, rows.Err()
}

func (store *Store) pruneObservationBatch(ctx context.Context, indexers, guids []string, options ObservationPruneOptions) (int64, int64, error) {
	if !options.Apply {
		var repeats int64
		err := store.Pool.QueryRow(ctx, repeatedObservationsCTE+` SELECT count(*) FROM repeats`, indexers, guids, options.ObservedBefore).Scan(&repeats)
		return repeats, 0, err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, repeatedObservationsCTE+`
		DELETE FROM release_observations observation USING repeats
		WHERE observation.id=repeats.id
		RETURNING observation.id::text, observation.indexer_instance_id::text, observation.operation_key, observation.native_guid,
			observation.title, observation.body_sha256, observation.raw_fields, observation.normalized_facts,
			observation.parse_version, observation.problems, observation.observed_at`, indexers, guids, options.ObservedBefore)
	if err != nil {
		return 0, 0, err
	}
	var deleted int64
	archived, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ArchivedObservation, error) {
		var observation ArchivedObservation
		err := row.Scan(&observation.ID, &observation.IndexerInstanceID, &observation.OperationKey, &observation.NativeGUID,
			&observation.Title, &observation.BodySHA256, &observation.RawFields, &observation.NormalizedFacts,
			&observation.ParseVersion, &observation.Problems, &observation.ObservedAt)
		return observation, err
	})
	if err != nil {
		return 0, 0, err
	}
	if len(archived) > 0 {
		if err := options.Archive(archived); err != nil {
			return 0, 0, fmt.Errorf("archive %d observations: %w", len(archived), err)
		}
	}
	deleted = int64(len(archived))
	if err := transaction.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return deleted, deleted, nil
}
