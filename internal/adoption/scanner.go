package adoption

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrUnsafeRoot = errors.New("adoption source root is not a real directory")
	ErrScanLimit  = errors.New("adoption discovery exceeded its file limit")
)

type Entry struct {
	Relative             string
	RecognizedCompanions []string
	UnknownCompanions    []string
}

var videoExtensions = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".m2ts": true}
var companionExtensions = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".nfo": true}

func Scan(ctx context.Context, root string, maximumFiles int) ([]Entry, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || maximumFiles < 1 {
		return nil, ErrUnsafeRoot
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafeRoot
	}
	filesByDirectory := map[string][]string{}
	videos := []string{}
	seen := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		seen++
		if seen > maximumFiles {
			return ErrScanLimit
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrUnsafeRoot
		}
		relative = filepath.ToSlash(relative)
		directory := filepath.ToSlash(filepath.Dir(relative))
		filesByDirectory[directory] = append(filesByDirectory[directory], relative)
		if videoExtensions[strings.ToLower(filepath.Ext(relative))] {
			videos = append(videos, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(videos)
	result := make([]Entry, 0, len(videos))
	for _, video := range videos {
		directory := filepath.ToSlash(filepath.Dir(video))
		base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
		item := Entry{Relative: video, RecognizedCompanions: []string{}, UnknownCompanions: []string{}}
		for _, candidate := range filesByDirectory[directory] {
			if candidate == video {
				continue
			}
			name := filepath.Base(candidate)
			if name != base+filepath.Ext(candidate) && !strings.HasPrefix(name, base+".") {
				continue
			}
			if companionExtensions[strings.ToLower(filepath.Ext(candidate))] {
				item.RecognizedCompanions = append(item.RecognizedCompanions, candidate)
			} else {
				item.UnknownCompanions = append(item.UnknownCompanions, candidate)
			}
		}
		sort.Strings(item.RecognizedCompanions)
		sort.Strings(item.UnknownCompanions)
		result = append(result, item)
	}
	return result, nil
}
