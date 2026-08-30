CREATE TABLE import_lists (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (name <> ''),
  provider text NOT NULL CHECK (provider IN ('imdb_export')),
  enabled boolean NOT NULL DEFAULT true,
  movie_root_id uuid REFERENCES library_roots(id) ON DELETE RESTRICT,
  movie_quality_profile_id uuid REFERENCES quality_profiles(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX import_lists_name_unique ON import_lists(lower(name));

CREATE TABLE import_list_snapshots (
  id uuid PRIMARY KEY,
  import_list_id uuid NOT NULL REFERENCES import_lists(id) ON DELETE RESTRICT,
  source_digest text NOT NULL CHECK (source_digest <> ''),
  item_count integer NOT NULL CHECK (item_count > 0 AND item_count <= 5000),
  pending_count integer NOT NULL CHECK (pending_count >= 0),
  existing_count integer NOT NULL CHECK (existing_count >= 0),
  unsupported_count integer NOT NULL CHECK (unsupported_count >= 0),
  created_at timestamptz NOT NULL,
  UNIQUE(import_list_id,source_digest)
);

CREATE TABLE import_list_snapshot_items (
  id uuid PRIMARY KEY,
  snapshot_id uuid NOT NULL REFERENCES import_list_snapshots(id) ON DELETE RESTRICT,
  ordinal integer NOT NULL CHECK (ordinal > 0),
  media_kind text NOT NULL CHECK (media_kind IN ('movie','series','unknown')),
  provider text NOT NULL CHECK (provider='imdb'),
  provider_id text NOT NULL CHECK (provider_id <> ''),
  title text NOT NULL CHECK (title <> ''),
  year smallint,
  state text NOT NULL CHECK (state IN ('pending_review','already_cataloged','unsupported')),
  attention text,
  existing_media_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
  UNIQUE(snapshot_id,ordinal),
  UNIQUE(snapshot_id,provider,provider_id)
);
