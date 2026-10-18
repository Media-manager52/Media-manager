package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func (service *AltMountReconcileService) HandleRetainedMovieInspection(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		JobID      string `json:"job_id"`
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		return errors.New("retained movie has no valid job")
	}
	workflowID, err := uuid.Parse(payload.WorkflowID)
	if err != nil || workflowID != event.AggregateID {
		return errors.New("retained movie has no valid workflow")
	}
	job, err := service.Store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Workflow.ID != workflowID || job.Workflow.Revision != event.AggregateRevision || job.Workflow.State != workflows.StateQueued || job.CurrentAttemptID == nil || *job.CurrentAttemptID != jobID {
		return nil
	}
	retained, err := service.Store.LoadRetainedMovieOutput(ctx, workflowID, jobID)
	if errors.Is(err, postgres.ErrConcurrentModification) {
		return nil
	}
	if err != nil {
		return err
	}
	selected := selectedMovieAssets(retained.Assets)
	media, err := service.Store.GetMedia(ctx, job.Media.ID)
	if err != nil {
		return err
	}
	if media.Kind != catalog.KindMovie || len(selected) != 1 || !mediaTitleMatches(filepath.Base(selected[0].RelativePath), media) {
		return service.Store.CompleteReleaseFailure(ctx, job, "movie_primary_ambiguous", "Retained movie still has no single primary file matching a saved title; output remains retained.", time.Now().UTC())
	}
	var config altMountConfig
	if err := json.Unmarshal(job.Client.Config, &config); err != nil {
		return err
	}
	authorizedRoot, rootErr := authorizedOutputRoot(config.LocalReadOnlyRoot, retained.RootRelative)
	verification, inspectionErr := inspectSubtitleRequirements(ctx, authorizedRoot, selected, subtitleAssets(retained.Assets), retained.Policy, service.MediaInfoProbe, rootErr)
	if inspectionErr != nil {
		if deferral := service.inspectionDeferral(config.LocalReadOnlyRoot, inspectionErr); deferral != nil {
			return deferral
		}
		return service.Store.CompleteImportWithCode(ctx, job, false, "output_inspection_failed", inspectionErr.Error(), time.Now().UTC())
	}
	recorded, err := service.Store.RecordSubtitleVerification(ctx, job, retained.InventoryID, verification, time.Now().UTC())
	if err != nil {
		return err
	}
	if recorded.Status != "verified" {
		return nil
	}
	return service.Store.CompleteImport(ctx, job, true, "retained movie primary file and subtitle requirements verified", time.Now().UTC())
}
