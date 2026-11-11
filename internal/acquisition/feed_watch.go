package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// Newest-uploads watching.
//
// Searching asks every indexer about one title at a time, so a title that is
// still missing is asked about again and again. The newest-uploads list turns
// that around: one request per indexer shows everything uploaded since the
// last read, and that list is compared with every waiting title at once.
//
// The list is only a signal. A new upload that passes a waiting title's
// title, identity and season/episode checks brings that title's own, already
// scheduled search forward. That search asks the indexers as always and
// applies every language, quality and identity rule unchanged; nothing is
// selected or downloaded from the list itself.
//
// A title whose round of attempts is used up has no scheduled search. For it
// a match brings the paced discovery forward and, with OpenRounds, opens the
// next round once that discovery holds a newly observed eligible release: the
// owner's "start next round" under the same guards, at most once a day per
// title. The round's own search then selects under the unchanged rules. With
// OpenRounds the watcher also opens that round when the title's own scheduled
// rescan found such a release, under the same guards and the same daily limit.
const (
	feedTick = time.Minute
	// One indexer is read at most every feedMinInterval and at least every
	// feedMaxInterval; in between the interval follows how much of a page is
	// new since the last read.
	feedMinInterval   = 5 * time.Minute
	feedStartInterval = 10 * time.Minute
	feedMaxInterval   = 20 * time.Minute
	// feedMaxPages bounds one read. Further pages are only asked for when the
	// first page holds nothing seen before, so uploads may have been missed.
	feedMaxPages = 3
	// feedBudgetShare is the part of an indexer's query allowance the reads
	// may use, counting every page of every read: one tenth.
	feedBudgetShare = 10
	// A failed read is retried after feedFailureBackoff, doubling up to
	// feedFailureCap, so a provider that cannot list its newest uploads costs
	// next to nothing and never keeps the indexer in cooldown for searches.
	feedFailureBackoff = time.Hour
	feedFailureCap     = 24 * time.Hour
	// feedQuiet is the least time between two searches of one title that the
	// list brings forward; feedAdvanceLimit bounds how many titles one tick
	// brings forward.
	feedQuiet        = 30 * time.Minute
	feedAdvanceLimit = 10
	// feedTitlesMaxAge is how long the list of waiting titles is reused.
	feedTitlesMaxAge = 3 * time.Minute
	// feedSeenLimit bounds the remembered upload IDs per indexer.
	feedSeenLimit = 5000
)

// feedCategories are the standard Newznab root categories for movies and TV,
// used when the indexer has no categories of its own configured.
var feedCategories = []int{2000, 5000}

// FeedWatcher reads the newest uploads of every enabled, commissioned Newznab
// indexer and brings the search of matching waiting titles forward.
type FeedWatcher struct {
	Search *SearchService
	Now    func() time.Time
	// OpenRounds lets a match open the next round of attempts for a title
	// whose round is used up, where the owner's own "start next round" would
	// be accepted and at most once a day per title. Without it a new round
	// stays the owner's action.
	OpenRounds bool

	seen     map[uuid.UUID]*feedSeen
	titles   *feedTitles
	titlesAt time.Time
}

// Run reads until ctx ends. Every failure is logged and retried on a later
// tick; the watcher never stops the worker.
func (watcher *FeedWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(feedTick)
	defer ticker.Stop()
	for {
		if err := watcher.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Newest-uploads watching: a pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (watcher *FeedWatcher) now() time.Time {
	if watcher.Now != nil {
		return watcher.Now().UTC()
	}
	return time.Now().UTC()
}

// RunOnce reads every indexer that is due and then brings matched searches
// forward.
func (watcher *FeedWatcher) RunOnce(ctx context.Context) error {
	store := watcher.Search.Store
	integrations, err := store.ListIntegrations(ctx)
	if err != nil {
		return err
	}
	states, err := store.IndexerFeedStates(ctx)
	if err != nil {
		return err
	}
	for _, integration := range integrations {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var config indexerConfig
		if integration.Kind != "newznab" || !integration.Enabled || json.Unmarshal(integration.Config, &config) != nil || !config.ModeVerified {
			continue
		}
		state := states[integration.ID]
		state.IndexerInstanceID = integration.ID
		now := watcher.now()
		if state.NextReadAt != nil && state.NextReadAt.After(now) {
			continue
		}
		titles, err := watcher.waitingTitles(ctx, now)
		if err != nil {
			return err
		}
		next, matches := watcher.read(ctx, integration, config, state, titles)
		inserted, err := store.RecordIndexerFeedRead(ctx, next, matches, watcher.now())
		if err != nil {
			// The matches were not saved: forget what this read saw, so the
			// next read compares the same uploads again.
			delete(watcher.seen, integration.ID)
			return err
		}
		if inserted > 0 {
			slog.Info("Newest-uploads watching: new uploads match waiting titles", "indexer", integration.Name, "matches", inserted)
		}
	}
	result, err := store.AdvanceFeedMatches(ctx, watcher.now(), feedQuiet, feedAdvanceLimit, watcher.OpenRounds)
	if err != nil {
		return err
	}
	if result.SearchAdvanced+result.DiscoveryAdvanced > 0 {
		slog.Info("Newest-uploads watching: searches brought forward", "searches", result.SearchAdvanced, "discoveries", result.DiscoveryAdvanced)
	}
	if result.RoundsOpened > 0 {
		slog.Info("Newest-uploads watching: new rounds opened for titles with a new eligible release", "rounds", result.RoundsOpened)
	}
	if !watcher.OpenRounds {
		return nil
	}
	rescan, err := store.OpenRescanFoundRounds(ctx, watcher.now(), feedAdvanceLimit)
	if err != nil {
		return err
	}
	if rescan.RoundsOpened > 0 {
		slog.Info("Automatic rounds: new rounds opened for titles whose rescan found a new eligible release", "rounds", rescan.RoundsOpened)
	}
	return nil
}

func (watcher *FeedWatcher) waitingTitles(ctx context.Context, now time.Time) (*feedTitles, error) {
	if watcher.titles != nil && now.Sub(watcher.titlesAt) < feedTitlesMaxAge {
		return watcher.titles, nil
	}
	waiting, err := watcher.Search.Store.ListFeedWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	watcher.titles, watcher.titlesAt = newFeedTitles(waiting), now
	return watcher.titles, nil
}

// read asks one indexer for its newest uploads and returns the reader state
// to save together with the uploads that match a waiting title.
func (watcher *FeedWatcher) read(ctx context.Context, integration postgres.Integration, config indexerConfig, state postgres.IndexerFeedState, titles *feedTitles) (postgres.IndexerFeedState, []postgres.FeedMatch) {
	service := watcher.Search
	budget := indexers.NewznabBudget(config.MinimumIntervalMS, config.QueryLimit, config.GrabLimit, config.BudgetWindow)
	interval := feedInterval(time.Duration(state.IntervalSeconds)*time.Second, budget)
	failed := func(outcome string) (postgres.IndexerFeedState, []postgres.FeedMatch) {
		now := watcher.now()
		state.ConsecutiveFailures++
		retry := now.Add(feedFailureDelay(state.ConsecutiveFailures))
		state.LastReadAt, state.NextReadAt, state.LastOutcome, state.IntervalSeconds = &now, &retry, outcome, int(interval/time.Second)
		return state, nil
	}
	if err := service.Scheduler.Configure(integration.ID.String(), budget); err != nil {
		return failed("indexer_configuration_unavailable")
	}
	secret, err := service.ResolveSecret(integration.SecretRef)
	if err != nil {
		return failed("indexer_secret_unavailable")
	}
	client, err := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: config.BaseURL, APIKey: secret, AllowHTTP: config.AllowHTTP}, service.Scheduler, service.Transport)
	if err != nil {
		return failed("indexer_client_invalid")
	}
	request := newznab.SearchRequest{Mode: "search", Categories: feedRequestCategories(config), Limit: searchPageLimit(config), Extended: true}
	seen := watcher.seenFor(integration.ID)
	firstRead := seen.empty()
	pages, err := readFeedPages(request, seen, func(next newznab.SearchRequest) (newznab.SearchPage, error) {
		return client.FeedPage(ctx, next)
	})
	if err != nil {
		var deferred interface{ RetryAt() time.Time }
		if errors.As(err, &deferred) {
			// Pacing, a cooldown or a used-up allowance: not a failure of the
			// list. Read when the indexer is free again.
			now := watcher.now()
			retry := deferred.RetryAt().UTC().Add(time.Second)
			if retry.Before(now.Add(feedTick)) {
				retry = now.Add(feedTick)
			}
			state.NextReadAt, state.LastOutcome, state.IntervalSeconds = &retry, "deferred", int(interval/time.Second)
			return state, nil
		}
		return failed(safeIndexerOutcomeCode(err))
	}
	now := watcher.now()
	interval = nextFeedInterval(interval, pages.firstPageFresh, pages.firstPageSize, firstRead, budget)
	next := now.Add(interval)
	state.LastReadAt, state.NextReadAt, state.LastOutcome, state.ConsecutiveFailures = &now, &next, "ok", 0
	state.IntervalSeconds, state.LastItemCount, state.LastNewCount = int(interval/time.Second), pages.items, len(pages.fresh)
	return state, titles.match(pages.fresh, now)
}

// feedPages is what one read of an indexer's newest uploads returned.
type feedPages struct {
	// items counts every upload listed; fresh holds those not seen before.
	items int
	fresh []releases.Observation
	// firstPageFresh of the firstPageSize uploads on the first page were new.
	firstPageFresh, firstPageSize int
}

// readFeedPages reads the first page and, while a whole page holds nothing
// seen before, up to feedMaxPages pages: uploads between this read and the
// last one may then lie further down. The very first read has nothing to
// catch up with and stays at one page. A failed first page is the read's
// error; a failed later page keeps what was read, and the next read goes on
// from there. Uploads without a provider ID of their own are skipped: they
// cannot be recognised again.
func readFeedPages(request newznab.SearchRequest, seen *feedSeen, fetch func(newznab.SearchRequest) (newznab.SearchPage, error)) (feedPages, error) {
	var pages feedPages
	firstRead := seen.empty()
	for page := 0; page < feedMaxPages; page++ {
		result, err := fetch(request)
		if err != nil {
			if page == 0 {
				return pages, err
			}
			break
		}
		pageFresh := 0
		for _, observation := range result.Observations {
			if observation.NativeGUID == "" || strings.HasPrefix(observation.NativeGUID, "weak:") {
				continue
			}
			if seen.add(observation.NativeGUID) {
				pageFresh++
				pages.fresh = append(pages.fresh, observation)
			}
		}
		pages.items += len(result.Observations)
		if page == 0 {
			pages.firstPageFresh, pages.firstPageSize = pageFresh, len(result.Observations)
		}
		if firstRead || len(result.Observations) == 0 || pageFresh < len(result.Observations) {
			break
		}
		request.Offset += len(result.Observations)
	}
	return pages, nil
}

// feedRequestCategories are the categories whose newest uploads are read:
// the indexer's own search categories, else the movie and series categories
// the owner selected for it, else the standard movie and TV roots.
func feedRequestCategories(config indexerConfig) []int {
	if len(config.Categories) > 0 {
		return config.Categories
	}
	if len(config.MovieCategoryIDs) > 0 && len(config.SeriesCategoryIDs) > 0 && !config.CategorySelectionReviewRequired {
		selected := append(append([]int{}, config.MovieCategoryIDs...), config.SeriesCategoryIDs...)
		if len(selected) <= maxPlannedCategoryIDs {
			return selected
		}
	}
	return feedCategories
}

func (watcher *FeedWatcher) seenFor(indexerID uuid.UUID) *feedSeen {
	if watcher.seen == nil {
		watcher.seen = map[uuid.UUID]*feedSeen{}
	}
	if watcher.seen[indexerID] == nil {
		watcher.seen[indexerID] = &feedSeen{known: map[string]bool{}}
	}
	return watcher.seen[indexerID]
}

// feedInterval is the saved read interval within its bounds. The lower bound
// also keeps the reads, with every page they may take, inside their share of
// the indexer's query allowance.
func feedInterval(saved time.Duration, budget indexers.Budget) time.Duration {
	interval := saved
	if interval <= 0 {
		interval = feedStartInterval
	}
	floor := feedMinInterval
	if budget.QueryLimit > 0 && budget.LimitWindow > 0 {
		// A read may take feedMaxPages requests, so every read is counted as
		// that many against the share.
		byBudget := budget.LimitWindow * feedMaxPages * feedBudgetShare / time.Duration(budget.QueryLimit)
		if byBudget > floor {
			floor = byBudget
		}
	}
	if interval > feedMaxInterval {
		interval = feedMaxInterval
	}
	if interval < floor {
		interval = floor
	}
	return interval
}

// nextFeedInterval follows the indexer's upload rate: read sooner when most
// of the first page is new, later when little of it is.
func nextFeedInterval(current time.Duration, fresh, pageSize int, firstRead bool, budget indexers.Budget) time.Duration {
	next := current
	switch {
	case firstRead || pageSize == 0:
	case fresh*10 >= pageSize*6:
		next = current * 2 / 3
	case fresh*10 < pageSize*2:
		next = current * 3 / 2
	}
	return feedInterval(next, budget)
}

func feedFailureDelay(consecutive int) time.Duration {
	delay := feedFailureBackoff
	for i := 1; i < consecutive && delay < feedFailureCap; i++ {
		delay *= 2
	}
	if delay > feedFailureCap {
		delay = feedFailureCap
	}
	return delay
}

// feedSeen remembers the newest upload IDs of one indexer, oldest forgotten
// first. It lives in memory: after a restart everything on the first page is
// new once, and matches already saved are not saved again.
type feedSeen struct {
	known map[string]bool
	order []string
}

func (seen *feedSeen) empty() bool { return len(seen.known) == 0 }

// add reports whether id was new.
func (seen *feedSeen) add(id string) bool {
	if seen.known[id] {
		return false
	}
	seen.known[id] = true
	seen.order = append(seen.order, id)
	if len(seen.order) > feedSeenLimit {
		drop := len(seen.order) - feedSeenLimit
		for _, old := range seen.order[:drop] {
			delete(seen.known, old)
		}
		seen.order = append([]string(nil), seen.order[drop:]...)
	}
	return true
}

// feedTitles is the list of waiting titles in a form new uploads can be
// compared with quickly.
type feedTitles struct {
	index *releases.TitleIndex
	media []feedMedia
}

type feedMedia struct {
	media     catalog.MediaItem
	workflows []feedWorkflow
}

type feedWorkflow struct {
	id    uuid.UUID
	scope []releases.Rule
}

func newFeedTitles(waiting []postgres.FeedWorkflow) *feedTitles {
	titles := &feedTitles{index: releases.NewTitleIndex()}
	position := map[uuid.UUID]int{}
	for _, item := range waiting {
		scope, err := workflowScopeRules(item.Variant)
		if err != nil {
			// A variant the search itself refuses is never matched.
			continue
		}
		entry, known := position[item.Media.ID]
		if !known {
			entry = len(titles.media)
			position[item.Media.ID] = entry
			titles.media = append(titles.media, feedMedia{media: item.Media})
			year := uint16(0)
			if item.Media.Kind == catalog.KindMovie && item.Media.Year != nil {
				year = *item.Media.Year
			}
			for _, title := range item.Media.SavedTitles() {
				titles.index.Add(title, year, entry)
			}
		}
		titles.media[entry].workflows = append(titles.media[entry].workflows, feedWorkflow{id: item.WorkflowID, scope: scope})
	}
	return titles
}

// match returns the uploads that a waiting workflow's search could accept as
// far as the name and the provider's IDs can tell: the release begins with a
// saved title, no provider ID contradicts the verified identity, the identity
// is positively supported, and the season and episode fit the workflow. These
// are the search's own checks (constrainMediaMatch, workflowScopeRules), so
// the list never matches more loosely than a search would.
func (titles *feedTitles) match(uploads []releases.Observation, now time.Time) []postgres.FeedMatch {
	var matches []postgres.FeedMatch
	for _, upload := range uploads {
		candidates := titles.index.Lookup(upload.Title)
		if len(candidates) == 0 {
			continue
		}
		observation := releases.InferTitleFacts(upload, now)
		for _, entry := range candidates {
			item := titles.media[entry]
			for _, workflow := range item.workflows {
				// A movie workflow has no scope rules; every upload starts
				// eligible, as in the search.
				evaluation := releases.Evaluation{ObservationID: observation.ID, Verdict: releases.VerdictEligible}
				if len(workflow.scope) > 0 {
					var err error
					if evaluation, err = releases.Evaluate(observation, workflow.scope); err != nil {
						continue
					}
				}
				if constrainMediaMatch(observation, evaluation, item.media).Verdict != releases.VerdictEligible {
					continue
				}
				matches = append(matches, postgres.FeedMatch{WorkflowID: workflow.id, NativeGUID: upload.NativeGUID, ReleaseTitle: upload.Title})
			}
		}
	}
	return matches
}
