package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
)

// TestRescanFoundReleaseOpensTheNextRoundOncePerDay proves that a title whose
// round is used up and whose own scheduled rescan found an eligible release
// gets exactly the round the owner's action opens: one bounded round, recorded
// as the worker, with nothing selected or submitted. The daily limit is shared
// with the newest-uploads path.
func TestRescanFoundReleaseOpensTheNextRoundOncePerDay(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	w, _ := seedFeedReadyRound(t, f, "Rescan Round", "903201", now.Add(-time.Hour))

	result, err := f.store.OpenRescanFoundRounds(f.ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.RoundsOpened != 1 || result.Refused != 0 {
		t.Fatalf("result=%+v", result)
	}
	opened, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.State != workflows.StateSearching || opened.Revision != w.Revision+1 || opened.SelectedRelease != nil || opened.GrabAttemptID != nil {
		t.Fatalf("workflow after the round: %+v", opened)
	}
	var ordinal, attempts int
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT round_ordinal,attempt_limit_snapshot FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, w.ID.String()).Scan(&ordinal, &attempts); err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 || attempts != f.store.AcquisitionLimits().AttemptsPerRound {
		t.Fatalf("round ordinal=%d attempts=%d", ordinal, attempts)
	}
	if n := f.count(t, `SELECT count(*) FROM workflow_transitions WHERE workflow_id=$1 AND actor='worker' AND to_state='searching' AND reason=$2`, w.ID.String(), rescanRoundReason); n != 1 {
		t.Fatalf("worker round transitions=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.acquisition_round_opened' AND actor='worker' AND detail->>'reason'='rescan_found'`, w.ID.String()); n != 1 {
		t.Fatalf("worker audit events=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, w.ID.String()); n != 0 {
		t.Fatalf("the round submitted a download by itself: %d", n)
	}

	// The new round is used up too and the rescan finds another release: no
	// round within a day, from either path.
	var round string
	if err := f.store.Pool.QueryRow(f.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test exhausted' WHERE workflow_id=$1 AND state='open' RETURNING id::text`, w.ID.String()).Scan(&round); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TransitionWorkflow(f.ctx, w.ID, opened.Revision, workflows.StateWaiting, "test exhausted", "test", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET exhausted_round_id=$2 WHERE workflow_id=$1`, w.ID.String(), round); err != nil {
		t.Fatal(err)
	}
	markFeedDiscoveryReady(t, f, w.ID, seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "rescan-second", 100, now.Add(time.Minute)))
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND completed_at IS NULL`, w.ID.String()); err != nil {
		t.Fatal(err)
	}
	soon := now.Add(time.Hour)
	if result, err = f.store.OpenRescanFoundRounds(f.ctx, soon, 10); err != nil || result != (RescanRoundResult{}) {
		t.Fatalf("within a day: result=%+v err=%v", result, err)
	}
	recordFeedMatch(t, f, w.ID, "rescan-feed-guid", soon)
	feed, err := f.store.AdvanceFeedMatches(f.ctx, soon, 30*time.Minute, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if feed.RoundsOpened != 0 {
		t.Fatalf("a feed match opened a second round within a day: %+v", feed)
	}
	if n := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, w.ID.String()); n != 0 {
		t.Fatalf("a second round opened within a day: %d", n)
	}

	later := now.Add(feedRoundInterval + time.Minute)
	if result, err = f.store.OpenRescanFoundRounds(f.ctx, later, 10); err != nil || result.RoundsOpened != 1 {
		t.Fatalf("after a day: result=%+v err=%v", result, err)
	}
}

// TestRescanFoundRoundRefusalWritesNothingAndIsNotAskedAgain covers the
// guards: when the owner's action would be refused nothing is written but a
// note on the discovery, and the same discovery is not asked again. A title
// whose discovery has found nothing is never asked.
func TestRescanFoundRoundRefusalWritesNothingAndIsNotAskedAgain(t *testing.T) {
	for _, name := range []string{"policy changed", "release already tried", "no release found"} {
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			w, candidate := seedFeedReadyRound(t, f, "Rescan Refused", "903211", now.Add(-time.Hour))
			wantRefused := 1
			switch name {
			case "policy changed":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy_version=policy_version+1,policy='{"required_subtitle_languages":["sv"]}' WHERE id=$1`, f.profile.ID.String()); err != nil {
					t.Fatal(err)
				}
			case "release already tried":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE candidate_decisions SET verdict='rejected' WHERE workflow_id=$1 AND candidate_id=$2`, w.ID.String(), candidate.String()); err != nil {
					t.Fatal(err)
				}
			case "no release found":
				wantRefused = 0
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET state='waiting',ready_candidate_id=NULL,ready_first_seen_sequence=NULL WHERE workflow_id=$1`, w.ID.String()); err != nil {
					t.Fatal(err)
				}
			}
			roundsBefore := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String())
			eventsBefore := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String())
			auditBefore := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1`, w.ID.String())

			result, err := f.store.OpenRescanFoundRounds(f.ctx, now, 10)
			if err != nil {
				t.Fatal(err)
			}
			if result.RoundsOpened != 0 || result.Refused != wantRefused {
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
			// The same discovery is not asked again on the next pass.
			if result, err = f.store.OpenRescanFoundRounds(f.ctx, now.Add(time.Minute), 10); err != nil || result != (RescanRoundResult{}) {
				t.Fatalf("second pass: result=%+v err=%v", result, err)
			}
			if wantRefused == 0 {
				return
			}
			// A later discovery generation is asked afresh: the owner's action
			// stays available and nothing is blocked for good.
			if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET rescan_generation=rescan_generation+1 WHERE workflow_id=$1`, w.ID.String()); err != nil {
				t.Fatal(err)
			}
			if result, err = f.store.OpenRescanFoundRounds(f.ctx, now.Add(2*time.Minute), 10); err != nil || result.Refused != 1 {
				t.Fatalf("new generation: result=%+v err=%v", result, err)
			}
		})
	}
}
