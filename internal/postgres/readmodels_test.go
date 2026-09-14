package postgres

import "testing"

func TestManagedPublicationPathStaysInsideLibraryRoot(t *testing.T) {
	tests := []struct {
		name                string
		origin              string
		plexVisibleTarget   string
		targetInsideLibrary bool
		want                string
	}{
		{
			name:                "contained target keeps the Plex-visible file",
			origin:              "altmount",
			plexVisibleTarget:   "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
			targetInsideLibrary: true,
			want:                "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
		},
		{
			name:                "escaping target reports the managed directory",
			origin:              "altmount",
			plexVisibleTarget:   "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
			targetInsideLibrary: false,
			want:                "/mnt/media-manager/libraries/movies/Movie (2020)",
		},
		{
			name:                "adoption path is unchanged",
			origin:              "adoption",
			plexVisibleTarget:   "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
			targetInsideLibrary: false,
			want:                "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
		},
		{
			name:                "empty target is unchanged",
			origin:              "altmount",
			plexVisibleTarget:   "",
			targetInsideLibrary: false,
			want:                "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := managedPublicationPath(test.origin, test.plexVisibleTarget, test.targetInsideLibrary); got != test.want {
				t.Fatalf("managedPublicationPath()=%q, want %q", got, test.want)
			}
		})
	}
}
