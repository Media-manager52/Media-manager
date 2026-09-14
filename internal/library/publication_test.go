package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestValidatePlanRequiresContainedCleanAbsoluteRoots proves publication is
// confined to configured roots before any filesystem mutation. A relative,
// non-canonical, or escaping destination can never become a symlink plan.
func TestValidatePlanRequiresContainedCleanAbsoluteRoots(t *testing.T) {
	t.Parallel()
	valid := LinkPlan{SourceRoot: "/source", SourceRelative: "release/movie.mkv", DestinationRoot: "/library/movies", DestinationRel: "Movie (2026)/Movie (2026).mkv", PlexVisibleSourceRoot: "/mnt/altmount"}
	if _, err := ValidatePlan(valid); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	cases := []struct {
		name string
		plan LinkPlan
	}{
		{name: "relative source root", plan: LinkPlan{SourceRoot: "source", SourceRelative: "movie.mkv", DestinationRoot: "/library", DestinationRel: "movie.mkv", PlexVisibleSourceRoot: "/mnt/altmount"}},
		{name: "non-canonical destination root", plan: LinkPlan{SourceRoot: "/source", SourceRelative: "movie.mkv", DestinationRoot: "/library/../library", DestinationRel: "movie.mkv", PlexVisibleSourceRoot: "/mnt/altmount"}},
		{name: "relative plex source root", plan: LinkPlan{SourceRoot: "/source", SourceRelative: "movie.mkv", DestinationRoot: "/library", DestinationRel: "movie.mkv", PlexVisibleSourceRoot: "mnt/altmount"}},
		{name: "escaping source relative", plan: LinkPlan{SourceRoot: "/source", SourceRelative: "../outside.mkv", DestinationRoot: "/library", DestinationRel: "movie.mkv", PlexVisibleSourceRoot: "/mnt/altmount"}},
		{name: "escaping destination relative", plan: LinkPlan{SourceRoot: "/source", SourceRelative: "movie.mkv", DestinationRoot: "/library", DestinationRel: "../outside.mkv", PlexVisibleSourceRoot: "/mnt/altmount"}},
		{name: "absolute destination relative", plan: LinkPlan{SourceRoot: "/source", SourceRelative: "movie.mkv", DestinationRoot: "/library", DestinationRel: "/etc/passwd", PlexVisibleSourceRoot: "/mnt/altmount"}},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidatePlan(testCase.plan); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("ValidatePlan(%+v) error=%v, want ErrUnsafePath", testCase.plan, err)
			}
		})
	}
}

func TestValidatePlanPinsPlexVisibleTarget(t *testing.T) {
	t.Parallel()
	plan, err := ValidatePlan(LinkPlan{SourceRoot: "/source", SourceRelative: "release/movie.mkv", DestinationRoot: "/library", DestinationRel: "Movie (2026)/Movie (2026).mkv", PlexVisibleSourceRoot: "/mnt/altmount"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlexVisibleTarget != "/mnt/altmount/release/movie.mkv" {
		t.Fatalf("unexpected Plex-visible target %q", plan.PlexVisibleTarget)
	}
}

// TestPublishSymlinkRejectsNonRegularSources proves AltMount output cannot be
// published through a directory or a nested symlink: only a contained regular
// file is ever linked.
func TestPublishSymlinkRejectsNonRegularSources(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	source, destination := filepath.Join(temp, "source"), filepath.Join(temp, "destination")
	if err := os.MkdirAll(filepath.Join(source, "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "release", "not-media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "release", "escape.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := PublishSymlink(LinkPlan{SourceRoot: source, SourceRelative: "release/not-media", DestinationRoot: destination, DestinationRel: "Movie (2026)/Movie (2026).mkv", PlexVisibleSourceRoot: "/mnt/altmount"}); !errors.Is(err, ErrSourceType) {
		t.Fatalf("directory source error=%v, want ErrSourceType", err)
	}
	if err := PublishSymlink(LinkPlan{SourceRoot: source, SourceRelative: "release/escape.mkv", DestinationRoot: destination, DestinationRel: "Movie (2026)/Movie (2026).mkv", PlexVisibleSourceRoot: "/mnt/altmount"}); err == nil {
		t.Fatal("escaping symlink source was published")
	}
	if _, err := os.Lstat(filepath.Join(destination, "Movie (2026)", "Movie (2026).mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe source created a publication: %v", err)
	}
}

// TestPublishSymlinkCannotEscapeThroughSymlinkedParent proves os.Root refuses a
// destination path that would traverse a symlinked directory outside the
// configured library root.
func TestPublishSymlinkCannotEscapeThroughSymlinkedParent(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	source, destination, outside := filepath.Join(temp, "source"), filepath.Join(temp, "destination"), filepath.Join(temp, "outside")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "movie.mkv"), []byte("fixture"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(destination, "escape")); err != nil {
		t.Fatal(err)
	}
	err := PublishSymlink(LinkPlan{SourceRoot: source, SourceRelative: "movie.mkv", DestinationRoot: destination, DestinationRel: "escape/movie.mkv", PlexVisibleSourceRoot: "/mnt/altmount"})
	if err == nil {
		t.Fatal("publication escaped the library root through a symlinked parent")
	}
	if entries, readErr := os.ReadDir(outside); readErr != nil || len(entries) != 0 {
		t.Fatalf("outside root was mutated: entries=%v err=%v", entries, readErr)
	}
}

// TestRemoveSymlinkNeverTouchesRealMedia proves a displaced removal cannot
// delete a real file or a link that no longer matches the recorded target.
func TestRemoveSymlinkNeverTouchesRealMedia(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	if err := os.WriteFile(filepath.Join(temp, "movie.mkv"), []byte("real media"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSymlink(temp, "movie.mkv", "/source/movie.mkv"); !errors.Is(err, ErrCollision) {
		t.Fatalf("real media removal error=%v, want ErrCollision", err)
	}
	if _, err := os.Stat(filepath.Join(temp, "movie.mkv")); err != nil {
		t.Fatalf("real media was removed: %v", err)
	}
	if err := os.Symlink("/source/other.mkv", filepath.Join(temp, "link.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSymlink(temp, "link.mkv", "/source/movie.mkv"); !errors.Is(err, ErrCollision) {
		t.Fatalf("mismatched link removal error=%v, want ErrCollision", err)
	}
	if err := os.Symlink("/source/movie.mkv", filepath.Join(temp, "expected.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSymlink(temp, "expected.mkv", "/source/movie.mkv"); err != nil {
		t.Fatalf("exact recorded link removal: %v", err)
	}
}

// TestMoveSymlinkRejectsEscapes keeps recycle displacement inside the library
// root even when a caller supplies an absolute or traversing destination.
func TestMoveSymlinkRejectsEscapes(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	if err := os.Symlink("/source/movie.mkv", filepath.Join(temp, "movie.mkv")); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{"/etc/passwd", "../outside.mkv"} {
		if err := MoveSymlink(temp, "movie.mkv", destination, "/source/movie.mkv"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("MoveSymlink destination %q error=%v, want ErrUnsafePath", destination, err)
		}
	}
}

// TestPreflightBatchValidatesEveryPlan leaves the library untouched when any
// plan in a season-pack batch is unsafe, so a late invalid asset cannot publish
// the earlier episodes.
func TestPreflightBatchValidatesEveryPlan(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	source, destination := filepath.Join(temp, "source"), filepath.Join(temp, "destination")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.mkv", "two.mkv"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	plans := []LinkPlan{
		{SourceRoot: source, SourceRelative: "one.mkv", DestinationRoot: destination, DestinationRel: "Show/Season 01/one.mkv", PlexVisibleSourceRoot: "/mnt/altmount"},
		{SourceRoot: source, SourceRelative: "two.mkv", DestinationRoot: destination, DestinationRel: "../escape.mkv", PlexVisibleSourceRoot: "/mnt/altmount"},
	}
	if _, err := PreflightBatch(plans); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("unsafe batch error=%v, want ErrUnsafePath", err)
	}
	if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
		t.Fatalf("unsafe batch mutated the library: entries=%v err=%v", entries, err)
	}
}
