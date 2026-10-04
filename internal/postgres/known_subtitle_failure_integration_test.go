package postgres

import (
	"testing"
	"time"
	"uuid"
)

func seedKnownSubtitleFailure(t *testing.T, f *failureRecoveryFixture) (uuid.UUID, uuid.UUID) {
	t.Helper()
	requireDanishForRecovery(t, f)
	w := f.newMovieWorkflow(t, "Movie", "915001")
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "known-failed")
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE release_candidates SET title='Film.2026.1080p.WEB-GROUP' WHERE id=$1`, r.CandidateID.String()); err != nil {
		t.Fatal(err)
	}
	intent, err := f.store.BeginAltMountSubmission(f.ctx, r, testNZBPackageIdentity(101), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	job, inventory, assets := seedSubmittedSubtitleOutput(t, f.ctx, f.store, w.ID, r.CandidateID, intent.JobID, "known-failed")
	if _, err := f.store.RecordSubtitleVerification(f.ctx, job, inventory, unverifiedSubtitleResult(assets[0], "no verified da subtitle"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	duplicate := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexerTwo.ID, "crosspost", 999999, time.Now().UTC())
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE release_candidates SET title=' film.2026.1080p.web-group ' WHERE id=$1`, duplicate.String()); err != nil {
		t.Fatal(err)
	}
	return w.ID, duplicate
}

func TestKnownSubtitleFailureGuardsPersistedSelectionAndRetrieval(t *testing.T) {
	for _, name := range []string{"known round", "retrieval admission", "retrieval alternate", "cross-post switch", "ready round"} {
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			wid, duplicate := seedKnownSubtitleFailure(t, f)
			different := seedEligibleCandidate(t, f.ctx, f.store, wid, f.indexerTwo.ID, "different-encode", 1, time.Now().UTC())
			switch name {
			case "known round":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now() WHERE workflow_id=$1 AND state='open'`, wid.String()); err != nil {
					t.Fatal(err)
				}
				if err := f.store.OpenKnownAcquisitionRound(f.ctx, wid, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if got := recoverySelected(t, f, wid).CandidateID; got != different {
					t.Fatalf("selected %s want distinct encode %s", got, different)
				}
			case "retrieval admission":
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1 WHERE id=$1`, wid.String(), duplicate.String()); err != nil {
					t.Fatal(err)
				}
				r, err := f.store.LoadRetrievalContext(f.ctx, wid, duplicate)
				if err != nil {
					t.Fatal(err)
				}
				allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC())
				if err != nil || allowed {
					t.Fatalf("duplicate admission=%v err=%v", allowed, err)
				}
				if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, wid.String()); got != 1 {
					t.Fatalf("new job created: %d", got)
				}
			case "retrieval alternate":
				r := seedRetrievalCandidate(t, f.ctx, f.store, wid, f.indexer.ID, "bad-nzb")
				if err := f.store.RecordReleaseRetrievalFailure(f.ctx, r, "newznab_invalid_nzb", "invalid NZB", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if got := recoverySelected(t, f, wid).CandidateID; got != different {
					t.Fatalf("selected %s want distinct encode %s", got, different)
				}
			case "cross-post switch":
				r := seedRetrievalCandidate(t, f.ctx, f.store, wid, f.indexer.ID, "budget-current")
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE release_candidates SET title='Film.2026.1080p.WEB-GROUP' WHERE id=$1`, r.CandidateID.String()); err != nil {
					t.Fatal(err)
				}
				changed, err := f.store.SelectCrossPostWithGrabBudget(f.ctx, r, time.Now().UTC())
				if err != nil || changed {
					t.Fatalf("duplicate budget switch=%v err=%v", changed, err)
				}
			case "ready round":
				var round string
				if err := f.store.Pool.QueryRow(f.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now() WHERE workflow_id=$1 AND state='open' RETURNING id::text`, wid.String()).Scan(&round); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,ready_candidate_id,ready_first_seen_sequence,created_at,updated_at) SELECT $1,$2,'ready',1,id,first_seen_sequence,now(),now() FROM release_candidates WHERE id=$3`, wid.String(), round, duplicate.String()); err != nil {
					t.Fatal(err)
				}
				if err := f.store.OpenNextAcquisitionRound(f.ctx, wid, time.Now().UTC()); err == nil {
					t.Fatal("duplicate ready release reopened a round")
				}
			}
			context, err := f.store.LoadSearchContext(f.ctx, wid)
			if err != nil {
				t.Fatal(err)
			}
			if len(context.Exclusions) == 0 || !context.Exclusions[0].MatchesTitle("Film.2026.1080p.WEB-GROUP") {
				t.Fatalf("missing completed cross-post evidence: %+v", context.Exclusions)
			}
		})
	}
}
