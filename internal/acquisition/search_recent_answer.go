package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// recentAnswerEvent is true for the search that follows a failed release.
func recentAnswerEvent(event postgres.OutboxEvent) bool {
	var payload searchEventPayload
	return json.Unmarshal(event.Payload, &payload) == nil && payload.ReuseRecentAnswer
}

// loadRecentAnswer returns the kept answers of the search that selected the
// failed release, as fully completed partial retry evidence, when they may be
// reused (see postgres.RecentSearchAnswerLifetime). Anything that no longer
// matches (another indexer set, moved candidate evidence) returns nothing and
// the search asks every indexer as before.
func (service *SearchService) loadRecentAnswer(ctx context.Context, search postgres.SearchContext, planned []plannedSearchIndexer, fingerprint []byte, now time.Time) (postgres.SearchPartialRetry, []releases.Observation, map[uuid.UUID]bool, error) {
	empty := postgres.SearchPartialRetry{}
	answer, found, err := service.Store.RecentSearchAnswer(ctx, search.Workflow.ID, search.SearchRoundID, now)
	if err != nil || !found {
		return empty, nil, map[uuid.UUID]bool{}, err
	}
	plannedIDs := plannedIndexerIDs(planned)
	if !samePlannedIndexerSet(answer.PlannedIndexerIDs, plannedIDs) || len(answer.CompletedIndexers) != len(plannedIDs) {
		return empty, nil, map[uuid.UUID]bool{}, nil
	}
	for _, plan := range planned {
		if _, ok := answer.CompletedIndexers[plan.Integration.ID]; !ok || plan.PlanError != "" {
			return empty, nil, map[uuid.UUID]bool{}, nil
		}
	}
	answer.IntentFingerprint = fingerprint
	observations, err := service.Store.LoadSearchPartialObservations(ctx, answer)
	if errors.Is(err, postgres.ErrSearchPartialRetryInvalid) {
		return empty, nil, map[uuid.UUID]bool{}, nil
	}
	if err != nil {
		return empty, nil, nil, err
	}
	reused := make(map[uuid.UUID]bool, len(answer.CompletedIndexers))
	for indexerID := range answer.CompletedIndexers {
		reused[indexerID] = true
	}
	return answer, observations, reused, nil
}
