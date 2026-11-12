package dbcore

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateTextKeepsWholeCharacters(t *testing.T) {
	text := strings.Repeat("a", 999) + "æøå"
	got := TruncateText(text, 1000)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 1000 || !strings.HasSuffix(got, "æ") {
		t.Fatalf("TruncateText = %q (%d runes)", got[990:], utf8.RuneCountInString(got))
	}
	if got := TruncateText("ok\xff", 10); got != "ok" {
		t.Fatalf("invalid UTF-8 kept: %q", got)
	}
	if got := TruncateText("kort", 10); got != "kort" {
		t.Fatalf("short text changed: %q", got)
	}
}
