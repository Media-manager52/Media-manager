package altmount

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/media-manager/media-manager/internal/library"
)

type OutputMapping struct {
	ReportedRoot      string `json:"reported_root"`
	LocalReadOnlyRoot string `json:"local_read_only_root"`
}
type OutputFile struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
}
type Inventory struct {
	ReportedPath  string       `json:"reported_path"`
	LocalRelative string       `json:"local_relative"`
	Files         []OutputFile `json:"files"`
	Digest        string       `json:"digest"`
}

var ErrOutputMapping = errors.New("AltMount output path is outside the configured read-only mapping")

func Enumerate(mapping OutputMapping, reportedPath string) (Inventory, error) {
	if !filepath.IsAbs(mapping.ReportedRoot) || !filepath.IsAbs(mapping.LocalReadOnlyRoot) || !filepath.IsAbs(reportedPath) || strings.ContainsRune(reportedPath, '\x00') {
		return Inventory{}, ErrOutputMapping
	}
	reportedRoot := filepath.Clean(mapping.ReportedRoot)
	reported := filepath.Clean(reportedPath)
	relative, err := filepath.Rel(reportedRoot, reported)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Inventory{}, ErrOutputMapping
	}
	if relative == "." {
		relative = ""
	}
	root, err := os.OpenRoot(filepath.Clean(mapping.LocalReadOnlyRoot))
	if err != nil {
		return Inventory{}, err
	}
	defer root.Close()
	start := relative
	if start == "" {
		start = "."
	}
	info, err := root.Stat(start)
	if err != nil {
		return Inventory{}, err
	}
	result := Inventory{ReportedPath: reported, LocalRelative: relative}
	if info.Mode().IsRegular() {
		parent := filepath.Dir(relative)
		if parent == "." {
			parent = ""
		}
		result.LocalRelative = parent
		if info.Size() > 0 {
			result.Files = append(result.Files, OutputFile{RelativePath: filepath.Base(relative), Size: info.Size()})
		}
	} else if info.IsDir() {
		if err := walkRoot(root, start, relative, &result.Files); err != nil {
			return Inventory{}, err
		}
	} else {
		return Inventory{}, ErrOutputMapping
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].RelativePath < result.Files[j].RelativePath })
	hash := sha256.New()
	for _, file := range result.Files {
		if _, err := library.ValidateRelative(file.RelativePath); err != nil {
			return Inventory{}, ErrOutputMapping
		}
		fmt.Fprintf(hash, "altmount-inventory-v1\x00%s\x00%d\x00", filepath.ToSlash(file.RelativePath), file.Size)
	}
	result.Digest = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func walkRoot(root *os.Root, current, base string, files *[]OutputFile) error {
	directory, err := root.Open(current)
	if err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := entry.Name()
		if current != "." {
			path = filepath.Join(current, entry.Name())
		}
		info, err := root.Stat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := walkRoot(root, path, base, files); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Size() == 0 {
			continue
		}
		relative := path
		if base != "" {
			relative, err = filepath.Rel(base, path)
			if err != nil {
				return err
			}
		}
		*files = append(*files, OutputFile{RelativePath: relative, Size: info.Size()})
	}
	return nil
}
