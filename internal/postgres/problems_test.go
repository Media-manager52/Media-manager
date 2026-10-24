package postgres

import "testing"

func TestHumanizeCode(t *testing.T) {
	tests := map[string]string{
		"season_pack_incomplete": "Season pack incomplete",
		"needs_attention":        "Needs attention",
		"":                       "Unknown",
		"altmount_job_attention": "Altmount job attention",
	}
	for input, want := range tests {
		if got := HumanizeCode(input); got != want {
			t.Fatalf("HumanizeCode(%q)=%q, want %q", input, got, want)
		}
	}
}
