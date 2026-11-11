package acquisition

import (
	"context"
	"errors"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
)

const (
	// maxSearchPages bounds how many result pages one indexer answers per
	// search: the first page plus follow-up pages while more results remain.
	maxSearchPages = 3
	// maxSearchResults bounds how many releases one indexer contributes to
	// one search across its pages.
	maxSearchResults = 1000
	// defaultSearchPageLimit is the page size asked for when neither the
	// provider's capabilities nor the owner state another one.
	defaultSearchPageLimit = indexers.DefaultSearchPageLimit
	// searchPagingWait is the total time one search may spend waiting out
	// indexer pacing before follow-up pages, across all its indexers. It keeps
	// the search well inside the worker lease.
	searchPagingWait = 12 * time.Second
)

// searchPageLimit is the page size one request asks an indexer for: the
// owner's page_limit setting, else the maximum the provider advertises, within
// the client bounds. A provider that answers with smaller pages than asked is
// still read in full, because paging follows the provider's stated total.
func searchPageLimit(config indexerConfig) int {
	return indexers.SearchPageLimit(config.PageLimit, config.MaximumLimit)
}

// searchFollowUpPages asks an indexer for further pages while results remain,
// so releases beyond the first page are seen at all. Results remain when the
// previous page was (nearly) full, or when the provider's stated total is
// clearly larger than what has been read: a provider may answer with smaller
// pages than asked. Every follow-up failure, deferral or exhausted wait budget
// keeps the pages already read: the search then behaves exactly as a one-page
// search.
func searchFollowUpPages(ctx context.Context, request newznab.SearchRequest, first newznab.SearchPage, waitBudget *time.Duration, run func(newznab.SearchRequest) (newznab.SearchPage, error)) []releases.Observation {
	result, _ := searchAllPages(ctx, request, first, waitBudget, run)
	return result
}

// searchAllPages is searchFollowUpPages that also reports whether everything
// the provider had was read: complete is false when paging stopped at the
// page or result bound, on a failure or deferral, or when the provider's
// stated total says more remain.
func searchAllPages(ctx context.Context, request newznab.SearchRequest, first newznab.SearchPage, waitBudget *time.Duration, run func(newznab.SearchRequest) (newznab.SearchPage, error)) (result []releases.Observation, complete bool) {
	result = first.Observations
	if request.Limit <= 0 || request.Offset != 0 {
		return result, false
	}
	// Providers drop a few items from a full page (parser or provider side).
	full := request.Limit - request.Limit/10
	seen := make(map[string]bool, len(result))
	for _, observation := range result {
		seen[observation.NativeGUID] = true
	}
	pageSize, total, offset := len(result), first.Total, 0
	for page := 1; page < maxSearchPages && len(result) < maxSearchResults; page++ {
		if pageSize >= full {
			offset += request.Limit
		} else if pageSize > 0 && moreStated(total, offset+pageSize) {
			// A short page with more stated: the provider caps its pages below
			// the asked size, so continue exactly where this page ended.
			offset += pageSize
		} else {
			return result, true
		}
		next := request
		next.Offset = offset
		more, err := run(next)
		var deferred interface{ RetryAt() time.Time }
		if errors.As(err, &deferred) {
			wait := time.Until(deferred.RetryAt()) + 50*time.Millisecond
			if wait > *waitBudget {
				return result, false
			}
			if wait > 0 {
				*waitBudget -= wait
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return result, false
				case <-timer.C:
				}
			}
			more, err = run(next)
		}
		if err != nil {
			return result, false
		}
		pageSize = len(more.Observations)
		if more.Total > 0 {
			total = more.Total
		}
		added := 0
		for _, observation := range more.Observations {
			if !seen[observation.NativeGUID] {
				seen[observation.NativeGUID] = true
				result = append(result, observation)
				added++
			}
		}
		if added == 0 {
			// The provider ignored the offset or has nothing further.
			return result, true
		}
	}
	return result, pageSize < full && !moreStated(total, offset+pageSize)
}

// moreStated reports whether a provider's stated total is clearly larger than
// the number of results read so far. A total within a tenth of what was read
// counts as read: providers that count a few results they do not deliver must
// not cost an extra request on every search.
func moreStated(total, read int) bool {
	return total > 0 && total-read > read/10
}
