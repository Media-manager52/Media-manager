CREATE TABLE adoption_batches (
  id uuid PRIMARY KEY,
  source_root text NOT NULL,
  media_kind text NOT NULL CHECK (media_kind IN ('movie','series')),
  default_root_id uuid NOT NULL REFERENCES library_roots(id),
  default_quality_profile_id uuid NOT NULL REFERENCES quality_profiles(id),
  monitoring_enabled boolean NOT NULL DEFAULT false,
  search_immediately boolean NOT NULL DEFAULT false,
  state text NOT NULL CHECK (state IN ('scanning','review','approved','executing','complete','failed')),
  created_at timestamptz NOT NULL,
  approved_at timestamptz,
  completed_at timestamptz
);

CREATE TABLE adoption_items (
  id uuid PRIMARY KEY,
  batch_id uuid NOT NULL REFERENCES adoption_batches(id) ON DELETE CASCADE,
  original_relative text NOT NULL,
  proposed_relative text,
  media_id uuid REFERENCES media_items(id),
  root_id uuid REFERENCES library_roots(id),
  quality_profile_id uuid REFERENCES quality_profiles(id),
  monitoring_enabled boolean,
  search_immediately boolean,
  recognized_companions jsonb NOT NULL DEFAULT '[]'::jsonb,
  unknown_companions jsonb NOT NULL DEFAULT '[]'::jsonb,
  review_state text NOT NULL CHECK (review_state IN ('pending','approved','rejected','needs_attention')),
  execution_state text NOT NULL DEFAULT 'pending',
  UNIQUE (batch_id, original_relative)
);

CREATE TABLE recycle_entries (
  id uuid PRIMARY KEY,
  library_link_version_id uuid REFERENCES library_link_versions(id),
  media_id uuid NOT NULL REFERENCES media_items(id),
  root_id uuid NOT NULL REFERENCES library_roots(id),
  entry_kind text NOT NULL CHECK (entry_kind IN ('link','real_file','directory')),
  original_path text NOT NULL,
  recycled_path text,
  expected_target text,
  altmount_source_retained boolean NOT NULL DEFAULT true,
  purge_after timestamptz NOT NULL,
  state text NOT NULL CHECK (state IN ('planned','retained','restore_planned','restored','purge_planned','awaiting_confirmation','purged','failed')),
  confirmation_required boolean NOT NULL,
  confirmed_by text,
  confirmed_at timestamptz,
  created_at timestamptz NOT NULL
);
CREATE INDEX recycle_due ON recycle_entries(purge_after) WHERE state = 'retained';

ALTER TABLE media_items ADD COLUMN deleted_at timestamptz;

CREATE TABLE prowlarr_backup_imports (
  id uuid PRIMARY KEY,
  backup_sha256 bytea NOT NULL UNIQUE,
  state text NOT NULL CHECK (state IN ('uploaded','parsed','review','approved','complete','failed')),
  parser_version text NOT NULL,
  created_at timestamptz NOT NULL,
  completed_at timestamptz,
  error_detail text
);

CREATE TABLE prowlarr_indexer_candidates (
  id uuid PRIMARY KEY,
  import_id uuid NOT NULL REFERENCES prowlarr_backup_imports(id) ON DELETE CASCADE,
  name text NOT NULL,
  implementation text NOT NULL,
  base_url text NOT NULL,
  categories jsonb NOT NULL DEFAULT '[]'::jsonb,
  sanitized_config jsonb NOT NULL,
  secret_ref text,
  review_state text NOT NULL CHECK (review_state IN ('pending','approved','rejected','needs_attention')),
  created_instance_id uuid REFERENCES integration_instances(id),
  UNIQUE (import_id, name, base_url)
);
