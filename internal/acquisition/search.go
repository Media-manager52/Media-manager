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
	BaseURL           string `json:"base_url"`
	AllowHTTP         bool   `json:"allow_http"`
	MinimumIntervalMS int64  `json:"minimum_interval_ms"`
	Mode              string `json:"mode"`
	ModeVerified      bool   `json:"mode_verified"`
	Categories        []int  `json:"categories"`
	Extended          bool   `json:"extended"`
}

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
	for _, language := range subtitlePolicy.Required {
		rules = append(rules, releases.Rule{Name: "Required subtitle " + language, Fact: "subtitle_languages", AllowedValues: []string{language}, Kind: releases.RuleRequired, Unknown: releases.UnknownAllow})
	}
	for _, language := range subtitlePolicy.Preferred {
		rules = append(rules, releases.Rule{Name: "Preferred subtitle " + language, Fact: "subtitle_languages", AllowedValues: []string{language}, Kind: releases.RulePreferred, Weight: 250, Unknown: releases.UnknownAllow})
	}
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
	var evaluated []postgres.EvaluatedObservation
	successfulIndexers := 0
	for _, integration := range search.Indexers {
		var config indexerConfig
		if err := json.Unmarshal(integration.Config, &config); err != nil {
			continue
		}
		secret, err := service.ResolveSecret(integration.SecretRef)
		if err != nil {
			continue
		}
		minimum := time.Duration(config.MinimumIntervalMS) * time.Millisecond
		if minimum < 3*time.Second {
			minimum = 3 * time.Second
		}
		if err := service.Scheduler.Configure(integration.ID.String(), indexers.Budget{MinimumInterval: minimum, FailureBackoff: 15 * time.Second, MaximumBackoff: 30 * time.Minute}); err != nil {
			return err
		}
		client, err := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: config.BaseURL, APIKey: secret, AllowHTTP: config.AllowHTTP}, service.Scheduler, service.Transport)
		if err != nil {
			continue
		}
		request := buildRequest(search, config)
		observations, err := client.Search(ctx, request)
		if err != nil {
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
			evaluated = append(evaluated, postgres.EvaluatedObservation{Observation: observation, Evaluation: evaluation})
		}
	}
	if successfulIndexers == 0 {
		return fmt.Errorf("all %d configured indexers failed before returning a valid search response", len(search.Indexers))
	}
	fingerprint := sha256.Sum256([]byte(search.Workflow.ID.String() + "\x00" + search.Variant + "\x00" + strconv.FormatUint(search.PolicyVersion, 10)))
	_, err = service.Store.PersistSearch(ctx, search, fingerprint[:], evaluated, time.Now().UTC())
	return err
}

func applyWorkflowExclusions(observation releases.Observation, evaluation releases.Evaluation, search postgres.SearchContext) releases.Evaluation {
	for _, exclusion := range search.Exclusions {
		if !exclusion.Matches(observation.IndexerID, observation.NativeGUID) {
			continue
		}
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "workflow subtitle verification", Fact: "native_release", Decision: releases.DecisionFail, Values: []string{observation.NativeGUID}, Reason: exclusion.Reason})
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
			if strings.EqualFold(strings.TrimSpace(value), expected) {
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

func buildRequest(search postgres.SearchContext, config indexerConfig) newznab.SearchRequest {
	mode := "search"
	params := []newznab.Param{}
	season := seasonNumber(search.Variant)
	if config.ModeVerified && (config.Mode == "movie" || config.Mode == "movie-search" || config.Mode == "tvsearch" || config.Mode == "tv-search" || config.Mode == "search") {
		mode = config.Mode
	}
	if mode == "movie-search" {
		mode = "movie"
	}
	if mode == "tv-search" {
		mode = "tvsearch"
	}
	if search.Media.Kind == catalog.KindSeries && season >= 0 {
		if mode == "tvsearch" {
			params = append(params, newznab.Param{Name: "q", Value: search.Media.Title}, newznab.Param{Name: "season", Value: strconv.Itoa(season)})
		} else {
			params = append(params, newznab.Param{Name: "q", Value: fmt.Sprintf("%s S%02d", search.Media.Title, season)})
		}
	} else {
		params = append(params, newznab.Param{Name: "q", Value: search.Media.Title})
		if search.Media.Year != nil {
			params = append(params, newznab.Param{Name: "year", Value: strconv.Itoa(int(*search.Media.Year))})
		}
	}
	return newznab.SearchRequest{Mode: mode, Params: params, Categories: config.Categories, Limit: 100, Extended: config.Extended}
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
