package acquisition

import (
	"context"
	"errors"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/releases"
)

const (
	// maxSearchPages bounds how many result pages one indexer answers per
	// search: the first page plus follow-up pages when a page comes back full.
	maxSearchPages = 3
	// searchPagingWait is the total time one search may spend waiting out
	// indexer pacing before follow-up pages, across all its indexers. It keeps
	// the search well inside the worker lease.
	searchPagingWait = 12 * time.Second
)

// searchFollowUpPages asks an indexer for further pages when the previous page
// was (nearly) full, so releases beyond the first page are seen at all. Every
// follow-up failure, deferral or exhausted wait budget keeps the pages already
// read: the search then behaves exactly as a one-page search.
func searchFollowUpPages(ctx context.Context, request newznab.SearchRequest, first []releases.Observation, waitBudget *time.Duration, run func(newznab.SearchRequest) ([]releases.Observation, error)) []releases.Observation {
	if request.Limit <= 0 || request.Offset != 0 {
		return first
	}
	// Providers drop a few items from a full page (parser or provider side).
	full := request.Limit - request.Limit/10
	seen := make(map[string]bool, len(first))
	for _, observation := range first {
		seen[observation.NativeGUID] = true
	}
	result, pageSize := first, len(first)
	for page := 1; page < maxSearchPages && pageSize >= full; page++ {
		next := request
		next.Offset = page * request.Limit
		more, err := run(next)
		var deferred interface{ RetryAt() time.Time }
		if errors.As(err, &deferred) {
			wait := time.Until(deferred.RetryAt()) + 50*time.Millisecond
			if wait > *waitBudget {
				return result
			}
			if wait > 0 {
				*waitBudget -= wait
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return result
				case <-timer.C:
				}
			}
			more, err = run(next)
		}
		if err != nil {
			return result
		}
		pageSize = len(more)
		added := 0
		for _, observation := range more {
			if !seen[observation.NativeGUID] {
				seen[observation.NativeGUID] = true
				result = append(result, observation)
				added++
			}
		}
		if added == 0 {
			// The provider ignored the offset or has nothing further.
			return result
		}
	}
	return result
}
