package postgres

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
)

func TestConfiguredRoundBudgetAndRetainedOutputLimit(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	if err := f.store.ConfigureAcquisitionLimits(AcquisitionLimits{4, 8}); err != nil {
		t.Fatal(err)
	}
	w := f.newMovieWorkflow(t, "Bounded configured attempts", "943001")
	for n := 1; n <= 8; n++ {
		if n == 5 {
			seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "new-round-candidate", 100, time.Now().UTC())
			if err := f.store.OpenKnownAcquisitionRound(f.ctx, w.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
		r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, fmt.Sprintf("distinct-%d", n))
		if allowed, err := f.store.PrepareReleaseRetrieval(f.ctx, r, time.Now().UTC()); err != nil || !allowed {
			t.Fatalf("attempt %d admission: %v %v", n, allowed, err)
		}
		intent, err := f.store.BeginAltMountSubmission(f.ctx, r, testNZBPackageIdentity(byte(n)), time.Now().UTC())
		if err != nil || !intent.ShouldSubmit {
			t.Fatalf("attempt %d submission: %+v %v", n, intent, err)
		}
		job, _, _ := seedSubmittedSubtitleOutput(t, f.ctx, f.store, w.ID, r.CandidateID, intent.JobID, fmt.Sprintf("retained-%d", n))
		if err := f.store.CompleteReleaseFailure(f.ctx, job, "output_empty", "no playable output", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		current, err := f.store.GetWorkflow(f.ctx, w.ID)
		if err != nil || current.State != workflows.StateWaiting {
			t.Fatalf("attempt %d state: %+v %v", n, current, err)
		}
		var state string
		if err := f.store.Pool.QueryRow(f.ctx, `SELECT state FROM acquisition_rounds WHERE workflow_id=$1 ORDER BY round_ordinal DESC LIMIT 1`, w.ID.String()).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if (state == "exhausted") != (n%4 == 0) {
			t.Fatalf("round exhausted at attempt %d: %s", n, state)
		}
	}
	seedEligibleCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "ninth", 100, time.Now().UTC())
	if err := f.store.OpenKnownAcquisitionRound(f.ctx, w.ID, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("retention limit not enforced: %v", err)
	}
	for _, test := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, 8},
		{`SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1`, 8},
		{`SELECT count(*) FROM workflow_package_exclusions WHERE workflow_id=$1`, 8},
		{`SELECT count(*) FROM altmount_output_inventories i JOIN altmount_jobs j ON j.id=i.job_id WHERE j.workflow_id=$1`, 8},
		{`SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND attempt_limit_snapshot=4`, 2},
	} {
		if got := f.count(t, test.query, w.ID.String()); got != test.want {
			t.Fatalf("%s: %d want %d", test.query, got, test.want)
		}
	}
}

func TestConfiguredLimitsDoNotRewriteExistingRound(t *testing.T) {
	f := newFailureRecoveryFixture(t, 42)
	w := f.newMovieWorkflow(t, "Immutable attempt snapshot", "943002")
	if err := Migrate(f.ctx, f.store.Pool); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ConfigureAcquisitionLimits(AcquisitionLimits{4, 8}); err != nil {
		t.Fatal(err)
	}
	if got := f.count(t, `SELECT attempt_limit_snapshot FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String()); got != 2 {
		t.Fatalf("existing snapshot rewritten: %d", got)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE acquisition_rounds SET attempt_limit_snapshot=4 WHERE workflow_id=$1`, w.ID.String()); err == nil {
		t.Fatal("immutable snapshot trigger was weakened")
	}
}
