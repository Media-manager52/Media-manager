package postgres

import (
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/workflows"
)

func requireDanishForRecovery(t *testing.T, f *failureRecoveryFixture) {
	t.Helper()
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy=policy||'{"required_subtitle_languages":["da"]}'::jsonb WHERE id=$1`, f.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
}

func recoverySelected(t *testing.T, f *failureRecoveryFixture, workflowID uuid.UUID) RetrievalContext {
	t.Helper()
	w, err := f.store.GetWorkflow(f.ctx, workflowID)
	if err != nil || w.SelectedRelease == nil || w.State != workflows.StateCandidateSelected {
		t.Fatalf("selected workflow=%+v err=%v", w, err)
	}
	r, err := f.store.LoadRetrievalContext(f.ctx, workflowID, *w.SelectedRelease)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRetrievalRecoveryUsesRemainingSlotAndBestDanishEvidence(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	requireDanishForRecovery(t, f)
	w := f.newMovieWorkflow(t, "Retrieval recovery", "910001")
	first := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "first-output")
	intent, err := f.store.BeginAltMountSubmission(f.ctx, first, testNZBPackageIdentity(91), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	job, inventory, _ := seedSubmittedSubtitleOutput(t, f.ctx, f.store, w.ID, first.CandidateID, intent.JobID, "first-output")
	if err := f.store.CompleteReleaseFailure(f.ctx, job, "output_empty", "no playable output", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "invalid-nzb")
	seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "unknown-high-score", 999999, time.Now().UTC())
	seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "NORDiC", 99999, time.Now().UTC())
	danish := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "danish-facts", 1, time.Now().UTC())
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE release_observations SET normalized_facts='[{"name":"subtitle_languages","values":["da"],"state":"observed"}]' WHERE id=(SELECT observation_id FROM release_candidates WHERE id=$1)`, danish.String()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, "newznab_invalid_nzb", "invalid NZB after bounded requests", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next := recoverySelected(t, f, w.ID)
	if next.CandidateID != danish || next.Workflow.Revision != r.Workflow.Revision+1 {
		t.Fatalf("wrong alternate/revision: %+v", next)
	}
	// Re-delivery of the old event must not exclude or move the new selection.
	if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, "newznab_invalid_nzb", "duplicate delivery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC()); err != nil || allowed {
		t.Fatalf("stale admission=%v err=%v", allowed, err)
	}
	if got := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`, w.ID.String()); got != 1 {
		t.Fatalf("alternate events=%d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE retrieval_round_id IS NOT NULL AND workflow_id=$1`, w.ID.String()); got != 1 {
		t.Fatalf("retrieval failures=%d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_output_inventories WHERE id=$1`, inventory.String()); got != 1 {
		t.Fatal("failed output lost")
	}
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, next, time.Now().UTC()); err != nil || !allowed {
		t.Fatalf("next admission=%v err=%v", allowed, err)
	}
	second, err := f.store.BeginAltMountSubmission(f.ctx, next, testNZBPackageIdentity(92), time.Now().UTC())
	if err != nil || !second.ShouldSubmit {
		t.Fatalf("remaining slot unavailable: %+v %v", second, err)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, w.ID.String()); got != 2 {
		t.Fatalf("jobs=%d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String()); got != 1 {
		t.Fatalf("rounds=%d", got)
	}
}

func TestRetrievalRecoveryBoundsFailuresAndSkipsDeniedProvider(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	requireDanishForRecovery(t, f)
	w := f.newMovieWorkflow(t, "Denied provider recovery", "910002")
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "denied")
	seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "same-provider-high-score", 999999, time.Now().UTC())
	for n := 0; n < 7; n++ {
		seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, fmt.Sprintf("spare-%d", n), 100-n, time.Now().UTC())
	}
	nordic := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "NORDiC", 1, time.Now().UTC())
	for n := 1; n <= 4; n++ {
		code := "newznab_invalid_nzb"
		if n == 1 {
			code = "newznab_http_403"
		}
		if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, code, "bounded test failure", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if n < 4 {
			r = recoverySelected(t, f, w.ID)
			if r.Indexer.ID != f.indexerTwo.ID {
				t.Fatal("returned to access-denied provider")
			}
			if n == 1 && r.CandidateID != nordic {
				t.Fatal("did not prefer Nordic clue over unknown evidence")
			}
		}
	}
	current, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil || current.State != workflows.StateNeedsAttention {
		t.Fatalf("unbounded recovery: %+v %v", current, err)
	}
	var code string
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT attention_code FROM workflows WHERE id=$1`, w.ID.String()).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "release_retrieval_limit" {
		t.Fatalf("attention=%s", code)
	}
	if got := f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND retrieval_round_id IS NOT NULL`, w.ID.String()); got != 4 {
		t.Fatalf("failures=%d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, w.ID.String()); got != 0 {
		t.Fatalf("retrieval consumed %d jobs", got)
	}
	if got := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`, w.ID.String()); got != 3 {
		t.Fatalf("queued %d alternatives", got)
	}
	if got := f.count(t, `SELECT count(*) FROM integration_instances WHERE id=$1 AND enabled`, f.indexer.ID.String()); got != 1 {
		t.Fatal("provider disabled globally")
	}
}

func TestRetrievalAdmissionStopsClosedRoundOrChangedPolicy(t *testing.T) {
	for _, change := range []string{"closed", "policy"} {
		t.Run(change, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Admission "+change, "910003")
			r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "selected")
			seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "alternate", 1, time.Now().UTC())
			query := `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now() WHERE workflow_id=$1`
			if change == "policy" {
				query = `UPDATE quality_profiles SET policy_version=policy_version+1 WHERE id=(SELECT quality_profile_id FROM workflows WHERE id=$1)`
			}
			if _, err := f.store.Pool.Exec(f.ctx, query, w.ID.String()); err != nil {
				t.Fatal(err)
			}
			if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC()); err != nil || allowed {
				t.Fatalf("admission=%v err=%v", allowed, err)
			}
			if got := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String()); got != 1 {
				t.Fatal("opened a round")
			}
			if got := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`, w.ID.String()); got != 0 {
				t.Fatal("queued blocked retrieval")
			}
		})
	}
}

func TestRetrievalAdmissionChecksRetainedOutputsBeforeGrab(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Retained output cap", "910004")
	var first string
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String()).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE acquisition_rounds SET state='exhausted' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	second := uuid.NewV7().String()
	for n, id := range []string{second, uuid.NewV7().String()} {
		state := "exhausted"
		if n == 1 {
			state = "open"
		}
		if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,opened_at) SELECT $1,workflow_id,$2,$3,2,0,policy_version,policy_snapshot,now() FROM acquisition_rounds WHERE id=$4`, id, n+2, state, first); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 4; n++ {
		key := fmt.Sprintf("retained-%d", n)
		c := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, key, 1, time.Now().UTC())
		round := first
		if n >= 2 {
			round = second
		}
		job := uuid.NewV7()
		if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot,acquisition_outcome) VALUES($1,$2,$3,$4,$5,$6,'needs_attention',now(),1,$7,$8,'output_empty')`, job.String(), w.ID.String(), c.String(), f.altMount.ID.String(), n+1, key+".nzb", round, n%2+1); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,1,$3,$4,now(),now())`, uuid.NewV7().String(), job.String(), key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "next")
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC()); err != nil || allowed {
		t.Fatalf("retained output cap admission=%v err=%v", allowed, err)
	}
	var code string
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT attention_code FROM workflows WHERE id=$1`, w.ID.String()).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "retained_failed_output_limit" {
		t.Fatalf("code=%s", code)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_output_inventories i JOIN altmount_jobs j ON j.id=i.job_id WHERE j.workflow_id=$1`, w.ID.String()); got != 4 {
		t.Fatal("retained files changed")
	}
}

func TestRetrievalRecoveryRejectsStaleAndDisabledAlternatives(t *testing.T) {
	for _, condition := range []string{"disabled", "policy", "native_block", "excluded", "attempted"} {
		t.Run(condition, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Alternative guards", "910005")
			bad := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "ineligible-alternate", 999, time.Now().UTC())
			good := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "eligible-alternate", 1, time.Now().UTC())
			r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "invalid")
			switch condition {
			case "disabled":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE integration_instances SET enabled=false WHERE id=$1`, f.indexerTwo.ID.String()); err != nil {
					t.Fatal(err)
				}
			case "policy":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE candidate_decisions SET policy_version=2 WHERE candidate_id=$1`, bad.String()); err != nil {
					t.Fatal(err)
				}
			case "native_block":
				scope := "native_release"
				if _, err := f.store.CreateReleaseBlockFromCandidate(f.ctx, bad, scope, "owner block", nil, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "excluded":
				if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,failure_kind,reason,created_at) SELECT workflow_id,id,indexer_instance_id,native_key,'release_retrieval','old failure',now() FROM release_candidates WHERE id=$1`, bad.String()); err != nil {
					t.Fatal(err)
				}
			case "attempted":
				// A failed submitted release remains unselectable even without an exclusion.
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET selected_release_id=$2 WHERE id=$1`, w.ID.String(), bad.String()); err != nil {
					t.Fatal(err)
				}
				old, err := f.store.LoadRetrievalContext(f.ctx, w.ID, bad)
				if err != nil {
					t.Fatal(err)
				}
				intent, err := f.store.BeginAltMountSubmission(f.ctx, old, testNZBPackageIdentity(94), time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkAltMountFailed(f.ctx, w.ID, intent.JobID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				r = seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "invalid-after-attempt")
			}
			if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, "newznab_invalid_nzb", "invalid", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			next := recoverySelected(t, f, w.ID)
			if next.CandidateID != good {
				t.Fatalf("selected unsafe alternate for %s: %+v", condition, next)
			}
		})
	}
}
