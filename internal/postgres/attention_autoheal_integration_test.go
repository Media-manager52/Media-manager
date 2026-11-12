package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestStuckAttentionIsResolvedWithoutOwner proves the sweeps hand attention
// that needs no owner decision back to the automatic flow: a round with no
// untried release waits for a new one, an uninspectable download is set aside
// for the next release, and a timed-out Plex confirmation is asked again once
// its pause has passed. An indexer access rejection still stays with the owner.
func TestStuckAttentionIsResolvedWithoutOwner(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()

	openRound := func(workflowID uuid.UUID) string {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,opened_at)
			SELECT $1,$2,1,'open',3,0,profile.policy_version,profile.policy,now() FROM workflows workflow JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id WHERE workflow.id=$2
			ON CONFLICT DO NOTHING`, uuid.NewV7().String(), workflowID.String()); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, workflowID.String()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	attention := func(workflowID uuid.UUID, code, detail string) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='needs_attention',attention_code=$2,attention_detail=$3,revision=revision+1,updated_at=now() WHERE id=$1`, workflowID.String(), code, detail); err != nil {
			t.Fatal(err)
		}
	}
	stateOf := func(workflowID uuid.UUID) (string, string) {
		t.Helper()
		var state, code string
		if err := store.Pool.QueryRow(ctx, `SELECT state,COALESCE(attention_code,'') FROM workflows WHERE id=$1`, workflowID.String()).Scan(&state, &code); err != nil {
			t.Fatal(err)
		}
		return state, code
	}

	// Round dead ends.
	_, duplicate := fixture.movieWorkflow(t, "Heal Duplicate", "593001")
	duplicateRound := openRound(duplicate)
	attention(duplicate, "duplicate_package_no_alternate", "The next eligible release resolved to an exact package already submitted in this workflow, and no unexcluded eligible candidate remains.")
	_, denied := fixture.movieWorkflow(t, "Heal Access Denied", "593002")
	deniedRound := openRound(denied)
	candidate := seedEligibleCandidate(t, ctx, store, denied, fixture.indexer.ID, "heal-denied", 10, now)
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,verification_id,failure_kind,reason,created_at,retrieval_round_id,retrieval_error_code)
		VALUES($1,$2,$3,'heal-denied',NULL,'release_retrieval','denied',now(),$4,'newznab_http_403')`, denied.String(), candidate.String(), fixture.indexer.ID.String(), deniedRound); err != nil {
		t.Fatal(err)
	}
	attention(denied, "release_retrieval_failed", "selected release NZB access was rejected")

	resumed, err := store.ResumeRoundDeadEnds(ctx, now, 10)
	if err != nil || resumed != 1 {
		t.Fatalf("dead ends resumed=%d err=%v, want 1", resumed, err)
	}
	if state, code := stateOf(duplicate); state != "waiting" || code != workflows.AttentionRoundExhaustedWaitingNewIdentity {
		t.Fatalf("duplicate dead end state=%q code=%q, want waiting for a new release", state, code)
	}
	var roundState string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM acquisition_rounds WHERE id=$1`, duplicateRound).Scan(&roundState); err != nil || roundState != "exhausted" {
		t.Fatalf("dead-end round state=%q err=%v, want exhausted", roundState, err)
	}
	var rescans int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.discovery_rescan_requested'`, duplicate.String()).Scan(&rescans); err != nil || rescans != 1 {
		t.Fatalf("discovery rescans=%d err=%v, want 1", rescans, err)
	}
	if state, code := stateOf(denied); state != "needs_attention" || code != "release_retrieval_failed" {
		t.Fatalf("access rejection state=%q code=%q, want it left with the owner", state, code)
	}
	if again, err := store.ResumeRoundDeadEnds(ctx, now, 10); err != nil || again != 0 {
		t.Fatalf("second sweep resumed=%d err=%v, want 0", again, err)
	}

	// An uninspectable download is set aside for the next release.
	_, uninspectable := fixture.movieWorkflow(t, "Heal Inspection", "593003")
	job, _, _ := seedSubtitleAttempt(t, ctx, store, uninspectable, fixture.altMount.ID, fixture.indexer.ID, 1, "heal-inspection")
	if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',error_code='output_inspection_failed' WHERE id=$1`, job.JobID.String()); err != nil {
		t.Fatal(err)
	}
	attention(uninspectable, "output_inspection_failed", "bounded media-info inspection failed for Movie.mkv: media-info track codec is unavailable")
	moved, err := store.ResumeInspectionFailures(ctx, now, 10)
	if err != nil || moved != 1 {
		t.Fatalf("inspection failures moved=%d err=%v, want 1", moved, err)
	}
	if state, code := stateOf(uninspectable); state != "waiting" || code == "output_inspection_failed" {
		t.Fatalf("inspection failure state=%q code=%q, want waiting for the next release", state, code)
	}
	var kind string
	if err := store.Pool.QueryRow(ctx, `SELECT failure_kind FROM workflow_release_exclusions WHERE workflow_id=$1 AND candidate_id=$2`, uninspectable.String(), job.CandidateID.String()).Scan(&kind); err != nil || kind != InspectionFailedCode {
		t.Fatalf("inspection exclusion kind=%q err=%v", kind, err)
	}
	var links int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM library_link_versions WHERE workflow_id=$1`, uninspectable.String()).Scan(&links); err != nil || links != 0 {
		t.Fatalf("uninspected release links=%d err=%v, want nothing published", links, err)
	}

	// A timed-out Plex confirmation is asked again after its pause.
	timedOut, _, _, _ := publishTimedOutMovie(t, fixture, "Heal Plex Timeout", "593004", "heal-plex")
	var timedOutAt time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT completed_at FROM plex_scan_requests WHERE workflow_id=$1 AND state='timed_out'`, timedOut.String()).Scan(&timedOutAt); err != nil {
		t.Fatal(err)
	}
	if queued, err := store.AutoReconfirmTimedOutPlex(ctx, timedOutAt.Add(59*time.Minute), 10); err != nil || queued != 0 {
		t.Fatalf("early reconfirm queued=%d err=%v, want 0 before the pause", queued, err)
	}
	if queued, err := store.AutoReconfirmTimedOutPlex(ctx, timedOutAt.Add(61*time.Minute), 10); err != nil || queued != 1 {
		t.Fatalf("reconfirm after pause queued=%d err=%v, want 1", queued, err)
	}
	if state, _ := stateOf(timedOut); state != "plex_scanning" {
		t.Fatalf("timed-out workflow state=%q, want plex_scanning", state)
	}
}

func TestPlexAutoReconfirmPausesGrow(t *testing.T) {
	for scans, want := range map[int]time.Duration{0: time.Hour, 1: time.Hour, 2: 6 * time.Hour, 3: 24 * time.Hour, 9: 24 * time.Hour} {
		if got := plexAutoReconfirmPause(scans); got != want {
			t.Fatalf("pause after %d scans=%v, want %v", scans, got, want)
		}
	}
}
