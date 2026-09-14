package library

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderMovieDeterministicNaming pins the exact Plex-safe movie layout for
// the verified provider identities Media Manager publishes. Naming is
// deterministic: the same verified facts always produce the same folder and
// file, and every known technical fact is normalized and sorted so a release
// token cannot collide with a duplicate release.
func TestRenderMovieDeterministicNaming(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		input      MovieName
		wantFolder string
		wantFile   string
	}{
		{
			name:       "tmdb with technical facts and release token",
			input:      MovieName{Title: "Dune: Part Two", Year: 2024, Provider: "tmdb", ProviderID: "693134", Technical: []string{"2160p", "UHD BluRay", "TrueHD Atmos", "MM-8f14e45f"}, Extension: ".mkv"},
			wantFolder: "Dune- Part Two (2024) {tmdb-693134}",
			wantFile:   "Dune- Part Two (2024) {tmdb-693134} - [2160p MM-8f14e45f TrueHDAtmos UHDBluRay].mkv",
		},
		{
			name:       "imdb fallback identity",
			input:      MovieName{Title: "Klaus", Year: 2019, Provider: "imdb", ProviderID: "tt4729430", Extension: "mp4"},
			wantFolder: "Klaus (2019) {imdb-tt4729430}",
			wantFile:   "Klaus (2019) {imdb-tt4729430}.mp4",
		},
		{
			name:       "illegal characters become deterministic separators",
			input:      MovieName{Title: `A/B:C*D?E"F<G>H|I`, Year: 2026, Provider: "tmdb", ProviderID: "42", Extension: ".mkv"},
			wantFolder: "A-B-C-D-E-F-G-H-I (2026) {tmdb-42}",
			wantFile:   "A-B-C-D-E-F-G-H-I (2026) {tmdb-42}.mkv",
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			folder, file, err := RenderMovie(testCase.input)
			if err != nil {
				t.Fatal(err)
			}
			if folder != testCase.wantFolder || file != testCase.wantFile {
				t.Fatalf("RenderMovie() = %q, %q; want %q, %q", folder, file, testCase.wantFolder, testCase.wantFile)
			}
			if _, err := DestinationRelative(folder, file); err != nil {
				t.Fatalf("rendered movie is not a contained relative path: %v", err)
			}
			// Re-rendering from the same facts must be byte-for-byte identical.
			againFolder, againFile, err := RenderMovie(testCase.input)
			if err != nil || againFolder != folder || againFile != file {
				t.Fatalf("movie naming is not deterministic: %q, %q (%v)", againFolder, againFile, err)
			}
		})
	}
}

func TestRenderMovieRejectsUnsafeOrIncompleteFacts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input MovieName
	}{
		{name: "missing title", input: MovieName{Year: 2026, Provider: "tmdb", ProviderID: "42", Extension: ".mkv"}},
		{name: "missing year", input: MovieName{Title: "Movie", Provider: "tmdb", ProviderID: "42", Extension: ".mkv"}},
		{name: "unsupported provider", input: MovieName{Title: "Movie", Year: 2026, Provider: "tvdb", ProviderID: "42", Extension: ".mkv"}},
		{name: "injected provider id", input: MovieName{Title: "Movie", Year: 2026, Provider: "tmdb", ProviderID: "42} {imdb-evil", Extension: ".mkv"}},
		{name: "unsupported extension", input: MovieName{Title: "Movie", Year: 2026, Provider: "tmdb", ProviderID: "42", Extension: ".nzb"}},
		{name: "extension directory traversal", input: MovieName{Title: "Movie", Year: 2026, Provider: "tmdb", ProviderID: "42", Extension: "../../etc/passwd"}},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := RenderMovie(testCase.input); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("RenderMovie(%+v) error=%v, want ErrUnsafePath", testCase.input, err)
			}
		})
	}
}

// TestRenderMovieTruncatesWithoutLosingPlexIdentity proves a very long title is
// bounded while the year, provider identity, extension, and trailer stay
// intact. Plex resolves media by the identity token, so it must never be
// truncated away.
func TestRenderMovieTruncatesWithoutLosingPlexIdentity(t *testing.T) {
	t.Parallel()
	folder, file, err := RenderMovie(MovieName{Title: strings.Repeat("Very Long Movie Title ", 40), Year: 2026, Provider: "tmdb", ProviderID: "123456", Technical: []string{"1080p"}, Extension: ".mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(folder, " (2026) {tmdb-123456}") {
		t.Fatalf("folder lost its Plex identity: %q", folder)
	}
	if !strings.HasSuffix(file, ".mkv") || !strings.Contains(file, " (2026) {tmdb-123456}") {
		t.Fatalf("file lost its Plex identity or extension: %q", file)
	}
	if len(file) > 220 || len(folder) > 180 {
		t.Fatalf("naming exceeded its filesystem budget: folder=%d file=%d", len(folder), len(file))
	}
}

// TestRenderEpisodeSetDeterministicNaming pins the exact Plex-safe season-pack
// layout, including multi-episode coverage and the release token that keeps an
// upgrade's replacement path distinct from the media it will replace.
func TestRenderEpisodeSetDeterministicNaming(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		show             string
		year             uint16
		providerID       string
		season           uint16
		episodes         []uint16
		title            string
		technical        []string
		extension        string
		wantFolder       string
		wantSeasonFolder string
		wantFile         string
	}{
		{
			name:             "multi episode pack asset",
			show:             "The Investigation",
			year:             2020,
			providerID:       "378992",
			season:           1,
			episodes:         []uint16{2, 1},
			technical:        []string{"1080p", "MM-abcdef12"},
			extension:        ".mkv",
			wantFolder:       "The Investigation (2020) {tvdb-378992}",
			wantSeasonFolder: "Season 01",
			wantFile:         "The Investigation (2020) - S01E01-E02 [1080p MM-abcdef12].mkv",
		},
		{
			name:             "single episode includes verified title",
			show:             "Adolescence",
			year:             2025,
			providerID:       "442533",
			season:           2,
			episodes:         []uint16{3},
			title:            "Episode Three",
			technical:        []string{"2160p"},
			extension:        ".mkv",
			wantFolder:       "Adolescence (2025) {tvdb-442533}",
			wantSeasonFolder: "Season 02",
			wantFile:         "Adolescence (2025) - S02E03 - Episode Three [2160p].mkv",
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			folder, seasonFolder, file, err := RenderEpisodeSet(testCase.show, testCase.year, "tvdb", testCase.providerID, testCase.season, testCase.episodes, testCase.title, testCase.technical, testCase.extension)
			if err != nil {
				t.Fatal(err)
			}
			if folder != testCase.wantFolder || seasonFolder != testCase.wantSeasonFolder || file != testCase.wantFile {
				t.Fatalf("RenderEpisodeSet() = %q, %q, %q; want %q, %q, %q", folder, seasonFolder, file, testCase.wantFolder, testCase.wantSeasonFolder, testCase.wantFile)
			}
			if _, err := DestinationRelative(folder, seasonFolder, file); err != nil {
				t.Fatalf("rendered episode is not a contained relative path: %v", err)
			}
		})
	}
}

func TestRenderEpisodeSetRejectsNonContiguousCoverage(t *testing.T) {
	t.Parallel()
	for _, episodes := range [][]uint16{{}, {0}, {1, 3}, {1, 2, 2}} {
		if _, _, _, err := RenderEpisodeSet("Show", 2026, "tvdb", "123", 1, episodes, "", nil, ".mkv"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("episodes %v error=%v, want ErrUnsafePath", episodes, err)
		}
	}
}

func TestDestinationRelativeRejectsEscapingParts(t *testing.T) {
	t.Parallel()
	if _, err := DestinationRelative("Show (2026)", "..", "file.mkv"); err == nil {
		t.Fatal("destination traversal was accepted")
	}
	if _, err := DestinationRelative("/absolute", "file.mkv"); err == nil {
		t.Fatal("absolute destination part was accepted")
	}
	got, err := DestinationRelative("Show (2026)", "Season 01", "Show (2026) - S01E01.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("Show (2026)", "Season 01", "Show (2026) - S01E01.mkv") {
		t.Fatalf("unexpected destination %q", got)
	}
}
