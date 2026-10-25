package acquisition

// Interactive browsing shares the production Newznab adapter and durable budget
// scheduler, but never creates an acquisition workflow or selects a release.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"golang.org/x/sync/singleflight"
)

var ErrBrowseExpired = errors.New("release choice expired; refresh the stream list")
var ErrBrowseInput = errors.New("invalid streaming search")
var browseIMDb = regexp.MustCompile(`^tt[0-9]{1,10}$`)

type BrowseInput struct {
	Kind    catalog.MediaKind `json:"kind"`
	IMDb    string            `json:"imdb_id"`
	Title   string            `json:"title"`
	Year    uint16            `json:"year"`
	Season  uint16            `json:"season"`
	Episode uint16            `json:"episode"`
}

func (in BrowseInput) Valid() bool {
	return browseIMDb.MatchString(in.IMDb) && len(strings.TrimSpace(in.Title)) > 0 && len(in.Title) <= 250 && in.Year >= 1880 && in.Year <= 2200 &&
		((in.Kind == catalog.KindMovie && in.Season == 0 && in.Episode == 0) || (in.Kind == catalog.KindSeries && in.Season > 0 && in.Season <= 999 && in.Episode > 0 && in.Episode <= 9999))
}

type BrowseRelease struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Indexer    string          `json:"indexer"`
	Facts      []releases.Fact `json:"facts"`
	ObservedAt time.Time       `json:"observed_at"`
}
type BrowseProvider struct {
	Name  string `json:"name"`
	State string `json:"state"`
}
type BrowseResult struct {
	Releases  []BrowseRelease  `json:"releases"`
	Providers []BrowseProvider `json:"providers"`
	ExpiresAt time.Time        `json:"expires_at"`
}
type browseChoice struct {
	input       BrowseInput
	integration postgres.Integration
	observation releases.Observation
	expires     time.Time
}
type browseCache struct {
	result BrowseResult
	until  time.Time
}
type BrowseService struct {
	Search  *SearchService
	mu      sync.Mutex
	choices map[string]browseChoice
	cache   map[BrowseInput]browseCache
	flights singleflight.Group
	active  int
}

func (s *BrowseService) Browse(ctx context.Context, in BrowseInput) (BrowseResult, error) {
	if !in.Valid() {
		return BrowseResult{}, ErrBrowseInput
	}
	in.Title = strings.TrimSpace(in.Title)
	s.mu.Lock()
	cached, ok := s.cache[in]
	s.mu.Unlock()
	if ok && time.Now().Before(cached.until) {
		return cached.result, nil
	}
	key, _ := json.Marshal(in)
	result := s.flights.DoChan(string(key), func() (any, error) {
		// A single disconnected viewer must not cancel a shared bounded search.
		work, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		return s.search(work, in)
	})
	select {
	case <-ctx.Done():
		return BrowseResult{}, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return BrowseResult{}, r.Err
		}
		return r.Val.(BrowseResult), nil
	}
}
func (s *BrowseService) search(ctx context.Context, in BrowseInput) (BrowseResult, error) {
	s.mu.Lock()
	if s.active >= 4 {
		s.mu.Unlock()
		return BrowseResult{}, errors.New("streaming search capacity reached")
	}
	s.active++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	all, err := s.Search.Store.ListIntegrations(ctx)
	if err != nil {
		return BrowseResult{}, err
	}
	media := catalog.MediaItem{Kind: in.Kind, Title: in.Title, Year: &in.Year, Identities: []catalog.ExternalIdentity{{Namespace: "imdb", Value: in.IMDb, State: catalog.IdentityVerified, Source: "addon"}}}
	// Prefer saved verified identity, original title and aliases when available.
	id, found, archived, err := s.Search.Store.MediaIDByIdentities(ctx, in.Kind, media.Identities)
	if err != nil {
		return BrowseResult{}, err
	}
	if found && !archived {
		media, err = s.Search.Store.GetMedia(ctx, id)
		if err != nil {
			return BrowseResult{}, err
		}
	}
	search := postgres.SearchContext{Media: media, Variant: "default"}
	if in.Kind == catalog.KindSeries {
		search.Variant = fmt.Sprintf("season:%02d:episode:%02d", in.Season, in.Episode)
	}
	result := BrowseResult{Releases: []BrowseRelease{}, Providers: []BrowseProvider{}, ExpiresAt: time.Now().UTC().Add(6 * time.Hour)}
	choices := map[string]browseChoice{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	lanes := make(chan struct{}, 3)
	for _, integration := range all {
		var cfg indexerConfig
		if integration.Kind != "newznab" || !integration.Enabled || json.Unmarshal(integration.Config, &cfg) != nil || !cfg.ModeVerified {
			continue
		}
		wg.Add(1)
		go func(i postgres.Integration, c indexerConfig) {
			defer wg.Done()
			select {
			case lanes <- struct{}{}:
				defer func() { <-lanes }()
			case <-ctx.Done():
				mu.Lock()
				result.Providers = append(result.Providers, BrowseProvider{Name: i.Name, State: "unavailable"})
				mu.Unlock()
				return
			}
			provider := BrowseProvider{Name: i.Name, State: "unavailable"}
			var observations []releases.Observation
			client, e := s.client(i, c)
			if e == nil {
				request := buildRequest(search, c)
				if plan, pe := s.Search.searchProfilePlan(ctx, i.ID, search, c); pe == nil {
					request = plan.Primary.Request
				} else if !errors.Is(pe, errNoActiveSearchProfile) {
					e = pe
				}
				request.Extended = true
				if e == nil {
					observations, e = client.Search(ctx, request)
				}
			}
			if e == nil {
				provider.State = "complete"
			} else {
				var retry interface{ RetryAt() time.Time }
				if errors.As(e, &retry) {
					provider.State = "deferred"
				}
			}

			// Extended search already includes media attributes on many indexers.
			// Bound optional enrichment to one matching release per provider so
			// opening a title cannot fan out hundreds of API requests.
			if e == nil {
				for n, observation := range observations {
					if hasProviderSubtitles(observation) || !browseMatches(releases.InferTitleFacts(observation, time.Now().UTC()), media, in) {
						continue
					}
					enriched, inspectErr := client.InspectMedia(ctx, observation)
					if waitBrowsePacing(ctx, inspectErr) {
						enriched, inspectErr = client.InspectMedia(ctx, observation)
					}
					if inspectErr == nil {
						observation = enriched
					}
					if !hasProviderSubtitles(observation) && advertisesNFO(observation) {
						enriched, inspectErr = client.InspectNFO(ctx, observation)
						if waitBrowsePacing(ctx, inspectErr) {
							enriched, inspectErr = client.InspectNFO(ctx, observation)
						}
						if inspectErr == nil {
							observation = enriched
						}
					}
					observations[n] = observation
					break
				}
			}
			mu.Lock()
			defer mu.Unlock()
			result.Providers = append(result.Providers, provider)
			seen := map[string]bool{}
			for _, o := range observations {
				if len(choices) >= 300 {
					break
				}
				if seen[o.NativeGUID] {
					continue
				}
				seen[o.NativeGUID] = true
				o = releases.InferTitleFacts(o, time.Now().UTC())
				if !browseMatches(o, media, in) {
					continue
				}
				if _, e := newznab.RetrievalID(o.NativeGUID, c.BaseURL); e != nil {
					continue
				}
				o.Fields = nil // Only exact identity, locator and normalized display facts are retained.
				token := uuid.NewV4().String()
				choices[token] = browseChoice{input: in, integration: i, observation: o, expires: result.ExpiresAt}
				result.Releases = append(result.Releases, BrowseRelease{ID: token, Title: o.Title, Indexer: i.Name, Facts: o.Facts, ObservedAt: o.ObservedAt})
			}
		}(integration, cfg)
	}
	wg.Wait()
	sort.Slice(result.Providers, func(i, j int) bool { return result.Providers[i].Name < result.Providers[j].Name })
	sort.SliceStable(result.Releases, func(i, j int) bool {
		a, b := result.Releases[i], result.Releases[j]
		ra, rb := browseRank(a.Facts), browseRank(b.Facts)
		if ra != rb {
			return ra > rb
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.Indexer < b.Indexer
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.choices == nil {
		s.choices = map[string]browseChoice{}
		s.cache = map[BrowseInput]browseCache{}
	}
	now := time.Now()
	for k, v := range s.choices {
		if now.After(v.expires) {
			delete(s.choices, k)
		}
	}
	for k, v := range s.cache {
		if now.After(v.until) {
			delete(s.cache, k)
		}
	}
	// Do not invalidate choices that a player may still have cached.
	if len(s.choices)+len(choices) > 10000 || len(s.cache) >= 128 {
		return BrowseResult{}, errors.New("streaming search capacity reached")
	}
	for k, v := range choices {
		s.choices[k] = v
	}
	ttl := 5 * time.Minute
	for _, p := range result.Providers {
		if p.State != "complete" {
			ttl = 30 * time.Second
			break
		}
	}
	s.cache[in] = browseCache{result: result, until: now.Add(ttl)}
	return result, nil
}
func (s *BrowseService) client(i postgres.Integration, c indexerConfig) (*newznab.Client, error) {
	if err := s.Search.Scheduler.Configure(i.ID.String(), indexers.NewznabBudget(c.MinimumIntervalMS, c.QueryLimit, c.GrabLimit, c.BudgetWindow)); err != nil {
		return nil, err
	}
	secret, err := s.Search.ResolveSecret(i.SecretRef)
	if err != nil {
		return nil, err
	}
	return newznab.New(newznab.Config{IndexerID: i.ID.String(), BaseURL: c.BaseURL, APIKey: secret, AllowHTTP: c.AllowHTTP, Timeout: 20 * time.Second}, s.Search.Scheduler, s.Search.Transport)
}

// Download is called only when the viewer selects a choice. Credentials and
// provider locators stay in this process; the addon receives validated NZB bytes.
func (s *BrowseService) Download(ctx context.Context, token string) ([]byte, string, BrowseInput, error) {
	s.mu.Lock()
	choice, ok := s.choices[token]
	s.mu.Unlock()
	if !ok || time.Now().After(choice.expires) {
		return nil, "", BrowseInput{}, ErrBrowseExpired
	}
	guard := func(ctx context.Context) error {
		all, e := s.Search.Store.ListIntegrations(ctx)
		if e != nil {
			return e
		}
		for _, i := range all {
			if i.ID == choice.integration.ID && i.Enabled && i.SecretRef == choice.integration.SecretRef && string(i.Config) == string(choice.integration.Config) {
				return nil
			}
		}
		return errors.New("indexer configuration changed; refresh the stream list")
	}
	if e := guard(ctx); e != nil {
		return nil, "", BrowseInput{}, e
	}
	var cfg indexerConfig
	if e := json.Unmarshal(choice.integration.Config, &cfg); e != nil {
		return nil, "", BrowseInput{}, e
	}
	client, e := s.client(choice.integration, cfg)
	if e != nil {
		return nil, "", BrowseInput{}, e
	}
	native, e := newznab.RetrievalID(choice.observation.NativeGUID, cfg.BaseURL)
	if e != nil {
		return nil, "", BrowseInput{}, e
	}
	locator := newznab.DownloadLocatorReference(choice.observation.DownloadURL, choice.observation.NativeGUID)
	nzb, e := client.GetNZBPackageFromLocatorGuarded(ctx, native, locator, guard)
	if e != nil {
		return nil, "", BrowseInput{}, e
	}
	// Stable across searches and restarts, distinct for different exact payloads.
	title := regexp.MustCompile(`[^a-zA-Z0-9._ -]+`).ReplaceAllString(choice.observation.Title, "_")
	if len(title) > 140 {
		title = title[:140]
	}
	hash := sha256.Sum256(nzb.Body)
	filename := fmt.Sprintf("%s.mm-%x.nzb", title, hash[:12])
	return nzb.Body, filename, choice.input, nil
}
func browseMatches(o releases.Observation, media catalog.MediaItem, in BrowseInput) bool {
	verdict := constrainMediaMatch(o, releases.Evaluation{Verdict: releases.VerdictEligible}, media)
	if verdict.Verdict != releases.VerdictEligible {
		return false
	}
	if in.Kind == catalog.KindMovie {
		return true
	}
	season, episode, pack := false, false, false
	for _, f := range o.Facts {
		if f.Name == "release_scope" && (f.State == releases.FactObserved || f.State == releases.FactInferred) {
			for _, v := range f.Values {
				if v == "season_pack" {
					pack = true
				}
			}
		}
	}
	for _, f := range o.Facts {
		if f.State != releases.FactObserved && f.State != releases.FactInferred {
			if f.Name == "season_number" || f.Name == "episode_number" || f.Name == "episode" {
				return false
			}
			continue
		}
		switch f.Name {
		case "season_number":
			for _, v := range f.Values {
				if v != strconv.Itoa(int(in.Season)) {
					return false
				}
				season = true
			}
		case "episode_number", "episode":
			for _, v := range f.Values {
				n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(v), "e"))
				if err == nil && n == 0 && pack {
					continue
				}
				if err != nil || n != int(in.Episode) {
					return false
				}
				episode = true
			}
		case "release_scope":
			for _, v := range f.Values {
				if v == "season_pack" {
					pack = true
				}
			}
		}
	}
	return season && (episode || pack)
}
func browseRank(facts []releases.Fact) int {
	rank := 0
	for _, f := range facts {
		if f.State != releases.FactObserved && f.State != releases.FactInferred {
			continue
		}
		for _, v := range f.Values {
			if f.Name == "subtitle_languages" && v == "da" {
				if f.State == releases.FactObserved {
					rank += 100000
				} else {
					rank += 50000
				}
			}
			if f.Name == "resolution" {
				switch v {
				case "2160p":
					rank += 400
				case "1080p":
					rank += 300
				case "720p":
					rank += 200
				}
			}
		}
	}
	return rank
}

func hasProviderSubtitles(o releases.Observation) bool {
	for _, f := range o.Facts {
		if f.Name == "subtitle_languages" && f.State == releases.FactObserved && len(f.Values) > 0 {
			return true
		}
	}
	return false
}
func advertisesNFO(o releases.Observation) bool {
	for _, f := range o.Fields {
		if strings.EqualFold(f.Name, "nfo") && (f.Value == "1" || strings.EqualFold(f.Value, "yes")) {
			return true
		}
	}
	return false
}
func waitBrowsePacing(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	var retry interface{ RetryAt() time.Time }
	if !errors.As(err, &retry) {
		return false
	}
	delay := time.Until(retry.RetryAt()) + 50*time.Millisecond
	if delay < 0 {
		delay = 0
	}
	if delay > 5*time.Second {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
