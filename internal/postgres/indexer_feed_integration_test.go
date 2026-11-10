package postgres

import (
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/workflows"
)

// seedFeedWaiting leaves a movie workflow waiting with its next targeted
// search scheduled a day ahead, as PersistSearch does after a search that
// found nothing.
func seedFeedWaiting(t *testing.T, f *failureRecoveryFixture, title, tmdb string, payload string) workflows.Workflow {
	t.Helper()
	created := f.newMovieWorkflow(t, title, tmdb)
	waiting, err := f.store.TransitionWorkflow(f.ctx, created.ID, created.Revision, workflows.StateWaiting, "test waiting", "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_id=$1 AND completed_at IS NULL`, waiting.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
 VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4::jsonb,now(),now()+interval '1 day')
 ON CONFLICT (aggregate_type,aggregate_id,aggregate_revision,event_type) DO UPDATE SET payload=EXCLUDED.payload,available_at=EXCLUDED.available_at,completed_at=NULL,lease_owner=NULL,lease_until=NULL`,
		uuid.NewV7().String(), waiting.ID.String(), int64(waiting.Revision), payload); err != nil {
		t.Fatal(err)
	}
	return waiting
}

func feedSearchAvailableAt(t *testing.T, f *failureRecoveryFixture, workflow workflows.Workflow) time.Time {
	t.Helper()
	var at time.Time
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT available_at FROM outbox_events WHERE aggregate_type='workflow' AND aggregate_id=$1 AND aggregate_revision=$2 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflow.ID.String(), int64(workflow.Revision)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func feedOutcomes(t *testing.T, f *failureRecoveryFixture, workflowID uuid.UUID) map[string]int {
	t.Helper()
	rows, err := f.store.Pool.Query(f.ctx, `SELECT outcome,count(*) FROM indexer_feed_matches WHERE workflow_id=$1 GROUP BY outcome`, workflowID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var outcome string
		var count int
		if err := rows.Scan(&outcome, &count); err != nil {
			t.Fatal(err)
		}
		result[outcome] = count
	}
	return result
}

func recordFeedMatch(t *testing.T, f *failureRecoveryFixture, workflowID uuid.UUID, guid string, now time.Time) int {
	t.Helper()
	next := now.Add(10 * time.Minute)
	inserted, err := f.store.RecordIndexerFeedRead(f.ctx, IndexerFeedState{IndexerInstanceID: f.indexer.ID, LastReadAt: &now, NextReadAt: &next, IntervalSeconds: 600, LastOutcome: "ok", LastItemCount: 100, LastNewCount: 7},
		[]FeedMatch{{WorkflowID: workflowID, NativeGUID: guid, ReleaseTitle: "Feed.Title.2026.1080p.WEB-DL"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return inserted
}

// TestFeedMatchBringsTheScheduledSearchForwardAndNothingElse proves the whole
// effect of a newest-uploads match: the waiting title's own next search is due
// now. The workflow, its revision, its selection and its rounds are untouched,
// so every release is still chosen by the search and its rules.
func TestFeedMatchBringsTheScheduledSearchForwardAndNothingElse(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := seedFeedWaiting(t, f, "Feed Advance", "903001", `{"reason":"retry"}`)
	other := seedFeedWaiting(t, f, "Feed Bystander", "903002", `{"reason":"retry"}`)
	now := time.Now().UTC().Truncate(time.Microsecond)
	roundsBefore := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String())

	if inserted := recordFeedMatch(t, f, w.ID, "feed-guid-1", now); inserted != 1 {
		t.Fatalf("inserted=%d", inserted)
	}
	// The same upload read again (for example after a restart) is not a new match.
	if inserted := recordFeedMatch(t, f, w.ID, "feed-guid-1", now); inserted != 0 {
		t.Fatalf("repeated upload inserted=%d", inserted)
	}
	states, err := f.store.IndexerFeedStates(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state := states[f.indexer.ID]; state.IntervalSeconds != 600 || state.LastOutcome != "ok" || state.LastItemCount != 100 || state.LastNewCount != 7 || state.NextReadAt == nil || !state.NextReadAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("state=%+v", state)
	}

	result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchAdvanced != 1 || result.SearchDue+result.DiscoveryAdvanced+result.NotApplicable+result.Expired != 0 {
		t.Fatalf("result=%+v", result)
	}
	if at := feedSearchAvailableAt(t, f, w); !at.Equal(now) {
		t.Fatalf("search available_at=%s want %s", at, now)
	}
	if at := feedSearchAvailableAt(t, f, other); !at.After(now.Add(23 * time.Hour)) {
		t.Fatalf("unmatched title was brought forward: %s", at)
	}
	after, err := f.store.GetWorkflow(f.ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != workflows.StateWaiting || after.Revision != w.Revision || after.SelectedRelease != nil || after.GrabAttemptID != nil {
		t.Fatalf("match changed the workflow: %+v", after)
	}
	if rounds := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, w.ID.String()); rounds != roundsBefore {
		t.Fatalf("rounds %d -> %d", roundsBefore, rounds)
	}
	for _, q := range []string{`SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`, `SELECT count(*) FROM release_candidates WHERE workflow_id=$1`} {
		if n := f.count(t, q, w.ID.String()); n != 0 {
			t.Fatalf("match started acquisition (%s): %d", q, n)
		}
	}
	if got := feedOutcomes(t, f, w.ID); got["search_advanced"] != 1 || len(got) != 1 {
		t.Fatalf("outcomes=%v", got)
	}

	// A second upload while that search is still due settles without a change.
	recordFeedMatch(t, f, w.ID, "feed-guid-2", now.Add(time.Minute))
	result, err = f.store.AdvanceFeedMatches(f.ctx, now.Add(time.Minute), 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchDue != 1 || result.SearchAdvanced != 0 {
		t.Fatalf("second result=%+v", result)
	}
	if at := feedSearchAvailableAt(t, f, w); !at.Equal(now) {
		t.Fatalf("due search was rescheduled: %s", at)
	}
	summaries, err := f.store.IndexerFeedSummaries(f.ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if summary := summaries[f.indexer.ID]; summary.Matches24h != 2 || summary.LastOutcome != "ok" {
		t.Fatalf("summary=%+v", summary)
	}
	// The owner's indexer view carries the same state; an indexer whose list
	// was never read has none.
	usages, err := f.store.IndexerUsages(f.ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, usage := range usages {
		switch {
		case usage.ID == f.indexer.ID && (usage.NewUploads == nil || usage.NewUploads.Matches24h != 2 || usage.NewUploads.LastReadAt == nil):
			t.Fatalf("indexer view new_uploads=%+v", usage.NewUploads)
		case usage.ID == f.indexerTwo.ID && usage.NewUploads != nil:
			t.Fatalf("unread indexer shows new_uploads=%+v", usage.NewUploads)
		}
	}
}

// TestFeedMatchWaitsOutRecentAndUnfinishedSearches proves the limits on what
// a match costs: one brought-forward search per quiet period, and never the
// remainder of an unfinished search, which keeps its saved answers and its own
// schedule.
func TestFeedMatchWaitsOutRecentAndUnfinishedSearches(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	recent := seedFeedWaiting(t, f, "Feed Recent", "903011", `{"reason":"retry"}`)
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,$4,$4)`, uuid.NewV7().String(), recent.ID.String(), []byte{1}, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	partial := seedFeedWaiting(t, f, "Feed Partial", "903012", `{"reason":"retry","partial_retry":true}`)
	recordFeedMatch(t, f, recent.ID, "recent-guid", now)
	recordFeedMatch(t, f, partial.ID, "partial-guid", now)

	result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result != (FeedAdvanceResult{}) {
		t.Fatalf("result=%+v", result)
	}
	for _, w := range []workflows.Workflow{recent, partial} {
		if at := feedSearchAvailableAt(t, f, w); !at.After(now.Add(23 * time.Hour)) {
			t.Fatalf("search was brought forward: %s", at)
		}
		if got := feedOutcomes(t, f, w.ID); got["pending"] != 1 || len(got) != 1 {
			t.Fatalf("outcomes=%v", got)
		}
	}

	// Once the quiet period has passed the recent title is brought forward.
	later := now.Add(25 * time.Minute)
	result, err = f.store.AdvanceFeedMatches(f.ctx, later, 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchAdvanced != 1 {
		t.Fatalf("later result=%+v", result)
	}
	if at := feedSearchAvailableAt(t, f, recent); !at.Equal(later) {
		t.Fatalf("recent available_at=%s", at)
	}

	// A match nobody could act on within a day is closed.
	result, err = f.store.AdvanceFeedMatches(f.ctx, now.Add(25*time.Hour), 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 1 {
		t.Fatalf("expiry result=%+v", result)
	}
	if got := feedOutcomes(t, f, partial.ID); got["expired"] != 1 || len(got) != 1 {
		t.Fatalf("partial outcomes=%v", got)
	}
}

// TestFeedMatchAdvancesDiscoveryOfAnExhaustedRoundAsTheWorker proves that a
// title whose round is used up gets its paced discovery brought forward, by
// the same guarded step the owner's button uses and audited as the worker,
// and that no round is opened.
func TestFeedMatchAdvancesDiscoveryOfAnExhaustedRoundAsTheWorker(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Feed Discovery", "903021")
	seedOwnerDiscovery(t, f, w)
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE aggregate_type='workflow' AND aggregate_id=$1 AND completed_at IS NULL`, w.ID.String()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	recordFeedMatch(t, f, w.ID, "discovery-guid", now)

	result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.DiscoveryAdvanced != 1 {
		t.Fatalf("result=%+v", result)
	}
	var generation int64
	var next time.Time
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT rescan_generation,next_rescan_at FROM workflow_retry_discovery WHERE workflow_id=$1`, w.ID.String()).Scan(&generation, &next); err != nil {
		t.Fatal(err)
	}
	if generation != 6 || !next.Equal(now) {
		t.Fatalf("generation=%d next=%s", generation, next)
	}
	if n := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='open'`, w.ID.String()); n != 0 {
		t.Fatalf("match opened a round: %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_events WHERE aggregate_id=$1 AND action='workflow.discovery_requested' AND actor='worker' AND detail->>'reason'='feed_match'`, w.ID.String()); n != 1 {
		t.Fatalf("worker audit events=%d", n)
	}
	if got := feedOutcomes(t, f, w.ID); got["discovery_advanced"] != 1 || len(got) != 1 {
		t.Fatalf("outcomes=%v", got)
	}
}

// TestFeedMatchForATitleThatIsNoLongerWaitingChangesNothing covers a title
// that started downloading, finished or went to the owner between the read
// and the pass.
func TestFeedMatchForATitleThatIsNoLongerWaitingChangesNothing(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := seedFeedWaiting(t, f, "Feed Gone", "903031", `{"reason":"retry"}`)
	now := time.Now().UTC().Truncate(time.Microsecond)
	recordFeedMatch(t, f, w.ID, "gone-guid", now)
	if _, err := f.store.TransitionWorkflow(f.ctx, w.ID, w.Revision, workflows.StateCancelled, "test cancelled", "test", now); err != nil {
		t.Fatal(err)
	}
	eventsBefore := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String())
	result, err := f.store.AdvanceFeedMatches(f.ctx, now, 30*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.NotApplicable != 1 || result.SearchAdvanced+result.DiscoveryAdvanced != 0 {
		t.Fatalf("result=%+v", result)
	}
	if events := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID.String()); events != eventsBefore {
		t.Fatalf("events %d -> %d", eventsBefore, events)
	}
	waiting, err := f.store.ListFeedWorkflows(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range waiting {
		if item.WorkflowID == w.ID {
			t.Fatal("a cancelled workflow is still compared with new uploads")
		}
	}
}

func TestListFeedWorkflowsCarriesTitlesYearAndIdentities(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := seedFeedWaiting(t, f, "Feed Listing", "903041", `{"reason":"retry"}`)
	waiting, err := f.store.ListFeedWorkflows(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range waiting {
		if item.WorkflowID != w.ID {
			continue
		}
		if item.Media.Title != "Feed Listing" || item.Media.Year == nil || *item.Media.Year != 2026 || item.Media.Kind != "movie" {
			t.Fatalf("media=%+v", item.Media)
		}
		if len(item.Media.Identities) != 1 || item.Media.Identities[0].Namespace != "tmdb" || item.Media.Identities[0].Value != "903041" || item.Media.Identities[0].State != "verified" {
			t.Fatalf("identities=%+v", item.Media.Identities)
		}
		return
	}
	t.Fatal("waiting workflow is not listed")
}
