package acquisition

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

const twoKlausReleasesXML = `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>` +
	`<item><title>Klaus.2019.1080p.BluRay.x264-AAA</title><guid>aaa-guid</guid><newznab:attr name="resolution" value="1920x1080"/></item>` +
	`<item><title>Klaus.2019.1080p.WEB-DL.x264-BBB</title><guid>bbb-guid</guid><newznab:attr name="resolution" value="1920x1080"/></item>` +
	`</channel></rss>`

// rememberedEligibleScenario runs the first search (the provider returns two
// eligible releases), fails the selected one the way a broken download does,
// lets age adjust the other release's last sighting, and runs the next search,
// for which the provider returns nothing, as a scan under another of the
// title's names does. It returns the workflow after the second search and the
// candidate that was not tried first.
func rememberedEligibleScenario(t *testing.T, age time.Duration) (workflows.Workflow, string, *postgres.Store) {
	t.Helper()
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	var calls atomic.Int32
	server, _ := newMovieSearchServer(t, func(url.Values) string {
		if calls.Add(1) == 1 {
			return twoKlausReleasesXML
		}
		return emptySearchXML()
	})
	t.Cleanup(server.Close)
	indexer := configureMovieSearchIndexerNamed(t, store, server.URL, "Provider")
	workflow := createMovieSearchWorkflow(t, store)
	service := newTwoProviderSearchService(store)

	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: workflow.Revision}); err != nil {
		t.Fatalf("first search: %v", err)
	}
	selected, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil {
		t.Fatalf("first search workflow=%+v err=%v", selected, err)
	}
	var failedKey, otherID string
	if err := store.Pool.QueryRow(ctx, `SELECT native_key FROM release_candidates WHERE id=$1`, selected.SelectedRelease.String()).Scan(&failedKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM release_candidates WHERE workflow_id=$1 AND id<>$2`, workflow.ID.String(), selected.SelectedRelease.String()).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_release_exclusions(workflow_id,candidate_id,indexer_instance_id,native_key,reason,created_at,failure_kind) VALUES($1,$2,$3,$4,'output could not be verified',now(),'output_verification')`, workflow.ID.String(), selected.SelectedRelease.String(), indexer.ID.String(), failedKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE release_candidates SET last_observed_at=last_observed_at-$2::interval WHERE id=$1`, otherID, age.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='waiting',selected_release_id=NULL,revision=revision+1 WHERE id=$1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	clearIndexerBudget(t, store, indexer.ID)
	service = newTwoProviderSearchService(store)
	waiting, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: waiting.Revision}); err != nil {
		t.Fatalf("second search: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls=%d, want 2", calls.Load())
	}
	after, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	return after, otherID, store
}

// TestSearchTriesRememberedEligibleRelease reproduces Sons of the Desert: the
// next scan searched another name and returned nothing, so the remaining good
// release was never tried. It is now selected from memory, and its provider
// freshness and locator are left as the provider last returned them.
func TestSearchTriesRememberedEligibleRelease(t *testing.T) {
	after, otherID, store := rememberedEligibleScenario(t, time.Hour)
	if after.State != workflows.StateCandidateSelected || after.SelectedRelease == nil || after.SelectedRelease.String() != otherID {
		t.Fatalf("workflow after second search=%+v, want the remembered release %s selected", after, otherID)
	}
	var count int
	var locator string
	var age time.Duration
	var lastObserved time.Time
	if err := store.Pool.QueryRow(context.Background(), `SELECT observation_count,download_locator_ref,last_observed_at FROM release_candidates WHERE id=$1`, otherID).Scan(&count, &locator, &lastObserved); err != nil {
		t.Fatal(err)
	}
	age = time.Since(lastObserved)
	if count != 1 || locator != "native:bbb-guid" && locator != "native:aaa-guid" || age < 50*time.Minute {
		t.Fatalf("remembered candidate count=%d locator=%q age=%s, want provider evidence unchanged", count, locator, age)
	}
}

// TestSearchForgetsOldEligibleRelease keeps the memory bounded: a release no
// provider has returned for longer than the window is not tried.
func TestSearchForgetsOldEligibleRelease(t *testing.T) {
	after, _, _ := rememberedEligibleScenario(t, rememberedEligibleWindow+time.Hour)
	if after.State != workflows.StateWaiting || after.SelectedRelease != nil {
		t.Fatalf("workflow after second search=%+v, want waiting", after)
	}
}
