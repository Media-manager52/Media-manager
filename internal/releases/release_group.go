package releases

import (
	"regexp"
	"strings"
)

// releaseGroupSuffix is the scene convention for the group that made a
// release: the last "-" token of the name ("Show.S01E01.1080p.WEB-DL-NTb").
var releaseGroupSuffix = regexp.MustCompile(`-([A-Za-z0-9]{1,24})$`)

// videoExtension is a file extension some providers leave on the name.
var videoExtension = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|ts|nzb)$`)

// ReleaseGroup returns the lowercased release group at the end of a release
// name, or "" when the name has none.
func ReleaseGroup(title string) string {
	name := videoExtension.ReplaceAllString(strings.TrimSpace(title), "")
	match := releaseGroupSuffix.FindStringSubmatch(name)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}

// DanishEvidence grades what a release says about Danish before download: 2
// for observed or inferred Danish subtitle or audio facts, 1 for a Danish
// clue in the name, 0 for nothing. It is a ranking clue, never proof.
func DanishEvidence(observation Observation) int {
	rank := 0
	if danishSubtitleHint.MatchString(observation.Title) {
		rank = 1
	}
	for _, fact := range observation.Facts {
		if (fact.Name == "subtitle_languages" || fact.Name == "audio_languages" || fact.Name == "audio_language_hints") && (fact.State == FactObserved || fact.State == FactInferred) && intersectsFold(fact.Values, []string{"da"}) {
			rank = 2
		}
	}
	return rank
}
