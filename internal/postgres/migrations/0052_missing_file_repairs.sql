-- A repair also replaces a published file that AltMount no longer serves.
-- When AltMount finds a file's Usenet articles missing during playback, its
-- own repair moves the file's metadata aside and asks Radarr or Sonarr for a
-- replacement. Media Manager is the acquisition system, so nothing replaces
-- the file and AltMount does not keep reporting it as corrupted: the library
-- symlink is left pointing at nothing and the title no longer plays.
--
-- Such a repair carries no AltMount health record; it is found by checking
-- that every current publication's AltMount file still exists.
ALTER TABLE altmount_repairs DROP CONSTRAINT IF EXISTS altmount_repairs_reason_check;
ALTER TABLE altmount_repairs ADD CONSTRAINT altmount_repairs_reason_check
  CHECK (reason IN ('corrupted', 'missing_required_audio', 'missing_file'));
