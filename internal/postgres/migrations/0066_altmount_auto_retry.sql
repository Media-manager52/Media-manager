-- A failed AltMount import is retried automatically instead of waiting for the
-- owner. Media Manager asks AltMount to retry the same queue item (the same
-- NZB, no new indexer request) after a growing pause; when the retries are
-- used up the release is excluded and the round moves on to the next one.
--
-- auto_retry_count     AltMount retries already requested for this job.
-- auto_retry_due_at    when the next retry may be requested (NULL: none planned).
-- auto_retry_requested_at  last time a retry was requested; paces retries
--                      across all jobs so AltMount is never flooded.
-- auto_resumed_at      when a job that had stopped at owner attention was
--                      picked up again automatically (at most once per job).
ALTER TABLE altmount_jobs
  ADD COLUMN auto_retry_count integer NOT NULL DEFAULT 0 CHECK (auto_retry_count BETWEEN 0 AND 20),
  ADD COLUMN auto_retry_due_at timestamptz,
  ADD COLUMN auto_retry_requested_at timestamptz,
  ADD COLUMN auto_resumed_at timestamptz;

CREATE INDEX altmount_jobs_auto_retry_requested ON altmount_jobs (auto_retry_requested_at DESC)
  WHERE auto_retry_requested_at IS NOT NULL;

-- A release AltMount could not import even after the automatic retries is
-- excluded like any other release failure, so the round tries another one.
ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN (
    'subtitle_verification','release_retrieval','duplicate_package','output_verification',
    'season_pack_incomplete','movie_primary_ambiguous','output_empty',
    'media_identity_mismatch','media_corrupt','password_protected',
    'archive_unsupported','no_playable_media','release_incomplete',
    'resolution_mismatch','import_failed'
  ));
