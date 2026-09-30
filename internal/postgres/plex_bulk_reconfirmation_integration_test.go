package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestReconfirmTimedOutPlexOnlyRequeuesTimedOutWorkflows proves the owner's
// bulk re-check queues exact-path confirmation for timed-out publications,
// leaves every other workflow untouched, and is idempotent.
func TestReconfirmTimedOutPlexOnlyRequeuesTimedOutWorkflows(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	timedOut, _, _, _ := publishTimedOutMovie(t, fixture, "Bulk Reconfirm Movie", "770001", "bulk-reconfirm")
	_, untouched := fixture.movieWorkflow(t, "Bulk Untouched Movie", "770002")

	result, err := fixture.store.ReconfirmTimedOutPlex(fixture.ctx, "owner", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Queued) != 1 || result.Queued[0] != timedOut || len(result.Skipped) != 0 {
		t.Fatalf("result=%+v, want only %s queued", result, timedOut)
	}
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, timedOut.String()).Scan(&state); err != nil || state != "plex_scanning" {
		t.Fatalf("timed-out workflow state=%q err=%v, want plex_scanning", state, err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, untouched.String()).Scan(&state); err != nil || state == "plex_scanning" {
		t.Fatalf("unrelated workflow state=%q err=%v, want unchanged", state, err)
	}

	again, err := fixture.store.ReconfirmTimedOutPlex(fixture.ctx, "owner", time.Now().UTC())
	if err != nil || len(again.Queued) != 0 || len(again.Skipped) != 0 {
		t.Fatalf("second pass=%+v err=%v, want nothing to do", again, err)
	}
}
