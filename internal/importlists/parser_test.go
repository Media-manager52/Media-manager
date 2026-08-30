package importlists

import (
	"strings"
	"testing"
)

func TestParseIMDbExportClassifiesMonitoringReview(t *testing.T) {
	items, err := ParseIMDbExport(strings.NewReader("Const,Title,Title Type,Year\ntt1234567,Example Movie,movie,2024\ntt7654321,Example Series,tvSeries,2025\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].MediaKind != "movie" || items[0].State != "pending_review" || items[1].MediaKind != "series" || items[1].State != "unsupported" {
		t.Fatalf("items=%+v", items)
	}
}

func TestParseIMDbExportRejectsDuplicateIdentity(t *testing.T) {
	_, err := ParseIMDbExport(strings.NewReader("Const,Title,Title Type,Year\ntt1234567,One,movie,2024\ntt1234567,Two,movie,2024\n"))
	if err == nil {
		t.Fatal("expected duplicate identity error")
	}
}
