package acquisition

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// nfoRejection is the exclusion kind for a release whose NFO shows that it
// lacks a required language. The kind keeps the exclusion to this exact
// provider release: unlike a completed output inspection it does not extend to
// cross-posts with the same title.
const nfoRejectionKind = "subtitle_verification"

// checkSelectedNFO reads the selected release's NFO before its NZB is
// retrieved, when the provider advertised one and gave no subtitle or audio
// language of its own. It returns a rejection reason only when the NFO states
// that a required language is missing (see NFOLanguages.MissingRequired).
// Every failure is ignored: the release then follows the normal
// download-and-inspect path exactly as before.
func (service *RetrievalService) checkSelectedNFO(ctx context.Context, retrieval postgres.RetrievalContext, client *newznab.Client) (string, bool) {
	recorded, err := service.Store.NFOCheckRecorded(ctx, retrieval.CandidateID)
	if err != nil || recorded {
		return "", false
	}
	check, err := service.Store.LoadNFOCheckContext(ctx, retrieval.Workflow.ID, retrieval.CandidateID)
	if err != nil {
		return "", false
	}
	policy, err := subtitles.ParsePolicy(check.Policy)
	if err != nil {
		return "", false
	}
	required := nfoCheckedLanguages(policy, check.OriginalLanguage)
	if len(required) == 0 || hasProviderLanguageEvidence(check.Observation) || !client.AdvertisesNFO(check.Observation) {
		return "", false
	}
	now := time.Now().UTC()
	body, err := client.InspectAdvertisedNFO(ctx, check.Observation)
	if err != nil {
		outcome := "unavailable"
		if !errors.Is(err, newznab.ErrNFOUnavailable) {
			outcome = "failed"
		}
		// A budget or pacing refusal sent no request: try again next time.
		var deferred interface{ RetryAt() time.Time }
		if errors.As(err, &deferred) {
			return "", false
		}
		_ = service.Store.RecordNFOCheck(ctx, retrieval.Workflow.ID, retrieval.CandidateID, outcome, "", now)
		return "", false
	}
	evidence := newznab.ParseNFOLanguages(body)
	for _, language := range required {
		if evidence.MissingRequired(language, check.Observation.Title) {
			reason := "release NFO lists subtitles " + nfoLanguageSummary(evidence.Subtitles) + " and audio " + nfoLanguageSummary(evidence.Audio) + " with no " + language + "; excluded before download"
			if err := service.Store.RecordNFOCheck(ctx, retrieval.Workflow.ID, retrieval.CandidateID, "rejected", reason, now); err != nil {
				return "", false
			}
			return reason, true
		}
	}
	_ = service.Store.RecordNFOCheck(ctx, retrieval.Workflow.ID, retrieval.CandidateID, "passed", "", now)
	return "", false
}

// nfoCheckedLanguages are the required subtitle-or-audio languages an NFO can
// rule out. A title originally in the language carries it as its own audio,
// so an NFO's subtitle list cannot rule it out.
func nfoCheckedLanguages(policy subtitles.Policy, originalLanguage string) []string {
	original, _ := subtitles.CanonicalLanguage(originalLanguage)
	languages := []string{}
	for _, language := range policy.Required {
		if language != "" && language != original {
			languages = append(languages, language)
		}
	}
	return languages
}

// hasProviderLanguageEvidence is true when the provider's own attributes or the
// release name already state subtitle or audio languages: search evaluation
// has then decided the language requirement on that evidence.
func hasProviderLanguageEvidence(observation releases.Observation) bool {
	observation = releases.InferTitleFacts(observation, observation.ObservedAt)
	for _, fact := range observation.Facts {
		switch strings.ToLower(strings.TrimSpace(fact.Name)) {
		case "subtitle_languages", "audio_languages", "audio_language_hints":
			if len(fact.Values) > 0 {
				return true
			}
		}
	}
	return false
}

func nfoLanguageSummary(languages []string) string {
	if len(languages) == 0 {
		return "none"
	}
	return strings.Join(languages, ",")
}
