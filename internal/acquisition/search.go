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
	BaseURL           string                 `json:"base_url"`
	AllowHTTP         bool                   `json:"allow_http"`
	MinimumIntervalMS int64                  `json:"minimum_interval_ms"`
	Mode              string                 `json:"mode"`
	ModeVerified      bool                   `json:"mode_verified"`
	Categories        []int                  `json:"categories"`
	Extended          bool                   `json:"extended"`
	SearchDialects    newznab.SearchDialects `json:"search_dialects"`
	DefaultLimit      int                    `json:"default_limit"`
	MaximumLimit      int                    `json:"maximum_limit"`
	QueryLimit        int                    `json:"query_limit"`
	GrabLimit         int                    `json:"grab_limit"`
	BudgetWindow      int64                  `json:"budget_window_seconds"`
}

const requiredSubtitleEvidenceWeight = 100_000

func (service *SearchService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	search, err := service.Store.LoadSearchContext(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if search.Workflow.State == workflows.StateCandidateSelected || search.Workflow.State == workflows.StateQueued || search.Workflow.State == workflows.StateSubmitting {
		return nil
	}
	if search.Workflow.State == workflows.StateSearching {
		if search.Workflow.Revision != event.AggregateRevision && search.Workflow.Revision != event.AggregateRevision+1 {
			return nil
		}
	} else {
		if search.Workflow.State != workflows.StateMonitored && search.Workflow.State != workflows.StateWaiting || search.Workflow.Revision != event.AggregateRevision {
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
	if strings.HasPrefix(search.Variant, "season:") {
		season := seasonNumber(search.Variant)
		if season < 0 {
			return errors.New("season workflow has an invalid variant")
		}
		rules = append([]releases.Rule{
			{Name: "Requested season", Fact: "season_number", AllowedValues: []string{strconv.Itoa(season)}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Complete season pack", Fact: "release_scope", AllowedValues: []string{"season_pack"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Prefer complete season pack", Fact: "release_scope", AllowedValues: []string{"season_pack"}, Kind: releases.RulePreferred, Weight: 10000, Unknown: releases.UnknownAllow},
		}, rules...)
	}
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
	for _, integration := range search.Indexers {
		var config indexerConfig
		if err := json.Unmarshal(integration.Config, &config); err != nil {
			continue
		}
		secret, err := service.ResolveSecret(integration.SecretRef)
		if err != nil {
			continue
		}
		budget := indexers.NewznabBudget(config.MinimumIntervalMS, config.QueryLimit, config.GrabLimit, config.BudgetWindow)
		if err := service.Scheduler.Configure(integration.ID.String(), budget); err != nil {
			return err
		}
		client, err := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: config.BaseURL, APIKey: secret, AllowHTTP: config.AllowHTTP}, service.Scheduler, service.Transport)
		if err != nil {
			continue
		}
		request := buildRequest(search, config)
		observations, err := client.Search(ctx, request)
		if err != nil {
			var current interface{ RetryAt() time.Time }
			if errors.As(err, &current) {
				var earliest interface{ RetryAt() time.Time }
				if earliestDeferral == nil || errors.As(earliestDeferral, &earliest) && current.RetryAt().Before(earliest.RetryAt()) {
					earliestDeferral = err
				}
			}
			continue
		}
		successfulIndexers++
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
	if successfulIndexers == 0 {
		if earliestDeferral != nil {
			return earliestDeferral
		}
		return fmt.Errorf("all %d configured indexers failed before returning a valid search response", len(search.Indexers))
	}
	fingerprint := sha256.Sum256([]byte(search.Workflow.ID.String() + "\x00" + search.Variant + "\x00" + strconv.FormatUint(search.PolicyVersion, 10)))
	_, err = service.Store.PersistSearch(ctx, search, fingerprint[:], evaluated, time.Now().UTC())
	return err
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
	if !releases.TitleMatches(observation.Title, media.Title) {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested title", Fact: "release_title", Decision: releases.DecisionFail, Values: []string{observation.Title}, Reason: "release title does not begin with the requested canonical title"})
		return evaluation
	}
	if media.Kind == catalog.KindMovie && media.Year != nil && !releases.TitleContainsYear(observation.Title, *media.Year) {
		if evaluation.Verdict != releases.VerdictRejected {
			evaluation.Verdict = releases.VerdictHeld
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested movie year", Fact: "release_title", Decision: releases.DecisionUnknown, Values: []string{observation.Title}, Reason: "requested year is not explicit in the release title"})
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
	season := seasonNumber(search.Variant)
	if search.Media.Kind == catalog.KindSeries && season >= 0 {
		if mode == "tvsearch" {
			params = appendSupported(params, dialect, "q", search.Media.Title)
			params = appendIdentity(params, dialect, search.Media, "tvdb", "tvdbid")
			params = appendIdentity(params, dialect, search.Media, "tmdb", "tmdbid")
			params = appendIdentity(params, dialect, search.Media, "imdb", "imdbid")
			params = appendSupported(params, dialect, "season", strconv.Itoa(season))
			if search.Media.Year != nil {
				params = appendSupported(params, dialect, "year", strconv.Itoa(int(*search.Media.Year)))
			}
		} else {
			params = append(params, newznab.Param{Name: "q", Value: fmt.Sprintf("%s S%02d", search.Media.Title, season)})
		}
	} else {
		query := search.Media.Title
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
