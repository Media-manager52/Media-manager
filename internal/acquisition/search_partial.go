package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/adapters/oldboys"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"uuid"
)

// plannedSearchIndexer freezes the exact request shape used for both the
// retry-intent fingerprint and this attempt. Provider planning performs no
// network I/O; credentials and clients are still built only for pending
// indexers.
type plannedSearchIndexer struct {
	Integration    postgres.Integration
	Config         indexerConfig
	Request        newznab.SearchRequest
	OldBoysPhase   uint64
	OldBoysRequest oldboys.SearchRequest
	PlanError      string
	ProfileID      uuid.UUID
	ProfileVersion uint64
}

func (service *SearchService) planSearchIndexers(ctx context.Context, search postgres.SearchContext, now time.Time) ([]plannedSearchIndexer, error) {
	planned := make([]plannedSearchIndexer, 0, len(search.Indexers))
	for _, integration := range search.Indexers {
		item := plannedSearchIndexer{Integration: integration}
		if err := json.Unmarshal(integration.Config, &item.Config); err != nil {
			item.PlanError = "indexer_configuration_unavailable"
			planned = append(planned, item)
			continue
		}
		item.Request = buildRequest(search, item.Config)
		switch integration.Kind {
		case "oldboys":
			phase, phaseErr := service.Store.OldBoysSearchPhase(ctx, search.Workflow.ID, integration)
			if phaseErr != nil {
				return nil, phaseErr
			}
			item.OldBoysPhase = phase
			nativeSearch := search
			nativeSearch.CompleteSearches = phase
			item.OldBoysRequest = oldBoysSearchRequest(nativeSearch)
		case "newznab":
			plan, profileID, profileVersion, planErr := service.searchProfilePlanWithIdentity(ctx, integration.ID, search, item.Config)
			switch {
			case planErr == nil:
				item.Request = plan.Primary.Request
				item.ProfileID = profileID
				item.ProfileVersion = profileVersion
			case errors.Is(planErr, errNoActiveSearchProfile):
				// The legacy commissioned dialect remains the deliberate fallback.
			default:
				item.PlanError = safeIndexerOutcomeCode(planErr)
			}
			item.Request = numberedSearchRequest(search, item.Request, now)
		}
		planned = append(planned, item)
	}
	return planned, nil
}

type searchIntentIndexer struct {
	ID             string                `json:"id"`
	Kind           string                `json:"kind"`
	Name           string                `json:"name"`
	Enabled        bool                  `json:"enabled"`
	Config         json.RawMessage       `json:"config"`
	SecretRef      string                `json:"secret_ref"`
	UpdatedAt      time.Time             `json:"updated_at"`
	Request        newznab.SearchRequest `json:"request"`
	OldBoysPhase   uint64                `json:"oldboys_phase,omitempty"`
	OldBoysRequest oldboys.SearchRequest `json:"oldboys_request,omitempty"`
	PlanError      string                `json:"plan_error,omitempty"`
	ProfileID      string                `json:"profile_id,omitempty"`
	ProfileVersion uint64                `json:"profile_version,omitempty"`
}

type searchIntent struct {
	WorkflowID          string                              `json:"workflow_id"`
	Media               any                                 `json:"media"`
	Variant             string                              `json:"variant"`
	RoundID             string                              `json:"round_id"`
	Mode                string                              `json:"mode"`
	PolicyVersion       uint64                              `json:"policy_version"`
	Policy              json.RawMessage                     `json:"policy"`
	ProfilePolicy       uint64                              `json:"profile_policy_version"`
	CurrentRelease      *releases.Observation               `json:"current_release,omitempty"`
	CurrentLinkVersions string                              `json:"current_link_versions"`
	CompleteSearches    uint64                              `json:"complete_searches"`
	EpisodeNumbering    any                                 `json:"episode_numbering"`
	Blocks              []postgres.ReleaseBlock             `json:"blocks"`
	Exclusions          []postgres.WorkflowReleaseExclusion `json:"exclusions"`
	RepairBrokenTitles  []string                            `json:"repair_broken_titles"`
	Indexers            []searchIntentIndexer               `json:"indexers"`
}

// searchIntentFingerprint is stable across retries of one intent. It excludes
// volatile workflow revision/state and evidence fetch timestamps, but includes
// the frozen round/policy, media/metadata, episode mapping, current release,
// query rotation, exclusions and the exact provider plans/configuration.
func searchIntentFingerprint(search postgres.SearchContext, mode string, planned []plannedSearchIndexer) ([]byte, error) {
	blocks := append([]postgres.ReleaseBlock(nil), search.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID.String() < blocks[j].ID.String() })
	exclusions := append([]postgres.WorkflowReleaseExclusion(nil), search.Exclusions...)
	sort.Slice(exclusions, func(i, j int) bool { return exclusions[i].CandidateID.String() < exclusions[j].CandidateID.String() })
	indexers := make([]searchIntentIndexer, 0, len(planned))
	for _, item := range planned {
		indexers = append(indexers, searchIntentIndexer{
			ID:             item.Integration.ID.String(),
			Kind:           item.Integration.Kind,
			Name:           item.Integration.Name,
			Enabled:        item.Integration.Enabled,
			Config:         item.Integration.Config,
			SecretRef:      item.Integration.SecretRef,
			UpdatedAt:      item.Integration.UpdatedAt,
			Request:        item.Request,
			OldBoysPhase:   item.OldBoysPhase,
			OldBoysRequest: item.OldBoysRequest,
			PlanError:      item.PlanError,
			ProfileID:      item.ProfileID.String(),
			ProfileVersion: item.ProfileVersion,
		})
	}
	sort.Slice(indexers, func(i, j int) bool { return indexers[i].ID < indexers[j].ID })
	input := searchIntent{
		WorkflowID:          search.Workflow.ID.String(),
		Media:               search.Media,
		Variant:             search.Variant,
		RoundID:             search.SearchRoundID.String(),
		Mode:                mode,
		PolicyVersion:       search.PolicyVersion,
		Policy:              search.Policy,
		ProfilePolicy:       search.ProfilePolicyVersion,
		CurrentRelease:      search.CurrentRelease,
		CurrentLinkVersions: search.CurrentLinkVersions,
		CompleteSearches:    search.CompleteSearches,
		EpisodeNumbering:    search.EpisodeNumbering,
		Blocks:              blocks,
		Exclusions:          exclusions,
		RepairBrokenTitles:  append([]string(nil), search.RepairBrokenTitles...),
		Indexers:            indexers,
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func plannedIndexerIDs(planned []plannedSearchIndexer) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(planned))
	for _, item := range planned {
		result = append(result, item.Integration.ID)
	}
	return result
}

func samePlannedIndexerSet(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[uuid.UUID]bool, len(left))
	for _, id := range left {
		values[id] = true
	}
	for _, id := range right {
		if !values[id] {
			return false
		}
	}
	return true
}

func (service *SearchService) loadPartialSearch(ctx context.Context, search postgres.SearchContext, planned []plannedSearchIndexer, mode string, fingerprint []byte, now time.Time) (postgres.SearchPartialRetry, []releases.Observation, map[uuid.UUID]bool, error) {
	empty := func() (postgres.SearchPartialRetry, []releases.Observation, map[uuid.UUID]bool, error) {
		return postgres.SearchPartialRetry{}, nil, map[uuid.UUID]bool{}, nil
	}
	if search.SearchRoundID == (uuid.UUID{}) {
		return empty()
	}
	state, err := service.Store.LoadSearchPartialRetry(ctx, search.Workflow.ID, search.SearchRoundID, mode, fingerprint, now)
	if errors.Is(err, postgres.ErrNotFound) {
		return empty()
	}
	if errors.Is(err, postgres.ErrSearchPartialRetryInvalid) {
		if err := service.Store.DeleteSearchPartialRetry(ctx, search.Workflow.ID, search.SearchRoundID, mode); err != nil {
			return postgres.SearchPartialRetry{}, nil, nil, err
		}
		return empty()
	}
	if err != nil {
		return postgres.SearchPartialRetry{}, nil, nil, err
	}
	plannedIDs := plannedIndexerIDs(planned)
	if !samePlannedIndexerSet(state.PlannedIndexerIDs, plannedIDs) {
		if err := service.Store.DeleteSearchPartialRetry(ctx, state.WorkflowID, state.RoundID, state.Mode); err != nil {
			return postgres.SearchPartialRetry{}, nil, nil, err
		}
		return empty()
	}
	plannedSet := make(map[uuid.UUID]bool, len(plannedIDs))
	for _, id := range plannedIDs {
		plannedSet[id] = true
	}
	for indexerID := range state.CompletedIndexers {
		if !plannedSet[indexerID] {
			if err := service.Store.DeleteSearchPartialRetry(ctx, state.WorkflowID, state.RoundID, state.Mode); err != nil {
				return postgres.SearchPartialRetry{}, nil, nil, err
			}
			return empty()
		}
	}
	observations, err := service.Store.LoadSearchPartialObservations(ctx, state)
	if errors.Is(err, postgres.ErrSearchPartialRetryInvalid) {
		if err := service.Store.DeleteSearchPartialRetry(ctx, state.WorkflowID, state.RoundID, state.Mode); err != nil {
			return postgres.SearchPartialRetry{}, nil, nil, err
		}
		return empty()
	}
	if err != nil {
		return postgres.SearchPartialRetry{}, nil, nil, err
	}
	reused := make(map[uuid.UUID]bool, len(state.CompletedIndexers))
	for indexerID := range state.CompletedIndexers {
		reused[indexerID] = true
	}
	return state, observations, reused, nil
}
