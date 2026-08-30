ALTER TABLE adopted_files
  DROP CONSTRAINT adopted_files_state_check,
  DROP CONSTRAINT adopted_files_lifecycle_times,
  ADD CONSTRAINT adopted_files_state_check CHECK (state IN ('current','recycle_planned','retained','restore_confirming','restore_attention','purged')),
  ADD CONSTRAINT adopted_files_lifecycle_times CHECK (
    (state = 'current' AND displaced_at IS NULL AND purged_at IS NULL) OR
    (state IN ('recycle_planned','retained','restore_confirming','restore_attention') AND displaced_at IS NOT NULL AND purged_at IS NULL) OR
    (state = 'purged' AND displaced_at IS NOT NULL AND purged_at IS NOT NULL)
  );

ALTER TABLE recycle_entries
  ADD COLUMN replacement_workflow_id uuid REFERENCES workflows(id) ON DELETE RESTRICT,
  ADD COLUMN restore_revision bigint NOT NULL DEFAULT 1 CHECK (restore_revision > 0),
  ADD COLUMN restore_attempts integer NOT NULL DEFAULT 0 CHECK (restore_attempts >= 0),
  ADD COLUMN restore_requested_at timestamptz,
  ADD COLUMN restore_deadline_at timestamptz,
  ADD COLUMN restore_refresh_at timestamptz,
  ADD COLUMN restore_requested_by text;

UPDATE recycle_entries entry
SET replacement_workflow_id = (
  SELECT replacement_auth.workflow_id
  FROM adopted_replacement_authorizations replacement_auth
  WHERE replacement_auth.adopted_file_id = entry.adopted_file_id
    AND replacement_auth.authorized_at <= entry.created_at
  ORDER BY replacement_auth.authorized_at DESC, replacement_auth.workflow_id DESC
  LIMIT 1
)
WHERE entry.purpose = 'upgrade_displacement';

ALTER TABLE recycle_entries
  ADD CONSTRAINT recycle_entries_upgrade_workflow CHECK (
    purpose <> 'upgrade_displacement' OR replacement_workflow_id IS NOT NULL
  ),
  ADD CONSTRAINT recycle_entries_real_restore_request CHECK (
    entry_kind <> 'real_file' OR state NOT IN ('restore_planned','awaiting_confirmation','restored','failed') OR (
      restore_requested_at IS NOT NULL AND
      restore_deadline_at IS NOT NULL AND
      restore_requested_by = 'owner'
    )
  );

CREATE TABLE adopted_restore_confirmations (
  recycle_entry_id uuid PRIMARY KEY REFERENCES recycle_entries(id) ON DELETE RESTRICT,
  observed_path text NOT NULL CHECK (observed_path <> ''),
  rating_key text NOT NULL CHECK (rating_key <> ''),
  provider_guid text NOT NULL CHECK (provider_guid <> ''),
  confirmed_at timestamptz NOT NULL
);
