ALTER TABLE adopted_files
  DROP CONSTRAINT adopted_files_state_check,
  ADD COLUMN displaced_at timestamptz,
  ADD COLUMN purged_at timestamptz,
  ADD CONSTRAINT adopted_files_state_check CHECK (state IN ('current','recycle_planned','retained','purged')),
  ADD CONSTRAINT adopted_files_lifecycle_times CHECK (
    (state = 'current' AND displaced_at IS NULL AND purged_at IS NULL) OR
    (state IN ('recycle_planned','retained') AND displaced_at IS NOT NULL AND purged_at IS NULL) OR
    (state = 'purged' AND displaced_at IS NOT NULL AND purged_at IS NOT NULL)
  );

CREATE TABLE adopted_replacement_authorizations (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  adopted_file_id uuid NOT NULL REFERENCES adopted_files(id) ON DELETE RESTRICT,
  destination_relative text NOT NULL CHECK (destination_relative <> ''),
  source_size bigint NOT NULL CHECK (source_size >= 0),
  source_modtime_ns bigint NOT NULL,
  authorized_by text NOT NULL CHECK (authorized_by <> ''),
  authorized_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id, adopted_file_id)
);

ALTER TABLE recycle_entries
  ADD COLUMN adopted_file_id uuid REFERENCES adopted_files(id) ON DELETE RESTRICT,
  ADD COLUMN purpose text NOT NULL DEFAULT 'media_delete' CHECK (purpose IN ('media_delete','upgrade_displacement')),
  ADD COLUMN expected_size bigint CHECK (expected_size IS NULL OR expected_size >= 0),
  ADD COLUMN expected_modtime_ns bigint,
  ADD CONSTRAINT recycle_entries_real_file_evidence CHECK (
    entry_kind <> 'real_file' OR (
      adopted_file_id IS NOT NULL AND
      library_link_version_id IS NULL AND
      expected_size IS NOT NULL AND
      expected_modtime_ns IS NOT NULL
    )
  ),
  ADD CONSTRAINT recycle_entries_upgrade_kind CHECK (
    (purpose = 'upgrade_displacement') = (entry_kind = 'real_file')
  ),
  ADD CONSTRAINT recycle_entries_real_purge_confirmation CHECK (
    entry_kind <> 'real_file' OR state NOT IN ('purge_planned','purged') OR
    (confirmed_by = 'owner' AND confirmed_at IS NOT NULL)
  );

CREATE UNIQUE INDEX recycle_adopted_file_once
  ON recycle_entries(adopted_file_id)
  WHERE adopted_file_id IS NOT NULL AND state NOT IN ('restored','purged');
