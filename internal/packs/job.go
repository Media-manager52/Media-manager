package packs

import (
	"errors"
	"time"
	"uuid"
)

type JobState string

const (
	JobSubmitted         JobState = "submitted"
	JobAltMountRunning   JobState = "altmount_running"
	JobAltMountCompleted JobState = "altmount_completed"
	JobEnumerating       JobState = "enumerating"
	JobMapping           JobState = "mapping"
	JobVerified          JobState = "verified"
	JobIncomplete        JobState = "incomplete"
	JobNeedsReview       JobState = "needs_review"
	JobFailed            JobState = "failed"
	JobPublishing        JobState = "publishing"
	JobPublished         JobState = "published"
)

type PackJob struct {
	ID                uuid.UUID `json:"id"`
	WorkflowID        uuid.UUID `json:"workflow_id"`
	AltMountQueueID   *int64    `json:"altmount_queue_id,omitempty"`
	State             JobState  `json:"state"`
	ExpectationDigest string    `json:"expectation_digest"`
	InventoryDigest   string    `json:"inventory_digest,omitempty"`
	MappingVersion    string    `json:"mapping_version"`
	Revision          uint64    `json:"revision"`
	UpdatedAt         time.Time `json:"updated_at"`
}

var ErrInvalidJobTransition = errors.New("invalid season-pack job transition")

var jobTransitions = map[JobState]map[JobState]struct{}{
	JobSubmitted:         jobSet(JobAltMountRunning, JobFailed),
	JobAltMountRunning:   jobSet(JobAltMountCompleted, JobFailed),
	JobAltMountCompleted: jobSet(JobEnumerating, JobFailed),
	JobEnumerating:       jobSet(JobMapping, JobIncomplete, JobNeedsReview, JobFailed),
	JobMapping:           jobSet(JobVerified, JobIncomplete, JobNeedsReview, JobFailed),
	JobVerified:          jobSet(JobPublishing, JobNeedsReview),
	JobIncomplete:        jobSet(JobEnumerating, JobFailed),
	JobNeedsReview:       jobSet(JobEnumerating, JobMapping, JobFailed),
	JobPublishing:        jobSet(JobPublished, JobNeedsReview, JobFailed),
}

func (job *PackJob) Transition(to JobState, now time.Time) error {
	if job.State == to {
		return nil
	}
	if _, ok := jobTransitions[job.State][to]; !ok {
		return ErrInvalidJobTransition
	}
	job.State = to
	job.Revision++
	job.UpdatedAt = now.UTC()
	return nil
}

func jobSet(states ...JobState) map[JobState]struct{} {
	result := make(map[JobState]struct{}, len(states))
	for _, state := range states {
		result[state] = struct{}{}
	}
	return result
}
