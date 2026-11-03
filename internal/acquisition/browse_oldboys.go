package acquisition

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/oldboys"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
)

const (
	// oldBoysBrowseCacheTTL is how long a complete title-only OldBoys result
	// serves every stream list for that title (all episodes share it).
	oldBoysBrowseCacheTTL = 10 * time.Minute
	// oldBoysBrowseRestTimeout bounds the background fetch of pages two to
	// four: three pacing intervals of five seconds plus the requests.
	oldBoysBrowseRestTimeout = 90 * time.Second
	maxOldBoysBrowseCache    = 200
)

// oldBoysBrowseObservations queries the native Danish provider for a stream
// list. OldBoys is searched by title only, returns at most 25 rows per page
// and allows one request every five seconds, so waiting for all four pages
// held the whole list for up to twenty seconds while every Newznab indexer
// had answered within a second.
//
// The list therefore gets page one at once. When page one is full, pages two
// to four are fetched in the background and the complete result replaces the
// cached one; partial reports this, so the caller marks the provider deferred
// and the list is cached only briefly. The next open of the title then shows
// every page. The title-only result is cached across episodes.
func (s *BrowseService) oldBoysBrowseObservations(ctx context.Context, client *oldboys.Client, integrationID string, in BrowseInput) ([]releases.Observation, bool, error) {
	request := oldBoysBrowseRequest(in)
	key := integrationID + "|" + request.Name + "|" + strconv.Itoa(request.Category)
	s.mu.Lock()
	if cached, ok := s.oldBoysCache[key]; ok && time.Now().Before(cached.until) {
		observations := append([]releases.Observation(nil), cached.observations...)
		s.mu.Unlock()
		return observations, cached.partial, nil
	}
	s.mu.Unlock()
	type firstPage struct {
		observations []releases.Observation
		partial      bool
	}
	value, err, _ := s.flights.Do("oldboys|"+key, func() (any, error) {
		request.Page = 1
		observations, rows, err := client.ProbeSearch(ctx, request, indexers.WorkloadBrowse, nil)
		if err != nil {
			return nil, err
		}
		more := rows >= oldboys.PageSize
		// A partial entry stays until the background fetch replaces it; its
		// lifetime only matters if that fetch is lost.
		lifetime := oldBoysBrowseCacheTTL
		if more {
			lifetime = oldBoysBrowseRestTimeout + time.Minute
		}
		s.storeOldBoysBrowse(key, observations, more, lifetime)
		if more {
			go s.finishOldBoysBrowse(client, request, key, observations, in)
		}
		return firstPage{observations: observations, partial: more}, nil
	})
	if err != nil {
		return nil, false, err
	}
	page := value.(firstPage)
	return append([]releases.Observation(nil), page.observations...), page.partial, nil
}

// finishOldBoysBrowse fetches the pages after a full first page, stopping at
// the first short page, a failure or MaxPage, and stores the complete result.
// It runs detached from the viewer's request.
func (s *BrowseService) finishOldBoysBrowse(client *oldboys.Client, request oldboys.SearchRequest, key string, first []releases.Observation, in BrowseInput) {
	ctx, cancel := context.WithTimeout(context.Background(), oldBoysBrowseRestTimeout)
	defer cancel()
	all := append([]releases.Observation(nil), first...)
	for page := 2; page <= oldboys.MaxPage; page++ {
		request.Page = page
		observations, rows, err := client.ProbeSearch(ctx, request, indexers.WorkloadBrowse, nil)
		if err != nil {
			slog.Info("OldBoys stream-list pages stopped early", "page", page, "error", err)
			break
		}
		all = append(all, observations...)
		if rows < oldboys.PageSize {
			break
		}
	}
	s.storeOldBoysBrowse(key, all, false, oldBoysBrowseCacheTTL)
	// Drop cached stream lists built from page one so the next open is
	// rebuilt from the complete OldBoys result.
	s.mu.Lock()
	for cached := range s.cache {
		if cached.Kind == in.Kind && cached.Title == in.Title {
			delete(s.cache, cached)
		}
	}
	s.mu.Unlock()
}

func (s *BrowseService) storeOldBoysBrowse(key string, observations []releases.Observation, partial bool, lifetime time.Duration) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.oldBoysCache == nil {
		s.oldBoysCache = map[string]oldBoysBrowseCache{}
	}
	if _, exists := s.oldBoysCache[key]; !exists && len(s.oldBoysCache) >= maxOldBoysBrowseCache {
		for oldKey, entry := range s.oldBoysCache {
			if now.After(entry.until) {
				delete(s.oldBoysCache, oldKey)
			}
		}
		if len(s.oldBoysCache) >= maxOldBoysBrowseCache {
			for oldKey := range s.oldBoysCache {
				delete(s.oldBoysCache, oldKey)
				break
			}
		}
	}
	s.oldBoysCache[key] = oldBoysBrowseCache{observations: append([]releases.Observation(nil), observations...), until: now.Add(lifetime), partial: partial}
}
