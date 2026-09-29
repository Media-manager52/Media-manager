package postgres

import (
	"testing"
	"time"
	"uuid"
)

// TestSeerrRevocationReachesEpisodeWorkflowsOfOwnedSeasons proves a revoked
// request stops the single-episode workflows dispatched from a season it owns:
// work that has not retrieved anything is cancelled, work that already started
// retrieval needs owner attention, and Plex-confirmed episodes are untouched.
// A season another approved request still tracks keeps its episodes.
func TestSeerrRevocationReachesEpisodeWorkflowsOfOwnedSeasons(t *testing.T) {
	fixture := newEpisodeStatusFixture(t)
	now := time.Now().UTC()
	seasonOne := fixture.seasonWorkflow(t, 1, 1, 2, 3)
	waitingEpisode := fixture.episodeWorkflow(t, 1, 1, "waiting", now)
	queuedEpisode := fixture.episodeWorkflow(t, 1, 2, "queued", now)
	confirmedEpisode := fixture.episodeWorkflow(t, 1, 3, "plex_confirmed", now)

	// Season 2 is also tracked by a second approved request.
	seasonTwo := fixture.seasonWorkflow(t, 2, 1)
	sharedEpisode := fixture.episodeWorkflow(t, 2, 1, "searching", now)
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO seerr_request_snapshots(seerr_instance_id,request_id,external_updated_at,state,identities,payload_sha256,revision,reconciled_at) VALUES($1,'311',$2,'2','[]',$3,1,$2)`, fixture.seerrID, now, []byte{4}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO seerr_request_workflows(seerr_instance_id,request_id,workflow_id,attached_snapshot_revision,owns_workflow) VALUES($1,'311',$2,1,false)`, fixture.seerrID, seasonTwo.String()); err != nil {
		t.Fatal(err)
	}

	needsAttention, err := fixture.store.ReconcileSeerrRevocation(fixture.ctx, fixture.seerrID, fixture.requestID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !needsAttention {
		t.Fatal("revocation with a retrieving episode must require owner attention")
	}

	want := map[uuid.UUID]struct {
		state    string
		code     string
		terminal bool
	}{
		seasonOne:        {"cancelled", "", true},
		waitingEpisode:   {"cancelled", "", true},
		queuedEpisode:    {"needs_attention", "seerr_request_revoked", false},
		confirmedEpisode: {"plex_confirmed", "", true},
		seasonTwo:        {"waiting", "", false},
		sharedEpisode:    {"searching", "", false},
	}
	for workflowID, expected := range want {
		var state, code string
		var terminal bool
		if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,COALESCE(attention_code,''),terminal_at IS NOT NULL FROM workflows WHERE id=$1`, workflowID.String()).Scan(&state, &code, &terminal); err != nil {
			t.Fatal(err)
		}
		if state != expected.state || code != expected.code || terminal != expected.terminal {
			t.Fatalf("workflow %s state=%q code=%q terminal=%v, want %+v", workflowID, state, code, terminal, expected)
		}
	}
	var transitions int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM workflow_transitions WHERE workflow_id=ANY($1::uuid[]) AND actor='seerr'`, []string{waitingEpisode.String(), queuedEpisode.String()}).Scan(&transitions); err != nil {
		t.Fatal(err)
	}
	if transitions != 2 {
		t.Fatalf("episode revocation transitions=%d, want 2", transitions)
	}

	// Replaying the revocation is a no-op for everything already settled.
	if _, err := fixture.store.ReconcileSeerrRevocation(fixture.ctx, fixture.seerrID, fixture.requestID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var attentionState string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, queuedEpisode.String()).Scan(&attentionState); err != nil {
		t.Fatal(err)
	}
	if attentionState != "needs_attention" {
		t.Fatalf("replayed revocation moved the attention episode to %q", attentionState)
	}
}
