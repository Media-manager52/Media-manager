CREATE TABLE release_blocks (
  id uuid PRIMARY KEY,
  scope text NOT NULL CHECK (scope IN ('native_release','pack_release')),
  indexer_instance_id uuid REFERENCES integration_instances(id),
  media_id uuid REFERENCES media_items(id),
  variant text,
  native_key text,
  release_title text NOT NULL CHECK (release_title <> ''),
  reason text NOT NULL CHECK (reason <> ''),
  expires_at timestamptz,
  created_at timestamptz NOT NULL,
  created_by text NOT NULL,
  CHECK (
    (scope='native_release' AND indexer_instance_id IS NOT NULL AND native_key IS NOT NULL AND media_id IS NULL AND variant IS NULL)
    OR
    (scope='pack_release' AND indexer_instance_id IS NULL AND native_key IS NULL AND media_id IS NOT NULL AND variant LIKE 'season:%')
  )
);

CREATE INDEX release_blocks_native_lookup
  ON release_blocks(indexer_instance_id,native_key)
  WHERE scope='native_release';
CREATE INDEX release_blocks_pack_lookup
  ON release_blocks(media_id,variant,lower(release_title))
  WHERE scope='pack_release';
CREATE INDEX release_blocks_active
  ON release_blocks(media_id,variant,expires_at);
