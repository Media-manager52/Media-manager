-- The owner can ask for a published file to be replaced. A file can play yet
-- play badly: when many of its Usenet articles are missing from the primary
-- providers, AltMount fetches them from a backup provider and every seek waits
-- several seconds. AltMount does not report such a file, so no sweep repairs
-- it. The owner's request is acquired exactly like a corrupted file's repair.
ALTER TABLE altmount_repairs DROP CONSTRAINT IF EXISTS altmount_repairs_reason_check;
ALTER TABLE altmount_repairs ADD CONSTRAINT altmount_repairs_reason_check
  CHECK (reason IN ('corrupted', 'missing_required_audio', 'missing_file', 'owner_requested'));
