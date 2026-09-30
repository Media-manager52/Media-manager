package postgres

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
)

func TestAltMountProgressKeepsStageValidAndWithinTheColumn(t *testing.T) {
	percent := 74
	// An ASCII letter then two-byte characters: a byte cut at 64 splits one.
	long := "a" + strings.Repeat("ø", 70)
	gotPercent, gotStage := altMountProgress(altmount.QueueItem{Percentage: &percent, Stage: "  " + long + "  "})
	if gotPercent == nil || *gotPercent != 74 {
		t.Fatalf("percent=%v, want 74", gotPercent)
	}
	if gotStage == nil || !utf8.ValidString(*gotStage) || utf8.RuneCountInString(*gotStage) != 64 {
		t.Fatalf("stage must be valid UTF-8 and exactly 64 characters, got %q", derefString(gotStage))
	}

	_, invalid := altMountProgress(altmount.QueueItem{Stage: "Download\xffing"})
	if invalid == nil || *invalid != "Downloading" {
		t.Fatalf("invalid bytes must be dropped, got %q", derefString(invalid))
	}

	outOfRange := 101
	noPercent, blank := altMountProgress(altmount.QueueItem{Percentage: &outOfRange, Stage: "   "})
	if noPercent != nil || blank != nil {
		t.Fatalf("out-of-range percent and blank stage must be absent, got %v %v", noPercent, blank)
	}
}

func derefString(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}
