package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
)

// TestQualityPolicyChangeParksSearchUntilOwnerApplies proves a profile edit
// never lets a search silently use the old round snapshot or retry forever:
// the search parks the workflow with quality_policy_changed and makes no
// provider request, the owner's apply-policy opens a round under the current
// version and re-searches, and a selection made under an older version is
// cleared, with its pending retrieval dropped as stale.
func TestQualityPolicyChangeParksSearchUntilOwnerApplies(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, func(url.Values) string {
		return movieSearchReleaseXML("Klaus.2019.1080p.WEB-DL")
	})
	defer server.Close()
	configureMovieSearchIndexer(t, store, server.URL, true)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)
	profileID := workflow.QualityProfileID

	updateProfile := func(weight int) uint64 {
		t.Helper()
		policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{
			{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold},
			{Name: "Prefer WEB", Fact: "source", AllowedValues: []string{"web"}, Kind: releases.RulePreferred, Weight: weight, Unknown: releases.UnknownAllow},
		}})
		if err != nil {
			t.Fatal(err)
		}
		updated, err := store.UpdateQualityProfile(ctx, postgres.QualityProfile{ID: profileID, MediaKind: catalog.KindMovie, Name: "Movies quality", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return updated.PolicyVersion
	}
	pendingSearchRevision := func() uint64 {
		t.Helper()
		var revision int64
		if err := store.Pool.QueryRow(ctx, `SELECT aggregate_revision FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL ORDER BY occurred_at DESC LIMIT 1`, workflow.ID.String()).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return uint64(revision)
	}
	lastRunPolicy := func() int64 {
		t.Helper()
		var version int64
		if err := store.Pool.QueryRow(ctx, `SELECT policy_version FROM search_runs WHERE workflow_id=$1 ORDER BY started_at DESC,id DESC LIMIT 1`, workflow.ID.String()).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}

	// 1. The profile changes before the first search runs.
	version := updateProfile(10)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("stale-policy search returned %v, want a parked workflow and nil", err)
	}
	if requests := captured(); len(requests) != 0 {
		t.Fatalf("stale-policy search sent %d provider requests", len(requests))
	}
	parked, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || parked.State != workflows.StateWaiting || parked.AttentionCode != workflows.AttentionQualityPolicyChanged || parked.OwnerAttentionState() != workflows.StateNeedsAttention {
		t.Fatalf("parked workflow=%+v err=%v", parked, err)
	}
	// Redelivering the original event is a stale no-op.
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil || len(captured()) != 0 {
		t.Fatalf("redelivered stale event err=%v requests=%d", err, len(captured()))
	}

	// 2. The owner applies the policy; the search runs under the new version.
	applied, err := store.ApplyQualityProfilePolicy(ctx, profileID, "owner", time.Now().UTC())
	if err != nil || applied.Applied != 1 || applied.Workflows[0].Outcome != postgres.PolicyOutcomeAppliedSearchQueued {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: pendingSearchRevision()}); err != nil {
		t.Fatalf("search after apply: %v", err)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil || lastRunPolicy() != int64(version) || len(captured()) != 1 {
		t.Fatalf("selected=%+v err=%v run policy=%d want %d requests=%d", selected, err, lastRunPolicy(), version, len(captured()))
	}
	var retrieveRevision int64
	var retrievePayload []byte
	if err := store.Pool.QueryRow(ctx, `SELECT aggregate_revision,payload FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested' AND completed_at IS NULL`, workflow.ID.String()).Scan(&retrieveRevision, &retrievePayload); err != nil {
		t.Fatal(err)
	}

	// 3. A later change arrives while the selection waits for retrieval.
	version = updateProfile(20)
	applied, err = store.ApplyQualityProfilePolicy(ctx, profileID, "owner", time.Now().UTC())
	if err != nil || applied.Applied != 1 || applied.Workflows[0].State != string(workflows.StateCandidateSelected) || applied.Workflows[0].Outcome != postgres.PolicyOutcomeAppliedSearchQueued {
		t.Fatalf("apply over selection=%+v err=%v", applied, err)
	}
	cleared, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || cleared.State != workflows.StateWaiting || cleared.SelectedRelease != nil || cleared.Revision != selected.Revision+2 {
		t.Fatalf("cleared=%+v err=%v", cleared, err)
	}
	retrieval := &RetrievalService{Store: store}
	if err := retrieval.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: uint64(retrieveRevision), Payload: retrievePayload}); err != nil {
		t.Fatalf("stale retrieval returned %v, want nil", err)
	}
	var jobs int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("stale retrieval created jobs=%d err=%v", jobs, err)
	}
	// Honor the indexer's persisted minimum interval exactly as the worker's
	// DeferOutbox would, rather than weakening pacing for the test.
	secondSearch := postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: pendingSearchRevision()}
	err = service.Handle(ctx, secondSearch)
	var deferred interface{ RetryAt() time.Time }
	if errors.As(err, &deferred) {
		time.Sleep(time.Until(deferred.RetryAt()) + 50*time.Millisecond)
		err = service.Handle(ctx, secondSearch)
	}
	if err != nil {
		t.Fatalf("search after second apply: %v", err)
	}
	reselected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || reselected.State != workflows.StateCandidateSelected || lastRunPolicy() != int64(version) {
		t.Fatalf("reselected=%+v err=%v run policy=%d want %d", reselected, err, lastRunPolicy(), version)
	}
	var openRounds, openOrdinal, openVersion int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*),max(round_ordinal),max(policy_version) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, workflow.ID.String()).Scan(&openRounds, &openOrdinal, &openVersion); err != nil || openRounds != 1 || openOrdinal != 3 || openVersion != int(version) {
		t.Fatalf("open rounds=%d ordinal=%d version=%d err=%v", openRounds, openOrdinal, openVersion, err)
	}
}
