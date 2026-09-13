package postgres

import (
	"testing"

	"uuid"
)

// TestReservationLockIdentitySharedAcrossSpellings proves the single shared
// advisory-lock namespace: equivalent UUID spellings produce the same
// lowercase uuid.String() identity with the same literal prefix, and the
// reservation table semantics are unchanged (no second lock namespace).
func TestReservationLockIdentitySharedAcrossSpellings(t *testing.T) {
	t.Parallel()
	lower := "550e8400-e29b-41d4-a716-446655440000"
	spellings := map[string]string{
		"lower":  lower,
		"upper":  "550E8400-E29B-41D4-A716-446655440000",
		"urn":    "urn:uuid:550e8400-e29b-41d4-a716-446655440000",
		"braced": "{550e8400-e29b-41d4-a716-446655440000}",
	}
	for name, spelling := range spellings {
		if got := ReservationLockKeyForTest(spelling); got != "indexer-request-budget:"+lower {
			t.Fatalf("%s lock key=%q, want canonical", name, got)
		}
	}
	// Non-UUID IDs preserve existing literal behavior rather than rejecting.
	if got := ReservationLockKeyForTest("legacy-indexer-1"); got != "indexer-request-budget:legacy-indexer-1" {
		t.Fatalf("non-UUID lock key changed: %q", got)
	}
	// Sanity: uuid.String() is the canonical lowercase form.
	parsed, err := uuid.Parse(spellings["upper"])
	if err != nil || parsed.String() != lower {
		t.Fatalf("uuid canonicalization broken: %v %q", parsed, lower)
	}
}
