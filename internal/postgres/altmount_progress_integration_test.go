package postgres

import (
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/catalog"
)

// TestAltMountProgressIsReportedOnlyAsObserved proves the workflow read model
// carries exactly the percentage AltMount reported for the current attempt,
// never an estimate, and drops it when AltMount stops reporting a valid one.
func TestAltMountProgressIsReportedOnlyAsObserved(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	_, workflowID := fixture.movieWorkflow(t, "Progress Movie", "880001")
	job, _, _ := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflowID, fixture.altMount.ID, fixture.indexer.ID, 1, "progress-1")

	summary := func() WorkflowSummary {
		t.Helper()
		items, err := fixture.store.ListWorkflowSummaries(fixture.ctx, 500, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == workflowID {
				return item
			}
		}
		t.Fatalf("workflow %s missing from summaries", workflowID)
		return WorkflowSummary{}
	}
	if before := summary(); before.DownloadPercent != nil || before.DownloadStage != "" {
		t.Fatalf("progress before any observation = %v %q, want none", before.DownloadPercent, before.DownloadStage)
	}

	percent := 42
	observe := func(value *int, stage string) {
		t.Helper()
		loaded, err := fixture.store.LoadAltMountJob(fixture.ctx, job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		item := altmount.QueueItem{Status: "processing", Stage: stage, Percentage: value, UpdatedAt: time.Now().UTC()}
		if err := fixture.store.ObserveAltMountAndReschedule(fixture.ctx, loaded, item, altmount.PhaseStreamingReady, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}

	observe(&percent, "  Downloading  ")
	got := summary()
	if got.DownloadPercent == nil || *got.DownloadPercent != 42 || got.DownloadStage != "Downloading" || got.DownloadObservedAt == nil {
		t.Fatalf("progress = %v %q %v, want 42 Downloading with a timestamp", got.DownloadPercent, got.DownloadStage, got.DownloadObservedAt)
	}

	invalid := 150
	observe(&invalid, "")
	if got := summary(); got.DownloadPercent != nil || got.DownloadStage != "" {
		t.Fatalf("out-of-range progress = %v %q, want none", got.DownloadPercent, got.DownloadStage)
	}

	observe(&percent, "Downloading")
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE workflows SET state='verifying' WHERE id=$1`, workflowID.String()); err != nil {
		t.Fatal(err)
	}
	if got := summary(); got.DownloadPercent != nil {
		t.Fatalf("progress after the download finished = %v, want none", *got.DownloadPercent)
	}
}
