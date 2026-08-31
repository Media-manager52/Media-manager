package platform

import (
	"errors"
	"strings"
	"testing"
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
