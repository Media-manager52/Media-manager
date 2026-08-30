ALTER TABLE library_link_versions
  ADD COLUMN altmount_job_id uuid REFERENCES altmount_jobs(id) ON DELETE RESTRICT;

UPDATE library_link_versions version
SET altmount_job_id = inventory.job_id
FROM altmount_output_assets asset
JOIN altmount_output_inventories inventory ON inventory.id = asset.inventory_id
WHERE version.source_asset_id = asset.id;

ALTER TABLE library_link_versions
  ALTER COLUMN altmount_job_id SET NOT NULL;

CREATE INDEX library_link_versions_attempt
  ON library_link_versions(workflow_id, altmount_job_id, state);

ALTER TABLE plex_scan_requests
  ADD COLUMN altmount_job_id uuid REFERENCES altmount_jobs(id) ON DELETE RESTRICT;

UPDATE plex_scan_requests scan
SET altmount_job_id = (
  SELECT version.altmount_job_id
  FROM library_link_versions version
  WHERE version.workflow_id = scan.workflow_id
  ORDER BY version.verified_at DESC NULLS LAST, version.id DESC
  LIMIT 1
);

UPDATE plex_scan_requests scan
SET altmount_job_id = workflow.grab_attempt_id
FROM workflows workflow
WHERE workflow.id = scan.workflow_id
  AND scan.altmount_job_id IS NULL
  AND workflow.grab_attempt_id IS NOT NULL;

CREATE INDEX plex_scan_attempt
  ON plex_scan_requests(workflow_id, altmount_job_id, requested_at DESC);

ALTER TABLE recycle_entries
  ADD COLUMN cycle_id uuid;

WITH cycle_ids AS (
  SELECT DISTINCT ON (media_id, created_at)
    media_id,
    created_at,
    id AS cycle_id
  FROM recycle_entries
  ORDER BY media_id, created_at, id
)
UPDATE recycle_entries entry
SET cycle_id = cycle.cycle_id
FROM cycle_ids cycle
WHERE entry.media_id = cycle.media_id
  AND entry.created_at = cycle.created_at;

ALTER TABLE recycle_entries
  ALTER COLUMN cycle_id SET NOT NULL;

CREATE INDEX recycle_cycle
  ON recycle_entries(media_id, cycle_id, state);
