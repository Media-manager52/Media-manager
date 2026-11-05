package acquisition

import (
	"testing"

	"github.com/media-manager/media-manager/internal/packs"
)

func TestPackFailureDetailNamesTheProblem(t *testing.T) {
	missing := packs.Report{Verdict: packs.VerdictIncomplete, Missing: []packs.EpisodeKey{{Season: 1, Episode: 3}, {Season: 1, Episode: 4}}}
	if got, want := packFailureDetail(missing), "season pack is incomplete; 2 expected episodes are missing"; got != want {
		t.Fatalf("incomplete detail = %q, want %q", got, want)
	}
	review := packs.Report{Verdict: packs.VerdictNeedsReview, Issues: []packs.Issue{
		{Code: "duplicate", Episodes: []packs.EpisodeKey{{Season: 1, Episode: 2}}},
		{Code: "unexpected", Path: "Show.S01E09.mkv"},
	}}
	if got, want := packFailureDetail(review), "season pack is needs_review; first problem: duplicate (S01E02) and 1 more"; got != want {
		t.Fatalf("needs_review detail = %q, want %q", got, want)
	}
	unexpected := packs.Report{Verdict: packs.VerdictNeedsReview, Issues: []packs.Issue{{Code: "unexpected", Path: "Show.S02E01.mkv"}}}
	if got, want := packFailureDetail(unexpected), "season pack is needs_review; first problem: unexpected (Show.S02E01.mkv)"; got != want {
		t.Fatalf("unexpected detail = %q, want %q", got, want)
	}
}
