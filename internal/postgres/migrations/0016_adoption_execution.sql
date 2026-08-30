ALTER TABLE adoption_batches ADD COLUMN execution_deadline timestamptz;

CREATE TABLE adoption_operations (
  id uuid PRIMARY KEY,
  batch_id uuid NOT NULL REFERENCES adoption_batches(id) ON DELETE CASCADE,
  item_id uuid NOT NULL REFERENCES adoption_items(id) ON DELETE CASCADE,
  ordinal integer NOT NULL CHECK (ordinal > 0),
  kind text NOT NULL CHECK (kind IN ('primary','recognized_companion','unknown_preserved')),
  source_relative text NOT NULL CHECK (source_relative <> '' AND source_relative !~ '(^/|\\|(^|/)\.\.(/|$))'),
  destination_relative text NOT NULL CHECK (destination_relative <> '' AND destination_relative !~ '(^/|\\|(^|/)\.\.(/|$))'),
  source_size bigint NOT NULL CHECK (source_size >= 0),
  source_modtime_ns bigint NOT NULL,
  state text NOT NULL CHECK (state IN ('planned','applied','verified','preserved','rolled_back','failed')),
  last_error text,
  applied_at timestamptz,
  verified_at timestamptz,
  rolled_back_at timestamptz,
  UNIQUE(batch_id,ordinal),
  UNIQUE(item_id,kind,source_relative)
);

CREATE TABLE adoption_plex_confirmations (
  id uuid PRIMARY KEY,
  batch_id uuid NOT NULL REFERENCES adoption_batches(id) ON DELETE RESTRICT,
  item_id uuid NOT NULL REFERENCES adoption_items(id) ON DELETE RESTRICT,
  observed_path text NOT NULL,
  rating_key text NOT NULL,
  provider_guid text NOT NULL,
  confirmed_at timestamptz NOT NULL,
  UNIQUE(item_id,observed_path)
);
