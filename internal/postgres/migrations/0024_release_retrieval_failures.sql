ALTER TABLE workflow_release_exclusions
  ALTER COLUMN verification_id DROP NOT NULL;

ALTER TABLE workflow_release_exclusions
  ADD COLUMN failure_kind text NOT NULL DEFAULT 'subtitle_verification'
  CHECK (failure_kind IN ('subtitle_verification','release_retrieval'));
