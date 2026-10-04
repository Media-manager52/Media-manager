package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/workflows"
)

func seedOwnerDiscovery(t *testing.T, f *failureRecoveryFixture, workflow workflows.Workflow) string {
	t.Helper()
	var round string
	if err := f.store.Pool.QueryRow(f.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test exhausted' WHERE workflow_id=$1 AND state='open' RETURNING id::text`, workflow.ID.String()).Scan(&round); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TransitionWorkflow(f.ctx, workflow.ID, workflow.Revision, workflows.StateWaiting, "test exhausted", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,next_rescan_at,complete_no_new_scans,pending_indexer_ids,created_at,updated_at)
 VALUES($1,$2,'waiting',5,now()+interval '1 day',7,ARRAY[$3::uuid],now(),now())`, workflow.ID.String(), round, f.indexerTwo.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
 VALUES($1,'workflow_retry_discovery',$2,5,'workflow.discovery_rescan_requested','{}',now(),now()+interval '1 day')`, uuid.NewV7().String(), workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	return round
}

func TestOwnerDiscoveryAdvancesScheduleWithoutOpeningRound(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Owner Discovery", "902001")
	seedOwnerDiscovery(t, f, w)
	before, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 2; i++ {
		if err := f.store.RequestWorkflowDiscovery(f.ctx, w.ID, now); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.Revision != before.Revision || after.SelectedRelease != nil || after.GrabAttemptID != nil {
		t.Fatalf("discovery changed acquisition state: before=%+v after=%+v", before, after)
	}
	var generation, complete int64
	var pending []string
	var next time.Time
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT rescan_generation,complete_no_new_scans,pending_indexer_ids::text[],next_rescan_at FROM workflow_retry_discovery WHERE workflow_id=$1`, w.ID.String()).Scan(&generation, &complete, &pending, &next); err != nil {
		t.Fatal(err)
	}
	if generation != 6 || complete != 7 || len(pending) != 1 || pending[0] != f.indexerTwo.ID.String() || !next.Equal(now) {
		t.Fatalf("generation=%d complete=%d pending=%v next=%s", generation, complete, pending, next)
	}
	if err := f.store.ValidateRecoveryDiscovery(f.ctx, w.ID, 5); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("old generation remained valid: %v", err)
	}
	if err := f.store.ValidateRecoveryDiscovery(f.ctx, w.ID, 6); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`} {
		if n := f.count(t, q, w.ID.String()); n != 0 {
			t.Fatalf("discovery started acquisition: %d", n)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND aggregate_revision=6 AND completed_at IS NULL`, w.ID.String()); n != 1 {
		t.Fatalf("due events=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.discovery_requested'`, w.ID.String()); n != 1 {
		t.Fatalf("audit events=%d", n)
	}
}

func TestOwnerDiscoveryRejectsUnsafeStates(t *testing.T) {
	for _, name := range []string{"monitored", "open round", "active job", "ready discovery", "no commissioned indexer", "stale discovery round"} {
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Discovery Guard", "902002")
			round := seedOwnerDiscovery(t, f, w)
			switch name {
			case "monitored":
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state='monitored' WHERE id=$1`, w.ID.String())
				if err != nil {
					t.Fatal(err)
				}
			case "open round":
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE acquisition_rounds SET state='open',exhausted_at=NULL,closed_at=NULL,close_reason=NULL WHERE id=$1`, round)
				if err != nil {
					t.Fatal(err)
				}
			case "active job":
				candidate := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "active-discovery", 100, time.Now().UTC())
				_, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot,nzb_package_sha256)
    VALUES($1,$2,$3,$4,1,'test.nzb','processing',now(),1,$5,1,$6)`, uuid.NewV7().String(), w.ID.String(), candidate.String(), f.altMount.ID.String(), round, make([]byte, 32))
				if err != nil {
					t.Fatal(err)
				}
			case "ready discovery":
				candidate := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "ready-discovery", 100, time.Now().UTC())
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE workflow_retry_discovery SET state='ready',next_rescan_at=NULL,ready_candidate_id=$2,ready_first_seen_sequence=(SELECT first_seen_sequence FROM release_candidates WHERE id=$2) WHERE workflow_id=$1`, w.ID.String(), candidate.String())
				if err != nil {
					t.Fatal(err)
				}
			case "no commissioned indexer":
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE integration_instances SET enabled=false WHERE kind='newznab'`)
				if err != nil {
					t.Fatal(err)
				}
			case "stale discovery round":
				_, err := f.store.Pool.Exec(f.ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at,closed_at,close_reason)
    SELECT $1,workflow_id,2,'cancelled',attempt_limit_snapshot,0,policy_version,policy_snapshot,upgrade_baseline_link_versions,now(),now(),'test newer round' FROM acquisition_rounds WHERE id=$2`, uuid.NewV7().String(), round)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.RequestWorkflowDiscovery(f.ctx, w.ID, time.Now().UTC()); err == nil {
				t.Fatal("unsafe discovery accepted")
			}
			if n := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.discovery_requested'`, w.ID.String()); n != 0 {
				t.Fatalf("failed request wrote audit %d", n)
			}
			var generation int64
			if err := f.store.Pool.QueryRow(f.ctx, `SELECT rescan_generation FROM workflow_retry_discovery WHERE workflow_id=$1`, w.ID.String()).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			if generation != 5 {
				t.Fatalf("failed request changed generation %d", generation)
			}
		})
	}
}

func TestSearchRetiresDiscoveryWhenCurrentPolicyRoundIsOpen(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Restored Policy Search", "902003")
	old := seedOwnerDiscovery(t, f, w)
	open := uuid.NewV7()
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
 SELECT $1,workflow_id,2,'open',attempt_limit_snapshot,0,policy_version,policy_snapshot,upgrade_baseline_link_versions,now() FROM acquisition_rounds WHERE id=$2`, open.String(), old); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ValidateRecoveryDiscovery(f.ctx, w.ID, 5); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("old discovery valid alongside open round: %v", err)
	}
	if err := f.store.RequestWorkflowSearch(f.ctx, w.ID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != workflows.StateSearching {
		t.Fatalf("state=%s", got.State)
	}
	var discovery, roundState string
	var generation int64
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT state,rescan_generation FROM workflow_retry_discovery WHERE workflow_id=$1`, w.ID.String()).Scan(&discovery, &generation); err != nil {
		t.Fatal(err)
	}
	if discovery != "paused" || generation != 6 {
		t.Fatalf("discovery=%s generation=%d", discovery, generation)
	}
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT state FROM acquisition_rounds WHERE id=$1`, open.String()).Scan(&roundState); err != nil {
		t.Fatal(err)
	}
	if roundState != "open" {
		t.Fatalf("current round=%s", roundState)
	}
}

func TestSearchDoesNotUseStaleDiscoveryToBypassPolicyOrActiveJob(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed policy", true: "active job"}[active], func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Stale Discovery Guard", "902004")
			old := seedOwnerDiscovery(t, f, w)
			open := uuid.NewV7()
			if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
   SELECT $1,workflow_id,2,'open',attempt_limit_snapshot,0,policy_version,policy_snapshot,upgrade_baseline_link_versions,now() FROM acquisition_rounds WHERE id=$2`, open.String(), old); err != nil {
				t.Fatal(err)
			}
			if active {
				candidate := seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "stale-active-job", 100, time.Now().UTC())
				if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot,nzb_package_sha256)
    VALUES($1,$2,$3,$4,1,'test.nzb','processing',now(),1,$5,1,$6)`, uuid.NewV7().String(), w.ID.String(), candidate.String(), f.altMount.ID.String(), open.String(), make([]byte, 32)); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy_version=policy_version+1 WHERE id=$1`, f.profile.ID.String()); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.RequestWorkflowSearch(f.ctx, w.ID, "", time.Now().UTC()); err == nil {
				t.Fatal("unsafe search accepted")
			}
			var discovery string
			if err := f.store.Pool.QueryRow(f.ctx, `SELECT state FROM workflow_retry_discovery WHERE workflow_id=$1`, w.ID.String()).Scan(&discovery); err != nil {
				t.Fatal(err)
			}
			if discovery != "waiting" {
				t.Fatalf("failed request mutated discovery %s", discovery)
			}
		})
	}
}
