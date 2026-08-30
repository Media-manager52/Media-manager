package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
