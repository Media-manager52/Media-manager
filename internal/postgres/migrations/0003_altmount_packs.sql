CREATE TABLE altmount_jobs (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  candidate_id uuid NOT NULL REFERENCES release_candidates(id),
  altmount_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  attempt_generation integer NOT NULL CHECK (attempt_generation > 0),
  upload_name text NOT NULL,
  state text NOT NULL CHECK (state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','import_complete','failed','cancel_requested','cancelled','removed','needs_attention')),
  external_queue_id bigint CHECK (external_queue_id > 0),
  submission_started_at timestamptz NOT NULL,
  submitted_at timestamptz,
  last_reconciled_at timestamptz,
  error_code text,
  error_detail text,
  revision bigint NOT NULL CHECK (revision > 0),
  UNIQUE (workflow_id, attempt_generation),
  UNIQUE (altmount_instance_id, upload_name),
  UNIQUE (altmount_instance_id, external_queue_id)
);

CREATE TABLE altmount_job_observations (
  id uuid PRIMARY KEY,
  job_id uuid NOT NULL REFERENCES altmount_jobs(id) ON DELETE CASCADE,
  observation_key bytea NOT NULL,
  external_state text NOT NULL,
  payload_sha256 bytea NOT NULL,
  source_timestamp timestamptz,
  observed_at timestamptz NOT NULL,
  UNIQUE (job_id, observation_key)
);

CREATE TABLE altmount_output_inventories (
  id uuid PRIMARY KEY,
  job_id uuid NOT NULL REFERENCES altmount_jobs(id) ON DELETE RESTRICT,
  generation integer NOT NULL CHECK (generation > 0),
  root_relative text NOT NULL CHECK (root_relative <> '' AND root_relative !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  inventory_digest bytea NOT NULL,
  observed_at timestamptz NOT NULL,
  frozen_at timestamptz NOT NULL,
  UNIQUE (job_id, generation),
  UNIQUE (job_id, inventory_digest)
);

CREATE TABLE altmount_output_assets (
  id uuid PRIMARY KEY,
  inventory_id uuid NOT NULL REFERENCES altmount_output_inventories(id) ON DELETE RESTRICT,
  relative_path text NOT NULL CHECK (relative_path <> '' AND relative_path !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  size_bytes bigint NOT NULL CHECK (size_bytes > 0),
  media_kind text,
  content_identity text,
  media_info jsonb,
  UNIQUE (inventory_id, relative_path)
);

CREATE TABLE pack_expectation_sets (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  series_identity text NOT NULL,
  season smallint NOT NULL CHECK (season >= 0),
  source_revision bigint NOT NULL,
  expectation_digest bytea NOT NULL,
  created_at timestamptz NOT NULL,
  frozen_at timestamptz NOT NULL,
  UNIQUE (workflow_id, expectation_digest)
);

CREATE TABLE pack_expected_episodes (
  expectation_id uuid NOT NULL REFERENCES pack_expectation_sets(id) ON DELETE RESTRICT,
  season smallint NOT NULL CHECK (season >= 0),
  episode smallint NOT NULL CHECK (episode > 0),
  provider_episode_id text,
  title text NOT NULL DEFAULT '',
  PRIMARY KEY (expectation_id, season, episode)
);

CREATE TABLE pack_evaluations (
  id uuid PRIMARY KEY,
  expectation_id uuid NOT NULL REFERENCES pack_expectation_sets(id) ON DELETE RESTRICT,
  inventory_id uuid NOT NULL REFERENCES altmount_output_inventories(id) ON DELETE RESTRICT,
  evaluator_version integer NOT NULL,
  inventory_digest bytea NOT NULL,
  verdict text NOT NULL CHECK (verdict IN ('verified','incomplete','needs_review','invalid')),
  evaluated_at timestamptz NOT NULL,
  UNIQUE (expectation_id, inventory_digest, evaluator_version)
);

CREATE TABLE pack_content_groups (
  id text PRIMARY KEY,
  evaluation_id uuid NOT NULL REFERENCES pack_evaluations(id) ON DELETE RESTRICT,
  ordinal integer NOT NULL,
  UNIQUE (evaluation_id, ordinal)
);
CREATE TABLE pack_group_assets (group_id text NOT NULL REFERENCES pack_content_groups(id) ON DELETE RESTRICT, asset_id uuid NOT NULL REFERENCES altmount_output_assets(id) ON DELETE RESTRICT, PRIMARY KEY (group_id, asset_id));
CREATE TABLE pack_group_episodes (group_id text NOT NULL REFERENCES pack_content_groups(id) ON DELETE RESTRICT, season smallint NOT NULL, episode smallint NOT NULL, PRIMARY KEY (group_id, season, episode));
CREATE TABLE pack_issues (id uuid PRIMARY KEY, evaluation_id uuid NOT NULL REFERENCES pack_evaluations(id) ON DELETE RESTRICT, code text NOT NULL, path text, detail text NOT NULL, episodes jsonb NOT NULL DEFAULT '[]'::jsonb);
