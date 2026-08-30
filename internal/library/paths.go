package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	ErrUnsafePath = errors.New("path is not safely contained")
	ErrCollision  = errors.New("destination already exists with different content")
	ErrSourceType = errors.New("source is not a supported media file")
)

type LinkPlan struct {
	SourceRoot            string `json:"source_root"`
	SourceRelative        string `json:"source_relative"`
	DestinationRoot       string `json:"destination_root"`
	DestinationRel        string `json:"destination_relative"`
	PlexVisibleSourceRoot string `json:"plex_visible_source_root"`
	PlexVisibleTarget     string `json:"plex_visible_target"`
}

func ValidateRelative(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.Contains(name, "//") || filepath.IsAbs(name) {
		return "", ErrUnsafePath
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return "", ErrUnsafePath
		}
	}
	first := strings.SplitN(filepath.ToSlash(name), "/", 2)[0]
	if strings.Contains(first, ":") || strings.HasPrefix(name, "//") {
		return "", ErrUnsafePath
	}
	clean := filepath.Clean(name)
	if clean != name || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrUnsafePath
	}
	return clean, nil
}

func ValidatePlan(plan LinkPlan) (LinkPlan, error) {
	var err error
	plan.SourceRelative, err = ValidateRelative(plan.SourceRelative)
	if err != nil {
		return LinkPlan{}, fmt.Errorf("source path: %w", err)
	}
	plan.DestinationRel, err = ValidateRelative(plan.DestinationRel)
	if err != nil {
		return LinkPlan{}, fmt.Errorf("destination path: %w", err)
	}
	if !filepath.IsAbs(plan.SourceRoot) || !filepath.IsAbs(plan.DestinationRoot) || !filepath.IsAbs(plan.PlexVisibleSourceRoot) {
		return LinkPlan{}, ErrUnsafePath
	}
	if filepath.Clean(plan.SourceRoot) != plan.SourceRoot || filepath.Clean(plan.DestinationRoot) != plan.DestinationRoot || filepath.Clean(plan.PlexVisibleSourceRoot) != plan.PlexVisibleSourceRoot {
		return LinkPlan{}, ErrUnsafePath
	}
	plan.PlexVisibleTarget = filepath.Join(plan.PlexVisibleSourceRoot, plan.SourceRelative)
	return plan, nil
}

// PublishSymlink creates one final Plex-facing link without ever mutating the
// source root. os.Root makes every destination operation reject path escapes
// and intermediate symlinks. Existing identical links are idempotent.
func PublishSymlink(plan LinkPlan) error {
	_, err := publishSymlink(plan)
	return err
}

func publishSymlink(plan LinkPlan) (bool, error) {
	validated, err := ValidatePlan(plan)
	if err != nil {
		return false, err
	}

	sourceRoot, err := os.OpenRoot(validated.SourceRoot)
	if err != nil {
		return false, fmt.Errorf("open source root: %w", err)
	}
	defer sourceRoot.Close()

	info, err := sourceRoot.Stat(validated.SourceRelative)
	if err != nil {
		return false, fmt.Errorf("stat source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, ErrSourceType
	}

	destinationRoot, err := os.OpenRoot(validated.DestinationRoot)
	if err != nil {
		return false, fmt.Errorf("open destination root: %w", err)
	}
	defer destinationRoot.Close()

	parent := filepath.Dir(validated.DestinationRel)
	if parent != "." {
		if err := destinationRoot.MkdirAll(parent, 0o755); err != nil {
			return false, fmt.Errorf("create destination directories: %w", err)
		}
	}

	if current, err := destinationRoot.Readlink(validated.DestinationRel); err == nil {
		if current == validated.PlexVisibleTarget {
			return false, nil
		}
		return false, ErrCollision
	} else if !errors.Is(err, fs.ErrNotExist) {
		if _, statErr := destinationRoot.Lstat(validated.DestinationRel); statErr == nil {
			return false, ErrCollision
		}
	}

	if err := destinationRoot.Symlink(validated.PlexVisibleTarget, validated.DestinationRel); err != nil {
		if errors.Is(err, fs.ErrExist) {
			if current, readErr := destinationRoot.Readlink(validated.DestinationRel); readErr == nil && current == validated.PlexVisibleTarget {
				return false, nil
			}
			return false, ErrCollision
		}
		return false, fmt.Errorf("create symlink: %w", err)
	}

	actual, err := destinationRoot.Readlink(validated.DestinationRel)
	if err != nil || actual != validated.PlexVisibleTarget {
		if actual == validated.PlexVisibleTarget {
			_ = destinationRoot.Remove(validated.DestinationRel)
		}
		return false, fmt.Errorf("verify symlink: %w", ErrCollision)
	}
	return true, nil
}

// PreflightBatch validates every source and collision before any link is
// created. This prevents a late season-pack collision from publishing earlier
// episodes.
func PreflightBatch(plans []LinkPlan) ([]LinkPlan, error) {
	validated := make([]LinkPlan, 0, len(plans))
	destinations := make(map[string]string, len(plans))
	for _, plan := range plans {
		current, err := ValidatePlan(plan)
		if err != nil {
			return nil, err
		}
		key := current.DestinationRoot + "\x00" + current.DestinationRel
		if previous, exists := destinations[key]; exists && previous != current.PlexVisibleTarget {
			return nil, ErrCollision
		}
		destinations[key] = current.PlexVisibleTarget
		sourceRoot, err := os.OpenRoot(current.SourceRoot)
		if err != nil {
			return nil, fmt.Errorf("open source root: %w", err)
		}
		info, statErr := sourceRoot.Stat(current.SourceRelative)
		_ = sourceRoot.Close()
		if statErr != nil {
			return nil, fmt.Errorf("stat source: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, ErrSourceType
		}
		destinationRoot, err := os.OpenRoot(current.DestinationRoot)
		if err != nil {
			return nil, fmt.Errorf("open destination root: %w", err)
		}
		existing, readErr := destinationRoot.Readlink(current.DestinationRel)
		if readErr == nil && existing != current.PlexVisibleTarget {
			_ = destinationRoot.Close()
			return nil, ErrCollision
		}
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			if _, lstatErr := destinationRoot.Lstat(current.DestinationRel); lstatErr == nil {
				_ = destinationRoot.Close()
				return nil, ErrCollision
			}
		}
		_ = destinationRoot.Close()
		validated = append(validated, current)
	}
	return validated, nil
}

// PublishBatch rolls back only links proven to have been created by this call.
// Durable operation journaling still records and reconciles every step.
func PublishBatch(plans []LinkPlan) error {
	validated, err := PreflightBatch(plans)
	if err != nil {
		return err
	}
	type createdLink struct{ root, relative, target string }
	created := make([]createdLink, 0, len(validated))
	for _, plan := range validated {
		wasCreated, publishErr := publishSymlink(plan)
		if publishErr != nil {
			for index := len(created) - 1; index >= 0; index-- {
				root, openErr := os.OpenRoot(created[index].root)
				if openErr != nil {
					continue
				}
				if target, readErr := root.Readlink(created[index].relative); readErr == nil && target == created[index].target {
					_ = root.Remove(created[index].relative)
				}
				_ = root.Close()
			}
			return publishErr
		}
		if wasCreated {
			created = append(created, createdLink{root: plan.DestinationRoot, relative: plan.DestinationRel, target: plan.PlexVisibleTarget})
		}
	}
	return nil
}

// RemoveSymlink removes only the exact displaced link recorded by Media
// Manager. It never follows the link and never removes a real media file.
func RemoveSymlink(destinationRoot, destinationRelative, expectedTarget string) error {
	if !filepath.IsAbs(destinationRoot) || filepath.Clean(destinationRoot) != destinationRoot || !filepath.IsAbs(expectedTarget) {
		return ErrUnsafePath
	}
	relative, err := ValidateRelative(destinationRelative)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(destinationRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	current, err := root.Readlink(relative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrCollision
	}
	if current != expectedTarget {
		return ErrCollision
	}
	if err := root.Remove(relative); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func MoveSymlink(destinationRoot, fromRelative, toRelative, expectedTarget string) error {
	if !filepath.IsAbs(destinationRoot) || filepath.Clean(destinationRoot) != destinationRoot || !filepath.IsAbs(expectedTarget) {
		return ErrUnsafePath
	}
	from, err := ValidateRelative(fromRelative)
	if err != nil {
		return err
	}
	to, err := ValidateRelative(toRelative)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(destinationRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	current, readErr := root.Readlink(from)
	if errors.Is(readErr, fs.ErrNotExist) {
		recycled, recycleErr := root.Readlink(to)
		if recycleErr == nil && recycled == expectedTarget {
			return nil
		}
		return ErrCollision
	}
	if readErr != nil || current != expectedTarget {
		return ErrCollision
	}
	parent := filepath.Dir(to)
	if parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	if _, err := root.Lstat(to); err == nil {
		return ErrCollision
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := root.Rename(from, to); err != nil {
		return err
	}
	moved, err := root.Readlink(to)
	if err != nil || moved != expectedTarget {
		return ErrCollision
	}
	return nil
}

// MoveRegularFile moves only a regular file whose size and modification time
// still match the durable review fingerprint. It never follows the file itself
// if it has been replaced by a symlink.
func MoveRegularFile(destinationRoot, fromRelative, toRelative string, expectedSize, expectedModTimeNS int64) error {
	if !filepath.IsAbs(destinationRoot) || filepath.Clean(destinationRoot) != destinationRoot || expectedSize < 0 {
		return ErrUnsafePath
	}
	from, err := ValidateRelative(fromRelative)
	if err != nil {
		return err
	}
	to, err := ValidateRelative(toRelative)
	if err != nil || from == to {
		return ErrUnsafePath
	}
	root, err := os.OpenRoot(destinationRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	fromErr := matchesRegularFingerprint(root, from, expectedSize, expectedModTimeNS)
	if errors.Is(fromErr, fs.ErrNotExist) {
		if err := matchesRegularFingerprint(root, to, expectedSize, expectedModTimeNS); err == nil {
			return nil
		}
		return ErrCollision
	}
	if fromErr != nil {
		return ErrCollision
	}
	if _, err := root.Lstat(to); err == nil {
		return ErrCollision
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if parent := filepath.Dir(to); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	if err := root.Rename(from, to); err != nil {
		return err
	}
	if err := matchesRegularFingerprint(root, to, expectedSize, expectedModTimeNS); err != nil {
		return ErrCollision
	}
	return nil
}

// InspectRegularFile returns a content fingerprint for a contained regular
// file. The open file is checked again after hashing so review cannot silently
// accept a file that changed while it was being inspected.
func InspectRegularFile(ctx context.Context, rootPath, relative string) (int64, int64, string, error) {
	if !filepath.IsAbs(rootPath) || filepath.Clean(rootPath) != rootPath {
		return 0, 0, "", ErrUnsafePath
	}
	validated, err := ValidateRelative(relative)
	if err != nil {
		return 0, 0, "", err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return 0, 0, "", err
	}
	defer root.Close()
	file, before, err := openRegular(root, validated)
	if err != nil {
		return 0, 0, "", err
	}
	defer file.Close()
	digest, err := hashReader(ctx, file)
	if err != nil {
		return 0, 0, "", err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return 0, 0, "", ErrCollision
	}
	return after.Size(), after.ModTime().UnixNano(), digest, nil
}

// TransferRegularFile copies a fingerprinted regular file between contained
// roots, atomically publishes the destination, verifies its content, and only
// then removes the source. Retrying safely reconciles crashes before or after
// the destination rename or source removal. The temporary name must be unique
// to the durable journal operation that owns it.
func TransferRegularFile(ctx context.Context, sourceRootPath, sourceRelative, destinationRootPath, destinationRelative, temporaryRelative string, expectedSize, expectedModTimeNS int64, expectedSHA256 string) error {
	if !filepath.IsAbs(sourceRootPath) || filepath.Clean(sourceRootPath) != sourceRootPath || !filepath.IsAbs(destinationRootPath) || filepath.Clean(destinationRootPath) != destinationRootPath || expectedSize < 0 || len(expectedSHA256) != sha256.Size*2 {
		return ErrUnsafePath
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return ErrUnsafePath
	}
	sourceRelative, err := ValidateRelative(sourceRelative)
	if err != nil {
		return err
	}
	destinationRelative, err = ValidateRelative(destinationRelative)
	if err != nil {
		return err
	}
	temporaryRelative, err = ValidateRelative(temporaryRelative)
	if err != nil || temporaryRelative == destinationRelative {
		return ErrUnsafePath
	}
	sourceRoot, err := os.OpenRoot(sourceRootPath)
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	destinationRoot, err := os.OpenRoot(destinationRootPath)
	if err != nil {
		return err
	}
	defer destinationRoot.Close()

	sourceErr := matchesRegularFingerprint(sourceRoot, sourceRelative, expectedSize, expectedModTimeNS)
	destinationErr := matchesRegularFingerprint(destinationRoot, destinationRelative, expectedSize, expectedModTimeNS)
	if errors.Is(sourceErr, fs.ErrNotExist) && destinationErr == nil {
		if err := matchesRegularContent(ctx, destinationRoot, destinationRelative, expectedSize, expectedModTimeNS, expectedSHA256); err != nil {
			return ErrCollision
		}
		return nil
	}
	if sourceErr != nil {
		return ErrCollision
	}
	if destinationErr == nil {
		if err := matchesRegularContent(ctx, sourceRoot, sourceRelative, expectedSize, expectedModTimeNS, expectedSHA256); err != nil {
			return ErrCollision
		}
		if err := matchesRegularContent(ctx, destinationRoot, destinationRelative, expectedSize, expectedModTimeNS, expectedSHA256); err != nil {
			return ErrCollision
		}
		if err := sourceRoot.Remove(sourceRelative); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return syncParent(sourceRoot, sourceRelative)
	}
	if !errors.Is(destinationErr, fs.ErrNotExist) {
		return ErrCollision
	}

	parent := filepath.Dir(destinationRelative)
	if parent != "." {
		if err := destinationRoot.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	if filepath.Dir(temporaryRelative) != parent {
		return ErrUnsafePath
	}
	if info, err := destinationRoot.Lstat(temporaryRelative); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ErrCollision
		}
		if err := destinationRoot.Remove(temporaryRelative); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	source, before, err := openRegular(sourceRoot, sourceRelative)
	if err != nil {
		return err
	}
	defer source.Close()
	if before.Size() != expectedSize || before.ModTime().UnixNano() != expectedModTimeNS {
		return ErrCollision
	}
	temporary, err := destinationRoot.OpenFile(temporaryRelative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, before.Mode().Perm())
	if err != nil {
		return err
	}
	copyOK := false
	defer func() {
		_ = temporary.Close()
		if !copyOK {
			_ = destinationRoot.Remove(temporaryRelative)
		}
	}()
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hasher), &contextReader{ctx: ctx, reader: source}); err != nil {
		return err
	}
	if hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		return ErrCollision
	}
	after, err := source.Stat()
	if err != nil || after.Size() != expectedSize || after.ModTime().UnixNano() != expectedModTimeNS {
		return ErrCollision
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	modTime := time.Unix(0, expectedModTimeNS)
	if err := destinationRoot.Chtimes(temporaryRelative, modTime, modTime); err != nil {
		return err
	}
	if err := matchesRegularContent(ctx, destinationRoot, temporaryRelative, expectedSize, expectedModTimeNS, expectedSHA256); err != nil {
		return ErrCollision
	}
	if err := destinationRoot.Rename(temporaryRelative, destinationRelative); err != nil {
		return err
	}
	copyOK = true
	if err := syncParent(destinationRoot, destinationRelative); err != nil {
		return err
	}
	if err := matchesRegularFingerprint(destinationRoot, destinationRelative, expectedSize, expectedModTimeNS); err != nil {
		return ErrCollision
	}
	if err := sourceRoot.Remove(sourceRelative); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return syncParent(sourceRoot, sourceRelative)
}

// RemoveRegularFile removes only the exact retained real-file fingerprint. A
// missing file is treated as an idempotent completion after an unknown worker
// outcome.
func RemoveRegularFile(destinationRoot, relative string, expectedSize, expectedModTimeNS int64) error {
	if !filepath.IsAbs(destinationRoot) || filepath.Clean(destinationRoot) != destinationRoot || expectedSize < 0 {
		return ErrUnsafePath
	}
	validated, err := ValidateRelative(relative)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(destinationRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := matchesRegularFingerprint(root, validated, expectedSize, expectedModTimeNS); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrCollision
	}
	if err := root.Remove(validated); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func matchesRegularFingerprint(root *os.Root, relative string, expectedSize, expectedModTimeNS int64) error {
	info, err := root.Lstat(relative)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != expectedSize || info.ModTime().UnixNano() != expectedModTimeNS {
		return ErrCollision
	}
	return nil
}

func matchesRegularContent(ctx context.Context, root *os.Root, relative string, expectedSize, expectedModTimeNS int64, expectedSHA256 string) error {
	file, before, err := openRegular(root, relative)
	if err != nil {
		return err
	}
	defer file.Close()
	if before.Size() != expectedSize || before.ModTime().UnixNano() != expectedModTimeNS {
		return ErrCollision
	}
	digest, err := hashReader(ctx, file)
	if err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil || after.Size() != expectedSize || after.ModTime().UnixNano() != expectedModTimeNS || digest != expectedSHA256 {
		return ErrCollision
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func hashReader(ctx context.Context, reader io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, &contextReader{ctx: ctx, reader: reader}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func syncParent(root *os.Root, relative string) error {
	parent := filepath.Dir(relative)
	if parent == "." {
		parent = "."
	}
	directory, err := root.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func openRegular(root *os.Root, relative string) (*os.File, os.FileInfo, error) {
	before, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrSourceType
	}
	file, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, ErrCollision
	}
	return file, opened, nil
}
