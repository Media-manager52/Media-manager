package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	module       = "github.com/media-manager/media-manager/"
	maxFileLines = 1000
)

// domainPackages hold pure rules and durable types. They do no I/O and know
// nothing about storage, providers or HTTP.
var domainPackages = []string{
	"internal/catalog",
	"internal/languages",
	"internal/library",
	"internal/packs",
	"internal/releases",
	"internal/workflows",
}

// rules says which packages a layer must never import. A rule matches the
// importing package by prefix.
var rules = []struct {
	layer     string
	forbidden []string
	why       string
}{
	{"internal/adapters/", []string{"internal/postgres", "internal/httpapi", "internal/acquisition", "internal/publication", "internal/intake", "internal/adoptionexec", "internal/commissioning", "internal/mcpserver", "internal/platform", "internal/backups", "internal/recycle", "internal/notifications", "internal/metadata"}, "an adapter talks to one external service; orchestration and storage call it, never the other way round"},
	{"internal/postgres", []string{"internal/adapters/", "internal/commissioningv2", "internal/prowlarrbackup", "internal/acquisition", "internal/publication", "internal/httpapi", "internal/intake", "internal/adoptionexec", "internal/commissioning", "internal/mcpserver", "internal/platform"}, "storage persists domain types; it must not call providers or services"},
	{"internal/httpapi", []string{"internal/adapters/"}, "HTTP handlers call services, which own provider calls, pacing and credentials"},
	{"internal/mcpserver", []string{"internal/"}, "the MCP server is an API client and must only use the public HTTP API"},
}

type goFile struct {
	path    string // repository-relative, slash separated
	pkg     string // repository-relative package directory
	imports []string
	lines   int
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func productionFiles(t *testing.T) []goFile {
	t.Helper()
	root := repoRoot(t)
	var files []goFile
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if name := entry.Name(); name == "testdata" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			file := goFile{path: rel, pkg: filepath.ToSlash(filepath.Dir(rel)), lines: strings.Count(string(source), "\n")}
			for _, spec := range parsed.Imports {
				imported := strings.Trim(spec.Path.Value, `"`)
				if strings.HasPrefix(imported, module) {
					file.imports = append(file.imports, strings.TrimPrefix(imported, module))
				}
			}
			files = append(files, file)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 100 {
		t.Fatalf("found only %d production files; the walk is broken", len(files))
	}
	return files
}

func within(pkg, prefix string) bool {
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(pkg+"/", prefix)
	}
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

func isDomain(pkg string) bool {
	for _, domain := range domainPackages {
		if pkg == domain {
			return true
		}
	}
	return false
}

func TestLayerBoundaries(t *testing.T) {
	seen := map[string]bool{}
	var violations []string
	for _, file := range productionFiles(t) {
		for _, imported := range file.imports {
			key := file.path + " -> " + imported
			reason := ""
			if isDomain(file.pkg) && !isDomain(imported) {
				reason = "domain packages may only import other domain packages: " + strings.Join(domainPackages, ", ")
			}
			for _, rule := range rules {
				if !within(file.pkg, rule.layer) {
					continue
				}
				for _, forbidden := range rule.forbidden {
					if within(imported, forbidden) {
						reason = rule.why
					}
				}
			}
			if imported == "internal/httpapi" && !strings.HasPrefix(file.pkg, "cmd/") {
				reason = "only cmd/ wires the HTTP API"
			}
			if reason == "" {
				continue
			}
			if knownImportViolations[key] {
				seen[key] = true
				continue
			}
			violations = append(violations, fmt.Sprintf("%s\n    %s", key, reason))
		}
	}
	sort.Strings(violations)
	for _, violation := range violations {
		t.Errorf("layer boundary crossed: %s", violation)
	}
	var fixed []string
	for key := range knownImportViolations {
		if !seen[key] {
			fixed = append(fixed, key)
		}
	}
	sort.Strings(fixed)
	for _, key := range fixed {
		t.Errorf("%q is fixed: delete it from knownImportViolations in allowlist.go", key)
	}
}

func TestFileSizeBudget(t *testing.T) {
	seen := map[string]bool{}
	for _, file := range productionFiles(t) {
		ceiling, listed := oversizedFiles[file.path]
		seen[file.path] = listed
		switch {
		case listed && file.lines <= maxFileLines:
			t.Errorf("%s now has %d lines: delete it from oversizedFiles in allowlist.go", file.path, file.lines)
		case listed && file.lines > ceiling:
			t.Errorf("%s grew to %d lines (ceiling %d): split it instead of growing it", file.path, file.lines, ceiling)
		case listed && file.lines < ceiling:
			t.Errorf("%s shrank to %d lines: lower its ceiling in allowlist.go to %d", file.path, file.lines, file.lines)
		case !listed && file.lines > maxFileLines:
			t.Errorf("%s has %d lines (limit %d): split it into focused files", file.path, file.lines, maxFileLines)
		}
	}
	for path := range oversizedFiles {
		if !seen[path] {
			t.Errorf("%s no longer exists: delete it from oversizedFiles in allowlist.go", path)
		}
	}
}
