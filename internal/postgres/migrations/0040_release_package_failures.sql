-- Release-attributable AltMount failures: archive sets, unplayable containers,
-- and packages with missing Usenet segments now exclude the exact release and
-- retry another candidate inside the open acquisition round instead of parking
-- the workflow as infrastructure attention.

ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN (
    'subtitle_verification','release_retrieval','duplicate_package','output_verification',
    'season_pack_incomplete','movie_primary_ambiguous','output_empty',
    'media_identity_mismatch','media_corrupt','password_protected',
    'archive_unsupported','no_playable_media','release_incomplete'
  ));
