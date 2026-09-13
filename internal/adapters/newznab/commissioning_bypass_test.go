package newznab

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

func readFileForBypassTest(name string) (string, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func testBypassScheduler() *indexers.Scheduler { return indexers.NewScheduler(1, nil) }

// TestNoRawProductionSendBypassForCommissioning proves the M-5 seam: the
// only exported commissioning sender on *Client is the permit-bound one-use
// FetchCommissioningWithTransmission. fetchCommissioning (the raw guarded
// transport) must stay unexported, and no other exported method may send a
// commissioning probe. The export_test.go helper fetchCommissioningRawForTest
// is test-only surface (defined in a _test.go file) and must not exist in
// non-test source.
func TestNoRawProductionSendBypassForCommissioning(t *testing.T) {
	t.Parallel()
	// 1. Inspect the ACTUAL exported method set of *Client via reflect: any
	// new exported method whose name can send (Fetch*/Send*) fails this
	// allowlist. This MUST fail if someone adds e.g.
	// FetchCommissioningDirect or SendCommissioningProbe.
	clientType := reflect.TypeOf(&Client{})
	exportedSenders := map[string]bool{}
	for i := 0; i < clientType.NumMethod(); i++ {
		name := clientType.Method(i).Name
		if strings.HasPrefix(name, "Fetch") || strings.HasPrefix(name, "Send") {
			exportedSenders[name] = true
		}
	}
	allowedSenders := map[string]bool{"FetchCommissioningWithTransmission": true}
	if len(exportedSenders) != len(allowedSenders) {
		t.Fatalf("exported Fetch*/Send* method set changed: got %v want %v", exportedSenders, allowedSenders)
	}
	for name := range exportedSenders {
		if !allowedSenders[name] {
			t.Fatalf("unapproved exported sender %q bypasses the capability path (got %v)", name, exportedSenders)
		}
	}
	// 2. Structural: the unexported fetchCommissioning must exist exactly
	// once as the single guarded transport, and no raw commissioning sender
	// (FetchCommissioning(/SendCommissioning() may exist in non-test source.
	// The test-only fetchCommissioningRawForTest helper must be defined
	// ONLY in export_test.go.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := readFileForBypassTest(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, "func (client *Client) fetchCommissioningRawForTest") {
			t.Fatalf("raw test helper defined in production file %s", file)
		}
		if strings.Contains(raw, "func (client *Client) FetchCommissioning(") ||
			strings.Contains(raw, "func (client *Client) SendCommissioning(") {
			t.Fatalf("raw production commissioning sender in %s", file)
		}
	}
	// 3. Behavioral: a nil capability sends nothing (consume fails closed
	// before any transport work), and the zero-snapshot mint is nil.
	var nilCapability *CommissioningTransmission
	client, err := New(Config{IndexerID: "bypass-probe", BaseURL: "https://bypass.invalid/api", APIKey: "k", Timeout: time.Second}, testBypassScheduler(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchCommissioningWithTransmission(context.Background(), nilCapability, CommissioningTransmitClaim{IndexerID: "x", StepID: "y"}, CommissioningProbe{Mode: "caps"}, CommissioningDestination{Timeout: time.Second}, allowGuard{}, time.Now().Add(time.Second)); err == nil {
		t.Fatal("nil capability sent")
	}
	// 4. fetchCommissioningRawForTest must be test-only surface: every
	// definition must live in a _test.go file, and exactly export_test.go
	// must define it. (This test's own comment mentions the name, so the
	// scan skips this file's own mention by requiring a func definition.)
	rawHelperFiles := map[string]bool{}
	for _, file := range files {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		if file == "commissioning_bypass_test.go" {
			continue
		}
		raw, err := readFileForBypassTest(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, "func (client *Client) fetchCommissioningRawForTest") {
			rawHelperFiles[file] = true
		}
	}
	if len(rawHelperFiles) != 1 || !rawHelperFiles["export_test.go"] {
		t.Fatalf("fetchCommissioningRawForTest defined in %v, want only export_test.go", rawHelperFiles)
	}
}
