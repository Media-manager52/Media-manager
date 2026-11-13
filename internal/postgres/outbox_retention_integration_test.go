package postgres

import (
	"testing"
	"time"

	"uuid"
)

// TestDeleteCompletedOutboxHistoryKeepsEverythingDedupeReliesOn proves the
// retention removes only superseded, old, completed events of the listed
// types, and keeps every event an enqueue path could still compare against:
// the newest event per aggregate and type, events at or after the aggregate's
// current revision, recent and pending events, and every other event type.
func TestDeleteCompletedOutboxHistoryKeepsEverythingDedupeReliesOn(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx, store := fixture.ctx, fixture.store
	workflow := fixture.newMovieWorkflow(t, "Outbox Retention", "900901")
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET revision=5 WHERE id=$1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=$1`, workflow.ID.String()); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	old := now.Add(-OutboxHistoryRetention - time.Hour)
	recent := now.Add(-time.Hour)
	insert := func(aggregateType string, aggregateID uuid.UUID, revision int64, eventType string, completed *time.Time) uuid.UUID {
		t.Helper()
		id := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at,completed_at) VALUES($1,$2,$3,$4,$5,'{}',$6,$6,$7)`,
			id.String(), aggregateType, aggregateID.String(), revision, eventType, old.Add(-time.Hour), completed); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Workflow at revision 5.
	supersededSearch := insert("workflow", workflow.ID, 2, "workflow.search_requested", &old)
	recentSearch := insert("workflow", workflow.ID, 3, "workflow.search_requested", &recent)
	currentRevisionSearch := insert("workflow", workflow.ID, 5, "workflow.search_requested", &old) // workflow has not moved past it
	pendingSearch := insert("workflow", workflow.ID, 6, "workflow.search_requested", nil)
	supersededTransition := insert("workflow", workflow.ID, 4, "workflow.transitioned", &old)
	newestTransition := insert("workflow", workflow.ID, 5, "workflow.transitioned", &old)
	// Never-removed type, although old and superseded.
	statusSyncOld := insert("workflow", workflow.ID, 2, "seerr.status_sync_requested", &old)
	statusSyncNewer := insert("workflow", workflow.ID, 3, "seerr.status_sync_requested", &old)

	// Aggregates whose rows no longer exist: only the newest event stays.
	discovery := uuid.NewV7()
	discoveryOld := insert("workflow_retry_discovery", discovery, 1, "workflow.discovery_rescan_requested", &old)
	discoveryNewest := insert("workflow_retry_discovery", discovery, 2, "workflow.discovery_rescan_requested", &old)
	delivery := uuid.NewV7()
	deliveryOld := insert("seerr_delivery", delivery, 1, "seerr.reconcile", &old)
	deliveryNewest := insert("seerr_delivery", delivery, 2, "seerr.reconcile", &old)
	// A listed event type under an unexpected aggregate type is never removed.
	other := uuid.NewV7()
	otherOld := insert("test", other, 1, "workflow.search_requested", &old)
	otherNewer := insert("test", other, 2, "workflow.search_requested", &old)

	removed, err := store.DeleteCompletedOutboxHistory(ctx, now.Add(-OutboxHistoryRetention), 10)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 4 {
		t.Fatalf("removed=%d, want 4", removed)
	}
	exists := func(id uuid.UUID) bool {
		t.Helper()
		var found bool
		if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_events WHERE id=$1)`, id.String()).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	for name, id := range map[string]uuid.UUID{"superseded search": supersededSearch, "superseded transition": supersededTransition, "old discovery": discoveryOld, "old delivery": deliveryOld} {
		if exists(id) {
			t.Errorf("%s was kept", name)
		}
	}
	for name, id := range map[string]uuid.UUID{
		"recent search": recentSearch, "current revision search": currentRevisionSearch, "pending search": pendingSearch,
		"newest transition": newestTransition, "old status sync": statusSyncOld, "newer status sync": statusSyncNewer,
		"newest discovery": discoveryNewest, "newest delivery": deliveryNewest, "other old": otherOld, "other newer": otherNewer,
	} {
		if !exists(id) {
			t.Errorf("%s was removed", name)
		}
	}

	// The Seerr reconcile path numbers its next event max(revision)+1; that
	// number must not move back.
	var next int64
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(max(aggregate_revision),0)+1 FROM outbox_events WHERE aggregate_type='seerr_delivery' AND aggregate_id=$1 AND event_type='seerr.reconcile'`, delivery.String()).Scan(&next); err != nil || next != 3 {
		t.Fatalf("next seerr revision=%d err=%v, want 3", next, err)
	}

	// A second run finds nothing more.
	if again, err := store.DeleteCompletedOutboxHistory(ctx, now.Add(-OutboxHistoryRetention), 10); err != nil || again != 0 {
		t.Fatalf("second run removed=%d err=%v, want 0", again, err)
	}
}
