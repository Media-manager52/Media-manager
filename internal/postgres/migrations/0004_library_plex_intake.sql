CREATE TABLE library_links (
  id uuid PRIMARY KEY,
  root_id uuid NOT NULL REFERENCES library_roots(id),
  destination_relative text NOT NULL CHECK (destination_relative <> '' AND destination_relative !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  current_version_id uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (root_id, destination_relative)
);

CREATE TABLE library_link_versions (
  id uuid PRIMARY KEY,
  library_link_id uuid NOT NULL REFERENCES library_links(id) ON DELETE RESTRICT,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  source_asset_id uuid NOT NULL REFERENCES altmount_output_assets(id) ON DELETE RESTRICT,
  generation integer NOT NULL CHECK (generation > 0),
  source_relative text NOT NULL,
  plex_visible_target text NOT NULL,
  state text NOT NULL CHECK (state IN ('planned','publishing','verified','current','displaced','quarantined')),
  verified_at timestamptz,
  displaced_at timestamptz,
  UNIQUE (library_link_id, generation),
  UNIQUE (workflow_id, source_asset_id)
);
ALTER TABLE library_links ADD CONSTRAINT library_links_current_version_fk FOREIGN KEY (current_version_id) REFERENCES library_link_versions(id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE library_link_removals (
  version_id uuid PRIMARY KEY REFERENCES library_link_versions(id) ON DELETE RESTRICT,
  expected_target text NOT NULL,
  removed_at timestamptz NOT NULL
);

CREATE TABLE plex_scan_requests (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  plex_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  plex_library_id text NOT NULL,
  import_generation bigint NOT NULL,
  state text NOT NULL CHECK (state IN ('planned','requested','polling','confirmed','timed_out','failed')),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  poll_attempts integer NOT NULL DEFAULT 0 CHECK (poll_attempts >= 0),
  requested_at timestamptz NOT NULL,
  last_polled_at timestamptz,
  deadline_at timestamptz NOT NULL,
  completed_at timestamptz,
  UNIQUE (workflow_id, plex_instance_id, plex_library_id, import_generation)
);

CREATE TABLE plex_confirmations (
  id uuid PRIMARY KEY,
  scan_request_id uuid NOT NULL REFERENCES plex_scan_requests(id) ON DELETE RESTRICT,
  link_version_id uuid NOT NULL REFERENCES library_link_versions(id) ON DELETE RESTRICT,
  plex_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  plex_item_key text NOT NULL,
  provider_namespace text NOT NULL,
  provider_value text NOT NULL,
  observed_path text NOT NULL,
  evidence_sha256 bytea NOT NULL,
  confirmed_at timestamptz NOT NULL,
  UNIQUE (link_version_id, plex_instance_id)
);

CREATE TABLE seerr_deliveries (
  id uuid PRIMARY KEY,
  seerr_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  source text NOT NULL CHECK (source IN ('webhook','poll')),
  event_id text,
  request_id text NOT NULL,
  payload_sha256 bytea NOT NULL,
  payload jsonb NOT NULL,
  received_at timestamptz NOT NULL,
  processed_at timestamptz,
  outcome text,
  error_detail text
);
CREATE UNIQUE INDEX seerr_event_once ON seerr_deliveries(seerr_instance_id, event_id) WHERE event_id IS NOT NULL;
CREATE UNIQUE INDEX seerr_payload_once ON seerr_deliveries(seerr_instance_id, request_id, payload_sha256) WHERE event_id IS NULL;

CREATE TABLE seerr_request_snapshots (
  seerr_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  request_id text NOT NULL,
  external_updated_at timestamptz,
  state text NOT NULL,
  identities jsonb NOT NULL,
  payload_sha256 bytea NOT NULL,
  last_delivery_id uuid REFERENCES seerr_deliveries(id),
  revision bigint NOT NULL,
  reconciled_at timestamptz NOT NULL,
  PRIMARY KEY (seerr_instance_id, request_id)
);

CREATE TABLE seerr_request_seasons (
  seerr_instance_id uuid NOT NULL,
  request_id text NOT NULL,
  season_number smallint NOT NULL CHECK (season_number >= 0),
  availability_status integer NOT NULL,
  requested boolean NOT NULL DEFAULT true,
  PRIMARY KEY (seerr_instance_id, request_id, season_number),
  FOREIGN KEY (seerr_instance_id, request_id) REFERENCES seerr_request_snapshots(seerr_instance_id, request_id) ON DELETE CASCADE
);

CREATE TABLE season_monitoring (
  media_id uuid NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
  season_number smallint NOT NULL CHECK (season_number >= 0),
  monitored boolean NOT NULL DEFAULT true,
  prefer_season_pack boolean NOT NULL DEFAULT true,
  search_immediately boolean NOT NULL DEFAULT true,
  state text NOT NULL DEFAULT 'wanted',
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (media_id, season_number)
);

CREATE TABLE indexer_budget_state (
  indexer_instance_id uuid PRIMARY KEY REFERENCES integration_instances(id),
  minimum_interval_ms bigint NOT NULL CHECK (minimum_interval_ms >= 0),
  next_allowed_at timestamptz,
  cooldown_until timestamptz,
  consecutive_failure integer NOT NULL DEFAULT 0,
  last_outcome text,
  revision bigint NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL
);

CREATE TABLE outbox_events (
  id uuid PRIMARY KEY,
  aggregate_type text NOT NULL,
  aggregate_id uuid NOT NULL,
  aggregate_revision bigint NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL,
  available_at timestamptz NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  lease_owner text,
  lease_until timestamptz,
  completed_at timestamptz,
  last_error text,
  UNIQUE (aggregate_type, aggregate_id, aggregate_revision, event_type)
);
CREATE INDEX outbox_ready ON outbox_events(available_at, occurred_at) WHERE completed_at IS NULL;
