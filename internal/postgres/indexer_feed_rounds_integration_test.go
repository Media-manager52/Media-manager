package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/workflows"
)

// seedFeedReadyRound leaves a movie workflow waiting with its round used up
// and discovery holding one newly observed eligible release: the state in
// which the owner's "start next round" is accepted.
func seedFeedReadyRound(t *testing.T, f *failureRecoveryFixture, title, tmdb string, observedAt time.Time) (workflows.Workflow, uuid.UUID) {
	t.Helper()
	w := f.newMovieWorkflow(t, title, tmdb)
	seedOwnerDiscovery(t, f, w)
	candidate := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "ready-"+tmdb, 100, observedAt)
	markFeedDiscoveryReady(t, f, w.ID, candidate)
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND completed_at IS NULL`, w.ID.String()); err != nil {
		t.Fatal(err)
	}
	waiting, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return waiting, candidate
}

func markFeedDiscoveryReady(t *testing.T, f *failureRecoveryFixture, workflowID, candidate uuid.UUID) {
	t.Helper()
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET state='ready',next_rescan_at=NULL,ready_candidate_id=$2,
 ready_first_seen_sequence=(SELECT first_seen_sequence FROM release_candidates WHERE id=$2) WHERE workflow_id=$1`, workflowID.String(), candidate.String()); err != nil {
		t.Fatal(err)
	}
}

func feedRoundTransitions(t *testing.T, f *failureRecoveryFixture, workflowID uuid.UUID) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM workflow_transitions WHERE workflow_id=$1 AND actor='worker' AND to_state='searching' AND reason=$2`, workflowID.String(), feedRoundReason)
}

// TestFeedMatchOpensTheNextRoundTheOwnerActionWouldOpen proves that a match
// for a title whose round is used up, and whose discovery holds an eligible
// release, opens exactly the round the owner's action opens: one bounded
// round, recorded as the worker, with nothing selected by the match itself.
// A second match within a day opens nothing; one after a day does.
func TestFeedMatchOpensTheNextRoundTheOwnerActionWouldOpen(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	w, _ := seedFeedReadyRound(t, f, "Feed Round", "903101", now.Add(-time.Hour))
	recordFeedMatch(t, f, w.ID, "round-guid-1", now)

	result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.RoundsOpened != 1 || result.NotApplicable != 0 {
		t.Fatalf("result=%+v", result)
	}
	var ordinal, attempts int
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT round_ordinal,attempt_limit_snapshot FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, w.ID.String()).Scan(&ordinal, &attempts); err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 || attempts != f.store.AcquisitionLimits().AttemptsPerRound {
		t.Fatalf("round ordinal=%d attempts=%d", ordinal, attempts)
	}
	opened, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.State != workflows.StateSearching || opened.Revision != w.Revision+1 || opened.SelectedRelease != nil || opened.GrabAttemptID != nil {
		t.Fatalf("workflow after the round: %+v", opened)
	}
	if n := feedRoundTransitions(t, f, w.ID); n != 1 {
		t.Fatalf("worker round transitions=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.acquisition_round_opened' AND actor='worker' AND detail->>'reason'='feed_match'`, w.ID.String()); n != 1 {
		t.Fatalf("worker audit events=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_type='workflow' AND aggregate_id=$1 AND aggregate_revision=$2 AND event_type='workflow.search_requested' AND completed_at IS NULL`, w.ID.String(), int64(opened.Revision)); n != 1 {
		t.Fatalf("round search events=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, w.ID.String()); n != 0 {
		t.Fatalf("the match submitted a download: %d", n)
	}
	if got := feedOutcomes(t, f, w.ID); got["round_opened"] != 1 || len(got) != 1 {
		t.Fatalf("outcomes=%v", got)
	}

	// The new round is used up too and discovery finds another release an
	// hour later: within a day of the first automatic round nothing opens.
	exhaustAgain := func(key string, observedAt time.Time) {
		t.Helper()
		var round string
		if err := f.store.Pool.QueryRow(f.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test exhausted' WHERE workflow_id=$1 AND state='open' RETURNING id::text`, w.ID.String()).Scan(&round); err != nil {
			t.Fatal(err)
		}
		current, err := f.store.GetWorkflow(f.ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.TransitionWorkflow(f.ctx, w.ID, current.Revision, workflows.StateWaiting, "test exhausted", "test", observedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET exhausted_round_id=$2 WHERE workflow_id=$1`, w.ID.String(), round); err != nil {
			t.Fatal(err)
		}
		markFeedDiscoveryReady(t, f, w.ID, seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, key, 100, observedAt))
		if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND completed_at IS NULL`, w.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	exhaustAgain("ready-second", now.Add(time.Minute))
	soon := now.Add(time.Hour)
	recordFeedMatch(t, f, w.ID, "round-guid-2", soon)
	result, err = f.store.AdvanceFeedMatches(f.ctx, soon, 30*time.Minute, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.RoundsOpened != 0 || result.NotApplicable != 1 {
		t.Fatalf("second match within a day: %+v", result)
	}
	if n := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, w.ID.String()); n != 0 {
		t.Fatalf("a second round opened within a day: %d", n)
	}

	later := now.Add(feedRoundInterval + time.Minute)
	recordFeedMatch(t, f, w.ID, "round-guid-3", later)
	result, err = f.store.AdvanceFeedMatches(f.ctx, later, 30*time.Minute, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.RoundsOpened != 1 {
		t.Fatalf("match after a day: %+v", result)
	}
	if n := feedRoundTransitions(t, f, w.ID); n != 2 {
		t.Fatalf("worker round transitions=%d", n)
	}
}

// TestFeedMatchOpensNoRoundTheOwnerActionWouldRefuse covers the guards: with
// the switch off, after a profile change, and when the found release was
// already tried, a match settles as not applicable and writes nothing else.
func TestFeedMatchOpensNoRoundTheOwnerActionWouldRefuse(t *testing.T) {
	for _, name := range []string{"switch off", "policy changed", "release already tried", "no release found"} {
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			w, candidate := seedFeedReadyRound(t, f, "Feed Refused", "903111", now.Add(-time.Hour))
			openRounds := true
			switch name {
			case "switch off":
				openRounds = false
			case "policy changed":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy_version=policy_version+1,policy='{"required_subtitle_languages":["sv"]}' WHERE id=$1`, f.profile.ID.String()); err != nil {
					t.Fatal(err)
				}
			case "release already tried":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE candidate_decisions SET verdict='rejected' WHERE workflow_id=$1 AND candidate_id=$2`, w.ID.String(), candidate.String()); err != nil {
					t.Fatal(err)
				}
			case "no release found":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET state='paused',ready_candidate_id=NULL,ready_first_seen_sequence=NULL WHERE workflow_id=$1`, w.ID.String()); err != nil {
					t.Fatal(err)
				}
			}
			roundsBefore := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String())
			eventsBefore := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String())
			auditBefore := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1`, w.ID.String())
			recordFeedMatch(t, f, w.ID, "refused-guid", now)

			result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10, openRounds)
			if err != nil {
				t.Fatal(err)
			}
			if result.RoundsOpened != 0 || result.NotApplicable != 1 {
				t.Fatalf("result=%+v", result)
			}
			after, err := f.store.GetWorkflow(f.ctx, w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.State != workflows.StateWaiting || after.Revision != w.Revision {
				t.Fatalf("workflow changed: %+v", after)
			}
			if rounds := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String()); rounds != roundsBefore {
				t.Fatalf("rounds %d -> %d", roundsBefore, rounds)
			}
			if events := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String()); events != eventsBefore {
				t.Fatalf("events %d -> %d", eventsBefore, events)
			}
			if audit := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1`, w.ID.String()); audit != auditBefore {
				t.Fatalf("audit events %d -> %d", auditBefore, audit)
			}
			if n := feedRoundTransitions(t, f, w.ID); n != 0 {
				t.Fatalf("worker round transitions=%d", n)
			}
			if got := feedOutcomes(t, f, w.ID); got["not_applicable"] != 1 || len(got) != 1 {
				t.Fatalf("outcomes=%v", got)
			}
		})
	}
}

// TestFeedDiscoveryThatFoundAReleaseOpensTheRoundOnALaterPass covers the two
// steps of a title whose discovery was still looking: the match brings the
// paced discovery forward, and once that discovery holds an eligible release
// a later pass opens the round. With the switch off the second step is the
// owner's.
func TestFeedDiscoveryThatFoundAReleaseOpensTheRoundOnALaterPass(t *testing.T) {
	for _, openRounds := range []bool{true, false} {
		name := "switch on"
		if !openRounds {
			name = "switch off"
		}
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Feed Two Steps", "903121")
			seedOwnerDiscovery(t, f, w)
			if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_type='workflow' AND aggregate_id=$1 AND completed_at IS NULL`, w.ID.String()); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			recordFeedMatch(t, f, w.ID, "two-step-guid", now)
			result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10, openRounds)
			if err != nil {
				t.Fatal(err)
			}
			if result.DiscoveryAdvanced != 1 || result.RoundsOpened != 0 {
				t.Fatalf("first pass: %+v", result)
			}
			// Still looking: a pass in between changes nothing.
			result, err = f.store.AdvanceFeedMatches(f.ctx, now.Add(time.Minute), 30*time.Minute, 10, openRounds)
			if err != nil {
				t.Fatal(err)
			}
			if result != (FeedAdvanceResult{}) {
				t.Fatalf("pass while discovery is looking: %+v", result)
			}

			found := now.Add(2 * time.Minute)
			markFeedDiscoveryReady(t, f, w.ID, seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "two-step-found", 100, found))
			result, err = f.store.AdvanceFeedMatches(f.ctx, found.Add(time.Minute), 30*time.Minute, 10, openRounds)
			if err != nil {
				t.Fatal(err)
			}
			open := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, w.ID.String())
			got := feedOutcomes(t, f, w.ID)
			if openRounds {
				if result.RoundsOpened != 1 || open != 1 || got["round_opened"] != 1 || len(got) != 1 {
					t.Fatalf("result=%+v open=%d outcomes=%v", result, open, got)
				}
				if n := feedRoundTransitions(t, f, w.ID); n != 1 {
					t.Fatalf("worker round transitions=%d", n)
				}
				return
			}
			if result != (FeedAdvanceResult{}) || open != 0 || got["discovery_advanced"] != 1 || len(got) != 1 {
				t.Fatalf("switch off: result=%+v open=%d outcomes=%v", result, open, got)
			}
		})
	}
}
