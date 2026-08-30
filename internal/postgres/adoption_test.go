package postgres

import (
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
)

func TestValidAdoptionVariantRequiresExactSeriesSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind    catalog.MediaKind
		variant string
		valid   bool
	}{
		{catalog.KindMovie, "default", true},
		{catalog.KindMovie, "season:01", false},
		{catalog.KindSeries, "season:00", true},
		{catalog.KindSeries, "season:12", true},
		{catalog.KindSeries, "", false},
		{catalog.KindSeries, "default", false},
		{catalog.KindSeries, "season:1", false},
		{catalog.KindSeries, "season:100", false},
	}
	for _, test := range tests {
		if got := validAdoptionVariant(test.kind, test.variant); got != test.valid {
			t.Errorf("validAdoptionVariant(%q, %q)=%v, want %v", test.kind, test.variant, got, test.valid)
		}
	}
}
