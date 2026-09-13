package httpapi

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/postgres"
)

// CommissioningV2Service is the narrow owner-authorized CAPS surface exposed
// by the owner HTTP API. The concrete implementation lives outside httpapi
// and is the only place that constructs a live executor, so the handler can
// never send a request by itself.
type CommissioningV2Service interface {
	CreateRun(ctx context.Context, integrationID uuid.UUID, fixtures commissioningv2.FixtureDocument) (postgres.IndexerCommissioningRun, error)
	Run(ctx context.Context, runID uuid.UUID) (postgres.IndexerCommissioningRun, error)
	AuthorizeCAPS(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCommissioningRun, error)
	SealSearchPlan(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64, movieCategories, seriesCategories []int, includeMovieFallback, includeSeasonFallback bool) (postgres.IndexerCommissioningRun, error)
	AuthorizeSearches(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCommissioningRun, error)
	ExecuteStep(ctx context.Context, integrationID, runID, stepID uuid.UUID) (commissioningv2.LiveExecuteResult, error)
	FinalizeDraft(ctx context.Context, integrationID, runID uuid.UUID, expectedRevision uint64) (postgres.IndexerCommissioningRun, error)
	Draft(ctx context.Context, integrationID, runID uuid.UUID) (postgres.IndexerCapabilityProfile, error)
}

// requireOwnerMutation layers owner authentication with the Milestone-B
// mutation protections: a trusted canonical origin, rejection of cross-site
// Fetch Metadata, and a strict application/json media type. Missing trusted
// origin configuration disables these mutations entirely.
func (server *Server) requireOwnerMutation(next http.HandlerFunc) http.HandlerFunc {
	return server.requireOwner(func(writer http.ResponseWriter, request *http.Request) {
		trustedOrigin := strings.TrimSpace(server.config.CommissioningV2Origin)
		if server.config.CommissioningV2 == nil || trustedOrigin == "" {
			writeError(writer, http.StatusServiceUnavailable, "not_configured", "commissioning v2 owner API is not configured")
			return
		}
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin == "" || origin != trustedOrigin {
			writeError(writer, http.StatusForbidden, "origin_rejected", "request origin is not approved")
			return
		}
		if site := strings.ToLower(strings.TrimSpace(request.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" && site != "none" {
			writeError(writer, http.StatusForbidden, "origin_rejected", "cross-site request rejected")
			return
		}
		mediaType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
		if mediaType != "application/json" {
			writeError(writer, http.StatusUnsupportedMediaType, "invalid_content_type", "application/json is required")
			return
		}
		next(writer, request)
	})
}

type commissioningV2RunPreview struct {
	RunID              string                       `json:"run_id"`
	IndexerID          string                       `json:"indexer_id"`
	State              string                       `json:"state"`
	Revision           uint64                       `json:"revision"`
	CapsAuthorizedAt   *time.Time                   `json:"caps_authorized_at,omitempty"`
	MaximumAPICalls    uint8                        `json:"maximum_api_calls"`
	PlannedAPICalls    uint8                        `json:"planned_api_calls"`
	GeneratedProfileID string                       `json:"generated_profile_id,omitempty"`
	Steps              []commissioningV2StepPreview `json:"steps"`
}

type commissioningV2StepPreview struct {
	StepID                  string `json:"step_id"`
	Ordinal                 uint8  `json:"ordinal"`
	Phase                   string `json:"phase"`
	TargetKind              string `json:"target_kind"`
	State                   string `json:"state"`
	StructuralRequestSHA256 string `json:"structural_request_sha256"`
}

func commissioningV2Preview(run postgres.IndexerCommissioningRun) commissioningV2RunPreview {
	preview := commissioningV2RunPreview{
		RunID:            run.ID.String(),
		IndexerID:        run.IndexerInstanceID.String(),
		State:            string(run.State),
		Revision:         run.Revision,
		CapsAuthorizedAt: run.CAPSAuthorizedAt,
		MaximumAPICalls:  run.MaximumAPICalls,
		PlannedAPICalls:  run.PlannedAPICalls,
		Steps:            make([]commissioningV2StepPreview, 0, len(run.Steps)),
	}
	if run.GeneratedProfileID != nil {
		preview.GeneratedProfileID = run.GeneratedProfileID.String()
	}
	for _, step := range run.Steps {
		preview.Steps = append(preview.Steps, commissioningV2StepPreview{
			StepID:                  step.ID.String(),
			Ordinal:                 step.Ordinal,
			Phase:                   string(step.Phase),
			TargetKind:              string(step.Target.Kind),
			State:                   string(step.State),
			StructuralRequestSHA256: hex.EncodeToString(step.StructuralRequestSHA256),
		})
	}
	return preview
}

func (server *Server) createCommissioningV2Run(writer http.ResponseWriter, request *http.Request) {
	integrationID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return
	}
	var fixtures commissioningv2.FixtureDocument
	if err := decodeJSON(request, &fixtures, 256<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "commissioning fixtures are invalid")
		return
	}
	run, err := server.config.CommissioningV2.CreateRun(request.Context(), integrationID, fixtures)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: commissioningV2Preview(run)})
}

func (server *Server) getCommissioningV2Run(writer http.ResponseWriter, request *http.Request) {
	if server.config.CommissioningV2 == nil {
		writeError(writer, http.StatusServiceUnavailable, "not_configured", "commissioning v2 owner API is not configured")
		return
	}
	runID, err := uuid.Parse(request.PathValue("run"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "run ID is invalid")
		return
	}
	run, err := server.config.CommissioningV2.Run(request.Context(), runID)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: commissioningV2Preview(run)})
}

func (server *Server) authorizeCommissioningV2CAPS(writer http.ResponseWriter, request *http.Request) {
	integrationID, runID, ok := commissioningV2PathIDs(writer, request)
	if !ok {
		return
	}
	expectedRevision, ok := decodeExpectedRevision(writer, request)
	if !ok {
		return
	}
	run, err := server.config.CommissioningV2.AuthorizeCAPS(request.Context(), integrationID, runID, expectedRevision)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: commissioningV2Preview(run)})
}

func (server *Server) sealCommissioningV2SearchPlan(writer http.ResponseWriter, request *http.Request) {
	integrationID, runID, ok := commissioningV2PathIDs(writer, request)
	if !ok {
		return
	}
	var input struct {
		ExpectedRevision              uint64 `json:"expected_revision"`
		MovieCategoryIDs              []int  `json:"movie_category_ids"`
		SeriesCategoryIDs             []int  `json:"series_category_ids"`
		IncludeMovieFallback          bool   `json:"include_movie_fallback"`
		IncludeCompleteSeasonFallback bool   `json:"include_complete_season_fallback"`
	}
	if err := decodeJSON(request, &input, 64<<10); err != nil || input.ExpectedRevision == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "expected_revision is required")
		return
	}
	run, err := server.config.CommissioningV2.SealSearchPlan(request.Context(), integrationID, runID, input.ExpectedRevision, input.MovieCategoryIDs, input.SeriesCategoryIDs, input.IncludeMovieFallback, input.IncludeCompleteSeasonFallback)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: commissioningV2Preview(run)})
}

func (server *Server) authorizeCommissioningV2Searches(writer http.ResponseWriter, request *http.Request) {
	integrationID, runID, ok := commissioningV2PathIDs(writer, request)
	if !ok {
		return
	}
	expectedRevision, ok := decodeExpectedRevision(writer, request)
	if !ok {
		return
	}
	run, err := server.config.CommissioningV2.AuthorizeSearches(request.Context(), integrationID, runID, expectedRevision)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: commissioningV2Preview(run)})
}

func decodeExpectedRevision(writer http.ResponseWriter, request *http.Request) (uint64, bool) {
	var input struct {
		ExpectedRevision uint64 `json:"expected_revision"`
	}
	if err := decodeJSON(request, &input, 8<<10); err != nil || input.ExpectedRevision == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "expected_revision is required")
		return 0, false
	}
	return input.ExpectedRevision, true
}

func (server *Server) finalizeCommissioningV2Draft(writer http.ResponseWriter, request *http.Request) {
	integrationID, runID, ok := commissioningV2PathIDs(writer, request)
	if !ok {
		return
	}
	expectedRevision, ok := decodeExpectedRevision(writer, request)
	if !ok {
		return
	}
	run, err := server.config.CommissioningV2.FinalizeDraft(request.Context(), integrationID, runID, expectedRevision)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: commissioningV2Preview(run)})
}

func (server *Server) getCommissioningV2Draft(writer http.ResponseWriter, request *http.Request) {
	if server.config.CommissioningV2 == nil {
		writeError(writer, http.StatusServiceUnavailable, "not_configured", "commissioning v2 owner API is not configured")
		return
	}
	integrationID, runID, ok := commissioningV2PathIDs(writer, request)
	if !ok {
		return
	}
	draft, err := server.config.CommissioningV2.Draft(request.Context(), integrationID, runID)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: draft})
}

func (server *Server) executeCommissioningV2CAPS(writer http.ResponseWriter, request *http.Request) {
	integrationID, runID, ok := commissioningV2PathIDs(writer, request)
	if !ok {
		return
	}
	stepID, err := uuid.Parse(request.PathValue("step"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "step ID is invalid")
		return
	}
	result, err := server.config.CommissioningV2.ExecuteStep(request.Context(), integrationID, runID, stepID)
	if err != nil {
		writeCommissioningV2Error(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"state":        string(result.State),
		"safe_outcome": string(result.SafeOutcome),
		"safe_code":    string(result.SafeCode),
		"retry_at":     result.RetryAt,
		"sent":         result.Sent,
	}})
}

func commissioningV2PathIDs(writer http.ResponseWriter, request *http.Request) (uuid.UUID, uuid.UUID, bool) {
	integrationID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "integration ID is invalid")
		return uuid.UUID{}, uuid.UUID{}, false
	}
	runID, err := uuid.Parse(request.PathValue("run"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "run ID is invalid")
		return uuid.UUID{}, uuid.UUID{}, false
	}
	return integrationID, runID, true
}

func writeCommissioningV2Error(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "commissioning object was not found")
	case errors.Is(err, commissioningv2.ErrLiveExecutorNotAuthorized):
		writeError(writer, http.StatusConflict, "not_authorized", "the exact run is not owner-authorized for CAPS")
	case errors.Is(err, commissioningv2.ErrLiveExecutorStateConflict), errors.Is(err, postgres.ErrConcurrentModification):
		writeError(writer, http.StatusConflict, "state_conflict", "the commissioning attempt state changed")
	case errors.Is(err, postgres.ErrIndexerCommissioningDraftUnavailable):
		writeError(writer, http.StatusConflict, "draft_unavailable", "the commissioning run is not ready for draft generation")
	case errors.Is(err, postgres.ErrIndexerCommissioningConfigChanged):
		writeError(writer, http.StatusConflict, "configuration_changed", "the integration configuration changed")
	case errors.Is(err, commissioningv2.ErrLiveExecutorCredential):
		writeError(writer, http.StatusServiceUnavailable, "credential_unavailable", "the commissioning credential is unavailable")
	case errors.Is(err, postgres.ErrInvalidIndexerCommissioning):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_request", "the commissioning request is invalid")
	default:
		writeError(writer, http.StatusInternalServerError, "commissioning_failed", "the commissioning action failed")
	}
}
