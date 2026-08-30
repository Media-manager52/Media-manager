CREATE TABLE IF NOT EXISTS schema_migrations (
  version bigint PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE integration_instances (
  id uuid PRIMARY KEY,
  kind text NOT NULL,
  name text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  secret_ref text,
	  is_default boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (kind, name)
);
CREATE UNIQUE INDEX integration_one_default_kind ON integration_instances(kind) WHERE is_default AND enabled;

CREATE TABLE library_roots (
  id uuid PRIMARY KEY,
  media_kind text NOT NULL CHECK (media_kind IN ('movie', 'series')),
  name text NOT NULL,
  destination_root text NOT NULL,
  plex_visible_root text NOT NULL,
  source_visible_root text NOT NULL,
  plex_instance_id uuid REFERENCES integration_instances(id),
  plex_library_id text,
  is_default boolean NOT NULL DEFAULT false,
  enabled boolean NOT NULL DEFAULT true,
  UNIQUE (media_kind, name)
);
CREATE UNIQUE INDEX library_roots_one_default ON library_roots(media_kind) WHERE is_default AND enabled;

CREATE TABLE quality_profiles (
  id uuid PRIMARY KEY,
  media_kind text NOT NULL CHECK (media_kind IN ('movie', 'series')),
  name text NOT NULL,
  policy_version bigint NOT NULL CHECK (policy_version > 0),
  policy jsonb NOT NULL,
  upgrades_enabled boolean NOT NULL DEFAULT false,
	  is_default boolean NOT NULL DEFAULT false,
  enabled boolean NOT NULL DEFAULT true,
  UNIQUE (media_kind, name)
);
CREATE UNIQUE INDEX quality_profiles_one_default ON quality_profiles(media_kind) WHERE is_default AND enabled;

CREATE TABLE media_items (
  id uuid PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('movie', 'series')),
  title text NOT NULL CHECK (btrim(title) <> ''),
  year smallint CHECK (year BETWEEN 1800 AND 9999),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (id, kind)
);

CREATE TABLE external_identities (
  id uuid PRIMARY KEY,
  media_id uuid NOT NULL,
  media_kind text NOT NULL,
  namespace text NOT NULL CHECK (btrim(namespace) <> ''),
  value text NOT NULL CHECK (btrim(value) <> ''),
  state text NOT NULL CHECK (state IN ('verified', 'unverified', 'needs_attention')),
  source text NOT NULL,
  evidence_sha256 bytea,
  observed_at timestamptz NOT NULL,
  FOREIGN KEY (media_id, media_kind) REFERENCES media_items(id, kind) ON DELETE CASCADE,
  UNIQUE (media_id, namespace, value)
);
CREATE UNIQUE INDEX external_identity_one_verified_namespace ON external_identities(media_id, namespace) WHERE state = 'verified';
CREATE UNIQUE INDEX external_identity_verified_kind_namespace ON external_identities(media_kind, namespace, value) WHERE state = 'verified';

CREATE TABLE audit_events (
  id uuid PRIMARY KEY,
  actor text NOT NULL,
  action text NOT NULL,
  aggregate_type text NOT NULL,
  aggregate_id uuid,
  detail jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at timestamptz NOT NULL
);
CREATE INDEX audit_events_aggregate ON audit_events(aggregate_type, aggregate_id, occurred_at DESC);

CREATE TABLE command_receipts (
  scope text NOT NULL,
  idempotency_key text NOT NULL,
  request_sha256 bytea NOT NULL,
  response_status integer,
  response_json jsonb,
  created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (scope, idempotency_key)
);
