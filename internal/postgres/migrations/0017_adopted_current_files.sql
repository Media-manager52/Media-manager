ALTER TABLE adoption_items
  ADD COLUMN variant text;

UPDATE adoption_items item
SET variant = 'default'
FROM adoption_batches batch
WHERE batch.id = item.batch_id
  AND batch.media_kind = 'movie'
  AND item.review_state = 'approved';

ALTER TABLE adoption_items
  ADD CONSTRAINT adoption_items_variant_valid CHECK (
    variant IS NULL OR
    variant = 'default' OR
    variant ~ '^season:[0-9]{2}$'
  );

CREATE TABLE adopted_files (
  id uuid PRIMARY KEY,
  adoption_batch_id uuid NOT NULL REFERENCES adoption_batches(id) ON DELETE RESTRICT,
  adoption_item_id uuid NOT NULL UNIQUE REFERENCES adoption_items(id) ON DELETE RESTRICT,
  media_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
  root_id uuid NOT NULL REFERENCES library_roots(id) ON DELETE RESTRICT,
  quality_profile_id uuid NOT NULL REFERENCES quality_profiles(id) ON DELETE RESTRICT,
  variant text NOT NULL CHECK (variant = 'default' OR variant ~ '^season:[0-9]{2}$'),
  original_source_root text NOT NULL CHECK (original_source_root <> ''),
  original_relative text NOT NULL CHECK (original_relative <> '' AND original_relative !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  destination_relative text NOT NULL CHECK (destination_relative <> '' AND destination_relative !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  source_size bigint NOT NULL CHECK (source_size >= 0),
  source_modtime_ns bigint NOT NULL,
  plex_observed_path text NOT NULL CHECK (plex_observed_path LIKE '/%'),
  plex_item_key text NOT NULL CHECK (plex_item_key <> ''),
  provider_guid text NOT NULL CHECK (provider_guid <> ''),
  quality_evidence_state text NOT NULL DEFAULT 'unassessed' CHECK (quality_evidence_state IN ('unassessed')),
  quality_evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  state text NOT NULL CHECK (state IN ('current')),
  verified_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX adopted_files_current_destination
  ON adopted_files(root_id, lower(destination_relative));

CREATE INDEX adopted_files_media_variant
  ON adopted_files(media_id, variant, state);
