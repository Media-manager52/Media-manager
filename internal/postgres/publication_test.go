package postgres

import (
	"strings"
	"testing"
	"uuid"
)

// TestReleaseTokenDistinguishesSelectionsWithTheSameTimestampPrefix is the
// regression guard for the upgrade collision: UUIDv7 values share their first
// 8 hex characters for roughly a minute, so a token derived only from that
// prefix cannot keep a replacement and the current release in separate Plex
// filenames.
func TestReleaseTokenDistinguishesSelectionsWithTheSameTimestampPrefix(t *testing.T) {
	t.Parallel()
	first := uuid.MustParse("01234567-0000-7000-8000-000000000001")
	second := uuid.MustParse("01234567-0000-7000-8000-000000000002")
	if first.String()[:8] != second.String()[:8] {
		t.Fatal("fixture no longer shares a UUIDv7 timestamp prefix")
	}
	firstToken, secondToken := releaseToken(first), releaseToken(second)
	if firstToken == secondToken {
		t.Fatalf("release selections sharing a UUIDv7 timestamp prefix collided at %q", firstToken)
	}
}

func TestReleaseTokenEmbedsFullSelectedReleaseIdentity(t *testing.T) {
	t.Parallel()
	releaseID := uuid.MustParse("01234567-89ab-7cde-8f01-23456789abcd")
	token := releaseToken(releaseID)
	canonical := strings.ReplaceAll(releaseID.String(), "-", "")
	if !strings.HasPrefix(token, "MM-") || !strings.Contains(token, canonical) {
		t.Fatalf("release token %q does not embed the full selected release %q", token, canonical)
	}
	if token != releaseToken(releaseID) {
		t.Fatal("release token is not deterministic for the same selected release")
	}
}
