package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

type SecretResolver func(string) (string, error)

type SearchService struct {
	Store         *postgres.Store
	Scheduler     *indexers.Scheduler
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
}

type indexerConfig struct {
	BaseURL                         string                 `json:"base_url"`
	AllowHTTP                       bool                   `json:"allow_http"`
	MinimumIntervalMS               int64                  `json:"minimum_interval_ms"`
	Mode                            string                 `json:"mode"`
	ModeVerified                    bool                   `json:"mode_verified"`
	Categories                      []int                  `json:"categories"`
	MovieCategoryIDs                []int                  `json:"movie_category_ids"`
	SeriesCategoryIDs               []int                  `json:"series_category_ids"`
	CategorySelectionReviewRequired bool                   `json:"category_selection_review_required"`
	Extended                        bool                   `json:"extended"`
	SearchDialects                  newznab.SearchDialects `json:"search_dialects"`
	DefaultLimit                    int                    `json:"default_limit"`
	MaximumLimit                    int                    `json:"maximum_limit"`
	QueryLimit                      int                    `json:"query_limit"`
	GrabLimit                       int                    `json:"grab_limit"`
	BudgetWindow                    int64                  `json:"budget_window_seconds"`
}

const requiredSubtitleEvidenceWeight = 100_000

type recoveryIndexersUnavailableError struct{ until time.Time }

func (err recoveryIndexersUnavailableError) Error() string {
	return "no enabled commissioned Newznab indexer is available for recovery discovery"
}
func (err recoveryIndexersUnavailableError) SafeCode() string   { return "recovery_indexers_unavailable" }
func (err recoveryIndexersUnavailableError) RetryAt() time.Time { return err.until.UTC() }

func (service *SearchService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	return service.handle(ctx, event, false)
}

func (service *SearchService) HandleDiscovery(ctx context.Context, event postgres.OutboxEvent) error {
	return service.handle(ctx, event, true)
}

func (service *SearchService) handle(ctx context.Context, event postgres.OutboxEvent, recovery bool) error {
	search, err := service.Store.LoadSearchContext(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if recovery {
		if err := service.Store.ValidateRecoveryDiscovery(ctx, event.AggregateID, int64(event.AggregateRevision)); err != nil {
			if errors.Is(err, postgres.ErrConcurrentModification) {
				return nil
			}
			return err
		}
		indexerFilter, filterErr := service.Store.RecoveryIndexerFilter(ctx, event.AggregateID, int64(event.AggregateRevision))
		if filterErr != nil {
			if errors.Is(filterErr, postgres.ErrConcurrentModification) {
				return nil
			}
			return filterErr
		}
		if indexerFilter != nil {
			filtered := search.Indexers[:0]
			for _, integration := range search.Indexers {
				if indexerFilter[integration.ID.String()] {
					filtered = append(filtered, integration)
				}
			}
			search.Indexers = filtered
		}
	} else if search.Workflow.State == workflows.StateCandidateSelected || search.Workflow.State == workflows.StateQueued || search.Workflow.State == workflows.StateSubmitting {
		return nil
	}
	if !recovery {
		if search.OpenRound == nil {
			// A closed or cancelled round can still have an older leased search
			// event. It is a stale audited no-op, never a reason to create a round.
			return nil
		}
		if search.OpenRound.PolicyVersion != search.ProfilePolicyVersion {
			// The profile changed after this round opened. Park the workflow for
			// the owner to apply the current policy instead of searching under
			// the stale snapshot or retrying a failing search forever.
			err := service.Store.MarkQualityPolicyReviewRequired(ctx, search.Workflow.ID, search.Workflow.Revision, search.OpenRound.PolicyVersion, search.ProfilePolicyVersion, time.Now().UTC())
			if errors.Is(err, postgres.ErrConcurrentModification) {
				return nil
			}
			return err
		}
		if search.OpenRound.UpgradeBaseline != search.CurrentLinkVersions {
			return errors.New("acquisition round snapshot changed before candidate selection; owner review is required")
		}
	}
	if len(search.Indexers) == 0 {
		if recovery {
			return recoveryIndexersUnavailableError{until: time.Now().UTC().Add(24 * time.Hour)}
		}
		return errors.New("no enabled commissioned Newznab indexer is available for this search")
	}
	if !recovery && search.Media.Kind == catalog.KindSeries && search.Workflow.Revision == event.AggregateRevision {
		if scope, ok := workflows.ParseVariant(search.Variant); ok && scope.Kind == workflows.VariantSeason {
			dispatched, dispatchErr := service.Store.DispatchAiringSeason(ctx, search.Workflow.ID, time.Now().UTC())
			if dispatchErr != nil {
				return dispatchErr
			}
			if dispatched {
				// A currently airing or partially acquired season is coordinated as
				// individual released episodes. The season workflow does not search
				// for a complete pack until the season is complete and entirely
				// missing.
				return nil
			}
		}
	}
	if recovery {
		// Recovery discovery is an evidence-only sub-workflow. It never changes
		// the acquisition workflow to searching or selects a release.
	} else if search.Workflow.State == workflows.StateSearching {
		if search.Workflow.Revision != event.AggregateRevision && search.Workflow.Revision != event.AggregateRevision+1 {
			return nil
		}
	} else {
		if search.Workflow.State != workflows.StateMonitored && search.Workflow.State != workflows.StateWaiting && search.Workflow.State != workflows.StateHeld || search.Workflow.Revision != event.AggregateRevision {
			return nil
		}
		search.Workflow, err = service.Store.TransitionWorkflow(ctx, search.Workflow.ID, search.Workflow.Revision, workflows.StateSearching, "targeted search requested", "worker", time.Now().UTC())
		if err != nil {
			return err
		}
	}
	rules, err := parseRules(search.Policy)
	if err != nil {
		return err
	}
	subtitlePolicy, err := subtitles.ParsePolicy(search.Policy)
	if err != nil {
		return err
	}
	rules = appendSubtitleRules(rules, subtitlePolicy)
	scopeRules, scopeErr := workflowScopeRules(search.Variant)
	if scopeErr != nil {
		return scopeErr
	}
	rules = append(scopeRules, rules...)
	var currentEvaluation *releases.Evaluation
	if search.CurrentRelease != nil {
		evaluatedCurrent, evaluateErr := releases.Evaluate(*search.CurrentRelease, rules)
		if evaluateErr != nil {
			return evaluateErr
		}
		currentEvaluation = &evaluatedCurrent
	}
	var evaluated []postgres.EvaluatedObservation
	successfulIndexers := 0
	var earliestDeferral error
	outcomes := make([]postgres.SearchIndexerOutcome, 0, len(search.Indexers))
	for _, integration := range search.Indexers {
		outcome := postgres.SearchIndexerOutcome{IndexerInstanceID: integration.ID, State: "failed", Detail: "indexer_configuration_unavailable"}
		var config indexerConfig
		if err := json.Unmarshal(integration.Config, &config); err != nil {
			outcomes = append(outcomes, outcome)
			continue
		}
		secret, err := service.ResolveSecret(integration.SecretRef)
		if err != nil {
			outcome.Detail = "indexer_secret_unavailable"
			outcomes = append(outcomes, outcome)
			continue
		}
		budget := indexers.NewznabBudget(config.MinimumIntervalMS, config.QueryLimit, config.GrabLimit, config.BudgetWindow)
		if err := service.Scheduler.Configure(integration.ID.String(), budget); err != nil {
			return err
		}
		client, err := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: config.BaseURL, APIKey: secret, AllowHTTP: config.AllowHTTP}, service.Scheduler, service.Transport)
		if err != nil {
			outcome.Detail = "indexer_client_invalid"
			outcomes = append(outcomes, outcome)
			continue
		}
		if recovery {
			if err := service.Store.ValidateRecoveryDiscovery(ctx, event.AggregateID, int64(event.AggregateRevision)); err != nil {
				if errors.Is(err, postgres.ErrConcurrentModification) {
					return nil
				}
				return err
			}
		}
		executeSearch := func(request newznab.SearchRequest) ([]releases.Observation, error) {
			if recovery {
				return client.BackgroundSearchGuarded(ctx, request, func(guardCtx context.Context) error {
					return service.Store.ValidateRecoveryDiscovery(guardCtx, event.AggregateID, int64(event.AggregateRevision))
				})
			}
			return client.Search(ctx, request)
		}
		// Prefer the immutable active capability profile. When it is unavailable
		// or cannot satisfy this item, keep the legacy commissioned dialect so an
		// indexer that has not completed profile-backed commissioning still works.
		// Only the primary recipe is executed here: the scheduler enforces the
		// per-indexer minimum interval, so a second same-call fallback would be
		// deferred back to the same zero-result state forever. A future milestone
		// may schedule the fallback as its own paced attempt.
		request := buildRequest(search, config)
		if plan, planErr := service.searchProfilePlan(ctx, integration.ID, search, config); planErr == nil {
			request = plan.Primary.Request
		} else if !errors.Is(planErr, errNoActiveSearchProfile) {
			outcome.Detail = safeIndexerOutcomeCode(planErr)
			outcomes = append(outcomes, outcome)
			continue
		}
		var observations []releases.Observation
		observations, err = executeSearch(request)
		if err != nil {
			if recovery && errors.Is(err, postgres.ErrConcurrentModification) {
				return nil
			}
			var current interface{ RetryAt() time.Time }
			if errors.As(err, &current) {
				retryAt := current.RetryAt().UTC()
				outcome.State, outcome.RetryAt, outcome.Detail = "deferred", &retryAt, safeIndexerOutcomeCode(err)
				var earliest interface{ RetryAt() time.Time }
				if earliestDeferral == nil || errors.As(earliestDeferral, &earliest) && current.RetryAt().Before(earliest.RetryAt()) {
					earliestDeferral = err
				}
			} else {
				outcome.Detail = safeIndexerOutcomeCode(err)
			}
			outcomes = append(outcomes, outcome)
			continue
		}
		successfulIndexers++
		outcome.State, outcome.Detail = "succeeded", ""
		outcomes = append(outcomes, outcome)
		for _, observation := range observations {
			observation = releases.InferTitleFacts(observation, time.Now().UTC())
			evaluation, evaluateErr := releases.Evaluate(observation, rules)
			if evaluateErr != nil {
				return evaluateErr
			}
			if len(rules) == 0 {
				evaluation.Verdict = releases.VerdictHeld
				evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "quality profile", Decision: releases.DecisionUnknown, Reason: "no release rules are configured"})
			}
			if strings.HasPrefix(observation.NativeGUID, "weak:") {
				evaluation.Verdict = releases.VerdictHeld
				evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "native release identity", Decision: releases.DecisionUnknown, Reason: "provider supplied no retrievable native GUID"})
			}
			evaluation = constrainMediaMatch(observation, evaluation, search.Media)
			evaluation = applyReleaseBlocks(observation, evaluation, search)
			evaluation = applyWorkflowExclusions(observation, evaluation, search)
			evaluation = applyUpgradeBaseline(observation, evaluation, search.CurrentRelease, currentEvaluation)
			evaluated = append(evaluated, postgres.EvaluatedObservation{Observation: observation, Evaluation: evaluation})
		}
	}
	if successfulIndexers == 0 && !recovery {
		if earliestDeferral != nil {
			return earliestDeferral
		}
		return fmt.Errorf("all %d configured indexers failed before returning a valid search response", len(search.Indexers))
	}
	fingerprintInput := search.Workflow.ID.String() + "\x00" + search.Variant + "\x00" + strconv.FormatUint(search.PolicyVersion, 10)
	if recovery {
		fingerprintInput += "\x00recovery\x00" + strconv.FormatUint(event.AggregateRevision, 10)
	}
	fingerprint := sha256.Sum256([]byte(fingerprintInput))
	_, err = service.Store.PersistSearch(ctx, search, fingerprint[:], evaluated, postgres.SearchPersistenceOptions{RecoveryRescan: recovery, RescanGeneration: int64(event.AggregateRevision), IndexerOutcomes: outcomes}, time.Now().UTC())
	if recovery && errors.Is(err, postgres.ErrConcurrentModification) {
		return nil
	}
	return err
}

func safeIndexerOutcomeCode(err error) string {
	var coded interface{ SafeCode() string }
	if errors.As(err, &coded) {
		code := strings.TrimSpace(coded.SafeCode())
		if code != "" && len(code) <= 100 {
			return code
		}
	}
	return "indexer_request_failed"
}

func applyUpgradeBaseline(observation releases.Observation, evaluation releases.Evaluation, current *releases.Observation, baseline *releases.Evaluation) releases.Evaluation {
	if current == nil || baseline == nil {
		return evaluation
	}
	if observation.IndexerID == current.IndexerID && observation.NativeGUID == current.NativeGUID {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "upgrade improvement", Fact: "current_release", Decision: releases.DecisionFail, Reason: "candidate is the exact release currently published"})
		return evaluation
	}
	// The same release title cross-posted under another indexer or GUID is the
	// same release. Once the current release fails a newly required property,
	// only this check keeps such a copy from being re-downloaded as a
	// "replacement".
	if title := strings.TrimSpace(current.Title); title != "" && strings.EqualFold(strings.TrimSpace(observation.Title), title) {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "upgrade improvement", Fact: "current_release", Decision: releases.DecisionFail, Values: []string{observation.Title}, Reason: "candidate has the exact release title currently published"})
		return evaluation
	}
	if evaluation.Verdict == releases.VerdictEligible && baseline.Verdict == releases.VerdictEligible && evaluation.Score <= baseline.Score {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "upgrade improvement", Fact: "quality_score", Decision: releases.DecisionFail, Reason: "candidate does not improve the current publication under this quality profile"})
	}
	return evaluation
}

func appendSubtitleRules(rules []releases.Rule, policy subtitles.Policy) []releases.Rule {
	for _, language := range policy.Required {
		rules = append(rules,
			releases.Rule{Name: "Required subtitle " + language, Fact: "subtitle_languages", AllowedValues: []string{language}, Kind: releases.RuleRequired, Unknown: releases.UnknownAllow},
			releases.Rule{Name: "Prefer verified required subtitle " + language, Fact: "subtitle_languages", AllowedValues: []string{language}, Kind: releases.RulePreferred, Weight: requiredSubtitleEvidenceWeight, Unknown: releases.UnknownAllow},
		)
	}
	for _, language := range policy.Preferred {
		rules = append(rules, releases.Rule{Name: "Preferred subtitle " + language, Fact: "subtitle_languages", AllowedValues: []string{language}, Kind: releases.RulePreferred, Weight: 250, Unknown: releases.UnknownAllow})
	}
	return rules
}

func applyWorkflowExclusions(observation releases.Observation, evaluation releases.Evaluation, search postgres.SearchContext) releases.Evaluation {
	for _, exclusion := range search.Exclusions {
		if !exclusion.Matches(observation.IndexerID, observation.NativeGUID) {
			continue
		}
		evaluation.Verdict = releases.VerdictRejected
		rule := "workflow release verification"
		if exclusion.FailureKind == "subtitle_verification" {
			rule = "workflow subtitle verification"
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: rule, Fact: "native_release", Decision: releases.DecisionFail, Values: []string{observation.NativeGUID}, Reason: exclusion.Reason})
		return evaluation
	}
	return evaluation
}

func applyReleaseBlocks(observation releases.Observation, evaluation releases.Evaluation, search postgres.SearchContext) releases.Evaluation {
	for _, block := range search.Blocks {
		if !block.Matches(observation.IndexerID, observation.NativeGUID, search.Media.ID, search.Variant, observation.Title) {
			continue
		}
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "release blocklist", Fact: "native_release", Decision: releases.DecisionFail, Values: []string{observation.Title}, Reason: block.Reason})
		return evaluation
	}
	return evaluation
}

func constrainMediaMatch(observation releases.Observation, evaluation releases.Evaluation, media catalog.MediaItem) releases.Evaluation {
	if !mediaTitleMatches(observation.Title, media) {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested title", Fact: "release_title", Decision: releases.DecisionFail, Values: []string{observation.Title}, Reason: "release title does not begin with a saved canonical, original or alias title"})
		return evaluation
	}
	verified := make(map[string]string)
	for _, identity := range media.Identities {
		if identity.State == catalog.IdentityVerified {
			verified[identity.Namespace+"_id"] = identity.Value
		}
	}
	positiveIdentity := false
	for _, fact := range observation.Facts {
		expected, comparable := verified[strings.ToLower(strings.TrimSpace(fact.Name))]
		if !comparable || len(fact.Values) == 0 {
			continue
		}
		if fact.State == releases.FactConflicting {
			if evaluation.Verdict != releases.VerdictRejected {
				evaluation.Verdict = releases.VerdictHeld
			}
			evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "verified media identity", Fact: fact.Name, Decision: releases.DecisionUnknown, Values: append([]string(nil), fact.Values...), Reason: "provider identity evidence conflicts"})
			continue
		}
		matched := false
		for _, value := range fact.Values {
			if providerIdentityEqual(fact.Name, value, expected) {
				matched = true
				break
			}
		}
		if !matched {
			evaluation.Verdict = releases.VerdictRejected
			evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "verified media identity", Fact: fact.Name, Decision: releases.DecisionFail, Values: append([]string(nil), fact.Values...), Reason: "provider identity belongs to different media"})
		} else {
			positiveIdentity = true
		}
	}
	// A provider identity that exactly matches a verified identity proves the
	// media, so the title year is only needed without one. Release years are
	// often a year off the metadata year (festival versus theatrical release).
	if media.Kind == catalog.KindMovie && !positiveIdentity && media.Year != nil && !releases.TitleContainsYear(observation.Title, *media.Year) {
		if evaluation.Verdict != releases.VerdictRejected {
			evaluation.Verdict = releases.VerdictHeld
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested movie year", Fact: "release_title", Decision: releases.DecisionUnknown, Values: []string{observation.Title}, Reason: "requested year is not explicit in the release title and no provider identity matched"})
	}
	if media.Kind == catalog.KindSeries && !positiveIdentity && (media.Year == nil || !releases.TitleContainsYear(observation.Title, *media.Year)) {
		if evaluation.Verdict != releases.VerdictRejected {
			evaluation.Verdict = releases.VerdictHeld
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "series identity evidence", Fact: "release_title", Decision: releases.DecisionUnknown, Values: []string{observation.Title}, Reason: "provider supplied no matching series ID and the canonical first-air year is not explicit"})
	}
	return evaluation
}

func providerIdentityEqual(factName, observed, expected string) bool {
	if strings.EqualFold(strings.TrimSpace(observed), strings.TrimSpace(expected)) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(factName), "imdb_id") {
		return false
	}
	observedID, observedOK := normalizeIMDbID(observed)
	expectedID, expectedOK := normalizeIMDbID(expected)
	return observedOK && expectedOK && observedID == expectedID
}

func normalizeIMDbID(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "tt")
	if len(value) < 7 || len(value) > 9 {
		return "", false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	return value, true
}

func buildRequest(search postgres.SearchContext, config indexerConfig) newznab.SearchRequest {
	dialect := config.SearchDialects.Movie
	if search.Media.Kind == catalog.KindSeries {
		dialect = config.SearchDialects.Series
	}
	if dialect.Mode == "" {
		dialect = legacyDialect(config)
	}
	mode := dialect.Mode
	params := []newznab.Param{}
	// Indexers match release names, which use scene spelling: send
	// "Forraeder Under kutten", not "Forræder: Under kutten".
	title := searchTitle(search)
	scope, scopeOK := workflows.ParseVariant(search.Variant)
	if search.Media.Kind == catalog.KindSeries && scopeOK && (scope.Kind == workflows.VariantSeason || scope.Kind == workflows.VariantEpisode) {
		season := int(scope.Season)
		if mode == "tvsearch" {
			params = appendSupported(params, dialect, "q", title)
			params = appendIdentity(params, dialect, search.Media, "tvdb", "tvdbid")
			params = appendIdentity(params, dialect, search.Media, "tmdb", "tmdbid")
			params = appendIdentity(params, dialect, search.Media, "imdb", "imdbid")
			params = appendSupported(params, dialect, "season", strconv.Itoa(season))
			if scope.Kind == workflows.VariantEpisode {
				params = appendSupported(params, dialect, "ep", strconv.Itoa(int(scope.Episode)))
			}
			if search.Media.Year != nil {
				params = appendSupported(params, dialect, "year", strconv.Itoa(int(*search.Media.Year)))
			}
		} else {
			token := fmt.Sprintf("%s S%02d", title, season)
			if scope.Kind == workflows.VariantEpisode {
				token = fmt.Sprintf("%s S%02dE%02d", title, season, scope.Episode)
			}
			params = append(params, newznab.Param{Name: "q", Value: token})
		}
	} else {
		query := title
		if mode == "search" && search.Media.Year != nil {
			query = fmt.Sprintf("%s %d", query, *search.Media.Year)
		}
		params = appendSupported(params, dialect, "q", query)
		params = appendIdentity(params, dialect, search.Media, "imdb", "imdbid")
		params = appendIdentity(params, dialect, search.Media, "tmdb", "tmdbid")
		if search.Media.Year != nil {
			params = appendSupported(params, dialect, "year", strconv.Itoa(int(*search.Media.Year)))
		}
	}
	limit := 100
	if config.MaximumLimit > 0 && config.MaximumLimit < limit {
		limit = config.MaximumLimit
	}
	return newznab.SearchRequest{Mode: mode, Params: params, Categories: config.Categories, Limit: limit, Extended: config.Extended}
}

func legacyDialect(config indexerConfig) newznab.SearchDialect {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	switch mode {
	case "movie-search":
		mode = "movie"
	case "tv-search":
		mode = "tvsearch"
	case "movie", "tvsearch", "search":
	default:
		mode = "search"
	}
	params := []string{"q"}
	if mode == "movie" {
		params = append(params, "year")
	}
	if mode == "tvsearch" {
		params = append(params, "season")
	}
	return newznab.SearchDialect{Mode: mode, Params: params}
}

func appendSupported(params []newznab.Param, dialect newznab.SearchDialect, name, value string) []newznab.Param {
	if value != "" && dialect.Supports(name) {
		return append(params, newznab.Param{Name: name, Value: value})
	}
	return params
}

func appendIdentity(params []newznab.Param, dialect newznab.SearchDialect, media catalog.MediaItem, namespace, parameter string) []newznab.Param {
	if !dialect.Supports(parameter) {
		return params
	}
	for _, identity := range media.Identities {
		if identity.State != catalog.IdentityVerified || !strings.EqualFold(identity.Namespace, namespace) {
			continue
		}
		value := strings.TrimSpace(identity.Value)
		if namespace == "imdb" {
			value = strings.TrimPrefix(strings.ToLower(value), "tt")
		}
		return appendSupported(params, dialect, parameter, value)
	}
	return params
}

func parseRules(policy json.RawMessage) ([]releases.Rule, error) {
	var wrapper struct {
		Rules []releases.Rule `json:"rules"`
	}
	if err := json.Unmarshal(policy, &wrapper); err == nil && wrapper.Rules != nil {
		return wrapper.Rules, nil
	}
	var rules []releases.Rule
	if err := json.Unmarshal(policy, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}
func seasonNumber(variant string) int {
	if !strings.HasPrefix(variant, "season:") {
		return -1
	}
	value, err := strconv.Atoi(strings.TrimPrefix(variant, "season:"))
	if err != nil || value < 0 {
		return -1
	}
	return value
}

// workflowScopeRules returns the durable scope rules that are prepended to a
// workflow's quality policy. A complete-season workflow still requires a season
// pack. An episode workflow requires the exact released episode and a single
// episode release, so a currently airing season never downloads unrelated
// future episodes and never waits for a pack that cannot exist yet.
func workflowScopeRules(variant string) ([]releases.Rule, error) {
	scope, ok := workflows.ParseVariant(variant)
	if !ok {
		if strings.HasPrefix(variant, "season:") {
			return nil, errors.New("season workflow has an invalid variant")
		}
		return nil, nil
	}
	switch scope.Kind {
	case workflows.VariantSeason:
		return []releases.Rule{
			{Name: "Requested season", Fact: "season_number", AllowedValues: []string{strconv.Itoa(int(scope.Season))}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Complete season pack", Fact: "release_scope", AllowedValues: []string{"season_pack"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Prefer complete season pack", Fact: "release_scope", AllowedValues: []string{"season_pack"}, Kind: releases.RulePreferred, Weight: 10000, Unknown: releases.UnknownAllow},
		}, nil
	case workflows.VariantEpisode:
		return []releases.Rule{
			{Name: "Requested season", Fact: "season_number", AllowedValues: []string{strconv.Itoa(int(scope.Season))}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Requested episode", Fact: "episode_number", AllowedValues: []string{strconv.Itoa(int(scope.Episode))}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Single episode release", Fact: "release_scope", AllowedValues: []string{"episode"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
		}, nil
	default:
		return nil, nil
	}
}
