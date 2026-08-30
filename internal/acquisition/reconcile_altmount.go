package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

type AltMountReconcileService struct {
	Store         *postgres.Store
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
}

const (
	altMountSubmissionVisibilityWindow = 30 * time.Minute
	altMountCompletedOutputGrace       = 5 * time.Minute
)

func (service *AltMountReconcileService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	job, err := service.Store.LoadAltMountJob(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if event.AggregateRevision != job.JobRevision || job.CurrentAttemptID == nil || *job.CurrentAttemptID != job.JobID || terminalAltMountJob(job.JobState) || job.Workflow.State == workflows.StateNeedsAttention || job.Workflow.State == workflows.StateCancelled {
		return nil
	}
	var config altMountConfig
	if err := json.Unmarshal(job.Client.Config, &config); err != nil {
		return err
	}
	secretValue, err := service.ResolveSecret(job.Client.SecretRef)
	if err != nil {
		return err
	}
	var secret altMountSecret
	if err := json.Unmarshal([]byte(secretValue), &secret); err != nil {
		return err
	}
	client, err := altmount.New(altmount.Config{BaseURL: config.BaseURL, Username: secret.Username, Password: secret.Password, AllowHTTP: config.AllowHTTP, RequestTimeout: time.Duration(config.RequestTimeoutMS) * time.Millisecond}, service.Transport)
	if err != nil {
		return err
	}
	if _, err := client.Login(ctx); err != nil {
		return err
	}
	if job.ExternalQueueID == nil {
		page, listErr := client.ListQueue(ctx, altmount.QueueQuery{Search: job.UploadName, Limit: 100, SortBy: "updated_at", SortOrder: "desc"})
		if listErr != nil {
			return listErr
		}
		matches := []altmount.QueueItem{}
		for _, item := range page.Items {
			if item.NZBDisplayName == job.UploadName || filepath.Base(item.NZBPath) == job.UploadName {
				matches = append(matches, item)
			}
		}
		if len(matches) == 0 {
			now := time.Now().UTC()
			if now.Before(job.SubmissionStartedAt.Add(altMountSubmissionVisibilityWindow)) {
				return service.Store.ScheduleAltMountReconcile(ctx, job, now.Add(10*time.Second))
			}
			return service.Store.CompleteImportWithCode(ctx, job, false, "altmount_identity_unresolved", "AltMount did not expose the stable upload name within 30 minutes. Do not start another search until the owner confirms the original upload was not accepted.", now)
		}
		if len(matches) > 1 {
			return service.Store.CompleteImport(ctx, job, false, "multiple AltMount jobs match the stable upload name", time.Now().UTC())
		}
		queueID := int64(matches[0].ID)
		if err := service.Store.AttachRecoveredQueue(ctx, job, queueID, time.Now().UTC()); err != nil {
			return err
		}
		job, err = service.Store.LoadAltMountJob(ctx, job.JobID)
		if err != nil {
			return err
		}
		if job.CurrentAttemptID == nil || *job.CurrentAttemptID != job.JobID || terminalAltMountJob(job.JobState) {
			return nil
		}
	}
	item, err := client.Queue(ctx, altmount.QueueID(*job.ExternalQueueID))
	if err != nil {
		return err
	}
	output := altmount.OutputObservation{}
	var inventory altmount.Inventory
	if item.StoragePath != nil && strings.TrimSpace(*item.StoragePath) != "" {
		inventory, err = altmount.Enumerate(altmount.OutputMapping{ReportedRoot: config.ReportedRoot, LocalReadOnlyRoot: config.LocalReadOnlyRoot}, *item.StoragePath)
		if err == nil {
			output = altmount.OutputObservation{Mapped: true, Exists: true, Readable: true, InventoryComplete: true}
		}
	}
	if item.Status == altmount.StatusCompleted && err != nil {
		completedAt := item.UpdatedAt
		if item.CompletedAt != nil && !item.CompletedAt.IsZero() {
			completedAt = *item.CompletedAt
		}
		now := time.Now().UTC()
		if completedAt.IsZero() || now.Before(completedAt.Add(altMountCompletedOutputGrace)) {
			return service.Store.ScheduleAltMountReconcile(ctx, job, now.Add(15*time.Second))
		}
		return service.Store.CompleteImportWithCode(ctx, job, false, "altmount_output_unavailable", "AltMount reported completion, but its read-only output was still unavailable after five minutes. Verify the shared-folder mount before retrying.", now)
	}
	phase := altmount.Reconcile(altmount.ReconcileInput{Queue: &item, Output: output})
	switch phase {
	case altmount.PhaseQueued, altmount.PhasePaused, altmount.PhaseProcessing, altmount.PhaseStreamingReady:
		return service.Store.ObserveAltMountAndReschedule(ctx, job, item, phase, time.Now().Add(10*time.Second))
	case altmount.PhaseFailed, altmount.PhaseNeedsAttention, altmount.PhaseUnknown:
		detail := "AltMount job needs attention"
		if item.ErrorMessage != nil {
			detail = *item.ErrorMessage
		}
		return service.Store.CompleteImport(ctx, job, false, detail, time.Now().UTC())
	case altmount.PhaseImportComplete:
		if len(inventory.Files) == 0 {
			return service.Store.CompleteImport(ctx, job, false, "AltMount completed without a readable media inventory", time.Now().UTC())
		}
		inventoryID, assets, persistErr := service.Store.PersistAltMountInventory(ctx, job, inventory, time.Now().UTC())
		if persistErr != nil {
			return persistErr
		}
		if job.Media.Kind == catalog.KindSeries {
			expectationID, seriesID, season, expected, expectationErr := service.Store.PackExpectation(ctx, job.Workflow.ID)
			if expectationErr != nil {
				return service.Store.CompleteImport(ctx, job, false, "season-pack expectation is missing", time.Now().UTC())
			}
			report := packs.Evaluate(seriesID, season, expected, assets)
			if err := service.Store.PersistPackEvaluation(ctx, expectationID, inventoryID, report, time.Now().UTC()); err != nil {
				return err
			}
			if !report.Publishable() {
				return service.Store.CompleteImport(ctx, job, false, fmt.Sprintf("season pack is %s; %d expected episodes are missing", report.Verdict, len(report.Missing)), time.Now().UTC())
			}
			return service.Store.CompleteImport(ctx, job, true, "season pack inventory verified", time.Now().UTC())
		}
		if !hasOneDominantMovie(inventory.Files) {
			return service.Store.CompleteImport(ctx, job, false, "movie release does not have one unambiguous primary media file", time.Now().UTC())
		}
		return service.Store.CompleteImport(ctx, job, true, "movie inventory verified", time.Now().UTC())
	}
	return errors.New("unhandled AltMount phase")
}

func terminalAltMountJob(state string) bool {
	switch state {
	case "import_complete", "failed", "cancelled", "removed", "needs_attention":
		return true
	default:
		return false
	}
}

func hasOneDominantMovie(files []altmount.OutputFile) bool {
	media := make([]altmount.OutputFile, 0, len(files))
	for _, file := range files {
		switch strings.ToLower(filepath.Ext(file.RelativePath)) {
		case ".mkv", ".mp4", ".m4v", ".avi", ".ts":
			if !strings.Contains(strings.ToLower(file.RelativePath), "sample") {
				media = append(media, file)
			}
		}
	}
	if len(media) == 1 {
		return true
	}
	if len(media) == 0 {
		return false
	}
	sort.Slice(media, func(i, j int) bool { return media[i].Size > media[j].Size })
	return media[0].Size > 0 && media[1].Size*5 < media[0].Size
}
