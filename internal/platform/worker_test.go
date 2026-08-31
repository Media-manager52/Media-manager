package platform

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type codedTestError struct {
	code string
}

func (err codedTestError) Error() string    { return "details must remain private" }
func (err codedTestError) SafeCode() string { return err.code }

func TestSafeErrorUsesOnlyValidatedCodes(t *testing.T) {
	if got := safeError(codedTestError{code: "newznab_invalid_nzb"}); got != "newznab_invalid_nzb" {
		t.Fatalf("safe error=%q", got)
	}
	unsafe := safeError(codedTestError{code: "secret=value"})
	if strings.Contains(unsafe, "secret") || unsafe == "secret=value" {
		t.Fatalf("unsafe error code escaped validation: %q", unsafe)
	}
	if got := safeError(errors.New("private details")); got != "*errors.errorString" {
		t.Fatalf("fallback error=%q", got)
	}
}

type deferredTestError struct{ at time.Time }

func (err deferredTestError) Error() string      { return "deferred" }
func (err deferredTestError) RetryAt() time.Time { return err.at }
func (err deferredTestError) SafeCode() string   { return "indexer_pacing_deferred" }

func TestNextRetryAtHonorsADeferredPacingDeadline(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	want := now.Add(10 * time.Minute)
	if got := nextRetryAt(deferredTestError{at: want}, 1, now); got != want {
		t.Fatalf("retry=%v want=%v", got, want)
	}
	if got := nextRetryAt(errors.New("ordinary"), 1, now); got != now.Add(2*time.Second) {
		t.Fatalf("ordinary retry=%v", got)
	}
}
