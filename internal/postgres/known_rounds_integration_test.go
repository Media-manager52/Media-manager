package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

func exhaustForKnownRound(t *testing.T, fixture *failureRecoveryFixture, id uuid.UUID, revision uint64) {
	t.Helper()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='test' WHERE workflow_id=$1 AND state='open'`, id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.TransitionWorkflow(fixture.ctx, id, revision, workflows.StateWaiting, "test known round", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func renameKnownCandidate(t *testing.T, fixture *failureRecoveryFixture, candidate uuid.UUID, title string) {
	t.Helper()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE release_observations SET title=$2 WHERE id=(SELECT observation_id FROM release_candidates WHERE id=$1)`, candidate.String(), title); err != nil {
		t.Fatal(err)
	}
}

// A release approved before a parser fix, or rejected since, must not take the
// first slot of a new round.
func TestOpenKnownAcquisitionRoundSkipsOutdatedApprovals(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Outdated Approvals", "900950")
	exhaustForKnownRound(t, fixture, workflow.ID, workflow.Revision)
	now := time.Now().UTC()

	chain := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "known-chain", 900, now)
	renameKnownCandidate(t, fixture, chain, "Show.S03E20E21.Two.Stories.1080p.WEB-DL-GROUP")
	rejected := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexerTwo.ID, "known-rejected-later", 800, now.Add(-time.Hour))
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) SELECT workflow_id,candidate_id,policy_version+1,'rejected',0,'[]'::jsonb,'test',$2 FROM candidate_decisions WHERE candidate_id=$1`, rejected.String(), now); err != nil {
		t.Fatal(err)
	}
	current := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "known-current", 100, now)

	if err := fixture.store.OpenKnownAcquisitionRound(fixture.ctx, workflow.ID, now); err != nil {
		t.Fatal(err)
	}
	var selected string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT COALESCE(selected_release_id::text,'') FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if selected != current.String() {
		t.Fatalf("selected=%s, want the still-current release %s (chain %s, rejected %s)", selected, current, chain, rejected)
	}
}

func TestOpenKnownAcquisitionRoundRefusesWhenOnlyOutdatedApprovalsRemain(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Only Outdated", "900951")
	exhaustForKnownRound(t, fixture, workflow.ID, workflow.Revision)
	now := time.Now().UTC()
	chain := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "only-chain", 900, now)
	renameKnownCandidate(t, fixture, chain, "Show.S03E20-E21.Two.Stories.1080p.WEB-DL-GROUP")

	err := fixture.store.OpenKnownAcquisitionRound(fixture.ctx, workflow.ID, now)
	var refusal ActionRefusal
	if !errors.As(err, &refusal) || refusal.Code != "no_known_eligible_release" {
		t.Fatalf("err=%v, want no_known_eligible_release", err)
	}
}
