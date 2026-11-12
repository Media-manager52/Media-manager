package acquisition

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/postgres"
)

// A failed AltMount import that is not clearly the release's fault is retried
// automatically: Media Manager asks AltMount to import the same queue item
// again (the same NZB, no indexer request) after a growing pause. When the
// retries are used up the release is excluded and the round moves on to the
// next release, so no failed import waits for the owner.
const (
	// altMountAutoRetrySpacing paces retries across all jobs. AltMount runs a
	// retried item outside its worker pool, so many retries at once would
	// overload it, which is what made most of these imports fail.
	altMountAutoRetrySpacing = 90 * time.Second
	// altMountBusyRetries is the retry budget when AltMount reported a
	// timeout or connection failure: a busy server or provider, not the
	// release. The pauses add up to about 17 hours, long enough to ride out
	// a provider outage before a release is given up.
	altMountBusyRetries = 4
	// altMountOtherRetries is the budget for any other unclassified failure.
	altMountOtherRetries = 2
)

var altMountAutoRetryPauses = []time.Duration{10 * time.Minute, time.Hour, 4 * time.Hour, 12 * time.Hour}

// altMountBusyFailure reports a failure detail that only says AltMount or a
// provider was too slow or unreachable.
func altMountBusyFailure(detail string) bool {
	normalized := strings.ToLower(detail)
	for _, marker := range []string{
		"context deadline exceeded", "context canceled", "i/o timeout",
		"connection refused", "connection reset", "socket not connected",
		"fast-fail validation inconclusive", "fast-fail stat probe inconclusive",
		"timeout",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func altMountAutoRetryLimit(detail string) int {
	if altMountBusyFailure(detail) {
		return altMountBusyRetries
	}
	return altMountOtherRetries
}

func altMountAutoRetryPause(retriesDone int) time.Duration {
	if retriesDone < 0 {
		retriesDone = 0
	}
	if retriesDone >= len(altMountAutoRetryPauses) {
		return altMountAutoRetryPauses[len(altMountAutoRetryPauses)-1]
	}
	return altMountAutoRetryPauses[retriesDone]
}

// altMountRetryJitter spreads jobs that wait for the same pacing slot over a
// few minutes, so they do not all wake up together.
func altMountRetryJitter(job postgres.AltMountJobContext) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(job.JobID.String()))
	return time.Duration(hash.Sum32()%180) * time.Second
}

// retryOrMoveOn handles an AltMount import failure that is not attributed to
// the release by its message: plan a retry, request a due retry, or give the
// release up once the retries are used.
func (service *AltMountReconcileService) retryOrMoveOn(ctx context.Context, client *altmount.HTTPClient, job postgres.AltMountJobContext, queueID altmount.QueueID, detail string) error {
	now := time.Now().UTC()
	limit := altMountAutoRetryLimit(detail)
	if job.AutoRetryCount >= limit {
		return service.Store.GiveUpAltMountImport(ctx, job, giveUpDetail(job.AutoRetryCount, detail), now)
	}
	if job.AutoRetryDueAt == nil {
		return service.Store.ScheduleAltMountAutoRetry(ctx, job, detail, now.Add(altMountAutoRetryPause(job.AutoRetryCount)))
	}
	if now.Before(*job.AutoRetryDueAt) {
		return service.Store.ScheduleAltMountReconcile(ctx, job, *job.AutoRetryDueAt)
	}
	claimed, free, err := service.Store.ClaimAltMountAutoRetry(ctx, job, now, altMountAutoRetrySpacing)
	if err != nil {
		return err
	}
	if !claimed {
		return service.Store.ScheduleAltMountReconcile(ctx, job, free.Add(altMountRetryJitter(job)))
	}
	// The retry is already counted and the job is read again shortly. If
	// AltMount refuses the request, that read sees the same failure and the
	// next retry (or the move to another release) follows from there.
	if err := client.RetryQueue(ctx, queueID); err != nil {
		return fmt.Errorf("AltMount retry request: %w", err)
	}
	return nil
}

func giveUpDetail(retries int, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "AltMount job failed"
	}
	if retries == 0 {
		return "AltMount could not import this release: " + detail
	}
	return fmt.Sprintf("AltMount could not import this release after %d automatic retries: %s", retries, detail)
}
