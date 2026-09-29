package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateRelativeRejectsEscapes(t *testing.T) {
	for _, value := range []string{"", ".", "..", "../outside", "/absolute", `..\\outside`, "bad\x00name"} {
		if _, err := ValidateRelative(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestPublishSymlinkIsContainedAndIdempotent(t *testing.T) {
	temp := t.TempDir()
	source := filepath.Join(temp, "source")
	destination := filepath.Join(temp, "destination")
	if err := os.MkdirAll(filepath.Join(source, "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "release", "movie.mkv"), []byte("fixture"), 0o444); err != nil {
		t.Fatal(err)
	}

	plan := LinkPlan{SourceRoot: source, SourceRelative: "release/movie.mkv", DestinationRoot: destination, DestinationRel: "Movie (2026)/Movie (2026).mkv", PlexVisibleSourceRoot: "/mnt/altmount"}
	if err := PublishSymlink(plan); err != nil {
		t.Fatal(err)
	}
	if err := PublishSymlink(plan); err != nil {
		t.Fatalf("idempotent publish failed: %v", err)
	}

	conflict := plan
	conflict.PlexVisibleSourceRoot = "/other"
	if err := PublishSymlink(conflict); !errors.Is(err, ErrCollision) {
		t.Fatalf("expected collision, got %v", err)
	}
}

func TestRenderMoviePlexDefault(t *testing.T) {
	folder, file, err := RenderMovie(MovieName{Title: "Dune: Part Two", Year: 2024, Provider: "tmdb", ProviderID: "693134", Technical: []string{"2160p", "UHD BluRay", "Dolby Vision", "TrueHD Atmos"}, Extension: ".mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if folder != "Dune- Part Two (2024) {tmdb-693134}" {
		t.Fatalf("unexpected folder %q", folder)
	}
	if filepath.Ext(file) != ".mkv" {
		t.Fatalf("unexpected file %q", file)
	}
}

func TestRenderEpisodeAllowsTVDBAndPreservesTokens(t *testing.T) {
	folder, season, file, err := RenderEpisode(strings.Repeat("Very Long Show ", 40), 2026, "tvdb", "12345", 1, 3, 4, "", []string{"2160p"}, ".mkv")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(folder, " (2026) {tvdb-12345}") || season != "Season 01" || !strings.Contains(file, "S01E03-E04") {
		t.Fatalf("identity token lost: %q %q %q", folder, season, file)
	}
}

func TestRenderMovieRejectsUnboundedExtensionAndIdentityInjection(t *testing.T) {
	if _, _, err := RenderMovie(MovieName{Title: "Movie", Year: 2026, Provider: "tmdb", ProviderID: "1} {imdb-evil", Extension: ".mkv"}); err == nil {
		t.Fatal("expected provider injection to fail")
	}
	if _, _, err := RenderMovie(MovieName{Title: "Movie", Year: 2026, Provider: "tmdb", ProviderID: "1", Extension: strings.Repeat("x", 1000)}); err == nil {
		t.Fatal("expected extension to fail")
	}
}

func TestValidateRelativeRejectsWindowsAndNoncanonicalPaths(t *testing.T) {
	for _, value := range []string{"C:/movie.mkv", "a//b.mkv", "a/../b.mkv", "a/./b.mkv"} {
		if _, err := ValidateRelative(value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

func TestBatchPreflightPublishesNothingOnLateCollision(t *testing.T) {
	temp := t.TempDir()
	source, destination := filepath.Join(temp, "source"), filepath.Join(temp, "destination")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "one.mkv"), []byte("one"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "two.mkv"), []byte("two"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "two.mkv"), []byte("collision"), 0o444); err != nil {
		t.Fatal(err)
	}
	plans := []LinkPlan{{SourceRoot: source, SourceRelative: "one.mkv", DestinationRoot: destination, DestinationRel: "one.mkv", PlexVisibleSourceRoot: "/mnt/altmount"}, {SourceRoot: source, SourceRelative: "two.mkv", DestinationRoot: destination, DestinationRel: "two.mkv", PlexVisibleSourceRoot: "/mnt/altmount"}}
	if err := PublishBatch(plans); !errors.Is(err, ErrCollision) {
		t.Fatalf("expected collision, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "one.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("first link was published before late collision")
	}
}

func TestRemoveSymlinkRequiresExactRecordedTarget(t *testing.T) {
	temp := t.TempDir()
	if err := os.Symlink("/source/one.mkv", filepath.Join(temp, "movie.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSymlink(temp, "movie.mkv", "/source/two.mkv"); !errors.Is(err, ErrCollision) {
		t.Fatalf("expected collision, got %v", err)
	}
	if err := RemoveSymlink(temp, "movie.mkv", "/source/one.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSymlink(temp, "movie.mkv", "/source/one.mkv"); err != nil {
		t.Fatal(err)
	}
}

func TestMoveAndRemoveRegularFileRequireExactFingerprint(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("synthetic fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "movie.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := MoveRegularFile(root, "movie.mkv", ".media-manager-recycle/id/movie.mkv", info.Size()+1, info.ModTime().UnixNano()); !errors.Is(err, ErrCollision) {
		t.Fatalf("mismatched size move error=%v", err)
	}
	if err := MoveRegularFile(root, "movie.mkv", ".media-manager-recycle/id/movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := MoveRegularFile(root, "movie.mkv", ".media-manager-recycle/id/movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatalf("idempotent move: %v", err)
	}
	if err := MoveRegularFile(root, ".media-manager-recycle/id/movie.mkv", "movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatalf("restore retained file: %v", err)
	}
	if err := MoveRegularFile(root, ".media-manager-recycle/id/movie.mkv", "movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatalf("idempotent restore: %v", err)
	}
	if err := MoveRegularFile(root, "movie.mkv", ".media-manager-recycle/id/movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatalf("retain restored fixture again: %v", err)
	}
	if err := RemoveRegularFile(root, ".media-manager-recycle/id/movie.mkv", info.Size(), info.ModTime().UnixNano()+1); !errors.Is(err, ErrCollision) {
		t.Fatalf("mismatched modtime remove error=%v", err)
	}
	if err := RemoveRegularFile(root, ".media-manager-recycle/id/movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRegularFile(root, ".media-manager-recycle/id/movie.mkv", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
}

func TestMoveSymlinkIsIdempotentAndRestorable(t *testing.T) {
	temp := t.TempDir()
	if err := os.Symlink("/source/movie.mkv", filepath.Join(temp, "movie.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := MoveSymlink(temp, "movie.mkv", ".media-manager-recycle/entry/movie.mkv", "/source/movie.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := MoveSymlink(temp, "movie.mkv", ".media-manager-recycle/entry/movie.mkv", "/source/movie.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := MoveSymlink(temp, ".media-manager-recycle/entry/movie.mkv", "movie.mkv", "/source/movie.mkv"); err != nil {
		t.Fatal(err)
	}
}

// TestRelocateSymlinkMovesAcrossRoots pins the relocation primitive used to
// correct a movie published into the wrong library root: the link moves
// between roots, stays idempotent, never overwrites a different target, and
// rejects escaping paths.
func TestRelocateSymlinkMovesAcrossRoots(t *testing.T) {
	fromRoot, toRoot := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "Movie (2026).mkv")
	if err := os.WriteFile(target, []byte("target fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	relative := "Movie (2026)/Movie (2026).mkv"
	if err := os.MkdirAll(filepath.Join(fromRoot, "Movie (2026)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(fromRoot, relative)); err != nil {
		t.Fatal(err)
	}
	if err := RelocateSymlink(fromRoot, toRoot, relative, relative, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(fromRoot, relative)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source link still present: %v", err)
	}
	moved, err := os.Readlink(filepath.Join(toRoot, relative))
	if err != nil || moved != target {
		t.Fatalf("relocated target=%q err=%v, want %q", moved, err, target)
	}
	// Idempotent rerun.
	if err := RelocateSymlink(fromRoot, toRoot, relative, relative, target); err != nil {
		t.Fatal(err)
	}
	// A different target at the destination is never overwritten.
	other := filepath.Join(t.TempDir(), "Other.mkv")
	if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(toRoot, "Other (2026)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fromRoot, "Other (2026)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(toRoot, "Other (2026)/Other.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(fromRoot, "Other (2026)/Other.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := RelocateSymlink(fromRoot, toRoot, "Other (2026)/Other.mkv", "Other (2026)/Other.mkv", target); !errors.Is(err, ErrCollision) {
		t.Fatalf("collision err=%v, want ErrCollision", err)
	}
	if err := RelocateSymlink(fromRoot, toRoot, "../escape", "../escape", target); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("escape err=%v, want ErrUnsafePath", err)
	}
}

func TestTransferRegularFileVerifiesAndReconcilesUnknownOutcomes(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	sourceRoot, destinationRoot := filepath.Join(temp, "source"), filepath.Join(temp, "destination")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "movie.mkv"), []byte("synthetic cross-root media fixture"), 0o640); err != nil {
		t.Fatal(err)
	}
	size, modTimeNS, digest, err := InspectRegularFile(context.Background(), sourceRoot, "movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	temporary := "library/.movie.mkv.operation.partial"
	if err := TransferRegularFile(context.Background(), sourceRoot, "movie.mkv", destinationRoot, "library/movie.mkv", temporary, size, modTimeNS, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "movie.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists after verified transfer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destinationRoot, "library", "movie.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := TransferRegularFile(context.Background(), sourceRoot, "movie.mkv", destinationRoot, "library/movie.mkv", temporary, size, modTimeNS, digest); err != nil {
		t.Fatalf("retry after source removal: %v", err)
	}

	if err := os.WriteFile(filepath.Join(sourceRoot, "movie.mkv"), []byte("synthetic cross-root media fixture"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(sourceRoot, "movie.mkv"), time.Unix(0, modTimeNS), time.Unix(0, modTimeNS)); err != nil {
		t.Fatal(err)
	}
	if err := TransferRegularFile(context.Background(), sourceRoot, "movie.mkv", destinationRoot, "library/movie.mkv", temporary, size, modTimeNS, digest); err != nil {
		t.Fatalf("retry with both verified copies: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "movie.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified duplicate source was not reconciled: %v", err)
	}
}

func TestTransferRegularFileRejectsCollisionAndSymlink(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	sourceRoot, destinationRoot := filepath.Join(temp, "source"), filepath.Join(temp, "destination")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "real.mkv"), []byte("reviewed fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	size, modTimeNS, digest, err := InspectRegularFile(context.Background(), sourceRoot, "real.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destinationRoot, "movie.mkv"), []byte("different fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := TransferRegularFile(context.Background(), sourceRoot, "real.mkv", destinationRoot, "movie.mkv", ".movie.operation.partial", size, modTimeNS, digest); !errors.Is(err, ErrCollision) {
		t.Fatalf("destination collision error=%v", err)
	}
	if err := os.Symlink("real.mkv", filepath.Join(sourceRoot, "link.mkv")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := InspectRegularFile(context.Background(), sourceRoot, "link.mkv"); err == nil {
		t.Fatal("final source symlink was followed")
	}
}
