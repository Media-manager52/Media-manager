package postgres

import (
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestClassifySeerrAutoRoute pins the metadata rules behind the single Auto
// root: kids content wins over holiday and original language, then
// documentary, then Christmas, then Danish originals, then the default.
func TestClassifySeerrAutoRoute(t *testing.T) {
	cases := []struct {
		name     string
		metadata *catalog.MediaMetadata
		want     string
	}{
		{"nil metadata", nil, autoRouteDefault},
		{"family genre", &catalog.MediaMetadata{Genres: []string{"Animation", "Family"}}, autoRouteKids},
		{"kids genre", &catalog.MediaMetadata{Genres: []string{"Kids"}}, autoRouteKids},
		{"documentary genre", &catalog.MediaMetadata{Genres: []string{"Documentary"}}, autoRouteDocumentary},
		{"christmas keyword", &catalog.MediaMetadata{Keywords: []string{"christmas movie"}}, autoRouteHoliday},
		{"holiday keyword", &catalog.MediaMetadata{Keywords: []string{"holiday"}}, autoRouteHoliday},
		{"danish original", &catalog.MediaMetadata{OriginalLanguage: "da"}, autoRouteDanish},
		{"danish original with drama genre", &catalog.MediaMetadata{OriginalLanguage: "da", Genres: []string{"Drama"}}, autoRouteDanish},
		{"family wins over danish", &catalog.MediaMetadata{OriginalLanguage: "da", Genres: []string{"Family"}}, autoRouteKids},
		{"documentary wins over danish", &catalog.MediaMetadata{OriginalLanguage: "da", Genres: []string{"Documentary"}}, autoRouteDocumentary},
		{"other language", &catalog.MediaMetadata{OriginalLanguage: "en", Genres: []string{"Action"}}, autoRouteDefault},
		{"adult animation without family stays default", &catalog.MediaMetadata{OriginalLanguage: "en", Genres: []string{"Animation", "Comedy"}}, autoRouteDefault},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := classifySeerrAutoRoute(testCase.metadata); got != testCase.want {
				t.Fatalf("classifySeerrAutoRoute(%+v)=%q, want %q", testCase.metadata, got, testCase.want)
			}
		})
	}
}
