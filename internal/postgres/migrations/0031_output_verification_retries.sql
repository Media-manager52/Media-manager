ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN ('subtitle_verification','release_retrieval','duplicate_package','output_verification'));
