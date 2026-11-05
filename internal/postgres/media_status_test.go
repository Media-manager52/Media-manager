package postgres

import (
	"testing"
	"uuid"
)

func TestScopeCoveredByLibraryFiles(t *testing.T) {
	media := uuid.NewV7()
	present := map[fileScope]bool{
		{media, "default", "standard"}:              true,
		{media, "season:01", "standard"}:            true,
		{media, "season:02:episode:01", "standard"}: true,
		{media, "season:02:episode:02", "standard"}: true,
	}
	for _, check := range []struct {
		variant, edition string
		expected         []int
		want             bool
	}{
		{"default", "standard", nil, true},
		{"default", "4k", nil, false},                    // the 4K copy is still wanted
		{"season:01:episode:05", "standard", nil, true},  // inside the season pack
		{"season:02", "standard", []int{1, 2}, true},     // every episode as its own file
		{"season:02", "standard", []int{1, 2, 3}, false}, // one episode is missing
		{"season:03", "standard", nil, false},            // unknown episode list
		{"season:02:episode:03", "standard", nil, false},
	} {
		if got := scopeCovered(present, check.expected, media, check.variant, check.edition); got != check.want {
			t.Errorf("%s/%s expected=%v covered=%v, want %v", check.variant, check.edition, check.expected, got, check.want)
		}
	}
}
