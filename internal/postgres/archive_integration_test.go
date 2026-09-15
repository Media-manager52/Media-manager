package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/workflows"
)

// TestArchiveMediaCancelsWorkflowsAndPreservesHistory proves the retirement
// semantics: confirmed unwanted media is removed from the active catalog and
// its non-terminal workflows are cancelled, while every durable row
// (transition, audit, workflow, media) stays inspectable and the operation is
// idempotent.
func TestArchiveMediaCancelsWorkflowsAndPreservesHistory(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Archive Candidate", "900801")

	result, err := fixture.store.ArchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyArchived || result.CancelledWorkflows != 1 || len(result.CancelledWorkflowIDs) != 1 || result.CancelledWorkflowIDs[0] != workflow.ID {
		t.Fatalf("archive result=%+v, want exactly the one active workflow cancelled", result)
	}

	var state string
	var terminal *time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state,terminal_at FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state, &terminal); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateCancelled) || terminal == nil {
		t.Fatalf("workflow state=%q terminal=%v, want cancelled and terminal", state, terminal)
	}

	var transitionActor, toState string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT actor,to_state FROM workflow_transitions WHERE workflow_id=$1 ORDER BY to_revision DESC LIMIT 1`, workflow.ID.String()).Scan(&transitionActor, &toState); err != nil {
		t.Fatal(err)
	}
	if transitionActor != "owner" || toState != string(workflows.StateCancelled) {
		t.Fatalf("last transition actor=%q to=%q, want owner -> cancelled", transitionActor, toState)
	}

	var deleted *time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT deleted_at FROM media_items WHERE id=$1`, workflow.MediaID.String()).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted == nil {
		t.Fatal("archived media was not soft-deleted")
	}

	var audit int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM audit_events WHERE action='media.archived' AND aggregate_id=$1`, workflow.MediaID.String()).Scan(&audit); err != nil || audit != 1 {
		t.Fatalf("media.archived audit events=%d err=%v, want 1", audit, err)
	}

	media, err := fixture.store.ListMedia(fixture.ctx, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range media {
		if item.ID == workflow.MediaID {
			t.Fatal("archived media still appears in the active catalog")
		}
	}

	again, err := fixture.store.ArchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC())
	if err != nil || !again.AlreadyArchived || again.CancelledWorkflows != 0 {
		t.Fatalf("second archive=%+v err=%v, want idempotent already-archived", again, err)
	}
}

// TestArchiveMediaRefusesPublishedFiles proves the protection gate: a media
// record with a current published link can never be archived (and therefore
// can never be unlinked) by this operation.
func TestArchiveMediaRefusesPublishedFiles(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	item, _ := fixture.publishedMovie(t, "Archive Protected", "900802")

	if _, err := fixture.store.ArchiveMedia(fixture.ctx, item.ID, time.Now().UTC()); !errors.Is(err, ErrMediaHasPublishedFiles) {
		t.Fatalf("archive with published files error=%v, want ErrMediaHasPublishedFiles", err)
	}
	var deleted *time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT deleted_at FROM media_items WHERE id=$1`, item.ID.String()).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted != nil {
		t.Fatal("refused archive still soft-deleted the media")
	}
}

// TestArchiveMediaRefusesInFlightJob proves an active AltMount acquisition
// cannot be archived out from under itself.
func TestArchiveMediaRefusesInFlightJob(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Archive In Flight", "900803")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "archive-in-flight")
	if _, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(33), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ArchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC()); !errors.Is(err, ErrMediaHasInFlightWork) {
		t.Fatalf("archive with in-flight job error=%v, want ErrMediaHasInFlightWork", err)
	}
}
