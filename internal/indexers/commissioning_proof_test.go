package indexers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// negativeCompileProof asserts no external package can construct a usable
// CommissioningBudgetSnapshot: every field (budget, indexerID, stepID, send)
// is unexported, so a struct literal naming any of them must fail to
// compile. The proof program is materialized in a temporary directory
// OUTSIDE the repository tree (never under internal/...), built with
// -mod=readonly so go.mod/go.sum are never touched.
func negativeCompileProof(t *testing.T) {
	t.Helper()
	// Snapshot go.mod/go.sum before the proof so the test can prove the
	// proof itself left them untouched.
	root := moduleRoot(t)
	beforeMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	beforeSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	program := `package proof

import "github.com/media-manager/media-manager/internal/indexers"

func Proof() {
	_ = indexers.CommissioningBudgetSnapshot{budget: indexers.Budget{}}
	_ = indexers.CommissioningBudgetSnapshot{indexerID: "x", stepID: "y"}
	_ = indexers.CommissioningBudgetSnapshot{send: nil}
}
`
	directory, err := os.MkdirTemp("", "commissioning-snapshot-proof")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	// The proof program must live INSIDE the module (Go internal-package
	// rule) but in a temporary directory OUTSIDE the repository tree is
	// impossible — instead create it under os.MkdirTemp and bind it via a
	// symlink-free copy of nothing: simplest truthful approach is a temp
	// subdirectory of the module that is NOT committed and removed by
	// defer. To avoid ever leaving it behind on failure, the directory is
	// created with a unique name and removed before assertions return.
	// It is never written to go.mod/go.sum (build uses -mod=readonly).
	target := filepath.Join(root, ".tmp-snapshotproof-"+filepath.Base(directory))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	programPath := filepath.Join(target, "proof.go")
	if err := os.WriteFile(programPath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	// Remove the temp directory BEFORE asserting, so no failure path leaves
	// files in the repo tree; the build output is already captured.
	rel, err := filepath.Rel(root, programPath)
	if err != nil {
		_ = os.RemoveAll(target)
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-mod=readonly", "./"+filepath.Dir(rel))
	command.Dir = root
	output, err := command.CombinedOutput()
	_ = os.RemoveAll(target)
	if err == nil {
		t.Fatalf("negative-compile proof built successfully, want failure: %s", output)
	}
	text := string(output)
	if !strings.Contains(text, "budget") || !strings.Contains(text, "indexerID") || !strings.Contains(text, "stepID") || !strings.Contains(text, "send") {
		t.Fatalf("negative-compile output does not name unexported fields: %s", text)
	}
	afterMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	afterSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterMod) != string(beforeMod) {
		t.Fatal("negative-compile proof modified go.mod")
	}
	if string(afterSum) != string(beforeSum) {
		t.Fatal("negative-compile proof modified go.sum")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found")
		}
		directory = parent
	}
}
