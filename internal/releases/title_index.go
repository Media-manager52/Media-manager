package releases

import "strings"

// TitleIndex finds, for a release name, the catalog entries whose saved title
// the name begins with. It answers the same question as TitleMatches and
// MovieTitleMatches for many titles at once, so a list of new uploads can be
// compared with a whole catalog. It only narrows: callers confirm every
// result with the normal title, identity and scope checks.
type TitleIndex struct {
	entries  map[string][]int
	maxWords int
}

func NewTitleIndex() *TitleIndex {
	return &TitleIndex{entries: map[string][]int{}}
}

// Add files entry under every release spelling of title. A year other than 0
// also files the spelling with its inner event year moved to the end.
func (index *TitleIndex) Add(title string, year uint16, entry int) {
	for _, spelling := range TitleSpellings(title) {
		index.add(normalizedTitleWords(spelling), entry)
		if year != 0 {
			if moved, ok := movedYearTitle(spelling, year); ok {
				index.add(moved, entry)
			}
		}
	}
}

func (index *TitleIndex) add(key string, entry int) {
	if key == "" {
		return
	}
	for _, existing := range index.entries[key] {
		if existing == entry {
			return
		}
	}
	index.entries[key] = append(index.entries[key], entry)
	if words := strings.Count(key, " ") + 1; words > index.maxWords {
		index.maxWords = words
	}
}

// Lookup returns every entry with a title that releaseTitle begins with, each
// once, in the order shorter titles first.
func (index *TitleIndex) Lookup(releaseTitle string) []int {
	words := strings.Fields(normalizedTitleWords(releaseTitle))
	if len(words) > index.maxWords {
		words = words[:index.maxWords]
	}
	var result []int
	seen := map[int]bool{}
	var prefix strings.Builder
	for i, word := range words {
		if i > 0 {
			prefix.WriteByte(' ')
		}
		prefix.WriteString(word)
		for _, entry := range index.entries[prefix.String()] {
			if !seen[entry] {
				seen[entry] = true
				result = append(result, entry)
			}
		}
	}
	return result
}
