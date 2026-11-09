package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAreaMapIsCurrent keeps scripts/dev/areas.conf honest: every file in
// internal/postgres belongs to exactly one area, and every listed file and
// package still exists. A map that drifts is worse than no map.
func TestAreaMapIsCurrent(t *testing.T) {
	root := repoRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "scripts", "dev", "areas.conf"))
	if err != nil {
		t.Fatal(err)
	}
	listed := parseAreaMap(t, root, string(content))

	entries, err := os.ReadDir(filepath.Join(root, "internal", "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		present[areaStem(entry.Name())] = true
	}
	for stem := range present {
		if listed[stem] == "" {
			t.Errorf("internal/postgres/%s is in no area; add it to scripts/dev/areas.conf", stem)
		}
	}
	for stem, area := range listed {
		if !present[stem] {
			t.Errorf("area %s lists %s, which is not in internal/postgres", area, stem)
		}
	}
}

func parseAreaMap(t *testing.T, root, content string) map[string]string {
	t.Helper()
	listed := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		columns := strings.Split(line, "|")
		if len(columns) != 3 {
			t.Fatalf("area line needs three columns: %q", line)
		}
		area := strings.TrimSpace(columns[0])
		for _, pkg := range strings.Fields(columns[1]) {
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(pkg))); err != nil || !info.IsDir() {
				t.Errorf("area %s lists package %s, which does not exist", area, pkg)
			}
		}
		for _, stem := range strings.Fields(columns[2]) {
			if other := listed[stem]; other != "" {
				t.Errorf("%s is listed in both %s and %s", stem, other, area)
			}
			listed[stem] = area
		}
	}
	return listed
}

// areaStem removes what the map leaves out of a file name.
func areaStem(name string) string {
	stem := strings.TrimSuffix(name, ".go")
	stem = strings.TrimSuffix(stem, "_test")
	return strings.TrimSuffix(stem, "_integration")
}
