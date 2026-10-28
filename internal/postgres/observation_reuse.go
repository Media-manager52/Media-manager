package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/releases"
)

// reusableObservation finds the candidate's current observation and decides
// whether the newly evaluated evidence is semantically identical. The
// candidate pointer is indexed by its unique workflow/indexer/native key, so
// this is a single-row lookup; it never scans the observation history.
//
// observation.observed_at and Fact.ObservedAt are fetch/processing timestamps,
// not provider publication evidence. They are deliberately excluded from the
// comparison. Raw fields, including provider timestamps such as pubdate,
// evidence indices, values, states, problems and parse version remain
// meaningful and therefore participate in the comparison.
func (store *Store) reusableObservation(ctx context.Context, transaction pgx.Tx, workflowID, indexerID uuid.UUID, nativeKey string, observation releases.Observation) (uuid.UUID, bool, error) {
	var previousIDText, previousIndexer, previousNative, operation, title, parseVersion string
	var rawFields, normalizedFacts, problems []byte
	err := transaction.QueryRow(ctx, `SELECT previous.id::text,previous.indexer_instance_id::text,previous.native_guid,previous.operation_key,previous.title,
		previous.raw_fields,previous.normalized_facts,previous.parse_version,previous.problems
		FROM release_candidates candidate
		JOIN release_observations previous ON previous.id=candidate.observation_id
		WHERE candidate.workflow_id=$1 AND candidate.indexer_instance_id=$2 AND candidate.native_key=$3`,
		workflowID.String(), indexerID.String(), nativeKey).Scan(&previousIDText, &previousIndexer, &previousNative, &operation, &title, &rawFields, &normalizedFacts, &parseVersion, &problems)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, false, nil
	}
	if err != nil {
		return uuid.UUID{}, false, err
	}
	previousID, err := uuid.Parse(previousIDText)
	if err != nil {
		return uuid.UUID{}, false, err
	}
	if previousIndexer != observation.IndexerID.String() || previousNative != observation.NativeGUID ||
		!semanticObservationEqual(operation, title, parseVersion, rawFields, normalizedFacts, problems, observation) {
		return uuid.UUID{}, false, nil
	}
	return previousID, true, nil
}

func semanticObservationEqual(operation, title, parseVersion string, rawFields, normalizedFacts, problems []byte, current releases.Observation) bool {
	if operation != current.Operation || title != current.Title || parseVersion != current.ParseVersion {
		return false
	}
	var previousFields []releases.RawField
	if err := json.Unmarshal(rawFields, &previousFields); err != nil || !reflect.DeepEqual(previousFields, current.Fields) {
		return false
	}
	var previousFacts []releases.Fact
	if err := json.Unmarshal(normalizedFacts, &previousFacts); err != nil || !factsEqualIgnoringObservedAt(previousFacts, current.Facts) {
		return false
	}
	var previousProblems []string
	if err := json.Unmarshal(problems, &previousProblems); err != nil || !reflect.DeepEqual(previousProblems, current.Problems) {
		return false
	}
	return true
}

func factsEqualIgnoringObservedAt(previous, current []releases.Fact) bool {
	if len(previous) != len(current) {
		return false
	}
	for index := range previous {
		left, right := previous[index], current[index]
		left.ObservedAt = time.Time{}
		right.ObservedAt = time.Time{}
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}
