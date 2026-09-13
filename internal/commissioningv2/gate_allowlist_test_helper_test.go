// Package commissioningv2 test helper: the C-58 drift guard parses the
// ACTUAL migration-0035 SQL gate allowlist member-for-member, so Go/SQL
// drift fails at build time. No second hard-coded allowlist lives here.
package commissioningv2

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gateAllowlistFromMigration extracts the gate-clearance allowlist from the
// real migration-0035 trigger source (the single-quoted outcome literals in
// the gate_step_outcome NOT IN (...) clause), not from a copy.
func gateAllowlistFromMigration(t *testing.T) map[SafeOutcome]bool {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var sqlPath string
	for {
		candidate := filepath.Join(directory, "internal", "postgres", "migrations", "0035_indexer_commissioning_live_gate.sql")
		if _, err := os.Stat(candidate); err == nil {
			sqlPath = candidate
			break
		}
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			candidate = filepath.Join(directory, "..", "postgres", "migrations", "0035_indexer_commissioning_live_gate.sql")
			if _, err := os.Stat(candidate); err == nil {
				sqlPath = candidate
				break
			}
			t.Fatalf("migration 0035 SQL not found from %s", directory)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("module root not found")
		}
		directory = parent
	}
	raw, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if !strings.Contains(source, "gate_step_outcome NOT IN (") {
		t.Fatal("migration 0035 gate allowlist clause not found")
	}
	clause := source[strings.Index(source, "gate_step_outcome NOT IN ("):]
	clause = clause[:strings.Index(clause, ");")+2]
	quoted := regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(clause, -1)
	allowlist := make(map[SafeOutcome]bool, len(quoted))
	for _, match := range quoted {
		outcome := SafeOutcome(match[1])
		if !ValidSafeOutcome(outcome) {
			t.Fatalf("migration allowlist member %q is not a valid SafeOutcome", match[1])
		}
		allowlist[outcome] = true
	}
	if len(allowlist) != 18 {
		t.Fatalf("migration allowlist has %d members, want 18: %v", len(allowlist), allowlist)
	}
	return allowlist
}
