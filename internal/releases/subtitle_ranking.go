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
	ranked := Rank(observations, evaluations)
	evidence := make(map[string]int, len(observations))
	for _, observation := range observations {
		rank := 0
		if danishSubtitleHint.MatchString(observation.Title) {
			rank = 1
		}
		for _, fact := range observation.Facts {
			if fact.Name == "subtitle_languages" && (fact.State == FactObserved || fact.State == FactInferred) && intersectsFold(fact.Values, []string{"da"}) {
				rank = 2
			}
		}
		evidence[observation.ID.String()] = rank
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Verdict != ranked[j].Verdict {
			return verdictOrder(ranked[i].Verdict) < verdictOrder(ranked[j].Verdict)
		}
		return evidence[ranked[i].ObservationID.String()] > evidence[ranked[j].ObservationID.String()]
	})
	return ranked
}
