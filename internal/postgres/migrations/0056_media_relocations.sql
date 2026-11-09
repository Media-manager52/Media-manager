-- A whole-title move is planned here before any link changes, so a restart can
-- finish the videos, subtitles and Plex refresh instead of leaving the title
-- split between two library roots.
CREATE TABLE media_relocations (
  id uuid PRIMARY KEY,
  media_id uuid NOT NULL REFERENCES media_items(id),
  target_root_id uuid NOT NULL REFERENCES library_roots(id),
  state text NOT NULL CHECK (state IN ('running','complete')),
  lease_until timestamptz,
  last_error text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  completed_at timestamptz
);
CREATE UNIQUE INDEX media_relocations_one_running ON media_relocations(media_id) WHERE state='running';

CREATE TABLE media_relocation_steps (
  relocation_id uuid NOT NULL REFERENCES media_relocations(id) ON DELETE CASCADE,
  ordinal integer NOT NULL,
  kind text NOT NULL CHECK (kind IN ('workflow','plex_folder')),
  workflow_id uuid REFERENCES workflows(id),
  root_id uuid REFERENCES library_roots(id),
  relative_path text,
  completed_at timestamptz,
  PRIMARY KEY (relocation_id, ordinal),
  CHECK ((kind='workflow' AND workflow_id IS NOT NULL) OR (kind='plex_folder' AND root_id IS NOT NULL AND relative_path IS NOT NULL))
);
