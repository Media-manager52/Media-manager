package releases

import (
	"strconv"
	"strings"
)

// ConflictingMovieYear detects an explicit release year immediately after the
// longest matching saved title. Years within the movie's name and resolution
// numbers are not release years. A one-year festival/theatrical difference is
// tolerated; a larger contradiction cannot be overridden by provider IDs.
func ConflictingMovieYear(releaseTitle string, year uint16, titles []string) bool {
	if year == 0 {
		return false
	}
	release := normalizedTitleWords(releaseTitle)
	matched := ""
	for _, title := range titles {
		for _, spelling := range TitleSpellings(title) {
			prefix := normalizedTitleWords(spelling)
			if prefix != "" && len(prefix) > len(matched) && strings.HasPrefix(release, prefix+" ") {
				matched = prefix
			}
		}
	}
	if matched == "" {
		return false
	}
	token := strings.Fields(strings.TrimPrefix(release, matched+" "))[0]
	if len(token) != 4 {
		return false
	}
	observed, err := strconv.Atoi(token)
	if err != nil || observed < 1888 || observed > 2199 {
		return false
	}
	difference := observed - int(year)
	return difference < -1 || difference > 1
}
