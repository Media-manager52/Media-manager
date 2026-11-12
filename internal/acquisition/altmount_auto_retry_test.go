package acquisition

import (
	"testing"
	"time"
)

// The failure messages below are the ones that stopped imports at owner
// attention in production.
func TestAltMountAutoRetryBudget(t *testing.T) {
	tests := []struct {
		detail string
		limit  int
	}{
		{"fast-fail validation inconclusive: context deadline exceeded", altMountBusyRetries},
		{"fast-fail STAT probe inconclusive: i/o timeout", altMountBusyRetries},
		{"dial tcp: connection refused", altMountBusyRetries},
		{"failed to ensure persistent NZB: failed to move NZB to persistent storage: failed to stat source file: stat /tmp/x.nzb: no such file or directory", altMountOtherRetries},
		{`failed to open 7zip archive "a.7z.001": sevenzip: error initialising: sevenzip: read error: sevenzip: unexpected id`, altMountOtherRetries},
		{`failed to process nested RAR archives: nested RAR "x" contained no extractable files`, altMountOtherRetries},
		{"failed to resolve embedded NZB: All attempts fail:\n#1: nntp: 430 no such article", altMountOtherRetries},
		{"AltMount job needs attention", altMountOtherRetries},
	}
	for _, test := range tests {
		if got := altMountAutoRetryLimit(test.detail); got != test.limit {
			t.Fatalf("limit(%q)=%d, want %d", test.detail, got, test.limit)
		}
	}
}

func TestAltMountAutoRetryPausesGrow(t *testing.T) {
	want := []time.Duration{10 * time.Minute, time.Hour, 4 * time.Hour, 12 * time.Hour, 12 * time.Hour}
	for done, pause := range want {
		if got := altMountAutoRetryPause(done); got != pause {
			t.Fatalf("pause after %d retries=%v, want %v", done, got, pause)
		}
	}
	if got := altMountAutoRetryPause(-1); got != 10*time.Minute {
		t.Fatalf("negative count pause=%v", got)
	}
}

func TestGiveUpDetailSaysWhatHappened(t *testing.T) {
	if got := giveUpDetail(4, "timeout"); got != "AltMount could not import this release after 4 automatic retries: timeout" {
		t.Fatalf("detail=%q", got)
	}
	if got := giveUpDetail(0, " "); got != "AltMount could not import this release: AltMount job failed" {
		t.Fatalf("detail=%q", got)
	}
}
