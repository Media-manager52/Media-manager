package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

// nfoReleaseXML is one release whose provider advertised an NFO (info link)
// but gave no subtitle or audio language attributes.
func nfoReleaseXML(base, title string) string {
	info := base + "?t=getnfo&amp;id=native-movie-release&amp;raw=1"
	return fmt.Sprintf(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>%s</title><guid>native-movie-release</guid><newznab:attr name="resolution" value="1920x1080"/><newznab:attr name="info" value="%s"/></item></channel></rss>`, title, info)
}

type nfoCheckFixture struct {
	store     *postgres.Store
	workflow  workflows.Workflow
	selected  workflows.Workflow
	indexer   postgres.Integration
	captured  func() []url.Values
	service   func() *RetrievalService
	event     postgres.OutboxEvent
	clearPace func()
}

func newNFOCheckFixture(t *testing.T, nfo string) nfoCheckFixture {
	t.Helper()
	return newNFOCheckFixtureWithRelease(t, nfo, nfoReleaseXML)
}

func newNFOCheckFixtureWithRelease(t *testing.T, nfo string, release func(base, title string) string) nfoCheckFixture {
	t.Helper()
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	var base string
	server, captured := newMovieSearchServer(t, func(query url.Values) string {
		switch query.Get("t") {
		case "getnfo":
			return nfo
		case "get":
			return "" // HTTP 500: the test only needs to see that a grab was attempted
		case "movie":
			return release(base, "Klaus 2019 1080p WEB-DL")
		default:
			return emptySearchXML()
		}
	})
	t.Cleanup(server.Close)
	base = server.URL
	indexer := configureMovieSearchIndexer(t, store, server.URL, false)
	workflow := createMovieSearchWorkflowWithPolicy(t, store, map[string]any{"required_subtitle_languages": []string{"da"}})
	if err := movieSearchService(store, server).Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("search: %v", err)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.SelectedRelease == nil || selected.State != workflows.StateCandidateSelected {
		t.Fatalf("selected workflow=%+v err=%v", selected, err)
	}
	clearPace := func() {
		if _, err := store.Pool.Exec(ctx, `UPDATE indexer_budget_state SET next_allowed_at=now()-interval '1 minute',cooldown_until=NULL WHERE indexer_instance_id=$1`, indexer.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	clearPace()
	payload, _ := json.Marshal(map[string]string{"workflow_id": workflow.ID.String(), "candidate_id": selected.SelectedRelease.String()})
	return nfoCheckFixture{
		store: store, workflow: workflow, selected: selected, indexer: indexer, captured: captured, clearPace: clearPace,
		// A fresh scheduler per run, like a restarted worker: pacing is read
		// from the durable state the previous run left.
		service: func() *RetrievalService {
			return &RetrievalService{Store: store, Scheduler: indexers.NewScheduler(2, store), ResolveSecret: func(string) (string, error) { return "test-api-key", nil }, Transport: server.Client().Transport}
		},
		event: postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: selected.Revision, Type: "release.retrieve_requested", Payload: payload, Attempts: 1},
	}
}

func (f nfoCheckFixture) requests(t string) int {
	count := 0
	for _, request := range f.captured() {
		if request.Get("t") == t {
			count++
		}
	}
	return count
}

func (f nfoCheckFixture) auditOutcome(t *testing.T) string {
	t.Helper()
	var outcome string
	if err := f.store.Pool.QueryRow(context.Background(), `SELECT detail->>'outcome' FROM audit_events WHERE action='release.nfo_checked' AND aggregate_id=$1`, f.selected.SelectedRelease.String()).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

// An NFO whose MediaInfo lists only English subtitle and audio tracks excludes
// the release before any grab: no NZB request, no job, no round slot, and the
// next candidate is searched immediately.
func TestNFOWithoutRequiredLanguageExcludesReleaseBeforeGrab(t *testing.T) {
	f := newNFOCheckFixture(t, "General\nComplete name : k.mkv\n\nAudio\nLanguage : English\n\nText #1\nLanguage : English\n\nText #2\nLanguage : English\n")
	ctx := context.Background()
	if err := f.service().Handle(ctx, f.event); err != nil {
		t.Fatalf("retrieval: %v", err)
	}
	if f.requests("getnfo") != 1 || f.requests("get") != 0 {
		t.Fatalf("getnfo=%d get=%d, want one NFO request and no grab", f.requests("getnfo"), f.requests("get"))
	}
	assertNoRetrievalAttempt(t, f.store, f.workflow.ID)
	current, err := f.store.GetWorkflow(ctx, f.workflow.ID)
	if err != nil || current.State != workflows.StateWaiting || current.SelectedRelease != nil {
		t.Fatalf("workflow=%+v err=%v", current, err)
	}
	var kind, reason string
	if err := f.store.Pool.QueryRow(ctx, `SELECT failure_kind,reason FROM workflow_release_exclusions WHERE workflow_id=$1`, f.workflow.ID.String()).Scan(&kind, &reason); err != nil {
		t.Fatal(err)
	}
	if kind != nfoRejectionKind || reason == "" {
		t.Fatalf("exclusion kind=%q reason=%q", kind, reason)
	}
	var searches int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND aggregate_revision=$2`, f.workflow.ID.String(), int64(current.Revision)).Scan(&searches); err != nil {
		t.Fatal(err)
	}
	if searches != 1 {
		t.Fatalf("queued searches=%d, want 1", searches)
	}
	if outcome := f.auditOutcome(t); outcome != "rejected" {
		t.Fatalf("audit outcome=%q", outcome)
	}
}

// An NFO that names Danish lets the grab proceed, and the run deferred by the
// NFO request's pacing goes straight to the NZB without a second NFO request.
func TestNFOWithRequiredLanguageProceedsAndIsCheckedOnce(t *testing.T) {
	f := newNFOCheckFixture(t, "RELEASE\n SUBTITLES.: DANISH, SWEDISH\n")
	ctx := context.Background()
	_ = f.service().Handle(ctx, f.event) // usually deferred: the NFO request took the pacing slot
	f.clearPace()
	_ = f.service().Handle(ctx, f.event)
	if f.requests("getnfo") != 1 {
		t.Fatalf("getnfo=%d, want exactly one", f.requests("getnfo"))
	}
	if f.requests("get") == 0 {
		t.Fatal("grab was not attempted after a passing NFO")
	}
	if outcome := f.auditOutcome(t); outcome != "passed" {
		t.Fatalf("audit outcome=%q", outcome)
	}
	var exclusions int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind=$2 AND reason LIKE 'release NFO%'`, f.workflow.ID.String(), nfoRejectionKind).Scan(&exclusions); err != nil {
		t.Fatal(err)
	}
	if exclusions != 0 {
		t.Fatalf("NFO exclusions=%d", exclusions)
	}
}

// A provider without the NFO ("No such item") is optional evidence: the grab
// proceeds and the indexer is not backed off.
func TestUnavailableNFODoesNotBackOffIndexer(t *testing.T) {
	f := newNFOCheckFixture(t, `<?xml version="1.0" encoding="UTF-8"?><error code="300" description="No such item"/>`)
	ctx := context.Background()
	_ = f.service().Handle(ctx, f.event)
	var failures int
	var outcome string
	if err := f.store.Pool.QueryRow(ctx, `SELECT consecutive_failure,last_outcome FROM indexer_budget_state WHERE indexer_instance_id=$1`, f.indexer.ID.String()).Scan(&failures, &outcome); err != nil {
		t.Fatal(err)
	}
	if failures != 0 || outcome != string(indexers.OutcomeNoResults) {
		t.Fatalf("after unavailable NFO: consecutive_failure=%d last_outcome=%q", failures, outcome)
	}
	f.clearPace()
	_ = f.service().Handle(ctx, f.event)
	if f.requests("getnfo") != 1 || f.requests("get") == 0 {
		t.Fatalf("getnfo=%d get=%d", f.requests("getnfo"), f.requests("get"))
	}
	if outcome := f.auditOutcome(t); outcome != "unavailable" {
		t.Fatalf("audit outcome=%q", outcome)
	}
}

// When the provider already stated subtitle languages, search evaluation
// decided the requirement: no NFO request is spent.
func TestProviderSubtitleAttributesSkipNFO(t *testing.T) {
	withSubs := func(base, title string) string {
		return strings.Replace(nfoReleaseXML(base, title), `<newznab:attr name="info"`, `<newznab:attr name="subs" value="Danish"/><newznab:attr name="info"`, 1)
	}
	f := newNFOCheckFixtureWithRelease(t, "Subtitles: English\n", withSubs)
	_ = f.service().Handle(context.Background(), f.event)
	if f.requests("getnfo") != 0 || f.requests("get") == 0 {
		t.Fatalf("getnfo=%d get=%d, want no NFO request and a grab", f.requests("getnfo"), f.requests("get"))
	}
}
