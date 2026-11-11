package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

type SecretResolver func(string) (string, error)

type SearchService struct {
	Store              *postgres.Store
	Scheduler          *indexers.Scheduler
	ResolveSecret      SecretResolver
	OldBoysCredentials *oldboyscredentials.Service
	Transport          http.RoundTripper
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
	PageLimit                       int                    `json:"page_limit"`
	QueryLimit                      int                    `json:"query_limit"`
	GrabLimit                       int                    `json:"grab_limit"`
	BudgetWindow                    int64                  `json:"budget_window_seconds"`
}

const requiredSubtitleEvidenceWeight = 100_000

// A remembered eligible release is offered again for this long after a
// provider last returned it, and at most this many per search.
const (
	rememberedEligibleWindow = 14 * 24 * time.Hour
	rememberedEligibleLimit  = 100
)

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
	if !recovery && partialRetryEvent(event) && search.Workflow.State == workflows.StateSearching && search.Workflow.Revision == event.AggregateRevision+1 {
		pending, err := service.Store.HasPendingOwnerSearch(ctx, search.Workflow.ID, search.Workflow.Revision, event.ID)
		if err != nil {
			return err
		}
		if pending {
			// The revision bump may be the owner's fresh-search transition, not
			// this continuation's own interrupted attempt. Let the owner event
			// run; reusing carried evidence here would skip providers explicitly
			// requested by the owner.
			return nil
		}
	}
	if recovery {
		if err := service.Store.ValidateRecoveryDiscovery(ctx, event.AggregateID, int64(event.AggregateRevision)); err != nil {
			if errors.Is(err, postgres.ErrConcurrentModification) {
				return nil
			}
			return err
		}
		// Recovery discovery used to narrow this attempt to pending_indexer_ids
		// from the previous generation. Durable partial completion now carries
		// the successful evidence forward and executes only the remaining
		// providers, so a full-indexer plan is safe: an invalid or expired chain
		// intentionally performs a complete evidence-only rescan instead of
		// inheriting stale progress.
	} else if search.Workflow.State == workflows.StateCandidateSelected || search.Workflow.State == workflows.StateQueued || search.Workflow.State == workflows.StateSubmitting {
		return nil
	}
	if !recovery {
		if search.OpenRound == nil {
			// A closed or cancelled round can still have an older leased search
			// event. It is a stale audited no-op, never a reason to create a round.
			return nil
		}
		if !search.RoundPolicyCurrent {
			// The profile's rules changed after this round opened. Park the
			// workflow for the owner to apply the current policy instead of
			// searching under the stale snapshot or retrying a failing search
			// forever. A rename or default change alone leaves the rules equal.
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
			dispatched, dispatchErr := service.Store.DispatchAiringSeason(ctx, search.Workflow.ID, time.Now().UTC(), search.Workflow.Revision)
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
	searchNow := time.Now().UTC()
	planned, err := service.planSearchIndexers(ctx, search, searchNow)
	if err != nil {
		return err
	}
	mode := "targeted"
	if recovery {
		mode = "recovery"
	}
	intentFingerprint, err := searchIntentFingerprint(search, mode, planned)
	if err != nil {
		return err
	}
	partialRetry, carriedObservations, reusedIndexers := postgres.SearchPartialRetry{}, []releases.Observation(nil), map[uuid.UUID]bool{}
	if partialRetryEvent(event) {
		partialRetry, carriedObservations, reusedIndexers, err = service.loadPartialSearch(ctx, search, planned, mode, intentFingerprint, searchNow)
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
	if search.Media.Metadata != nil {
		subtitlePolicy = subtitlePolicy.WithOriginalLanguage(search.Media.Metadata.OriginalLanguage)
	}
	rules = appendSubtitleRules(rules, subtitlePolicy)
	qualityRules := append([]releases.Rule{}, rules...)
	scopeRules, scopeErr := workflowScopeRules(search.Variant)
	if scopeErr != nil {
		return scopeErr
	}
	rules = append(scopeRules, rules...)
	var currentEvaluation *releases.Evaluation
	if search.CurrentRelease != nil {
		currentObservation, currentRules := *search.CurrentRelease, rules
		if currentObservation.ParseVersion == "oldboys-v1" {
			currentObservation, currentRules = oldBoysRangeScope(currentObservation, search, scopeRules, qualityRules)
		}
		evaluatedCurrent, evaluateErr := releases.Evaluate(currentObservation, currentRules)
		if evaluateErr != nil {
			return evaluateErr
		}
		currentEvaluation = &evaluatedCurrent
	}
	var evaluated []postgres.EvaluatedObservation
	indexerKinds := make(map[uuid.UUID]bool, len(search.Indexers))
	for _, integration := range search.Indexers {
		indexerKinds[integration.ID] = integration.Kind == "oldboys"
	}
	reusedObservationIDs := map[uuid.UUID]bool{}
	for _, observation := range carriedObservations {
		oldboys := indexerKinds[observation.IndexerID]
		evaluatedObservation, evaluation, evaluateErr := evaluateCandidateObservation(observation, search, rules, scopeRules, qualityRules, currentEvaluation, oldboys, searchNow)
		if evaluateErr != nil {
			return evaluateErr
		}
		evaluated = append(evaluated, postgres.EvaluatedObservation{Observation: evaluatedObservation, Evaluation: evaluation, Reused: true})
		reusedObservationIDs[observation.ID] = true
	}
	// Carried evidence is provider evidence from an earlier successful
	// attempt. It is not a fresh provider response and must not inflate
	// candidate freshness or observation counts.
	successfulIndexers := len(reusedIndexers)
	shareSeason, err := service.seasonShareApplies(ctx, search)
	if err != nil {
		return err
	}
	var earliestDeferral error
	pagingWait := searchPagingWait
	outcomes := make([]postgres.SearchIndexerOutcome, 0, len(search.Indexers))
	for _, plan := range planned {
		integration := plan.Integration
		if reusedIndexers[integration.ID] {
			outcomes = append(outcomes, postgres.SearchIndexerOutcome{IndexerInstanceID: integration.ID, State: "succeeded", Detail: "reused partial search evidence"})
			continue
		}
		outcome := postgres.SearchIndexerOutcome{IndexerInstanceID: integration.ID, State: "failed", Detail: "indexer_configuration_unavailable"}
		config := plan.Config
		if plan.PlanError != "" {
			outcome.Detail = plan.PlanError
			outcomes = append(outcomes, outcome)
			continue
		}
		budget := indexers.NewznabBudget(config.MinimumIntervalMS, config.QueryLimit, config.GrabLimit, config.BudgetWindow)
		if err := service.Scheduler.Configure(integration.ID.String(), budget); err != nil {
			return err
		}
		var executeSearch func(newznab.SearchRequest) ([]releases.Observation, error)
		var searchPage func(newznab.SearchRequest) (newznab.SearchPage, error)
		recoveryGuard := func(guardCtx context.Context) error {
			if recovery {
				return service.Store.ValidateRecoveryDiscovery(guardCtx, event.AggregateID, int64(event.AggregateRevision))
			}
			return nil
		}
		if integration.Kind == "oldboys" {
			client, clientErr := newOldBoysClient(ctx, service.Store, service.Scheduler, service.OldBoysCredentials, integration, service.Transport)
			if clientErr != nil {
				outcome.Detail = "oldboys_client_unavailable"
				outcomes = append(outcomes, outcome)
				continue
			}
			nativeSearch := search
			nativeSearch.CompleteSearches = plan.OldBoysPhase
			executeSearch = func(newznab.SearchRequest) ([]releases.Observation, error) {
				workload := indexers.WorkloadInteractive
				if recovery {
					workload = indexers.WorkloadBackground
				}
				return client.Search(ctx, oldBoysSearchRequest(nativeSearch), workload, recoveryGuard)
			}
		} else {
			secret, secretErr := service.ResolveSecret(integration.SecretRef)
			if secretErr != nil {
				outcome.Detail = "indexer_secret_unavailable"
				outcomes = append(outcomes, outcome)
				continue
			}
			client, clientErr := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: config.BaseURL, APIKey: secret, AllowHTTP: config.AllowHTTP}, service.Scheduler, service.Transport)
			if clientErr != nil {
				outcome.Detail = "indexer_client_invalid"
				outcomes = append(outcomes, outcome)
				continue
			}
			searchPage = func(request newznab.SearchRequest) (newznab.SearchPage, error) {
				if recovery {
					return client.BackgroundSearchPageGuarded(ctx, request, recoveryGuard)
				}
				return client.SearchPage(ctx, request)
			}
			executeSearch = func(request newznab.SearchRequest) ([]releases.Observation, error) {
				first, err := searchPage(request)
				if err != nil {
					return first.Observations, err
				}
				return searchFollowUpPages(ctx, request, first, &pagingWait, searchPage), nil
			}
		}
		if recovery {
			if err := service.Store.ValidateRecoveryDiscovery(ctx, event.AggregateID, int64(event.AggregateRevision)); err != nil {
				if errors.Is(err, postgres.ErrConcurrentModification) {
					return nil
				}
				return err
			}
		}
		// The exact provider plan was frozen before the retry state was loaded,
		// so a capability/plan change cannot silently reuse incompatible evidence.
		request := plan.Request
		var observations []releases.Observation
		var seasonLocators []string
		seasonDetail, seasonShared := "", false
		err = nil
		if shareSeason && searchPage != nil {
			observations, seasonLocators, seasonDetail, seasonShared, err = service.seasonAnswer(ctx, integration.ID, search, request, searchNow, &pagingWait, searchPage)
		}
		if !seasonShared && err == nil {
			observations, err = executeSearch(request)
		}
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
		outcome.State, outcome.Detail = "succeeded", seasonDetail
		outcomes = append(outcomes, outcome)
		for index, observation := range observations {
			observation, evaluation, evaluateErr := evaluateCandidateObservation(observation, search, rules, scopeRules, qualityRules, currentEvaluation, integration.Kind == "oldboys", searchNow)
			if evaluateErr != nil {
				return evaluateErr
			}
			entry := postgres.EvaluatedObservation{Observation: observation, Evaluation: evaluation, ProviderResult: true}
			if seasonShared {
				// A season answer holds every episode; keep this episode's.
				if !episodeInScope(observation, scopeRules) {
					continue
				}
				entry.Stored, entry.LocatorRef = true, seasonLocators[index]
			}
			evaluated = append(evaluated, entry)
		}
	}
	if successfulIndexers == 0 && !recovery {
		if earliestDeferral != nil {
			return earliestDeferral
		}
		return fmt.Errorf("all %d configured indexers failed before returning a valid search response", len(search.Indexers))
	}
	// A new frozen episode mapping can make stored held candidates eligible.
	// Re-score their existing observations with the same policy and scope rules;
	// no provider call or new candidate row is needed. Without this, the first
	// search after a mapping change would keep showing the historical held
	// decision forever because indexers no longer return the old release.
	if !recovery && search.Media.Kind == catalog.KindSeries && search.EpisodeNumbering.Year != 0 {
		held, heldErr := service.Store.HeldCandidateObservations(ctx, search.Workflow.ID)
		if heldErr != nil {
			return heldErr
		}
		for _, observation := range held {
			if reusedObservationIDs[observation.ID] {
				continue
			}
			oldboys, planned := indexerKinds[observation.IndexerID]
			if !planned {
				continue
			}
			observation, evaluation, evaluateErr := evaluateCandidateObservation(observation, search, rules, scopeRules, qualityRules, currentEvaluation, oldboys, searchNow)
			if evaluateErr != nil {
				return evaluateErr
			}
			evaluated = append(evaluated, postgres.EvaluatedObservation{Observation: observation, Evaluation: evaluation})
		}
	}
	// Each scan searches one of the title's names, so the good releases found
	// under the English title are rarely returned by the next scan under an
	// alias. Re-evaluate recently seen eligible releases so the next slot of a
	// round can try the next one; exclusions, blocks and attempts still apply.
	if !recovery {
		returned := map[string]bool{}
		for _, entry := range evaluated {
			returned[entry.Observation.IndexerID.String()+"\x00"+entry.Observation.NativeGUID] = true
		}
		remembered, rememberErr := service.Store.RememberedEligibleObservations(ctx, search.Workflow.ID, search.PolicyVersion, searchNow.Add(-rememberedEligibleWindow), rememberedEligibleLimit)
		if rememberErr != nil {
			return rememberErr
		}
		for _, observation := range remembered {
			if reusedObservationIDs[observation.ID] || returned[observation.IndexerID.String()+"\x00"+observation.NativeGUID] {
				continue
			}
			oldboys, planned := indexerKinds[observation.IndexerID]
			if !planned {
				continue
			}
			observation, evaluation, evaluateErr := evaluateCandidateObservation(observation, search, rules, scopeRules, qualityRules, currentEvaluation, oldboys, searchNow)
			if evaluateErr != nil {
				return evaluateErr
			}
			// Reused keeps the candidate's provider freshness, observation and
			// download locator exactly as the provider last returned them.
			evaluated = append(evaluated, postgres.EvaluatedObservation{Observation: observation, Evaluation: evaluation, Reused: true})
			returned[observation.IndexerID.String()+"\x00"+observation.NativeGUID] = true
		}
	}
	danishGroups, err := service.Store.DanishGroupRecords(ctx)
	if err != nil {
		return err
	}
	holdUnlikelyDanishGroups(evaluated, subtitlePolicy, danishGroups)
	var state *postgres.SearchPartialRetry
	if search.SearchRoundID != (uuid.UUID{}) {
		state = &postgres.SearchPartialRetry{
			WorkflowID:        search.Workflow.ID,
			RoundID:           search.SearchRoundID,
			Mode:              mode,
			IntentFingerprint: intentFingerprint,
			PlannedIndexerIDs: plannedIndexerIDs(planned),
			CompletedIndexers: partialRetry.CompletedIndexers,
		}
	}
	persisted, persistErr := service.Store.PersistSearch(ctx, search, intentFingerprint, evaluated, postgres.SearchPersistenceOptions{
		RecoveryRescan:   recovery,
		RescanGeneration: int64(event.AggregateRevision),
		IndexerOutcomes:  outcomes,
		PartialRetry:     state,
		ReusedIndexerIDs: reusedIndexers,
		// Recovery discovery has its own generation guard and does not advance
		// the workflow revision. A pending paced search event at the waiting
		// revision must not be mistaken for an owner supersede.
		Continuation: !recovery && partialRetryEvent(event),
	}, time.Now().UTC())
	err = persistErr
	if recovery && errors.Is(err, postgres.ErrConcurrentModification) {
		return nil
	}
	if err == nil && !recovery && search.Media.Kind == catalog.KindSeries && (persisted.State == workflows.StateWaiting || persisted.State == workflows.StateHeld) {
		if scope, ok := workflows.ParseVariant(search.Variant); ok && scope.Kind == workflows.VariantSeason {
			_, dispatchErr := service.Store.DispatchAiringSeason(ctx, persisted.ID, time.Now().UTC(), persisted.Revision)
			if dispatchErr != nil && !errors.Is(dispatchErr, postgres.ErrConcurrentModification) {
				return dispatchErr
			}
		}
	}
	return err
}

func evaluateCandidateObservation(observation releases.Observation, search postgres.SearchContext, rules, scopeRules, qualityRules []releases.Rule, currentEvaluation *releases.Evaluation, oldboys bool, now time.Time) (releases.Observation, releases.Evaluation, error) {
	observation = releases.InferTitleFacts(observation, now)
	if oldboys {
		observation = bindOldBoysCatalogue(observation, search, now)
	}
	observation = applyEpisodeNumbering(observation, search, now)
	observation = releases.NarrowPartialSeasonRange(observation, search.SeasonLastEpisode)
	observationRules := rules
	if oldboys {
		observation, observationRules = oldBoysRangeScope(observation, search, scopeRules, qualityRules)
	}
	evaluation, err := releases.Evaluate(observation, observationRules)
	if err != nil {
		return observation, releases.Evaluation{}, err
	}
	if len(rules) == 0 {
		evaluation.Verdict = releases.VerdictHeld
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "quality profile", Decision: releases.DecisionUnknown, Category: releases.CategoryQuality, Reason: "no release rules are configured"})
	}
	if strings.HasPrefix(observation.NativeGUID, "weak:") {
		evaluation.Verdict = releases.VerdictHeld
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "native release identity", Decision: releases.DecisionUnknown, Category: releases.CategoryIdentity, Reason: "provider supplied no retrievable native GUID"})
	}
	evaluation = constrainMediaMatch(observation, evaluation, search.Media)
	evaluation = applyReleaseBlocks(observation, evaluation, search)
	evaluation = applyWorkflowExclusions(observation, evaluation, search)
	evaluation = applyRepairExclusions(observation, evaluation, search.RepairBrokenTitles)
	evaluation = applyEditionRules(observation, evaluation, search)
	if len(search.RepairBrokenTitles) == 0 {
		// A repair replaces a broken publication: any eligible release is an
		// improvement, so the upgrade baseline does not apply.
		evaluation = applyUpgradeBaseline(observation, evaluation, search.CurrentRelease, currentEvaluation)
	}
	return observation, evaluation, nil
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
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "upgrade improvement", Fact: "current_release", Decision: releases.DecisionFail, Category: releases.CategoryUpgrade, Reason: "candidate is the exact release currently published"})
		return evaluation
	}
	// The same release title cross-posted under another indexer or GUID is the
	// same release. Once the current release fails a newly required property,
	// only this check keeps such a copy from being re-downloaded as a
	// "replacement".
	if title := strings.TrimSpace(current.Title); title != "" && strings.EqualFold(strings.TrimSpace(observation.Title), title) {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "upgrade improvement", Fact: "current_release", Decision: releases.DecisionFail, Values: []string{observation.Title}, Category: releases.CategoryUpgrade, Reason: "candidate has the exact release title currently published"})
		return evaluation
	}
	if evaluation.Verdict == releases.VerdictEligible && baseline.Verdict == releases.VerdictEligible && evaluation.Score <= baseline.Score {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "upgrade improvement", Fact: "quality_score", Decision: releases.DecisionFail, Category: releases.CategoryUpgrade, Reason: "candidate does not improve the current publication under this quality profile"})
	}
	return evaluation
}

func appendSubtitleRules(rules []releases.Rule, policy subtitles.Policy) []releases.Rule {
	for _, language := range policy.Required {
		rules = append(rules,
			// A required language is satisfied by a subtitle or an audio track in
			// that language (a Danish dub needs no Danish subtitles).
			releases.Rule{Name: "Required subtitle or audio " + language, Fact: releases.SubtitleOrAudioLanguagesFact, AllowedValues: []string{language}, Kind: releases.RuleRequired, Unknown: releases.UnknownAllow},
			releases.Rule{Name: "Prefer verified required subtitle or audio " + language, Fact: releases.SubtitleOrAudioLanguagesFact, AllowedValues: []string{language}, Kind: releases.RulePreferred, Weight: requiredSubtitleEvidenceWeight, Unknown: releases.UnknownAllow},
		)
	}
	for _, language := range policy.RequiredAudio {
		// A required audio language needs a dub: subtitles do not count. Only
		// releases with Danish-audio evidence in their name are acquired;
		// releases without any evidence are held instead of downloaded and
		// rejected after inspection. A title originally in that language
		// carries it as its own audio, so its releases need no evidence.
		unknown := releases.UnknownHold
		if language == policy.OriginalLanguage {
			unknown = releases.UnknownAllow
		}
		rules = append(rules,
			releases.Rule{Name: "Required audio " + language, Fact: releases.AudioLanguageEvidenceFact, AllowedValues: []string{language}, Kind: releases.RuleRequired, Unknown: unknown},
			releases.Rule{Name: "Prefer explicit audio " + language, Fact: "audio_languages", AllowedValues: []string{language}, Kind: releases.RulePreferred, Weight: requiredSubtitleEvidenceWeight, Unknown: releases.UnknownAllow},
		)
	}
	for _, language := range policy.Preferred {
		rules = append(rules, releases.Rule{Name: "Preferred subtitle " + language, Fact: "subtitle_languages", AllowedValues: []string{language}, Kind: releases.RulePreferred, Weight: 250, Unknown: releases.UnknownAllow})
	}
	return rules
}

// applyEditionRules keeps each edition in its own resolution: a 4K edition
// only acquires releases that show 2160p, and once a title has a 4K edition the
// standard edition acquires at most 1080p (its existing publication is kept).
// Conflicting evidence that includes 2160p is never trusted either way.
func applyEditionRules(observation releases.Observation, evaluation releases.Evaluation, search postgres.SearchContext) releases.Evaluation {
	fourK := search.Edition == postgres.Edition4K
	if !fourK && !search.FourKEditionExists {
		return evaluation
	}
	values, state := releaseResolution(observation)
	has2160 := containsFold(values, "2160p")
	only2160 := has2160 && len(values) == 1 && state != releases.FactConflicting && state != releases.FactUnknown
	if fourK && !only2160 {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "4K edition", Fact: "resolution", Decision: releases.DecisionFail, Values: values, Category: releases.CategoryResolutionCodec, Reason: "the 4K edition only acquires releases that are clearly 2160p"})
		return evaluation
	}
	if !fourK && has2160 {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "standard edition", Fact: "resolution", Decision: releases.DecisionFail, Values: values, Category: releases.CategoryResolutionCodec, Reason: "this title has a 4K edition, so the standard edition stays at 1080p or lower"})
	}
	return evaluation
}

// releaseResolution returns the resolution fact the release rules use: the
// last fact with that name, exactly as Evaluate resolves it.
func releaseResolution(observation releases.Observation) ([]string, releases.FactState) {
	var values []string
	state := releases.FactUnknown
	for _, fact := range observation.Facts {
		if strings.EqualFold(strings.TrimSpace(fact.Name), "resolution") {
			values, state = nil, fact.State
			for _, value := range fact.Values {
				if canonical, ok := releases.CanonicalResolution(value); ok {
					value = canonical
				}
				values = append(values, strings.ToLower(strings.TrimSpace(value)))
			}
		}
	}
	return values, state
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

// applyRepairExclusions rejects the exact releases whose published files lost
// their Usenet articles, so a repair never acquires the same broken release.
func applyRepairExclusions(observation releases.Observation, evaluation releases.Evaluation, brokenTitles []string) releases.Evaluation {
	title := strings.TrimSpace(observation.Title)
	for _, broken := range brokenTitles {
		if title != "" && strings.EqualFold(title, strings.TrimSpace(broken)) {
			evaluation.Verdict = releases.VerdictRejected
			evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "AltMount repair", Fact: "release_title", Decision: releases.DecisionFail, Values: []string{observation.Title}, Category: releases.CategoryExclusion, Reason: "this release's published file is being replaced (broken, or missing a required audio language)"})
			return evaluation
		}
	}
	return evaluation
}

func applyWorkflowExclusions(observation releases.Observation, evaluation releases.Evaluation, search postgres.SearchContext) releases.Evaluation {
	for _, exclusion := range search.Exclusions {
		if !exclusion.Matches(observation.IndexerID, observation.NativeGUID) && !exclusion.MatchesTitle(observation.Title) {
			continue
		}
		evaluation.Verdict = releases.VerdictRejected
		rule := "workflow release verification"
		if exclusion.FailureKind == "subtitle_verification" {
			rule = "workflow subtitle verification"
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: rule, Fact: "native_release", Decision: releases.DecisionFail, Values: []string{observation.NativeGUID}, Category: releases.CategoryExclusion, Reason: exclusion.Reason})
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
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "release blocklist", Fact: "native_release", Decision: releases.DecisionFail, Values: []string{observation.Title}, Category: releases.CategoryExclusion, Reason: block.Reason})
		return evaluation
	}
	return evaluation
}

func constrainMediaMatch(observation releases.Observation, evaluation releases.Evaluation, media catalog.MediaItem) releases.Evaluation {
	if !mediaTitleMatches(observation.Title, media) {
		evaluation.Verdict = releases.VerdictRejected
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested title", Fact: "release_title", Decision: releases.DecisionFail, Values: []string{observation.Title}, Category: releases.CategoryIdentity, Reason: "release title does not begin with a saved canonical, original or alias title"})
		return evaluation
	}
	verified := make(map[string]string)
	for _, identity := range media.Identities {
		if identity.State == catalog.IdentityVerified {
			verified[identity.Namespace+"_id"] = identity.Value
		}
	}
	positiveIdentity := hasOldBoysCatalogueBinding(observation, media)
	if positiveIdentity {
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "native Danish series catalogue binding", Fact: "oldboys_catalogue_binding", Decision: releases.DecisionPass, Values: []string{media.ID.String()}, Category: releases.CategoryIdentity, Reason: "inferred from exact original/canonical title and explicit scene scope; provider IDs remain unknown"})
	}
	for _, fact := range observation.Facts {
		expected, comparable := verified[strings.ToLower(strings.TrimSpace(fact.Name))]
		if !comparable || len(fact.Values) == 0 {
			continue
		}
		if fact.State == releases.FactConflicting {
			if evaluation.Verdict != releases.VerdictRejected {
				evaluation.Verdict = releases.VerdictHeld
			}
			evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "verified media identity", Fact: fact.Name, Decision: releases.DecisionUnknown, Values: append([]string(nil), fact.Values...), Category: releases.CategoryIdentity, Reason: "provider identity evidence conflicts"})
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
			evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "verified media identity", Fact: fact.Name, Decision: releases.DecisionFail, Values: append([]string(nil), fact.Values...), Category: releases.CategoryIdentity, Reason: "provider identity belongs to different media"})
		} else {
			positiveIdentity = true
		}
	}
	// Provider IDs can be mislabelled. Explicit remake years remain conflicting
	// even with a matching ID; festival/theatrical differences of one year pass.
	if media.Kind == catalog.KindMovie && media.Year != nil && releases.ConflictingMovieYear(observation.Title, *media.Year, media.SavedTitles()) {
		if evaluation.Verdict != releases.VerdictRejected {
			evaluation.Verdict = releases.VerdictHeld
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested movie year", Fact: "release_title", Decision: releases.DecisionUnknown, Values: []string{observation.Title}, Category: releases.CategoryIdentity, Reason: "explicit release year conflicts with the requested movie; matching provider IDs do not resolve the contradiction"})
	}
	if media.Kind == catalog.KindMovie && !positiveIdentity && media.Year != nil && !releases.TitleContainsYear(observation.Title, *media.Year) {
		if evaluation.Verdict != releases.VerdictRejected {
			evaluation.Verdict = releases.VerdictHeld
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "requested movie year", Fact: "release_title", Decision: releases.DecisionUnknown, Values: []string{observation.Title}, Category: releases.CategoryIdentity, Reason: "requested year is not explicit in the release title and no provider identity matched"})
	}
	if media.Kind == catalog.KindSeries && !positiveIdentity && (media.Year == nil || !releases.TitleContainsYear(observation.Title, *media.Year)) {
		if danishTaggedSeriesRelease(observation.Title, media) {
			evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "series identity evidence", Fact: "release_title", Decision: releases.DecisionPass, Values: []string{observation.Title}, Category: releases.CategoryIdentity, Reason: "the release name is the exact series name with a season token and a Danish language tag"})
			return evaluation
		}
		if evaluation.Verdict != releases.VerdictRejected {
			evaluation.Verdict = releases.VerdictHeld
		}
		evaluation.Explanations = append(evaluation.Explanations, releases.Explanation{Rule: "series identity evidence", Fact: "release_title", Decision: releases.DecisionUnknown, Values: []string{observation.Title}, Category: releases.CategoryIdentity, Reason: "provider supplied no matching series ID and the canonical first-air year is not explicit"})
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
	if len(value) == 0 || len(value) > 9 {
		return "", false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	// Indexers pad the number to different widths: 0362165 and 00362165 are
	// the same title.
	return strings.TrimLeft(value, "0"), true
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
	return newznab.SearchRequest{Mode: mode, Params: params, Categories: config.Categories, Limit: searchPageLimit(config), Extended: config.Extended}
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
