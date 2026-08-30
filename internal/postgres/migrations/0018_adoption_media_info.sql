ALTER TABLE adoption_operations
  ADD COLUMN media_info jsonb,
  ADD COLUMN media_info_sha256 bytea,
  ADD COLUMN media_info_recorded_at timestamptz,
  ADD CONSTRAINT adoption_operations_media_info_consistent CHECK (
    (media_info IS NULL AND media_info_sha256 IS NULL AND media_info_recorded_at IS NULL) OR
    (kind = 'primary' AND media_info IS NOT NULL AND media_info_sha256 IS NOT NULL AND octet_length(media_info_sha256) = 32 AND media_info_recorded_at IS NOT NULL)
  );

ALTER TABLE adopted_files
  DROP CONSTRAINT adopted_files_quality_evidence_state_check,
  ADD CONSTRAINT adopted_files_quality_evidence_state_check CHECK (
    quality_evidence_state IN ('unassessed','media_info')
  );
