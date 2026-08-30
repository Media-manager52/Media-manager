CREATE TABLE workflows (
  id uuid PRIMARY KEY,
  media_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
  state text NOT NULL CHECK (state IN ('monitored','waiting','searching','candidate_selected','submitting','transport_unknown','queued','streaming_ready','import_complete','verifying','publishing','plex_scanning','plex_confirmed','needs_attention','held','blocked','quarantined','cancelled')),
  root_id uuid NOT NULL REFERENCES library_roots(id),
  quality_profile_id uuid NOT NULL REFERENCES quality_profiles(id),
	  download_client_id uuid REFERENCES integration_instances(id),
  variant text NOT NULL DEFAULT 'default',
  selected_release_id uuid,
  grab_attempt_id uuid,
  attention_code text,
  attention_detail text,
  revision bigint NOT NULL CHECK (revision > 0),
  terminal_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX workflows_one_active_variant ON workflows(media_id, variant) WHERE terminal_at IS NULL;
CREATE INDEX workflows_reconcile ON workflows(updated_at) WHERE terminal_at IS NULL;

CREATE TABLE workflow_transitions (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  to_revision bigint NOT NULL,
  from_state text NOT NULL,
  to_state text NOT NULL,
  reason text NOT NULL,
  actor text NOT NULL,
  occurred_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id, to_revision)
);

CREATE TABLE search_runs (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  mode text NOT NULL CHECK (mode IN ('feed','targeted','manual','commissioning')),
  query_fingerprint bytea NOT NULL,
  state text NOT NULL,
  policy_version bigint NOT NULL,
  started_at timestamptz NOT NULL,
  completed_at timestamptz
);

CREATE TABLE release_observations (
  id uuid PRIMARY KEY,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  operation_key text NOT NULL,
  native_guid text NOT NULL,
  title text NOT NULL,
  body_sha256 bytea NOT NULL,
  raw_fields jsonb NOT NULL,
  normalized_facts jsonb NOT NULL,
  parse_version text NOT NULL,
  problems jsonb NOT NULL DEFAULT '[]'::jsonb,
  observed_at timestamptz NOT NULL
);
CREATE INDEX release_observations_native ON release_observations(indexer_instance_id, native_guid, observed_at DESC);

CREATE TABLE release_candidates (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  search_run_id uuid NOT NULL REFERENCES search_runs(id) ON DELETE CASCADE,
  observation_id uuid NOT NULL REFERENCES release_observations(id),
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id),
  native_key text NOT NULL,
  title text NOT NULL,
  size_bytes bigint CHECK (size_bytes >= 0),
  published_at timestamptz,
  download_locator_ref text NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (workflow_id, id),
  UNIQUE (workflow_id, indexer_instance_id, native_key)
);

CREATE TABLE candidate_decisions (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  candidate_id uuid NOT NULL,
  policy_version bigint NOT NULL,
  verdict text NOT NULL CHECK (verdict IN ('eligible','held','rejected')),
  score integer NOT NULL,
  reasons jsonb NOT NULL,
  actor text NOT NULL,
  decided_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id, candidate_id, policy_version),
  FOREIGN KEY (workflow_id, candidate_id) REFERENCES release_candidates(workflow_id, id)
);

CREATE TABLE candidate_selections (
  workflow_id uuid PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
  candidate_id uuid NOT NULL,
  selected_at timestamptz NOT NULL,
  actor text NOT NULL,
  reason text NOT NULL,
  FOREIGN KEY (workflow_id, candidate_id) REFERENCES release_candidates(workflow_id, id)
);

ALTER TABLE workflows ADD CONSTRAINT workflows_selected_release_fk FOREIGN KEY (id, selected_release_id) REFERENCES release_candidates(workflow_id, id) DEFERRABLE INITIALLY DEFERRED;
