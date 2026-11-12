-- A downloaded release whose media file media-info cannot inspect (for
-- example "track codec is unavailable" or "invalid audio channel count") can
-- never be verified, so it is excluded like any other release failure and
-- the round tries the next release instead of waiting for the owner.
ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN (
    'subtitle_verification','release_retrieval','duplicate_package','output_verification',
    'season_pack_incomplete','movie_primary_ambiguous','output_empty',
    'media_identity_mismatch','media_corrupt','password_protected',
    'archive_unsupported','no_playable_media','release_incomplete',
    'resolution_mismatch','import_failed','inspection_failed'
  ));
