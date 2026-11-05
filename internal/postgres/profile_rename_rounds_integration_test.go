package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
)

// renameRoundsCase prepares, on a fresh fixture, one workflow whose selected
// release was refused with an access error inside its open round, and one
// workflow whose round is exhausted with a known eligible release. Both rounds
// were opened under the profile's first version. edit then changes the
// profile through the ordinary owner update.
func renameRoundsCase(t *testing.T, edit func(profile QualityProfile) QualityProfile) (*failureRecoveryFixture, workflows.Workflow, RetrievalContext, workflows.Workflow) {
	t.Helper()
	f := newFailureRecoveryFixture(t)
	open := f.newMovieWorkflow(t, "Rename open round", "930001")
	refused := seedRetrievalCandidate(t, f.ctx, f.store, open.ID, f.indexer.ID, "rename-access")
	seedAccessRetryDecision(t, f, refused)
	if err := f.store.RecordReleaseRetrievalFailure(f.ctx, refused, "newznab_http_403", "access denied", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	exhausted := f.newMovieWorkflow(t, "Rename exhausted round", "930002")
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test' WHERE workflow_id=$1 AND state='open'`, exhausted.ID.String()); err != nil {
		t.Fatal(err)
	}
	waiting, err := f.store.TransitionWorkflow(f.ctx, exhausted.ID, exhausted.Revision, workflows.StateWaiting, "test exhausted round", "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	seedEligibleCandidate(t, f.ctx, f.store, exhausted.ID, f.indexer.ID, "rename-known", 10, time.Now().UTC())

	profile := storedQualityProfile(t, f.ctx, f.store, f.profile.ID)
	if profile.PolicyVersion != 1 {
		t.Fatalf("fixture profile version=%d, want 1", profile.PolicyVersion)
	}
	if _, err := f.store.UpdateQualityProfile(f.ctx, edit(profile), profile.PolicyVersion); err != nil {
		t.Fatal(err)
	}
	if after := storedQualityProfile(t, f.ctx, f.store, f.profile.ID); after.PolicyVersion != 2 {
		t.Fatalf("edited profile version=%d, want 2: every edit stays the optimistic token", after.PolicyVersion)
	}
	return f, open, refused, waiting
}

// TestRenamingAProfileKeepsItsRoundsCurrent proves a rename (version bumped,
// rules identical) does not park open rounds for policy review, does not stop
// an access-refused release from being retried and fetched, and still lets the
// owner open a round from a release known under the old version.
func TestRenamingAProfileKeepsItsRoundsCurrent(t *testing.T) {
	f, open, refused, exhausted := renameRoundsCase(t, func(profile QualityProfile) QualityProfile {
		profile.Name = "Dansk tekst eller tale"
		return profile
	})

	search, err := f.store.LoadSearchContext(f.ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !search.RoundPolicyCurrent || search.PolicyVersion != 1 || search.ProfilePolicyVersion != 2 {
		t.Fatalf("round current=%v version=%d profile=%d, want a current round still at version 1 under profile version 2", search.RoundPolicyCurrent, search.PolicyVersion, search.ProfilePolicyVersion)
	}

	failed, err := f.store.GetWorkflow(f.ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := f.store.ListWorkflowSummaries(f.ctx, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	offered := false
	for _, summary := range summaries {
		if summary.ID == open.ID && summary.AccessRejectedCandidateID != nil && *summary.AccessRejectedCandidateID == refused.CandidateID {
			offered = true
		}
	}
	if !offered {
		t.Fatal("the access retry is no longer offered after a rename")
	}
	if err := f.store.RetryAccessRejectedRelease(f.ctx, open.ID, refused.CandidateID, failed.Revision, time.Now().UTC()); err != nil {
		t.Fatalf("access retry after a rename: %v", err)
	}
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, recoverySelected(t, f, open.ID), time.Now().UTC()); err != nil || !allowed {
		t.Fatalf("NZB retrieval after a rename allowed=%v err=%v", allowed, err)
	}

	if err := f.store.OpenKnownAcquisitionRound(f.ctx, exhausted.ID, time.Now().UTC()); err != nil {
		t.Fatalf("known round after a rename: %v", err)
	}
	known, err := f.store.LoadSearchContext(f.ctx, exhausted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !known.RoundPolicyCurrent || known.PolicyVersion != 1 {
		t.Fatalf("known round current=%v version=%d, want a current round at the decision's version 1", known.RoundPolicyCurrent, known.PolicyVersion)
	}
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, recoverySelected(t, f, exhausted.ID), time.Now().UTC()); err != nil || !allowed {
		t.Fatalf("NZB retrieval in the known round allowed=%v err=%v", allowed, err)
	}
	if got := f.count(t, `SELECT count(*) FROM workflows WHERE attention_code=$1`, workflows.AttentionQualityPolicyChanged); got != 0 {
		t.Fatalf("%d workflows were parked for policy review by a rename", got)
	}
}

// TestChangingProfileRulesStillMakesRoundsStale is the guard on the other
// side: a real change to the rules keeps every stale-round refusal in force.
func TestChangingProfileRulesStillMakesRoundsStale(t *testing.T) {
	f, open, refused, exhausted := renameRoundsCase(t, func(profile QualityProfile) QualityProfile {
		profile.Policy = requiredAudioPolicy(t, "eac3")
		return profile
	})

	search, err := f.store.LoadSearchContext(f.ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if search.RoundPolicyCurrent {
		t.Fatal("a round frozen under the old rules counts as current")
	}

	failed, err := f.store.GetWorkflow(f.ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RetryAccessRejectedRelease(f.ctx, open.ID, refused.CandidateID, failed.Revision, time.Now().UTC()); err == nil {
		t.Fatal("an access retry under stale rules was accepted")
	}
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, refused, time.Now().UTC()); err != nil && !errors.Is(err, ErrConcurrentModification) || allowed {
		t.Fatalf("NZB retrieval under stale rules allowed=%v err=%v", allowed, err)
	}
	if err := f.store.OpenKnownAcquisitionRound(f.ctx, exhausted.ID, time.Now().UTC()); err == nil {
		t.Fatal("a known round was opened from a decision made under the old rules")
	}
}
