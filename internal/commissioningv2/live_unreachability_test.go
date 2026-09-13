package commissioningv2

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// listedPackage is the subset of `go list -json` shape this guard needs. It
// is intentionally limited to non-test inputs: go list reports GoFiles
// WITHOUT _test.go files, so scanning these files is a production-only scan.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
}

// transitiveProductionClosure returns the full transitive dependency closure
// of a production root, including the root package itself. This is the real
// `go list -deps` graph the acceptance criterion requires; it is consumed
// (not discarded) by the guard below.
func transitiveProductionClosure(t *testing.T, moduleRoot, root string) []listedPackage {
	t.Helper()
	command := exec.Command("go", "list", "-deps", "-json", "./"+root)
	command.Dir = moduleRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps -json %s: %v\n%s", root, err, output)
	}
	var packages []listedPackage
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatal(err)
		}
		packages = append(packages, pkg)
	}
	if len(packages) == 0 {
		t.Fatalf("no transitive dependencies for %s", root)
	}
	return packages
}

const (
	liveExecutorPackage = "github.com/media-manager/media-manager/internal/commissioningv2"
	liveStorePackage    = "github.com/media-manager/media-manager/internal/postgres"
	liveGuardPackage    = "github.com/media-manager/media-manager/internal/adapters/newznab"
	// liveOwnerServicePackage is the SINGLE Milestone-B production package
	// allowed to construct the live executor: the owner-authorized CAPS
	// service. The HTTP layer only sees a narrow service interface.
	liveOwnerServicePackage = "github.com/media-manager/media-manager/internal/commissioning"
	liveOwnerAPIPackage     = "github.com/media-manager/media-manager/internal/httpapi"
)

// TestLiveExecutorAbsentFromProductionDependencyGraph is the Milestone-A/B
// production-reachability structural guard. In Milestone A NO production
// package could import the executor. Milestone B deliberately opens exactly
// ONE owner-authorized path:
//
//   - internal/commissioning (LiveCAPSService) is the only production
//     constructor of a LiveExecutor, and only for an explicitly
//     owner-authorized CAPS step;
//   - internal/httpapi exposes a narrow CommissioningV2Service interface and
//     never constructs an executor itself.
//
// Every other production package (durable store, acquisition, retrieval,
// workers, UI) must remain executor-free. The guard walks the REAL transitive
// dependency closure of every production root and fails when:
//
//  1. any package in the closure directly imports the live-executor package
//     other than the allowlisted durable store, owner service, and owner API
//     packages; or
//  2. any non-test file in the closure references a Milestone-A/B execution
//     symbol from a package other than that symbol's defining packages and
//     the owner service.
func TestLiveExecutorAbsentFromProductionDependencyGraph(t *testing.T) {
	t.Parallel()
	roots := []string{"cmd/media-manager", "internal/httpapi", "internal/acquisition"}
	// allowedReferrers maps each Milestone-A execution symbol to the exact
	// packages that legitimately declare or select it. Any other non-test
	// occurrence in the production closure is a wiring regression.
	allowedReferrers := map[string]map[string]bool{
		"NewLocalLiveExecutor":                         {liveExecutorPackage: true, liveOwnerServicePackage: true},
		"CloseInterruptedAfterUncertainFinalization":   {liveExecutorPackage: true},
		"CloseSucceededAfterKnownSuccessfulCompletion": {liveExecutorPackage: true},
		"ClaimIntent":                        {liveExecutorPackage: true},
		"MarkClaimedStepTransmitting":        {liveExecutorPackage: true, liveStorePackage: true},
		"CompleteLiveAttempt":                {liveExecutorPackage: true, liveStorePackage: true},
		"ReserveCommissioningRequest":        {liveExecutorPackage: true, liveStorePackage: true},
		"FetchCommissioningWithTransmission": {liveGuardPackage: true, liveExecutorPackage: true},
	}
	// importAllowlist is the exact set of production packages permitted to
	// import the live-executor domain package: the durable store (shared
	// domain types), the single owner-authorized CAPS service, and the owner
	// HTTP API (narrow interface only). Any other importer risks pulling the
	// executor into an unrelated production path and must be reviewed.
	importAllowlist := map[string]bool{
		liveStorePackage:        true,
		liveOwnerServicePackage: true,
		liveOwnerAPIPackage:     true,
	}
	// go list patterns resolve from the module root, not from this package
	// directory: locate go.mod upward and run there.
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found")
		}
		directory = parent
	}
	for _, root := range roots {
		closure := transitiveProductionClosure(t, directory, root)
		// Positive control: the closure must actually contain the live
		// execution packages, otherwise the scan below is vacuous.
		foundLive := false
		for _, pkg := range closure {
			if pkg.ImportPath == liveExecutorPackage {
				foundLive = true
			}
			// Import-edge guard: no production package may import the
			// live-executor package unless explicitly allowlisted.
			for _, imported := range pkg.Imports {
				if imported == liveExecutorPackage && !importAllowlist[pkg.ImportPath] {
					t.Fatalf("production root %s: package %s imports live-executor package %s (not allowlisted)", root, pkg.ImportPath, liveExecutorPackage)
				}
			}
		}
		if !foundLive {
			t.Fatalf("production root %s closure does not reach %s; unreachability guard is vacuous", root, liveExecutorPackage)
		}
		// Symbol guard over EVERY non-test file in the closure.
		for _, pkg := range closure {
			if pkg.Dir == "" {
				continue
			}
			for _, file := range pkg.GoFiles {
				if strings.HasSuffix(file, "_test.go") {
					continue
				}
				source, err := os.ReadFile(filepath.Join(pkg.Dir, file))
				if err != nil {
					t.Fatal(err)
				}
				for symbol, referrers := range allowedReferrers {
					if referrers[pkg.ImportPath] {
						continue
					}
					if strings.Contains(string(source), symbol) {
						t.Fatalf("production root %s: package %s references Milestone-A execution API %q (%s)", root, pkg.ImportPath, symbol, file)
					}
				}
			}
		}
	}
	// Positive control: the leaf package itself must exist and provide the
	// test-only constructor, so the absence above is meaningful (not a
	// renamed-package tautology).
	var constructor func(LiveExecutorDeps) (*LiveExecutor, error) = NewLocalLiveExecutor
	if constructor == nil {
		t.Fatal("test-only live-executor constructor missing")
	}
}
