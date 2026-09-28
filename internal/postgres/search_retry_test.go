package postgres

import (
	"testing"
	"time"
)

func TestNoSelectionRetryDelayBacksOffToCap(t *testing.T) {
	t.Parallel()
	for runs, want := range map[int]time.Duration{
		0:  30 * time.Minute,
		1:  30 * time.Minute,
		2:  time.Hour,
		3:  2 * time.Hour,
		4:  4 * time.Hour,
		5:  8 * time.Hour,
		6:  12 * time.Hour,
		48: 12 * time.Hour,
	} {
		if got := noSelectionRetryDelay(runs); got != want {
			t.Fatalf("noSelectionRetryDelay(%d)=%s, want %s", runs, got, want)
		}
	}
}
