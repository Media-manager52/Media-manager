package releases

import (
	"strconv"
	"strings"
)

// MovieTitleMatches also accepts a catalog event year moved after the title:
// "SummerSlam 2026 Sunday" -> "SummerSlam Sunday 2026". Every other word and
// its order must match, and the moved token must be the canonical movie year.
// This does not invent aliases, accept other event parts, or reorder words.
func MovieTitleMatches(releaseTitle, requestedTitle string, year uint16) bool {
	if TitleMatches(releaseTitle, requestedTitle) {
		return true
	}
	if year == 0 {
		return false
	}
	wanted := strconv.Itoa(int(year))
	for _, spelling := range TitleSpellings(requestedTitle) {
		words := strings.Fields(normalizedTitleWords(spelling))
		index, occurrences := -1, 0
		for i, word := range words {
			if word == wanted {
				index, occurrences = i, occurrences+1
			}
		}
		if occurrences != 1 || index <= 0 || index >= len(words)-1 {
			continue
		}
		moved := append(append([]string{}, words[:index]...), words[index+1:]...)
		moved = append(moved, wanted)
		if TitleMatches(releaseTitle, strings.Join(moved, " ")) {
			return true
		}
	}
	return false
}
