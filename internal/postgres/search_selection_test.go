package postgres

import (
	"errors"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestSelectRankedCandidateFallsBackToKnownEligible pins the fix for rounds
// whose novelty cutoff predates every eligible release: the first pass still
// prefers a new post, but the search must fall back to a known, never-attempted
// eligible release instead of parking the workflow as held forever.
func TestSelectRankedCandidateFallsBackToKnownEligible(t *testing.T) {
	t.Parallel()
	novel := releases.Evaluation{ObservationID: uuid.New(), Verdict: releases.VerdictEligible, Score: 100}
	old := releases.Evaluation{ObservationID: uuid.New(), Verdict: releases.VerdictEligible, Score: 900}
	held := releases.Evaluation{ObservationID: uuid.New(), Verdict: releases.VerdictHeld}
	ranked := []releases.Evaluation{old, novel, held} // releases.Rank puts eligible first by score.
	notAttempted := func(releases.Evaluation) (bool, error) { return false, nil }
	onlyNovel := func(evaluation releases.Evaluation) bool { return evaluation.ObservationID == novel.ObservationID }

	selected, state, err := selectRankedCandidate(ranked, notAttempted, onlyNovel)
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || selected.ObservationID != novel.ObservationID || state != workflows.StateCandidateSelected {
		t.Fatalf("selected=%+v state=%s, want the novel eligible candidate", selected, state)
	}

	// No novel candidate is selectable: the known eligible one is used.
	selected, state, err = selectRankedCandidate(ranked, notAttempted, func(releases.Evaluation) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || selected.ObservationID != old.ObservationID || state != workflows.StateCandidateSelected {
		t.Fatalf("selected=%+v state=%s, want the known eligible candidate", selected, state)
	}

	// Every eligible candidate was already attempted: no selection, held wins
	// over waiting because a held candidate was observed.
	allAttempted := func(releases.Evaluation) (bool, error) { return true, nil }
	selected, state, err = selectRankedCandidate(ranked, allAttempted, onlyNovel)
	if err != nil {
		t.Fatal(err)
	}
	if selected != nil || state != workflows.StateHeld {
		t.Fatalf("selected=%+v state=%s, want held", selected, state)
	}

	// No held candidate and nothing selectable: waiting.
	selected, state, err = selectRankedCandidate([]releases.Evaluation{novel}, allAttempted, onlyNovel)
	if err != nil {
		t.Fatal(err)
	}
	if selected != nil || state != workflows.StateWaiting {
		t.Fatalf("selected=%+v state=%s, want waiting", selected, state)
	}

	sentinel := errors.New("attempted lookup failed")
	if _, _, err := selectRankedCandidate(ranked, func(releases.Evaluation) (bool, error) { return false, sentinel }, onlyNovel); !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want %v", err, sentinel)
	}
}
