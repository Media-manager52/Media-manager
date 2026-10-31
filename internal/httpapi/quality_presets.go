package httpapi

import (
	"errors"
	"net/http"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"uuid"
)

func (server *Server) listQualityProfilePresets(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: releases.Presets()})
}

// applyQualityProfilePreset replaces only soft scoring rules for the preset's
// managed facts. It never touches required rules, language requirements or the
// profile's other policy fields, and it never applies the new policy to an
// existing acquisition round: the owner still calls apply-policy explicitly.
func (server *Server) applyQualityProfilePreset(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "quality profile ID is invalid")
		return
	}
	var input applyQualityProfilePresetInput
	if err := decodeJSON(request, &input, 64<<10); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	profiles, err := server.store.ListQualityProfiles(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load quality profiles")
		return
	}
	var profile *postgres.QualityProfile
	for index := range profiles {
		if profiles[index].ID == id {
			profile = &profiles[index]
			break
		}
	}
	if profile == nil {
		writeError(writer, http.StatusNotFound, "not_found", "quality profile was not found")
		return
	}
	if input.PolicyVersion == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "current policy_version is required")
		return
	}
	if input.PolicyVersion != profile.PolicyVersion {
		writeError(writer, http.StatusConflict, "quality_profile_changed", "the quality profile changed; reload it and retry")
		return
	}
	rules, err := releases.ParsePolicyRules(profile.Policy)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_quality_profile", "quality profile policy must contain release rules")
		return
	}
	applied, err := releases.ApplyPreset(rules, releases.Preset(input.Preset))
	if errors.Is(err, releases.ErrUnknownPreset) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_quality_preset", "quality preset is not supported")
		return
	}
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_quality_profile", "quality preset could not be applied to this profile")
		return
	}
	policy, err := releases.WithPolicyRules(profile.Policy, applied)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "quality profile policy could not be updated")
		return
	}
	updated, err := server.store.UpdateQualityProfilePolicyAtVersion(request.Context(), profile.ID, policy, input.PolicyVersion)
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "quality profile was not found")
		return
	}
	if errors.Is(err, postgres.ErrConcurrentModification) {
		writeError(writer, http.StatusConflict, "quality_profile_changed", "the quality profile changed; reload it and retry")
		return
	}
	if errors.Is(err, postgres.ErrInvalidQualityProfilePolicy) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_quality_profile", "quality profile could not be updated with this preset")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "quality profile could not be updated")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: updated, Meta: map[string]string{
		"workflow_policy": "existing acquisition rounds keep their snapshotted policy until the owner applies the policy",
	}})
}
