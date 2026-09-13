package postgres

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"uuid"
)

// readSourceFile returns the source of the named file in this package's
// directory, asserting the focused function marker is present so the
// structural test cannot silently assert against the wrong text.
func readSourceFile(name, marker string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	source := string(raw)
	if !strings.Contains(source, marker) {
		return "", os.ErrNotExist
	}
	return source, nil
}

// commissioningTestAuthorizer is a test-only durable authorizer that
// authorizes the exact ("indexer-x", "step-x") attempt.
type commissioningTestAuthorizer struct{}

func (*commissioningTestAuthorizer) AuthorizedTransmittingCommissioningStep(_ context.Context, indexerID, stepID string) bool {
	return indexerID == "indexer-x" && stepID == "step-x"
}

// TestAuthorizedTransmittingStepUsesSingleStatement is CP1-7's focused
// query/store test that runs without a live DB: it asserts the durable
// authorizer's read is ONE SQL statement (one MVCC snapshot) joining the
// commissioning step, the run/indexer identity, and the budget gate, with
// all five authorization predicates (exact indexer, exact step, state =
// transmitting, transmitted_at IS NOT NULL, gate exactly this step) inside
// that single statement. Full DB execution remains Pass 2.
func TestAuthorizedTransmittingStepUsesSingleStatement(t *testing.T) {
	t.Parallel()
	source, err := readSourceFile("indexer_commissioning_live.go", "func (store *Store) authorizedTransmittingStep")
	if err != nil {
		t.Fatal(err)
	}
	// Isolate the authorizer function body: from its declaration to the
	// next top-level func/var declaration.
	start := strings.Index(source, "func (store *Store) authorizedTransmittingStep")
	body := source[start:]
	for _, marker := range []string{"\nfunc ", "\nvar ", "\nconst ", "\ntype "} {
		if index := strings.Index(body[len("func (store *Store) authorizedTransmittingStep"):], marker); index >= 0 {
			body = body[:len("func (store *Store) authorizedTransmittingStep")+index]
			break
		}
	}
	queryRowCalls := strings.Count(body, "QueryRow(")
	if queryRowCalls != 1 {
		t.Fatalf("authorizer performs %d QueryRow calls, want exactly 1", queryRowCalls)
	}
	for _, predicate := range []string{
		"s.state='transmitting'",
		"s.transmitted_at IS NOT NULL",
		"commissioning_gate_step_id",
		"JOIN indexer_commissioning_runs",
		"JOIN indexer_budget_state",
		"r.indexer_instance_id=$1",
		"s.id=$2",
		"SELECT EXISTS",
	} {
		if !strings.Contains(body, predicate) {
			t.Fatalf("authorizer single statement missing %q", predicate)
		}
	}
}

// TestCompletionFingerprintRevalidation proves §10.4 without a database: the
// caller-carried expected structural fingerprint (from the authorized/claimed
// request identity, not from the locked step) must match the locked durable
// step's structural_request_sha256; target kind and category mismatches fail
// closed; the classification input target must match the locked durable
// target. Category ordering and duplicate-category mismatches fail closed;
// nil vs empty category sets compare equivalent (both len 0) and succeed,
// proving the approved empty-set semantics intentionally.
func TestCompletionFingerprintRevalidation(t *testing.T) {
	t.Parallel()
	structural := make([]byte, 32)
	for i := range structural {
		structural[i] = byte(i + 1)
	}
	target := newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
	step := IndexerCommissioningStep{
		ID:                      uuid.New(),
		RunID:                   uuid.New(),
		Target:                  target,
		StructuralRequestSHA256: structural,
		CategoryIDs:             []int{2000, 2040},
	}
	fingerprint, err := commissioningv2.SHA256DigestFromBytes(structural)
	if err != nil {
		t.Fatal(err)
	}
	base := commissioningv2.LiveCompletion{
		ExpectedStructuralFingerprint: fingerprint,
		ExpectedTargetKind: commissioningv2.TargetIdentity{
			Kind:        string(target.Kind),
			RecipeKey:   target.RecipeKey,
			CategoryIDs: []int{2000, 2040},
		},
		ExpectedCategoryIDs: []int{2000, 2040},
	}
	base.Classified.Inputs.TargetKind = target

	// Correct expected fingerprint succeeds.
	if err := revalidateCompletionTarget(base, step); err != nil {
		t.Fatalf("correct fingerprint rejected: %v", err)
	}

	// One-bit fingerprint mismatch fails closed.
	bit := base
	flipped := fingerprint
	flipped[0] ^= 0x01
	bit.ExpectedStructuralFingerprint = flipped
	if err := revalidateCompletionTarget(bit, step); err == nil {
		t.Fatal("one-bit fingerprint mismatch accepted")
	}

	// Target-kind mismatch fails closed.
	kind := base
	kind.ExpectedTargetKind.Kind = string(newznab.EvidenceTargetCAPS)
	kind.ExpectedTargetKind.RecipeKey = ""
	if err := revalidateCompletionTarget(kind, step); err == nil {
		t.Fatal("target-kind mismatch accepted")
	}

	// Category mismatch fails closed.
	categories := base
	categories.ExpectedCategoryIDs = []int{2000, 5000}
	categories.ExpectedTargetKind.CategoryIDs = []int{2000, 5000}
	if err := revalidateCompletionTarget(categories, step); err == nil {
		t.Fatal("category mismatch accepted")
	}

	// Classification input target mismatch fails closed.
	inputs := base
	inputs.Classified.Inputs.TargetKind = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}
	if err := revalidateCompletionTarget(inputs, step); err == nil {
		t.Fatal("classification target mismatch accepted")
	}

	// Category ordering mismatch fails closed: [2040, 2000] != [2000, 2040].
	ordered := base
	ordered.ExpectedCategoryIDs = []int{2040, 2000}
	ordered.ExpectedTargetKind.CategoryIDs = []int{2040, 2000}
	if err := revalidateCompletionTarget(ordered, step); err == nil {
		t.Fatal("category ordering mismatch accepted")
	}

	// Duplicate category mismatch fails closed: length differs.
	duplicated := base
	duplicated.ExpectedCategoryIDs = []int{2000, 2040, 2040}
	duplicated.ExpectedTargetKind.CategoryIDs = []int{2000, 2040, 2040}
	if err := revalidateCompletionTarget(duplicated, step); err == nil {
		t.Fatal("duplicate category mismatch accepted")
	}

	// Nil vs empty category semantics: both represent the same persisted
	// empty category set (len 0), so they compare equivalent and succeed.
	emptyStep := step
	emptyStep.CategoryIDs = nil
	emptyBase := base
	emptyBase.ExpectedCategoryIDs = []int{}
	emptyBase.ExpectedTargetKind.CategoryIDs = []int{}
	if err := revalidateCompletionTarget(emptyBase, emptyStep); err != nil {
		t.Fatalf("nil vs empty category sets rejected: %v", err)
	}
	// Non-empty vs empty still fails closed.
	nonEmpty := base
	nonEmpty.ExpectedCategoryIDs = []int{}
	nonEmpty.ExpectedTargetKind.CategoryIDs = []int{}
	if err := revalidateCompletionTarget(nonEmpty, step); err == nil {
		t.Fatal("empty-vs-nonempty category sets accepted")
	}
}

// TestBoundaryCanonicalAdmissionUppercaseURN proves the F-2 boundary: the
// authoritative boundary helper parses accepted UUID spellings and emits
// canonical uuid.String() for the MatchesAttempt comparison, so
// uppercase/URN spellings of the same attempt reach the canonical
// comparison. Full B-62 DB coverage comes in Correction Pass 2.
func TestBoundaryCanonicalAdmissionUppercaseURN(t *testing.T) {
	t.Parallel()
	if _, _, err := canonicalBoundaryIdentity("not-a-uuid"); err == nil {
		t.Fatal("malformed boundary identity accepted")
	}
	upper := "550E8400-E29B-41D4-A716-446655440000"
	urn := "urn:uuid:550e8400-e29b-41d4-a716-446655440000"
	for name, spelling := range map[string]string{"upper": upper, "urn": urn} {
		_, canonical, err := canonicalBoundaryIdentity(spelling)
		if err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
		if canonical != "550e8400-e29b-41d4-a716-446655440000" {
			t.Fatalf("%s canonical=%q", name, canonical)
		}
	}
}
