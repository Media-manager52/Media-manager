package postgres

import (
	"context"
	"errors"
	"strings"
	"time"
	"uuid"
)

// InspectionFailedCode is the release failure recorded when media-info could
// not inspect a downloaded release, so it could never be verified.
const InspectionFailedCode = "inspection_failed"

// ResumeInspectionFailures moves workflows that stopped at owner attention
// because media-info could not inspect their download (output_inspection_failed)
// on to the next release: the release is excluded exactly as a new failure
// would be now. Nothing is published without inspection.
func (store *Store) ResumeInspectionFailures(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := store.Pool.Query(ctx, `SELECT job.id::text,COALESCE(workflow.attention_detail,'') FROM workflows workflow
		JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id
		WHERE workflow.state='needs_attention' AND workflow.terminal_at IS NULL AND workflow.attention_code='output_inspection_failed'
		  AND job.state='needs_attention' AND job.acquisition_closed_at IS NULL AND job.round_id IS NOT NULL
		ORDER BY workflow.updated_at,workflow.id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type failure struct{ job, detail string }
	items := []failure{}
	for rows.Next() {
		var item failure
		if err := rows.Scan(&item.job, &item.detail); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	moved := 0
	for _, item := range items {
		jobID, err := uuid.Parse(item.job)
		if err != nil {
			return moved, err
		}
		job, err := store.LoadAltMountJob(ctx, jobID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return moved, err
		}
		detail := strings.TrimSpace(item.detail)
		if detail == "" {
			detail = "media-info could not inspect this release"
		}
		if err := store.CompleteReleaseFailure(ctx, job, InspectionFailedCode, detail, now); errors.Is(err, ErrConcurrentModification) {
			continue
		} else if err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// plexAutoReconfirmPauses is how long after a timed-out Plex confirmation the
// next automatic confirmation is queued: 1 hour after the first timeout, 6
// after the second, 24 after the third. RequestPlexReconfirmation's own limit
// still applies; a workflow past it stays with the owner, because repeated
// timeouts point at a Plex identity, path or mount problem.
var plexAutoReconfirmPauses = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour}

func plexAutoReconfirmPause(scans int) time.Duration {
	index := scans - 1
	if index < 0 {
		index = 0
	}
	if index >= len(plexAutoReconfirmPauses) {
		index = len(plexAutoReconfirmPauses) - 1
	}
	return plexAutoReconfirmPauses[index]
}

// AutoReconfirmTimedOutPlex queues exact-path Plex confirmation again for
// workflows whose confirmation timed out long enough ago, without the owner.
// Only the existing verified links are confirmed; no library-wide scan.
func (store *Store) AutoReconfirmTimedOutPlex(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,latest.completed_at,
		(SELECT count(*) FROM plex_scan_requests scan WHERE scan.workflow_id=workflow.id AND scan.altmount_job_id=workflow.grab_attempt_id)
		FROM workflows workflow
		JOIN LATERAL (SELECT scan.completed_at FROM plex_scan_requests scan WHERE scan.workflow_id=workflow.id AND scan.altmount_job_id=workflow.grab_attempt_id
			ORDER BY scan.requested_at DESC,scan.id DESC LIMIT 1) latest ON true
		WHERE workflow.state='needs_attention' AND workflow.terminal_at IS NULL AND workflow.attention_code='plex_timeout'
		  AND (SELECT count(*) FROM plex_scan_requests scan WHERE scan.workflow_id=workflow.id AND scan.altmount_job_id=workflow.grab_attempt_id) <= $1
		ORDER BY workflow.updated_at,workflow.id LIMIT $2`, maxPlexReconfirmations, limit)
	if err != nil {
		return 0, err
	}
	type timedOut struct {
		id        uuid.UUID
		completed *time.Time
		scans     int
	}
	items := []timedOut{}
	for rows.Next() {
		var text string
		var item timedOut
		if err := rows.Scan(&text, &item.completed, &item.scans); err != nil {
			rows.Close()
			return 0, err
		}
		if item.id, err = uuid.Parse(text); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	queued := 0
	for _, item := range items {
		if item.completed != nil && now.Before(item.completed.Add(plexAutoReconfirmPause(item.scans))) {
			continue
		}
		if _, err := store.RequestPlexReconfirmation(ctx, item.id, "worker", now); err != nil {
			if plexReconfirmSkippable(err) {
				continue
			}
			return queued, err
		}
		queued++
	}
	return queued, nil
}
