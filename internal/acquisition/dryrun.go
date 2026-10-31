package acquisition

import (
	"context"
	"errors"
	"slices"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// DryRunCandidate is one stored candidate replayed through the same evaluator
// the worker uses. It is computed in memory and never persisted.
type DryRunCandidate struct {
	CandidateID       uuid.UUID                    `json:"candidate_id"`
	Title             string                       `json:"title"`
	IndexerID         uuid.UUID                    `json:"indexer_id"`
	IndexerKind       string                       `json:"indexer_kind"`
	Observation       releases.Observation         `json:"-"`
	Evaluation        releases.Evaluation          `json:"evaluation"`
	Explanation       releases.DecisionExplanation `json:"explanation"`
	Attempted         bool                         `json:"attempted"`
	Excluded          bool                         `json:"excluded"`
	Selected          bool                         `json:"selected"`
	FirstSeenSequence int64                        `json:"first_seen_sequence"`
}

// DryRunResult answers "what would the current round choose from the stored
// observations, and why?" It is read-only: no provider request, download,
// AltMount submission, publication or exclusion mutation happens here.
type DryRunResult struct {
	WorkflowID              uuid.UUID         `json:"workflow_id"`
	PolicyVersion           uint64            `json:"policy_version"`
	ProfilePolicyVersion    uint64            `json:"profile_policy_version"`
	PolicySource            string            `json:"policy_source"`
	Candidates              []DryRunCandidate `json:"candidates"`
	RankedCandidateIDs      []uuid.UUID       `json:"ranked_candidate_ids"`
	BestEligibleCandidateID *uuid.UUID        `json:"best_eligible_candidate_id,omitempty"`
	Limitations             []string          `json:"limitations"`
	GeneratedAt             time.Time         `json:"generated_at"`
	Truncated               bool              `json:"truncated"`
}

const dryRunCandidateLimit = 200

// DryRunStore is the read-only subset of the repository used by the dry run.
// Keeping it narrow makes the replay testable without a live database.
type DryRunStore interface {
	LoadSearchContext(ctx context.Context, workflowID uuid.UUID) (postgres.SearchContext, error)
	WorkflowCandidateObservations(ctx context.Context, workflowID uuid.UUID, limit int) ([]postgres.CandidateObservation, error)
}

func DryRunWorkflow(ctx context.Context, store DryRunStore, workflowID uuid.UUID, limit int) (DryRunResult, error) {
	if limit < 1 || limit > dryRunCandidateLimit {
		limit = dryRunCandidateLimit
	}
	search, err := store.LoadSearchContext(ctx, workflowID)
	if err != nil {
		return DryRunResult{}, err
	}
	now := time.Now().UTC()
	rules, err := parseRules(search.Policy)
	if err != nil {
		return DryRunResult{}, err
	}
	subtitlePolicy, err := subtitles.ParsePolicy(search.Policy)
	if err != nil {
		return DryRunResult{}, err
	}
	if search.Media.Metadata != nil {
		subtitlePolicy = subtitlePolicy.WithOriginalLanguage(search.Media.Metadata.OriginalLanguage)
	}
	rules = appendSubtitleRules(rules, subtitlePolicy)
	qualityRules := append([]releases.Rule{}, rules...)
	scopeRules, err := workflowScopeRules(search.Variant)
	if err != nil {
		return DryRunResult{}, err
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
			return DryRunResult{}, evaluateErr
		}
		currentEvaluation = &evaluatedCurrent
	}
	stored, err := store.WorkflowCandidateObservations(ctx, workflowID, limit+1)
	if err != nil {
		return DryRunResult{}, err
	}
	truncated := len(stored) > limit
	if truncated {
		stored = stored[:limit]
	}
	policySource := dryRunPolicySource(search)
	limitations := []string{
		"Uses only stored observations; no indexer search, NZB download, AltMount submission or Plex publication is performed.",
		dryRunPolicySourceLimitation(policySource),
		"Attempted, excluded and already-selected candidates are marked but not removed from the per-candidate explanation. The best eligible candidate skips them.",
		"Novelty cutoffs and the final persisted selection are not re-applied; this is a policy replay, not a new acquisition decision.",
		"The workflow, policy, exclusions and candidates are read in separate queries; a concurrent change can mix states in this informational response.",
		"Open-round indexer authorization denials (indexer HTTP 401/403) and cross-candidate known subtitle failures are not replayed, so the best eligible candidate can differ from the worker's final choice.",
	}
	if truncated {
		limitations = append(limitations, "Only the most recently observed candidates were replayed; the ranking may omit other stored candidates.")
	}
	result := DryRunResult{
		WorkflowID:           workflowID,
		PolicyVersion:        search.PolicyVersion,
		ProfilePolicyVersion: search.ProfilePolicyVersion,
		PolicySource:         policySource,
		Candidates:           make([]DryRunCandidate, 0, len(stored)),
		RankedCandidateIDs:   make([]uuid.UUID, 0, len(stored)),
		Limitations:          limitations,
		GeneratedAt:          now,
		Truncated:            truncated,
	}
	observations := make([]releases.Observation, 0, len(stored))
	evaluations := make([]releases.Evaluation, 0, len(stored))
	candidateByObservation := make(map[uuid.UUID]uuid.UUID, len(stored))
	blocked := make(map[uuid.UUID]bool, len(stored))
	for _, candidate := range stored {
		oldboys := candidate.IndexerKind == "oldboys"
		observation, evaluation, evaluateErr := evaluateCandidateObservation(candidate.Observation, search, rules, scopeRules, qualityRules, currentEvaluation, oldboys, now)
		if evaluateErr != nil {
			return DryRunResult{}, evaluateErr
		}
		if evaluation.Explanations == nil {
			evaluation.Explanations = []releases.Explanation{}
		}
		explanation := releases.ExplainDecisionForScope(evaluation.Verdict, evaluation.Score, evaluation.Explanations, observation.Facts, releases.DryRunReplayEvidenceScope)
		result.Candidates = append(result.Candidates, DryRunCandidate{
			CandidateID:       candidate.CandidateID,
			Title:             observation.Title,
			IndexerID:         observation.IndexerID,
			IndexerKind:       candidate.IndexerKind,
			Observation:       observation,
			Evaluation:        evaluation,
			Explanation:       explanation,
			Attempted:         candidate.Attempted,
			Excluded:          candidate.Excluded,
			Selected:          candidate.Selected,
			FirstSeenSequence: candidate.FirstSeenSequence,
		})
		observations = append(observations, observation)
		evaluations = append(evaluations, evaluation)
		if _, exists := candidateByObservation[observation.ID]; exists {
			return DryRunResult{}, errors.New("two candidates share one stored observation; dry-run ranking is ambiguous")
		}
		candidateByObservation[observation.ID] = candidate.CandidateID
		blocked[candidate.CandidateID] = candidate.Attempted || candidate.Excluded || candidate.Selected
	}
	ranked := releases.Rank(observations, evaluations)
	if slices.Contains(subtitlePolicy.Required, "da") || slices.Contains(subtitlePolicy.RequiredAudio, "da") {
		ranked = releases.RankForDanishSubtitles(observations, evaluations)
	}
	for _, evaluation := range ranked {
		candidateID, ok := candidateByObservation[evaluation.ObservationID]
		if !ok {
			continue
		}
		result.RankedCandidateIDs = append(result.RankedCandidateIDs, candidateID)
		if result.BestEligibleCandidateID == nil && evaluation.Verdict == releases.VerdictEligible && !blocked[candidateID] {
			selected := candidateID
			result.BestEligibleCandidateID = &selected
		}
	}
	return result, nil
}

func dryRunPolicySource(search postgres.SearchContext) string {
	switch {
	case search.OpenRound != nil:
		return "open_round"
	case search.SearchRoundID != (uuid.UUID{}):
		return "discovery_round"
	default:
		return "profile"
	}
}

func dryRunPolicySourceLimitation(source string) string {
	switch source {
	case "open_round":
		return "The open acquisition round's policy snapshot is used for evaluation; changing the profile does not rewrite this snapshot."
	case "discovery_round":
		return "The exhausted discovery round's policy snapshot is used for evaluation; changing the profile does not rewrite this snapshot."
	default:
		return "No open or discovery round snapshot applies; the profile's current policy is used for evaluation."
	}
}
