package httpapi

import (
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
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

func TestRedactSupportTextCoversEveryCredentialShape(t *testing.T) {
	for _, line := range []string{
		"detail: GET https://user:hunter2@example.test/api failed",
		"X-Api-Key: hunter2",
		"Cookie: session=hunter2",
		"https://example.test/api?apikey=hunter2&t=search",
		"X-Plex-Token=hunter2",
		"password: hunter2",
	} {
		if got := redactSupportText(line); strings.Contains(got, "hunter2") {
			t.Fatalf("secret survived redaction: %q", got)
		}
	}
}

func TestRedactErrorLogRemovesCredentialsFromProviderText(t *testing.T) {
	log := redactErrorLog(postgres.ErrorLog{Entries: []postgres.ErrorLogEntry{{
		Subject: "https://indexer.test/api?apikey=hunter2&t=search",
		Detail:  "GET https://user:hunter2@example.test/nzb failed",
	}, {
		Subject: "Movie.2026.1080p.WEB-GROUP",
		Detail:  "no files were successfully processed",
	}}})
	for _, entry := range log.Entries[:1] {
		if strings.Contains(entry.Subject, "hunter2") || strings.Contains(entry.Detail, "hunter2") {
			t.Fatalf("secret survived redaction: %+v", entry)
		}
	}
	if log.Entries[1].Subject != "Movie.2026.1080p.WEB-GROUP" || log.Entries[1].Detail != "no files were successfully processed" {
		t.Fatalf("ordinary text changed: %+v", log.Entries[1])
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
