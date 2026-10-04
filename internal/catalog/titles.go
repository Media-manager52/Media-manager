package catalog

import "strings"

// SavedTitles are names already attached to this exact catalog item. Search
// evaluation and the final retrieval guard must use the same provenance rules.
func (media MediaItem) SavedTitles() []string {
	titles := []string{media.Title}
	if media.Metadata != nil {
		titles = append(titles, media.Metadata.OriginalTitle)
		for _, alias := range media.Metadata.Aliases {
			if strings.TrimSpace(alias.Source) != "" {
				titles = append(titles, alias.Name)
			}
		}
	}
	return titles
}
