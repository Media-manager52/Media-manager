CREATE TABLE media_collections (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (name <> ''),
  description text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX media_collections_name_unique ON media_collections(lower(name));

CREATE TABLE media_collection_members (
  collection_id uuid NOT NULL REFERENCES media_collections(id) ON DELETE CASCADE,
  media_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
  position integer NOT NULL CHECK (position >= 0),
  added_at timestamptz NOT NULL,
  PRIMARY KEY (collection_id,media_id),
  UNIQUE (collection_id,position)
);
