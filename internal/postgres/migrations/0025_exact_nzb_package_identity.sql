ALTER TABLE altmount_jobs
  ADD COLUMN nzb_package_sha256 bytea
  CHECK (nzb_package_sha256 IS NULL OR octet_length(nzb_package_sha256) = 32);

CREATE INDEX altmount_jobs_workflow_package_identity
  ON altmount_jobs(workflow_id, nzb_package_sha256)
  WHERE nzb_package_sha256 IS NOT NULL;

ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN ('subtitle_verification','release_retrieval','duplicate_package'));
