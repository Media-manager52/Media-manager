-- A repair also replaces a published file that lacks an audio language its
-- library requires (for example Danish audio in the kids libraries). The old
-- file keeps playing until Plex confirms the replacement.
--
-- Such an upgrade can find nothing (many titles have no Danish dub). It is
-- then closed as not_found and tried again after retry_after, so a bounded
-- number of upgrades search at any time. A title that does not need one (its
-- original language is the required one) is recorded as skipped.
ALTER TABLE altmount_repairs
  ADD COLUMN reason text NOT NULL DEFAULT 'corrupted' CHECK (reason IN ('corrupted', 'missing_required_audio')),
  ADD COLUMN created_workflow boolean NOT NULL DEFAULT false,
  ADD COLUMN retry_after timestamptz,
  ALTER COLUMN altmount_health_id DROP NOT NULL;

ALTER TABLE altmount_repairs DROP CONSTRAINT altmount_repairs_state_check;
ALTER TABLE altmount_repairs ADD CONSTRAINT altmount_repairs_state_check CHECK (state IN ('open', 'repaired', 'needs_attention', 'not_found', 'skipped'));
ALTER TABLE altmount_repairs ADD CONSTRAINT altmount_repairs_corrupted_health CHECK (reason <> 'corrupted' OR altmount_health_id IS NOT NULL);
ALTER TABLE altmount_repairs ADD CONSTRAINT altmount_repairs_retry CHECK (state <> 'not_found' OR retry_after IS NOT NULL);

-- Recently closed or skipped upgrades of the same file are not reopened.
CREATE INDEX altmount_repairs_link_history ON altmount_repairs(broken_link_version_id, reason) WHERE broken_link_version_id IS NOT NULL;
CREATE INDEX altmount_repairs_unmanaged_history ON altmount_repairs(unmanaged_location, unmanaged_relative, reason) WHERE unmanaged_root_id IS NOT NULL;
CREATE INDEX altmount_repairs_open_reason ON altmount_repairs(reason, created_at) WHERE state = 'open';
