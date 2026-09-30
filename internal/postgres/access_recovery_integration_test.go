package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/workflows"
)

func seedAccessRetryDecision(t *testing.T, f *failureRecoveryFixture, r RetrievalContext) {
	t.Helper()
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,1,'eligible',1,'[]','policy',now())`, r.Workflow.ID.String(), r.CandidateID.String()); err != nil {
		t.Fatal(err)
	}
}

func TestRetryAccessRejectedRelease(t *testing.T) {
	for _, code := range []string{"newznab_http_401", "newznab_http_403", "historical_one_request", "historical_two_requests"} {
		t.Run(code, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Access retry", "920001")
			r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "access-denied")
			seedAccessRetryDecision(t, f, r)
			failureCode := code
			if code == "historical_one_request" || code == "historical_two_requests" {
				failureCode = "newznab_http_403"
			}
			if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, failureCode, "access denied", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if code == "historical_one_request" || code == "historical_two_requests" {
				reason := "selected release NZB access was rejected after one bounded request (newznab_http_403)"
				if code == "historical_two_requests" {
					reason = "selected release NZB could not be retrieved after two bounded requests (newznab_http_403)"
				}
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_release_exclusions SET retrieval_round_id=NULL,retrieval_error_code=NULL,reason=$2 WHERE workflow_id=$1`, w.ID.String(), reason); err != nil {
					t.Fatal(err)
				}
			}
			failed, err := f.store.GetWorkflow(f.ctx, w.ID)
			if err != nil {
				t.Fatal(err)
			}
			summaries, err := f.store.ListWorkflowSummaries(f.ctx, 100, false)
			if err != nil || len(summaries) != 1 || summaries[0].AccessRejectedCandidateID == nil || *summaries[0].AccessRejectedCandidateID != r.CandidateID {
				t.Fatalf("recovery not offered: %+v %v", summaries, err)
			}
			if err := f.store.RetryAccessRejectedRelease(f.ctx, w.ID, r.CandidateID, failed.Revision, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			next := recoverySelected(t, f, w.ID)
			if next.CandidateID != r.CandidateID || next.Workflow.Revision != failed.Revision+1 {
				t.Fatalf("wrong retry: %+v", next)
			}
			if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC()); err != nil || allowed {
				t.Fatalf("old event accepted: %v %v", allowed, err)
			}
			if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, next, time.Now().UTC()); err != nil || !allowed {
				t.Fatalf("retry blocked: %v %v", allowed, err)
			}
			if err := f.store.RetryAccessRejectedRelease(f.ctx, w.ID, r.CandidateID, failed.Revision, time.Now().UTC()); !errors.Is(err, ErrConcurrentModification) {
				t.Fatalf("duplicate retry: %v", err)
			}
			for _, query := range []string{
				`SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`,
				`SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.access_rejected_release_retried'`,
				`SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`,
			} {
				if got := f.count(t, query, w.ID.String()); got != 1 {
					t.Fatalf("count=%d for %s", got, query)
				}
			}
			if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, w.ID.String()); got != 0 {
				t.Fatal("retry consumed a submission slot")
			}
			// A repeated access failure must restore the same provider avoidance.
			if err := f.store.RecordReleaseRetrievalFailure(f.ctx, next, failureCode, "still denied", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			current, _ := f.store.GetWorkflow(f.ctx, w.ID)
			if current.State != workflows.StateNeedsAttention || f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`, w.ID.String()) != 1 {
				t.Fatal("access failure no longer blocks the provider")
			}
		})
	}
}

func TestRetryAccessRejectedReleaseRefusesUnsafeRecovery(t *testing.T) {
	for _, condition := range []string{"invalid_nzb", "output_failure", "wrong_revision", "wrong_candidate", "closed_round", "policy_changed", "decision_changed", "disabled", "owner_block", "pack_block", "attempted", "active_job", "publication", "other_provider_rejection", "cancelled", "revoked", "retrieval_limit", "unknown_historical", "old_round"} {
		t.Run(condition, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Unsafe access retry", "920002")
			r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "access-denied")
			seedAccessRetryDecision(t, f, r)
			code := "newznab_http_403"
			if condition == "invalid_nzb" {
				code = "newznab_invalid_nzb"
			}
			if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, code, "failed", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			failed, _ := f.store.GetWorkflow(f.ctx, w.ID)
			revision, candidate := failed.Revision, r.CandidateID
			var query string
			args := []any{w.ID.String()}
			switch condition {
			case "wrong_revision":
				revision--
			case "wrong_candidate":
				candidate = uuid.NewV7()
			case "unknown_historical":
				query = `UPDATE workflow_release_exclusions SET retrieval_round_id=NULL,retrieval_error_code=NULL,reason='Some unknown failure containing newznab_http_403' WHERE workflow_id=$1`
			case "old_round":
				query = `UPDATE workflow_release_exclusions SET retrieval_round_id=NULL,retrieval_error_code=NULL,created_at='2000-01-01',reason='selected release NZB access was rejected after one bounded request (newznab_http_403)' WHERE workflow_id=$1`
			case "output_failure":
				query = `UPDATE workflow_release_exclusions SET failure_kind='subtitle_verification',retrieval_round_id=NULL,retrieval_error_code=NULL WHERE workflow_id=$1`
			case "closed_round":
				query = `UPDATE acquisition_rounds SET state='exhausted' WHERE workflow_id=$1`
			case "policy_changed":
				query = `UPDATE quality_profiles SET policy_version=policy_version+1 WHERE id=(SELECT quality_profile_id FROM workflows WHERE id=$1)`
			case "decision_changed":
				query = `UPDATE candidate_decisions SET verdict='rejected' WHERE workflow_id=$1`
			case "disabled":
				query = `UPDATE integration_instances SET enabled=false WHERE id=(SELECT indexer_instance_id FROM release_candidates WHERE workflow_id=$1)`
			case "owner_block", "pack_block":
				scope := "native_release"
				if condition == "pack_block" {
					scope = "pack_release"
					if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET variant='season:01' WHERE id=$1`, w.ID.String()); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.store.CreateReleaseBlockFromCandidate(f.ctx, r.CandidateID, scope, "owner block", nil, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "attempted", "active_job":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state='candidate_selected' WHERE id=$1`, w.ID.String()); err != nil {
					t.Fatal(err)
				}
				selected, err := f.store.LoadRetrievalContext(f.ctx, w.ID, r.CandidateID)
				if err != nil {
					t.Fatal(err)
				}
				intent, err := f.store.BeginAltMountSubmission(f.ctx, selected, testNZBPackageIdentity(98), time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				if condition == "attempted" {
					if err := f.store.MarkAltMountFailed(f.ctx, w.ID, intent.JobID, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				}
				query = `UPDATE workflows SET state='needs_attention',attention_code='release_retrieval_failed' WHERE id=$1`
			case "publication":
				// Re-use a real publication fixture, then associate its current link with this workflow.
				_, published := f.publishedMovie(t, "Published access guard", "920003")
				query = `UPDATE library_link_versions SET workflow_id=$1 WHERE id=$2`
				args = append(args, published.String())
			case "other_provider_rejection", "retrieval_limit":
				count := 1
				if condition == "retrieval_limit" {
					count = 4
				}
				for n := 0; n < count; n++ {
					indexer := f.indexer.ID
					if condition == "retrieval_limit" {
						indexer = f.indexerTwo.ID
						code = "newznab_invalid_nzb"
					}
					c := seedEligibleCandidate(t, f.ctx, f.store, w.ID, indexer, uuid.NewV7().String(), 1, time.Now().UTC())
					if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,failure_kind,reason,created_at,retrieval_round_id,retrieval_error_code) SELECT c.workflow_id,c.id,c.indexer_instance_id,c.native_key,'release_retrieval','other failure',now(),r.id,$2 FROM release_candidates c JOIN acquisition_rounds r ON r.workflow_id=c.workflow_id AND r.state='open' WHERE c.id=$1`, c.String(), code); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled":
				query = `UPDATE workflows SET state='cancelled',terminal_at=now() WHERE id=$1`
			case "revoked":
				query = `UPDATE workflows SET attention_code='seerr_request_revoked' WHERE id=$1`
			}
			if query != "" {
				if _, err := f.store.Pool.Exec(f.ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := f.store.GetWorkflow(f.ctx, w.ID)
			exclusions := f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`, w.ID.String())
			if err := f.store.RetryAccessRejectedRelease(f.ctx, w.ID, candidate, revision, time.Now().UTC()); err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			after, _ := f.store.GetWorkflow(f.ctx, w.ID)
			if after.State != before.State || after.Revision != before.Revision || f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`, w.ID.String()) != exclusions {
				t.Fatal("refusal changed state or exclusions")
			}
			if f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`, w.ID.String()) != 0 {
				t.Fatal("refusal queued retrieval")
			}
		})
	}
}

func TestRetryAccessRejectedReleaseKeepsFailedOutputAndOtherExclusions(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Keep failed output", "920004")
	first := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "failed-output")
	intent, err := f.store.BeginAltMountSubmission(f.ctx, first, testNZBPackageIdentity(97), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	job, inventory, _ := seedSubmittedSubtitleOutput(t, f.ctx, f.store, w.ID, first.CandidateID, intent.JobID, "failed-output")
	if err := f.store.CompleteReleaseFailure(f.ctx, job, "output_empty", "no playable output", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "access-denied")
	seedAccessRetryDecision(t, f, r)
	if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, "newznab_http_403", "access denied", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	failed, _ := f.store.GetWorkflow(f.ctx, w.ID)
	if err := f.store.RetryAccessRejectedRelease(f.ctx, w.ID, r.CandidateID, failed.Revision, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next := recoverySelected(t, f, w.ID)
	if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, next, time.Now().UTC()); err != nil || !allowed {
		t.Fatalf("remaining slot blocked: %v %v", allowed, err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind='output_empty'`,
		`SELECT count(*) FROM workflow_package_exclusions WHERE workflow_id=$1`,
		`SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1`,
		`SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`,
	} {
		if got := f.count(t, query, w.ID.String()); got != 1 {
			t.Fatalf("lost failed-output guard: %s count=%d", query, got)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_output_inventories WHERE id=$1`, inventory.String()); got != 1 {
		t.Fatal("retained inventory changed")
	}
}
