package httpapi

import (
	"strings"
	"testing"
)

func TestRedactSupportText(t *testing.T) {
	input := "Version: test\nAuthorization: Bearer abc123\napi_key=secret\nnormal detail: release failed"
	got := redactSupportText(input)
	if strings.Contains(got, "abc123") || strings.Contains(got, "api_key=secret") {
		t.Fatalf("secret survived redaction: %q", got)
	}
	if !strings.Contains(got, "normal detail: release failed") {
		t.Fatalf("ordinary detail was removed: %q", got)
	}
}

func TestHumanBytesValue(t *testing.T) {
	if got := humanBytesValue(0); got != "0 B" {
		t.Fatalf("0 bytes = %q", got)
	}
	if got := humanBytesValue(10 << 30); got != "10 GiB" {
		t.Fatalf("10 GiB = %q", got)
	}
}
