package acquisition

import (
	"strings"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

const maxSearchTitles = 8

// Use only names already attached to this catalog item. An alias changes the
// title evidence, never the verified IDs, year, episode scope or output policy.
func mediaTitleMatches(releaseTitle string, media catalog.MediaItem) bool {
	if releases.TitleMatches(releaseTitle, media.Title) {
		return true
	}
	if media.Metadata == nil {
		return false
	}
	if releases.TitleMatches(releaseTitle, media.Metadata.OriginalTitle) {
		return true
	}
	for _, alias := range media.Metadata.Aliases {
		if strings.TrimSpace(alias.Source) != "" && releases.TitleMatches(releaseTitle, alias.Name) {
			return true
		}
	}
	return false
}

// One title per scheduled scan, not a burst of extra requests. Original and
// canonical names come first, followed by Danish aliases, then other aliases.
// CompleteSearches is durable; deferred/replayed work keeps the same query.
func searchTitle(search postgres.SearchContext) string {
	titles := make([]string, 0, maxSearchTitles)
	seen := map[string]bool{}
	add := func(title string) {
		query := releases.SearchQueryTitle(title)
		key := strings.ToLower(query)
		if key != "" && !seen[key] && len(titles) < maxSearchTitles {
			seen[key] = true
			titles = append(titles, query)
		}
	}
	if search.Media.Metadata != nil {
		add(search.Media.Metadata.OriginalTitle)
	}
	add(search.Media.Title)
	if search.Media.Metadata != nil {
		for _, danish := range []bool{true, false} {
			for _, alias := range search.Media.Metadata.Aliases {
				language := strings.ToLower(strings.TrimSpace(alias.Language))
				isDanish := language == "da" || language == "dan" || language == "dk"
				if strings.TrimSpace(alias.Source) != "" && danish == isDanish {
					add(alias.Name)
				}
			}
		}
	}
	if len(titles) == 0 {
		return ""
	}
	return titles[search.CompleteSearches%uint64(len(titles))]
}
