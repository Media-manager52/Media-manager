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
	// Not yet due work is never claimed early, whatever its class.
	if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,'plex.confirm_requested','{}',now()-interval '1 day',now()+interval '1 hour')`, uuid.NewV7().String(), uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}

	for _, want := range []uuid.UUID{reconcile, confirm, oldSearch, newSearch, discovery} {
		claimed, err := store.ClaimOutbox(ctx, "test-owner", 1, time.Minute)
		if err != nil || len(claimed) != 1 || claimed[0].ID != want {
			t.Fatalf("claimed=%+v err=%v, want %s", claimed, err, want)
		}
		if err := store.CompleteOutbox(ctx, claimed[0].ID, "test-owner"); err != nil {
			t.Fatal(err)
		}
	}
	if claimed, err := store.ClaimOutbox(ctx, "test-owner", 1, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("not-yet-due event claimed=%+v err=%v", claimed, err)
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
