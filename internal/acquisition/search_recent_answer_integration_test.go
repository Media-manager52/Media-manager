package acquisition

import (
	"context"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
	"net/url"
	"uuid"
)

func twoMovieReleasesXML() string {
	return `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>` +
		`<item><title>Klaus.2019.1080p.WEB-DL-ALPHA</title><guid>native-alpha</guid><newznab:attr name="resolution" value="1920x1080"/></item>` +
		`<item><title>Klaus.2019.1080p.BluRay-BRAVO</title><guid>native-bravo</guid><newznab:attr name="resolution" value="1920x1080"/></item>` +
		`</channel></rss>`
}

// rejectSelectedPackage records the selected release's package as rejected
// before submission and returns the search event that follows it.
func rejectSelectedPackage(t *testing.T, store *postgres.Store, workflowID uuid.UUID) (uuid.UUID, postgres.OutboxEvent) {
	t.Helper()
	ctx := context.Background()
	workflow, err := store.GetWorkflow(ctx, workflowID)
	if err != nil || workflow.State != workflows.StateCandidateSelected || workflow.SelectedRelease == nil {
		t.Fatalf("selection=%+v %v", workflow, err)
	}
	retrieval, err := store.LoadRetrievalContext(ctx, workflowID, *workflow.SelectedRelease)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPreSubmissionRejection(ctx, retrieval, "archive_unsupported", "test package rejected", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var event postgres.OutboxEvent
	var idText string
	if err := store.Pool.QueryRow(ctx, `SELECT id::text,aggregate_revision,payload FROM outbox_events
		WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL ORDER BY occurred_at DESC LIMIT 1`, workflowID.String()).Scan(&idText, &event.AggregateRevision, &event.Payload); err != nil {
		t.Fatal(err)
	}
	event.ID, _ = uuid.Parse(idText)
	event.AggregateID = workflowID
	event.AggregateType = "workflow"
	event.Type = "workflow.search_requested"
	return *workflow.SelectedRelease, event
}

func searchRequestsOnly(requests []url.Values) int {
	count := 0
	for _, request := range requests {
		if request.Get("t") != "get" && request.Get("t") != "caps" {
			count++
		}
	}
	return count
}

// TestRetryAfterFailedReleaseReusesTheSearchThatFoundIt covers the search
// after a failed release: it picks the next release from the answer of the
// search that selected the failed one, without asking the indexer again, and
// the failed release stays excluded. An answer older than its lifetime is
// not reused.
func TestRetryAfterFailedReleaseReusesTheSearchThatFoundIt(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, func(url.Values) string { return twoMovieReleasesXML() })
	defer server.Close()
	configureNamedSearchIndexer(t, store, "Source A", server.URL)
	workflow := createMovieSearchWorkflow(t, store)
	service := movieSearchService(store, server)
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatal(err)
	}
	if got := searchRequestsOnly(captured()); got != 1 {
		t.Fatalf("first search requests=%d, want 1", got)
	}
	failed, event := rejectSelectedPackage(t, store, workflow.ID)
	if err := service.Handle(ctx, event); err != nil {
		t.Fatal(err)
	}
	if got := searchRequestsOnly(captured()); got != 1 {
		t.Fatalf("search requests after the failed release=%d, want still 1 (answer reused)", got)
	}
	next, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || next.State != workflows.StateCandidateSelected || next.SelectedRelease == nil || *next.SelectedRelease == failed {
		t.Fatalf("next selection=%+v (failed %s) %v", next, failed, err)
	}
	var detail string
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(indexer.detail,'') FROM search_run_indexers indexer JOIN search_runs run ON run.id=indexer.search_run_id
		WHERE run.workflow_id=$1 ORDER BY run.started_at DESC LIMIT 1`, workflow.ID.String()).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "reused recent search answer" {
		t.Fatalf("indexer detail=%q", detail)
	}

	// The second release fails too, but the answer is now older than its
	// lifetime: the indexer is asked again.
	if _, err := store.Pool.Exec(ctx, `UPDATE search_recent_answers SET fetched_at=fetched_at-$2::interval WHERE workflow_id=$1`, workflow.ID.String(), (postgres.RecentSearchAnswerLifetime + time.Minute).String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET next_allowed_at=now()-interval '1 minute',cooldown_until=NULL,consecutive_failure=0`); err != nil {
		t.Fatal(err)
	}
	_, event = rejectSelectedPackage(t, store, workflow.ID)
	if err := movieSearchService(store, server).Handle(ctx, event); err != nil {
		t.Fatal(err)
	}
	if got := searchRequestsOnly(captured()); got != 2 {
		t.Fatalf("search requests after an expired answer=%d, want 2", got)
	}
}
