package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestClaimOutboxPrefersLifecycleWorkOverSearchBacklog proves a large, older
// search backlog cannot delay due lifecycle work such as Plex confirmation,
// and that evidence-only discovery still comes last.
func TestClaimOutboxPrefersLifecycleWorkOverSearchBacklog(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	insert := func(eventType string, age time.Duration) uuid.UUID {
		t.Helper()
		id := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,$3,'{}',$4,now())`, id.String(), uuid.NewV7().String(), eventType, time.Now().UTC().Add(-age)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	discovery := insert("workflow.discovery_rescan_requested", 3*time.Hour)
	oldSearch := insert("workflow.search_requested", 2*time.Hour)
	newSearch := insert("workflow.search_requested", time.Hour)
	reconcile := insert("altmount.reconcile_requested", time.Minute)
	confirm := insert("plex.confirm_requested", time.Second)
	// Lifecycle work that keeps failing (here a repair sweep while Plex
	// hangs) goes after healthy lifecycle work, though it is the oldest, and
	// still before the search backlog.
	failing := insert("altmount.repair_sweep_requested", 4*time.Hour)
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET attempts=$2 WHERE id=$1`, failing.String(), outboxFailingAttempts); err != nil {
		t.Fatal(err)
	}
	// Not yet due work is never claimed early, whatever its class.
	if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,'plex.confirm_requested','{}',now()-interval '1 day',now()+interval '1 hour')`, uuid.NewV7().String(), uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}

	for _, want := range []uuid.UUID{reconcile, confirm, failing, oldSearch, newSearch, discovery} {
		claimed, err := store.ClaimOutbox(ctx, "test-owner", 1, time.Minute, false)
		if err != nil || len(claimed) != 1 || claimed[0].ID != want {
			t.Fatalf("claimed=%+v err=%v, want %s", claimed, err, want)
		}
		if err := store.CompleteOutbox(ctx, claimed[0].ID, "test-owner"); err != nil {
			t.Fatal(err)
		}
	}
	if claimed, err := store.ClaimOutbox(ctx, "test-owner", 1, time.Minute, false); err != nil || len(claimed) != 0 {
		t.Fatalf("not-yet-due event claimed=%+v err=%v", claimed, err)
	}
}

// TestRepairSweepsDoNotPileUpWhileOneIsOpen proves a sweep that has not
// finished (for example because Plex hangs) keeps later half-hours from
// queueing more sweeps, and that a new one is queued once it finished.
func TestRepairSweepsDoNotPileUpWhileOneIsOpen(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	count := func() int {
		t.Helper()
		var open int
		if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='altmount.repair_sweep_requested' AND completed_at IS NULL`).Scan(&open); err != nil {
			t.Fatal(err)
		}
		return open
	}
	now := time.Now().UTC()
	if _, err := store.QueueAltMountRepairSweeps(ctx, now); err != nil {
		t.Fatal(err)
	}
	first := count()
	if first == 0 {
		t.Fatal("no repair sweep queued")
	}
	for half := 1; half <= 3; half++ {
		if _, err := store.QueueAltMountRepairSweeps(ctx, now.Add(time.Duration(half)*30*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(); got != first {
		t.Fatalf("open sweeps=%d after three more half-hours, want %d (no pile-up)", got, first)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=now() WHERE event_type='altmount.repair_sweep_requested'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueAltMountRepairSweeps(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != first {
		t.Fatalf("open sweeps after the earlier one finished=%d, want %d", got, first)
	}
}

// TestPlexConfirmationWindowStartsAtRefresh proves time a planned scan spent
// waiting in the outbox does not count against Plex: requesting the refresh
// restarts the confirmation window from that moment.
func TestPlexConfirmationWindowStartsAtRefresh(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	_, workflowID := fixture.movieWorkflow(t, "Delayed Scan Movie", "570001")
	job, _, _ := fixture.seedVerifiedMovieOutput(t, workflowID, 1, "delayed-scan")
	sourceFile := fixture.localRoot + "/completed/delayed-scan/Movie.mkv"
	if err := os.MkdirAll(fixture.localRoot+"/completed/delayed-scan", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("read-only altmount output"), 0o444); err != nil {
		t.Fatal(err)
	}
	publicationContext, err := fixture.store.LoadPublicationContext(fixture.ctx, workflowID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	scanID, _, _, _ := fixture.publishMoviePlans(t, workflowID, job.JobID, publicationContext)
	// The scan event waited behind other work until after its planned deadline.
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE plex_scan_requests SET requested_at=now()-interval '30 minutes',deadline_at=now()-interval '10 minutes' WHERE id=$1`, scanID.String()); err != nil {
		t.Fatal(err)
	}
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, before); err != nil {
		t.Fatal(err)
	}
	var state string
	var deadline time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,deadline_at FROM plex_scan_requests WHERE id=$1`, scanID.String()).Scan(&state, &deadline); err != nil {
		t.Fatal(err)
	}
	if state != "polling" || deadline.Before(before.Add(plexConfirmationWindow-time.Second)) || deadline.After(time.Now().UTC().Add(plexConfirmationWindow)) {
		t.Fatalf("refreshed scan state=%q deadline=%s, want about %s", state, deadline, before.Add(plexConfirmationWindow))
	}
}

// Both indexer classes must get a turn while lifecycle work remains first.
// A preferred but leased/not-yet-due event must not prevent useful work.
func TestClaimOutboxAlternatesIndexerPriorityWithoutDelayingLifecycle(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()
	insert := func(kind string, age time.Duration) uuid.UUID {
		t.Helper()
		id := uuid.NewV7()
		_, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,$3,'{}',$4,now())`, id.String(), uuid.NewV7().String(), kind, time.Now().Add(-age))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	oldDiscovery := insert("workflow.discovery_rescan_requested", 8*time.Hour)
	newDiscovery := insert("workflow.discovery_rescan_requested", 7*time.Hour)
	oldSearch := insert("workflow.search_requested", 2*time.Hour)
	newSearch := insert("workflow.search_requested", time.Hour)
	future := insert("workflow.discovery_rescan_requested", 24*time.Hour)
	leased := insert("workflow.discovery_rescan_requested", 25*time.Hour)
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET available_at=now()+interval '1 hour' WHERE id=$1`, future.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET lease_owner='other-worker',lease_until=now()+interval '1 hour' WHERE id=$1`, leased.String()); err != nil {
		t.Fatal(err)
	}
	lifecycle := insert("plex.confirm_requested", time.Second)
	for _, step := range []struct {
		preferDiscovery bool
		want            uuid.UUID
	}{
		{true, lifecycle},
		{false, oldSearch},
		{true, oldDiscovery},
		{false, newSearch},
		{true, newDiscovery},
	} {
		got, err := store.ClaimOutbox(ctx, "fair-worker", 1, time.Minute, step.preferDiscovery)
		if err != nil || len(got) != 1 || got[0].ID != step.want {
			t.Fatalf("preference=%v got=%+v err=%v want=%s", step.preferDiscovery, got, err, step.want)
		}
		if step.want == oldSearch {
			// A pacing deferral stays in the queue and must not block the
			// following discovery turn or be re-claimed before its deadline.
			err = store.DeferOutbox(ctx, step.want, "fair-worker", "indexer_pacing_deferred", time.Now().Add(time.Hour))
		} else {
			err = store.CompleteOutbox(ctx, step.want, "fair-worker")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.ClaimOutbox(ctx, "fair-worker", 1, time.Minute, true); err != nil || len(got) != 0 {
		t.Fatalf("claimed deferred or leased work: %+v, %v", got, err)
	}
	// Preference is not a filter: if discovery is preferred but absent,
	// an available search still runs.
	fallback := insert("workflow.search_requested", time.Second)
	got, err := store.ClaimOutbox(ctx, "fair-worker", 1, time.Minute, true)
	if err != nil || len(got) != 1 || got[0].ID != fallback {
		t.Fatalf("fallback=%+v err=%v", got, err)
	}
}

// TestWorkInProgressIsNotReportedAsRetrying proves an event that is simply
// being handled (claimed once, still leased) is not counted as retrying in
// the queue, task, health or problem views; one that failed and waits for its
// next try still is.
func TestWorkInProgressIsNotReportedAsRetrying(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,'backup.create_requested','{}',now(),now())`, uuid.NewV7().String(), uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox(ctx, "test-owner", 1, time.Hour, false)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	retrying := func() (int64, int64, int64, int) {
		t.Helper()
		queue, err := store.QueueSnapshot(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		var queueRetrying int64
		for _, item := range queue.ByType {
			queueRetrying += item.Retrying
		}
		tasks, err := store.ListTaskStatuses(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var taskRetrying int64
		for _, task := range tasks {
			taskRetrying += task.Retrying
		}
		health, err := store.SystemHealth(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		problems, err := store.Problems(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		workerProblems := 0
		for _, problem := range problems.Problems {
			if problem.Code == "worker.retrying" {
				workerProblems++
			}
		}
		return queueRetrying, taskRetrying, health.Outbox.Retrying, workerProblems
	}
	if queue, tasks, health, problems := retrying(); queue != 0 || tasks != 0 || health != 0 || problems != 0 {
		t.Fatalf("work in progress counted as retrying: queue=%d tasks=%d health=%d problems=%d", queue, tasks, health, problems)
	}
	if err := store.FailOutbox(ctx, claimed[0].ID, "test-owner", "boom", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if queue, tasks, health, problems := retrying(); queue != 1 || tasks != 1 || health != 1 || problems != 1 {
		t.Fatalf("a failed event waiting for its retry: queue=%d tasks=%d health=%d problems=%d, want 1 each", queue, tasks, health, problems)
	}
}
