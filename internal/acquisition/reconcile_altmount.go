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
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

type AltMountReconcileService struct {
	Store          *postgres.Store
	ResolveSecret  SecretResolver
	Transport      http.RoundTripper
	MediaInfoProbe interface {
		InspectWithin(context.Context, string, string) (mediainfo.Evidence, error)
	}
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
	client, err := newAltMountClient(config, job.Client.SecretRef, service.ResolveSecret, service.Transport)
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
			return service.Store.CompleteImportWithCode(ctx, job, false, "altmount_identity_ambiguous", "Multiple AltMount jobs match the stable upload name. Reconcile the original submission before another acquisition.", time.Now().UTC())
		}
		queueID := int64(matches[0].ID)
		if err := service.Store.AttachRecoveredQueue(ctx, job, queueID, time.Now().UTC()); err != nil {
			return err
		}
		// Queue attachment advances the job revision and durably schedules a
		// matching reconciliation event. Finish this older event so any later
		// error cannot be swallowed as a stale retry.
		return nil
	}
	item, err := client.Queue(ctx, altmount.QueueID(*job.ExternalQueueID))
	if err != nil {
		var apiError altmount.APIError
		if errors.As(err, &apiError) && apiError.StatusCode == http.StatusNotFound && strings.TrimSpace(job.ErrorDetail) != "" {
			// AltMount already pruned the failed queue item, so the persisted
			// failure detail is the only durable evidence. Route it exactly like
			// a live failure instead of retrying the reconcile forever.
			detail := job.ErrorDetail
			if failureCode, releaseAttributable := releaseAttributableQueueFailure(detail); releaseAttributable {
				return service.Store.CompleteReleaseFailure(ctx, job, failureCode, detail, time.Now().UTC())
			}
			attentionCode := job.ErrorCode
			if attentionCode == "" {
				attentionCode = "altmount_job_attention"
			}
			return service.Store.CompleteImportWithCode(ctx, job, false, attentionCode, detail, time.Now().UTC())
		}
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
		if output.InventoryComplete {
			if _, _, persistErr := service.Store.PersistAltMountInventory(ctx, job, inventory, time.Now().UTC()); persistErr != nil {
				return persistErr
			}
		}
		detail := "AltMount job needs attention"
		if item.ErrorMessage != nil {
			detail = *item.ErrorMessage
		}
		if failureCode, releaseAttributable := releaseAttributableQueueFailure(detail); releaseAttributable {
			return service.Store.CompleteReleaseFailure(ctx, job, failureCode, detail, time.Now().UTC())
		}
		return service.Store.CompleteImportWithCode(ctx, job, false, "altmount_job_attention", detail, time.Now().UTC())
	case altmount.PhaseImportComplete:
		inventoryID, assets, persistErr := service.Store.PersistAltMountInventory(ctx, job, inventory, time.Now().UTC())
		if persistErr != nil {
			return persistErr
		}
		if len(inventory.Files) == 0 {
			return service.Store.CompleteReleaseFailure(ctx, job, "output_empty", "AltMount completed this release without a readable media inventory", time.Now().UTC())
		}
		var selected []subtitles.Asset
		detail := "movie inventory verified"
		if job.Media.Kind == catalog.KindSeries {
			expectationID, seriesID, season, expected, expectationErr := service.Store.PackExpectation(ctx, job.Workflow.ID)
			if expectationErr != nil {
				return service.Store.CompleteImportWithCode(ctx, job, false, "season_expectation_missing", "The immutable season-pack expectation is missing; owner recovery is required.", time.Now().UTC())
			}
			report := packs.Evaluate(seriesID, season, expected, assets)
			if err := service.Store.PersistPackEvaluation(ctx, expectationID, inventoryID, report, time.Now().UTC()); err != nil {
				return err
			}
			if !report.Publishable() {
				return service.Store.CompleteReleaseFailure(ctx, job, "season_pack_incomplete", fmt.Sprintf("season pack is %s; %d expected episodes are missing", report.Verdict, len(report.Missing)), time.Now().UTC())
			}
			selected = selectedPackAssets(report, assets)
			detail = "season pack inventory and subtitle requirements verified"
		} else {
			selected = selectedMovieAssets(assets)
			if len(selected) != 1 {
				return service.Store.CompleteReleaseFailure(ctx, job, "movie_primary_ambiguous", "movie release does not have one unambiguous primary media file", time.Now().UTC())
			}
			detail = "movie inventory and subtitle requirements verified"
		}
		policy, policyErr := service.Store.SubtitlePolicyForJob(ctx, job.JobID)
		if policyErr != nil {
			return policyErr
		}
		allAssets := subtitleAssets(assets)
		authorizedRoot, rootErr := authorizedOutputRoot(config.LocalReadOnlyRoot, inventory.LocalRelative)
		verification, inspectionErr := inspectSubtitleRequirements(ctx, authorizedRoot, selected, allAssets, policy, service.MediaInfoProbe, rootErr)
		if inspectionErr != nil {
			return service.Store.CompleteImportWithCode(ctx, job, false, "output_inspection_failed", inspectionErr.Error(), time.Now().UTC())
		}
		recorded, recordErr := service.Store.RecordSubtitleVerification(ctx, job, inventoryID, verification, time.Now().UTC())
		if recordErr != nil {
			return recordErr
		}
		if recorded.Status != "verified" {
			return nil
		}
		return service.Store.CompleteImport(ctx, job, true, detail, time.Now().UTC())
	}
	return errors.New("unhandled AltMount phase")
}

func (service *AltMountReconcileService) HandleRetainedOutputReverification(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		WorkflowID string `json:"workflow_id"`
		JobID      string `json:"job_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	workflowID, err := uuid.Parse(payload.WorkflowID)
	if err != nil || workflowID != event.AggregateID {
		return errors.New("retained-output reverification has no valid workflow")
	}
	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		return errors.New("retained-output reverification has no valid AltMount job")
	}
	job, err := service.Store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Workflow.ID != workflowID || job.Workflow.Revision != event.AggregateRevision || job.Workflow.State != workflows.StateVerifying || job.CurrentAttemptID == nil || *job.CurrentAttemptID != jobID || job.CandidateID == (uuid.UUID{}) {
		return nil
	}
	retained, err := service.Store.LoadRetainedSubtitleOutput(ctx, workflowID, jobID)
	if errors.Is(err, postgres.ErrConcurrentModification) {
		return nil
	}
	if err != nil {
		return err
	}
	selected := retained.Selected
	if retained.MediaKind == catalog.KindMovie {
		selected = selectedMovieAssets(retained.Assets)
	}
	allAssets := subtitleAssets(retained.Assets)
	var config altMountConfig
	if err := json.Unmarshal(job.Client.Config, &config); err != nil {
		return err
	}
	authorizedRoot, rootErr := authorizedOutputRoot(config.LocalReadOnlyRoot, retained.RootRelative)
	verification, inspectionErr := inspectSubtitleRequirements(ctx, authorizedRoot, selected, allAssets, retained.Policy, service.MediaInfoProbe, rootErr)
	if inspectionErr != nil {
		return inspectionErr
	}
	return service.Store.CompleteRetainedSubtitleReverification(ctx, job, event.AggregateRevision, retained.InventoryID, verification, time.Now().UTC())
}

func inspectSubtitleRequirements(ctx context.Context, authorizedRoot string, selected, inventory []subtitles.Asset, policy subtitles.Policy, probe subtitles.Probe, inspectionErr error) (subtitles.Verification, error) {
	if inspectionErr == nil {
		verification, err := subtitles.Verify(ctx, authorizedRoot, selected, inventory, policy, probe)
		if err == nil {
			return verification, nil
		}
		inspectionErr = err
	}
	detail := "completed AltMount output could not be inspected safely"
	if inspectionErr != nil {
		detail = inspectionErr.Error()
	}
	return subtitles.Verification{}, errors.New(detail)
}

func terminalAltMountJob(state string) bool {
	switch state {
	case "import_complete", "failed", "cancelled", "removed", "needs_attention":
		return true
	default:
		return false
	}
}

// releaseAttributableQueueFailure maps AltMount queue errors that identify a
// defective release payload to a release failure code. Those failures exclude
// the exact release and package and retry another candidate inside the open
// acquisition round instead of parking the workflow as infrastructure
// attention. Errors that do not match stay owner-visible infrastructure
// attention items.
func releaseAttributableQueueFailure(detail string) (string, bool) {
	normalized := strings.ToLower(detail)
	switch {
	case strings.Contains(normalized, "compressed files are not supported"),
		strings.Contains(normalized, "failed to iterate rar archive"),
		strings.Contains(normalized, "password protected"),
		strings.Contains(normalized, "rar2") && strings.Contains(normalized, "compression"):
		return releases.PackageFailureArchiveUnsupported, true
	case strings.Contains(normalized, "no files with allowed extensions"):
		return releases.PackageFailureNoPlayableMedia, true
	case strings.Contains(normalized, "incomplete or missing parts"):
		return releases.PackageFailureReleaseIncomplete, true
	}
	return "", false
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

func selectedMovieAssets(assets []packs.ObservedAsset) []subtitles.Asset {
	media := make([]packs.ObservedAsset, 0, len(assets))
	for _, asset := range assets {
		if playableOutputAsset(asset.RelativePath) && !strings.Contains(strings.ToLower(asset.RelativePath), "sample") {
			media = append(media, asset)
		}
	}
	sort.Slice(media, func(i, j int) bool { return media[i].Size > media[j].Size })
	if len(media) == 0 || len(media) > 1 && media[1].Size*5 >= media[0].Size {
		return nil
	}
	return []subtitles.Asset{{ID: media[0].ID, RelativePath: media[0].RelativePath, Size: media[0].Size}}
}

func playableOutputAsset(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mp4", ".m4v", ".avi", ".ts":
		return true
	}
	return false
}

func selectedPackAssets(report packs.Report, assets []packs.ObservedAsset) []subtitles.Asset {
	byID := make(map[uuid.UUID]packs.ObservedAsset, len(assets))
	for _, asset := range assets {
		byID[asset.ID] = asset
	}
	seen := map[uuid.UUID]bool{}
	result := []subtitles.Asset{}
	for _, group := range report.Groups {
		for _, assetID := range group.AssetIDs {
			asset, exists := byID[assetID]
			if !exists || seen[assetID] {
				continue
			}
			seen[assetID] = true
			result = append(result, subtitles.Asset{ID: asset.ID, RelativePath: asset.RelativePath, Size: asset.Size})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RelativePath < result[j].RelativePath })
	return result
}

func subtitleAssets(assets []packs.ObservedAsset) []subtitles.Asset {
	result := make([]subtitles.Asset, 0, len(assets))
	for _, asset := range assets {
		result = append(result, subtitles.Asset{ID: asset.ID, RelativePath: asset.RelativePath, Size: asset.Size})
	}
	return result
}

func authorizedOutputRoot(localReadOnlyRoot, localRelative string) (string, error) {
	root := filepath.Clean(localReadOnlyRoot)
	if !filepath.IsAbs(root) {
		return "", errors.New("AltMount read-only root is invalid")
	}
	if localRelative == "" || localRelative == "." {
		return root, nil
	}
	relative, err := library.ValidateRelative(filepath.Clean(localRelative))
	if err != nil || relative != localRelative {
		return "", errors.New("AltMount completed output root is unsafe")
	}
	return filepath.Join(root, relative), nil
}
