package postgres

import "testing"

func TestRedactNotificationEndpoint(t *testing.T) {
	if value := RedactNotificationEndpoint("https://hooks.example.test/services/path-token"); value != "https://hooks.example.test/…" {
		t.Fatalf("redacted endpoint=%q", value)
	}
	if value := RedactNotificationEndpoint("not a URL"); value != "redacted" {
		t.Fatalf("invalid endpoint redaction=%q", value)
	}
}
