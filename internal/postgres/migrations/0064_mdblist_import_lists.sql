-- Public MDBList lists the owner follows, like Radarr/Sonarr list imports.
-- Every title that appears on such a list is added to the library through
-- the same path as "Add", with the list's library folder and quality profile
-- (or the defaults). A title that leaves the list is never removed, and a
-- title the owner removed from the library is never added again by a list.
ALTER TABLE import_lists DROP CONSTRAINT import_lists_provider_check;
ALTER TABLE import_lists
  ADD CONSTRAINT import_lists_provider_check CHECK (provider IN ('imdb_export','mdblist')),
  ADD COLUMN source_owner text,
  ADD COLUMN source_slug text,
  ADD COLUMN series_root_id uuid REFERENCES library_roots(id) ON DELETE RESTRICT,
  ADD COLUMN series_quality_profile_id uuid REFERENCES quality_profiles(id) ON DELETE RESTRICT,
  ADD COLUMN sync_revision bigint NOT NULL DEFAULT 0,
  ADD COLUMN next_sync_at timestamptz,
  ADD COLUMN last_synced_at timestamptz,
  ADD COLUMN last_sync_state text CHECK (last_sync_state IN ('ok','failed','needs_configuration')),
  ADD COLUMN last_sync_detail text,
  ADD CONSTRAINT import_lists_mdblist_source CHECK (
    provider <> 'mdblist' OR (
      source_owner ~ '^[A-Za-z0-9._-]{1,100}$' AND source_slug ~ '^[A-Za-z0-9._-]{1,200}$'
    )
  );
CREATE UNIQUE INDEX import_lists_mdblist_source_unique ON import_lists(lower(source_owner),lower(source_slug)) WHERE provider='mdblist';
CREATE INDEX import_lists_mdblist_due ON import_lists(next_sync_at) WHERE provider='mdblist' AND enabled;

CREATE TABLE import_list_entries (
  import_list_id uuid NOT NULL REFERENCES import_lists(id) ON DELETE CASCADE,
  media_kind text NOT NULL CHECK (media_kind IN ('movie','series')),
  tmdb_id bigint NOT NULL CHECK (tmdb_id > 0),
  imdb_id text CHECK (imdb_id ~ '^tt[0-9]{7,12}$'),
  tvdb_id bigint CHECK (tvdb_id > 0),
  title text NOT NULL CHECK (title <> '' AND length(title) <= 500),
  year smallint CHECK (year BETWEEN 1800 AND 9999),
  rank integer NOT NULL CHECK (rank >= 0),
  state text NOT NULL CHECK (state IN ('pending','added','in_library','removed_earlier','failed')),
  detail text CHECK (detail IS NULL OR length(detail) <= 500),
  media_id uuid REFERENCES media_items(id) ON DELETE SET NULL,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  first_seen_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (import_list_id, media_kind, tmdb_id)
);
CREATE INDEX import_list_entries_open ON import_list_entries(import_list_id, rank) WHERE state IN ('pending','failed');
