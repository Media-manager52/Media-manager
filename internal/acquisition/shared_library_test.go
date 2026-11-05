package acquisition

import (
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

// TestSharedPlexLibraryKeepsEachRootsFiles proves a Plex movie library with two
// folders (movies/1080p as the standard root and movies/2160p as the 4K root,
// which Plex merges into two versions of one film) gives each symlink to the
// root whose folder holds it, whichever root walks the library first.
func TestSharedPlexLibraryKeepsEachRootsFiles(t *testing.T) {
	base := t.TempDir()
	standardFolder := filepath.Join(base, "movies", "1080p")
	fourKFolder := filepath.Join(base, "movies", "2160p")
	source := "/mnt/altmount"
	link := func(folder, name, target string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(folder, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(source, target), filepath.Join(folder, name, name+".mkv")); err != nil {
			t.Fatal(err)
		}
	}
	link(standardFolder, "Film (2025)", "movies/Film.2025.1080p/film.mkv")
	link(fourKFolder, "Film (2025)", "movies/Film.2025.2160p/film.mkv")
	standard := postgres.LibraryRoot{ID: uuid.NewV7(), Name: "Film", Enabled: true, Edition: postgres.EditionStandard, DestinationRoot: standardFolder, PlexVisibleRoot: "/movies/1080p", SourceVisibleRoot: source}
	fourK := postgres.LibraryRoot{ID: uuid.NewV7(), Name: "Film 4K", Enabled: true, Edition: postgres.Edition4K, DestinationRoot: fourKFolder, PlexVisibleRoot: "/movies/2160p", SourceVisibleRoot: source}
	roots := []postgres.LibraryRoot{standard, fourK}
	// Both roots see both folders of the one Plex library.
	locations := map[string]string{"/movies/1080p": standardFolder, "/movies/2160p": fourKFolder}
	for _, order := range [][]postgres.LibraryRoot{{standard, fourK}, {fourK, standard}} {
		index, walked := map[string][]unmanagedLink{}, map[string]bool{}
		for _, root := range order {
			if err := collectRootLinks(index, walked, roots, root, locations); err != nil {
				t.Fatal(err)
			}
		}
		for target, want := range map[string]string{"movies/Film.2025.1080p/film.mkv": postgres.EditionStandard, "movies/Film.2025.2160p/film.mkv": postgres.Edition4K} {
			links := index[target]
			if len(links) != 1 || links[0].Root.Edition != want {
				t.Fatalf("order %s first: %s indexed as %+v, want one %s link", order[0].Name, target, links, want)
			}
		}
	}
	// A whole Plex folder nested inside another root's folder still belongs
	// to the closer root, and a folder outside every root belongs to none.
	nested := postgres.LibraryRoot{ID: uuid.NewV7(), Enabled: true, DestinationRoot: filepath.Join(base, "movies")}
	all := []postgres.LibraryRoot{standard, fourK, nested}
	if !otherRootOwns(all, nested, filepath.Join(fourKFolder, "Film (2025)")) || otherRootOwns(all, fourK, filepath.Join(fourKFolder, "Film (2025)")) {
		t.Fatal("the closest root folder must own a path")
	}
	if otherRootOwns(all, standard, filepath.Join(base, "elsewhere", "x.mkv")) {
		t.Fatal("a folder outside every root belongs to no other root")
	}
	disabled := fourK
	disabled.Enabled = false
	if otherRootOwns([]postgres.LibraryRoot{standard, disabled}, standard, fourKFolder) {
		t.Fatal("a disabled root owns nothing")
	}
}
