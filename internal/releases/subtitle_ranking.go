package releases

import (
	"regexp"
	"sort"
)

// DanishSubtitleHintPattern is shared with PostgreSQL selection. These title
// tokens are ranking clues only, never subtitle facts or proof of verification.
const DanishSubtitleHintPattern = `(?i)(^|[^[:alnum:]])(nordic|danish|dansk|dansubs?|dksubs?)($|[^[:alnum:]])`

var danishSubtitleHint = regexp.MustCompile(DanishSubtitleHintPattern)

// RankForDanishSubtitles preserves eligibility and policy scores. Within each
// verdict, explicit observed/inferred da evidence outranks broad title clues,
// which outrank unknown evidence. The caller must require Danish subtitles.
func RankForDanishSubtitles(observations []Observation, evaluations []Evaluation) []Evaluation {
	return RankForDanishSubtitlesWith(observations, evaluations, RankOptions{})
}

// RankForDanishSubtitlesWith is RankForDanishSubtitles over RankWith's order.
func RankForDanishSubtitlesWith(observations []Observation, evaluations []Evaluation, options RankOptions) []Evaluation {
	ranked := RankWith(observations, evaluations, options)
	evidence := make(map[string]int, len(observations))
	tier := make(map[string]int, len(observations))
	for _, observation := range observations {
		evidence[observation.ID.String()] = DanishEvidence(observation)
		tier[observation.ID.String()] = options.danishGroupTier(ReleaseGroup(observation.Title))
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Verdict != ranked[j].Verdict {
			return verdictOrder(ranked[i].Verdict) < verdictOrder(ranked[j].Verdict)
		}
		left, right := ranked[i].ObservationID.String(), ranked[j].ObservationID.String()
		if evidence[left] != evidence[right] {
			return evidence[left] > evidence[right]
		}
		return tier[left] > tier[right]
	})
	return ranked
}
