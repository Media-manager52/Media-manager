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
	for _, spelling := range TitleSpellings(requestedTitle) {
		if moved, ok := movedYearTitle(spelling, year); ok && TitleMatches(releaseTitle, moved) {
			return true
		}
	}
	return false
}

// movedYearTitle is a normalized title with its single, inner event year
// moved to the end ("summerslam 2026 sunday" -> "summerslam sunday 2026").
func movedYearTitle(title string, year uint16) (string, bool) {
	wanted := strconv.Itoa(int(year))
	words := strings.Fields(normalizedTitleWords(title))
	index, occurrences := -1, 0
	for i, word := range words {
		if word == wanted {
			index, occurrences = i, occurrences+1
		}
	}
	if occurrences != 1 || index <= 0 || index >= len(words)-1 {
		return "", false
	}
	moved := append(append([]string{}, words[:index]...), words[index+1:]...)
	return strings.Join(append(moved, wanted), " "), true
}
