package postgres

import "fmt"

// Selection from persisted decisions needs the same exact-title subtitle guard
// as fresh searches. Otherwise stale eligible cross-posts spend grabs and slots
// after an output inspection has already disproved the required subtitles.
// Callers supply only internal SQL aliases, never request data.
func knownSubtitleFailureSQL(alias string) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM workflow_release_exclusions failed_exclusion
		JOIN release_candidates failed_candidate ON failed_candidate.id=failed_exclusion.candidate_id
		 AND failed_candidate.workflow_id=failed_exclusion.workflow_id
		JOIN subtitle_verifications failed_verification ON
		 (failed_verification.id=failed_exclusion.verification_id OR
		  (failed_exclusion.failure_kind='movie_primary_ambiguous' AND failed_verification.candidate_id=failed_candidate.id))
		 AND failed_verification.workflow_id=failed_exclusion.workflow_id AND failed_verification.status='unverified'
		WHERE failed_exclusion.workflow_id=%[1]s.workflow_id AND failed_exclusion.failure_kind IN ('subtitle_verification','movie_primary_ambiguous')
		 AND btrim(failed_candidate.title)<>'' AND lower(btrim(failed_candidate.title))=lower(btrim(%[1]s.title))
	)`, alias)
}
