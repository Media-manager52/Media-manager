package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

type policyRound struct {
	id       string
	ordinal  int
	state    string
	version  int64
	cutoff   int64
	baseline string
	reason   string
}

func workflowRounds(t *testing.T, fixture *plexPublicationFixture, workflowID uuid.UUID) []policyRound {
	t.Helper()
	rows, err := fixture.store.Pool.Query(fixture.ctx, `SELECT id::text,round_ordinal,state,policy_version,novelty_cutoff_sequence,upgrade_baseline_link_versions,COALESCE(close_reason,'') FROM acquisition_rounds WHERE workflow_id=$1 ORDER BY round_ordinal`, workflowID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []policyRound
	for rows.Next() {
		var round policyRound
		if err := rows.Scan(&round.id, &round.ordinal, &round.state, &round.version, &round.cutoff, &round.baseline, &round.reason); err != nil {
			t.Fatal(err)
		}
		result = append(result, round)
	}
	return result
}

func pendingEvents(t *testing.T, fixture *plexPublicationFixture, workflowID uuid.UUID, eventType string) int {
	t.Helper()
	var count int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type=$2 AND completed_at IS NULL`, workflowID.String(), eventType).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestUpdateQualityProfilePolicyAtVersionRejectsStaleWrite(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	expected := fixture.profile.PolicyVersion
	policy, err := releases.WithPolicyRules(fixture.profile.Policy, []releases.Rule{{Name: "Preset preference", Fact: "resolution", AllowedValues: []string{"2160p"}, Kind: releases.RulePreferred, Weight: 10, Unknown: releases.UnknownAllow}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.store.UpdateQualityProfilePolicyAtVersion(fixture.ctx, fixture.profile.ID, policy, expected)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PolicyVersion != expected+1 || updated.Name != fixture.profile.Name || updated.Default != fixture.profile.Default || updated.Enabled != fixture.profile.Enabled || updated.UpgradesEnabled != fixture.profile.UpgradesEnabled {
		t.Fatalf("updated profile=%+v", updated)
	}
	if _, err := fixture.store.UpdateQualityProfilePolicyAtVersion(fixture.ctx, fixture.profile.ID, policy, expected); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("stale write error=%v, want ErrConcurrentModification", err)
	}
	profiles, err := fixture.store.ListQualityProfiles(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, profile := range profiles {
		if profile.ID != fixture.profile.ID {
			continue
		}
		found = true
		if profile.PolicyVersion != expected+1 || profile.Name != fixture.profile.Name || profile.Default != fixture.profile.Default || profile.Enabled != fixture.profile.Enabled || profile.UpgradesEnabled != fixture.profile.UpgradesEnabled {
			t.Fatalf("stored profile after stale write=%+v", profile)
		}
	}
	if !found {
		t.Fatal("updated profile not found")
	}
}

// TestApplyQualityProfilePolicyAcrossWorkflowStates proves the owner policy
// application leaves every searchable workflow on the current policy without
// SQL: open rounds in waiting, held, and searching are superseded while their
// schedules continue, an exhausted workflow gets a new round and a search, an
// attempt-free attention workflow only gets the new round, in-flight work is
// skipped and reported, and a second application is a no-op.
func TestApplyQualityProfilePolicyAcrossWorkflowStates(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()
	transition := func(workflowID uuid.UUID, states ...workflows.State) workflows.Workflow {
		t.Helper()
		current, err := store.GetWorkflow(ctx, workflowID)
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range states {
			if current, err = store.TransitionWorkflow(ctx, workflowID, current.Revision, state, "test", "test", now); err != nil {
				t.Fatalf("transition to %s: %v", state, err)
			}
		}
		return current
	}

	_, waitingID := fixture.movieWorkflow(t, "Policy Waiting", "580001")
	waiting := transition(waitingID, workflows.StateWaiting)
	if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested','{}',now(),now()+interval '30 minutes')`, uuid.NewV7().String(), waitingID.String(), int64(waiting.Revision)); err != nil {
		t.Fatal(err)
	}
	_, heldID := fixture.movieWorkflow(t, "Policy Held", "580002")
	held := transition(heldID, workflows.StateSearching, workflows.StateHeld)
	_, searchingID := fixture.movieWorkflow(t, "Policy Searching", "580003")
	searching := transition(searchingID, workflows.StateSearching)
	_, attentionID := fixture.movieWorkflow(t, "Policy Attention", "580004")
	attention := transition(attentionID, workflows.StateNeedsAttention)
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET attention_code='release_retrieval_failed' WHERE id=$1`, attentionID.String()); err != nil {
		t.Fatal(err)
	}
	_, inFlightID := fixture.movieWorkflow(t, "Policy In Flight", "580005")
	inFlightJob, _, _ := seedSubtitleAttempt(t, ctx, store, inFlightID, fixture.altMount.ID, fixture.indexer.ID, 1, "policy-in-flight")

	// An exhausted workflow waiting on discovery, with a ready candidate.
	_, exhaustedID := fixture.movieWorkflow(t, "Policy Exhausted", "580006")
	exhausted := transition(exhaustedID, workflows.StateWaiting)
	exhaustedRound := workflowRounds(t, fixture, exhaustedID)[0].id
	readyCandidate := seedEligibleCandidate(t, ctx, store, exhaustedID, fixture.indexer.ID, "policy-exhausted-ready", 10, now)
	if _, err := store.Pool.Exec(ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=now(),closed_at=now(),close_reason='season_pack_incomplete' WHERE id=$1`, exhaustedRound); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,complete_no_new_scans,ready_candidate_id,ready_first_seen_sequence,created_at,updated_at)
		SELECT $1,$2,'ready',1,0,$3,candidate.first_seen_sequence,now(),now() FROM release_candidates candidate WHERE candidate.id=$3`, exhaustedID.String(), exhaustedRound, readyCandidate.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET attention_code=$2 WHERE id=$1`, exhaustedID.String(), workflows.AttentionRoundExhaustedWaitingNewIdentity); err != nil {
		t.Fatal(err)
	}

	// The owner tightens the profile, now requiring Danish subtitles.
	policy, err := json.Marshal(map[string]any{
		"rules":                       []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}},
		"required_subtitle_languages": []string{"da"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: fixture.profile.ID, MediaKind: catalog.KindMovie, Name: fixture.profile.Name, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}, storedQualityProfileVersion(t, ctx, store, fixture.profile.ID))
	if err != nil {
		t.Fatal(err)
	}

	// Reopening the exhausted round's old snapshot is refused.
	if err := store.OpenNextAcquisitionRound(ctx, exhaustedID, now); err == nil || !strings.Contains(err.Error(), "apply the current policy") {
		t.Fatalf("stale exhausted round reopen error=%v", err)
	}
	// In-flight output verification fails closed to the live profile: a
	// tightening can only add requirements for an attempt that already started,
	// so Danish stays required even though the round snapshot is empty.
	jobPolicy, err := store.SubtitlePolicyForJob(ctx, inFlightJob.JobID)
	if err != nil || len(jobPolicy.Required) != 1 || jobPolicy.Required[0] != "da" {
		t.Fatalf("in-flight job policy=%+v err=%v, want fail-closed Danish requirement", jobPolicy, err)
	}

	result, err := store.ApplyQualityProfilePolicy(ctx, fixture.profile.ID, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[uuid.UUID]PolicyWorkflowOutcome{}
	for _, outcome := range result.Workflows {
		outcomes[outcome.WorkflowID] = outcome
	}
	want := map[uuid.UUID]string{
		waitingID:   PolicyOutcomeApplied,
		heldID:      PolicyOutcomeApplied,
		searchingID: PolicyOutcomeApplied,
		attentionID: PolicyOutcomeApplied,
		inFlightID:  PolicyOutcomeInFlight,
		exhaustedID: PolicyOutcomeAppliedSearchQueued,
	}
	for workflowID, outcome := range want {
		if outcomes[workflowID].Outcome != outcome {
			t.Fatalf("workflow %s outcome=%+v, want %s (all=%+v)", workflowID, outcomes[workflowID], outcome, result)
		}
	}
	if result.PolicyVersion != updated.PolicyVersion || result.Applied != 5 || result.Skipped != 1 {
		t.Fatalf("result=%+v", result)
	}

	for _, workflowID := range []uuid.UUID{waitingID, heldID, searchingID, attentionID} {
		rounds := workflowRounds(t, fixture, workflowID)
		if len(rounds) != 2 || rounds[0].state != "cancelled" || !strings.Contains(rounds[0].reason, "superseded by profile version") ||
			rounds[1].state != "open" || rounds[1].ordinal != 2 || rounds[1].version != int64(updated.PolicyVersion) || rounds[1].cutoff != 0 || rounds[1].id != outcomes[workflowID].RoundID.String() {
			t.Fatalf("workflow %s rounds=%+v", workflowID, rounds)
		}
	}
	// Existing state and schedule continue unchanged for round-only workflows.
	for workflowID, before := range map[uuid.UUID]workflows.Workflow{waitingID: waiting, heldID: held, searchingID: searching, attentionID: attention} {
		after, err := store.GetWorkflow(ctx, workflowID)
		if err != nil || after.State != before.State || after.Revision != before.Revision {
			t.Fatalf("round-only workflow before=%+v after=%+v err=%v", before, after, err)
		}
	}
	if pendingEvents(t, fixture, waitingID, "workflow.search_requested") != 1 || pendingEvents(t, fixture, heldID, "workflow.search_requested") != 0 {
		t.Fatal("round-only application changed the search schedule")
	}
	// The exhausted workflow gets round 2 under the new policy and a search now.
	exhaustedAfter, err := store.GetWorkflow(ctx, exhaustedID)
	if err != nil || exhaustedAfter.State != workflows.StateWaiting || exhaustedAfter.AttentionCode != "" || exhaustedAfter.Revision != exhausted.Revision+1 {
		t.Fatalf("exhausted after=%+v err=%v", exhaustedAfter, err)
	}
	rounds := workflowRounds(t, fixture, exhaustedID)
	if len(rounds) != 2 || rounds[0].state != "exhausted" || rounds[1].state != "open" || rounds[1].version != int64(updated.PolicyVersion) {
		t.Fatalf("exhausted rounds=%+v", rounds)
	}
	var discoveryState string
	var searchRevision int64
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT state FROM workflow_retry_discovery WHERE workflow_id=$1),(SELECT aggregate_revision FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL)`, exhaustedID.String()).Scan(&discoveryState, &searchRevision); err != nil || discoveryState != "paused" || uint64(searchRevision) != exhaustedAfter.Revision {
		t.Fatalf("exhausted discovery=%q search revision=%d err=%v", discoveryState, searchRevision, err)
	}
	// In-flight work is untouched and still on its original round.
	if rounds := workflowRounds(t, fixture, inFlightID); len(rounds) != 1 || rounds[0].state != "open" || rounds[0].version != int64(fixture.profile.PolicyVersion) {
		t.Fatalf("in-flight rounds=%+v", rounds)
	}
	var audits int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='workflow.quality_policy_applied' AND actor='owner')+(SELECT count(*) FROM audit_events WHERE action='quality_profile.policy_applied' AND aggregate_id=$1)`, fixture.profile.ID.String()).Scan(&audits); err != nil || audits != 6 {
		t.Fatalf("audit events=%d err=%v, want 5 workflow + 1 profile", audits, err)
	}

	again, err := store.ApplyQualityProfilePolicy(ctx, fixture.profile.ID, "owner", now.Add(2*time.Second))
	if err != nil || again.Applied != 0 || again.Skipped != 6 {
		t.Fatalf("second application=%+v err=%v", again, err)
	}
}

// TestApplyQualityProfilePolicyRespectsRetainedOutputLimit proves granting a
// new round through a policy change honors the same storage-review limit as
// OpenNextAcquisitionRound.
func TestApplyQualityProfilePolicyRespectsRetainedOutputLimit(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	_, workflowID := fixture.movieWorkflow(t, "Policy Storage Limit", "580010")
	// Four retained failed outputs across two exhausted two-slot rounds.
	var firstRound string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, workflowID.String()).Scan(&firstRound); err != nil {
		t.Fatal(err)
	}
	for generation := 1; generation <= 2; generation++ {
		job, _, _ := seedSubtitleAttempt(t, ctx, store, workflowID, fixture.altMount.ID, fixture.indexer.ID, generation, "policy-limit-"+string(rune('a'+generation)))
		if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='needs_attention',acquisition_outcome='season_pack_incomplete' WHERE id=$1`, job.JobID.String()); err != nil {
			t.Fatal(err)
		}
	}
	roundID := uuid.NewV7().String()
	if _, err := store.Pool.Exec(ctx, `UPDATE acquisition_rounds SET state='exhausted' WHERE id=$1`, firstRound); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
		SELECT $1,workflow_id,2,'exhausted',2,0,policy_version,policy_snapshot,upgrade_baseline_link_versions,now() FROM acquisition_rounds WHERE id=$2`, roundID, firstRound); err != nil {
		t.Fatal(err)
	}
	for slot := 1; slot <= 2; slot++ {
		key := "policy-limit-round2-" + string(rune('a'+slot))
		candidate := seedEligibleCandidate(t, ctx, store, workflowID, fixture.indexer.ID, key, 1, time.Now().UTC())
		jobID := uuid.NewV7()
		packageIdentity := testNZBPackageIdentity(byte(10 + slot))
		if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,nzb_package_sha256,round_id,round_slot,acquisition_outcome) VALUES($1,$2,$3,$4,$5,$6,'needs_attention',now(),1,$7,$8,$9,'season_pack_incomplete')`, jobID.String(), workflowID.String(), candidate.String(), fixture.altMount.ID.String(), 2+slot, key+".nzb", packageIdentity[:], roundID, slot); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,1,$3,$4,now(),now())`, uuid.NewV7().String(), jobID.String(), "completed/"+key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,complete_no_new_scans,created_at,updated_at) VALUES($1,$2,'waiting',1,0,now(),now())`, workflowID.String(), roundID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='waiting',grab_attempt_id=NULL,selected_release_id=NULL,revision=revision+1 WHERE id=$1`, workflowID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateQualityProfile(ctx, QualityProfile{ID: fixture.profile.ID, MediaKind: catalog.KindMovie, Name: fixture.profile.Name, Policy: fixture.profile.Policy, UpgradesEnabled: true, Default: true, Enabled: true}, storedQualityProfileVersion(t, ctx, store, fixture.profile.ID)); err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyQualityProfilePolicy(ctx, fixture.profile.ID, "owner", time.Now().UTC())
	if err != nil || len(result.Workflows) != 1 || result.Workflows[0].Outcome != PolicyOutcomeRetainedOutputLimit {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if rounds := workflowRounds(t, fixture, workflowID); len(rounds) != 2 || rounds[0].state != "exhausted" || rounds[1].state != "exhausted" {
		t.Fatalf("storage-limited workflow rounds=%+v", rounds)
	}
}
