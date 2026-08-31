CREATE TABLE subtitle_verifications (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  altmount_job_id uuid NOT NULL UNIQUE REFERENCES altmount_jobs(id) ON DELETE RESTRICT,
  candidate_id uuid NOT NULL REFERENCES release_candidates(id) ON DELETE RESTRICT,
  inventory_id uuid NOT NULL REFERENCES altmount_output_inventories(id) ON DELETE RESTRICT,
  attempt_generation integer NOT NULL CHECK (attempt_generation > 0),
  status text NOT NULL CHECK (status IN ('verified','unverified')),
  required_languages text[] NOT NULL DEFAULT '{}',
  preferred_languages text[] NOT NULL DEFAULT '{}',
  observed_languages text[] NOT NULL DEFAULT '{}',
  evidence jsonb NOT NULL,
  failure_reasons jsonb NOT NULL DEFAULT '[]'::jsonb,
  retry_state text NOT NULL CHECK (retry_state IN ('not_needed','queued','exhausted','no_alternate')),
  verified_at timestamptz,
  created_at timestamptz NOT NULL,
  UNIQUE (workflow_id, attempt_generation),
  FOREIGN KEY (workflow_id, candidate_id) REFERENCES release_candidates(workflow_id, id)
);

CREATE TABLE subtitle_verification_companions (
  verification_id uuid NOT NULL REFERENCES subtitle_verifications(id) ON DELETE RESTRICT,
  video_asset_id uuid NOT NULL REFERENCES altmount_output_assets(id) ON DELETE RESTRICT,
  subtitle_asset_id uuid NOT NULL REFERENCES altmount_output_assets(id) ON DELETE RESTRICT,
  language text NOT NULL,
  raw_language text NOT NULL,
  forced boolean,
  hearing_impaired boolean,
  relative_path text NOT NULL CHECK (relative_path <> '' AND relative_path !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  PRIMARY KEY (verification_id, video_asset_id, subtitle_asset_id),
  UNIQUE (verification_id, subtitle_asset_id)
);

CREATE TABLE workflow_release_exclusions (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  candidate_id uuid NOT NULL,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE RESTRICT,
  native_key text NOT NULL,
  verification_id uuid NOT NULL REFERENCES subtitle_verifications(id) ON DELETE RESTRICT,
  reason text NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id, candidate_id),
  UNIQUE (workflow_id, indexer_instance_id, native_key),
  FOREIGN KEY (workflow_id, candidate_id) REFERENCES release_candidates(workflow_id, id)
);

CREATE TABLE library_link_companions (
  id uuid PRIMARY KEY,
  link_version_id uuid NOT NULL REFERENCES library_link_versions(id) ON DELETE RESTRICT,
  source_asset_id uuid NOT NULL REFERENCES altmount_output_assets(id) ON DELETE RESTRICT,
  language text NOT NULL,
  forced boolean,
  hearing_impaired boolean,
  source_relative text NOT NULL CHECK (source_relative <> '' AND source_relative !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  destination_relative text NOT NULL CHECK (destination_relative <> '' AND destination_relative !~ '(^/|\\\\|(^|/)\.\.(/|$))'),
  plex_visible_target text NOT NULL,
  state text NOT NULL CHECK (state IN ('planned','publishing','verified','current','displaced','quarantined')),
  verified_at timestamptz,
  displaced_at timestamptz,
  UNIQUE (link_version_id, source_asset_id),
  UNIQUE (link_version_id, destination_relative)
);

CREATE INDEX subtitle_verifications_owner_review
  ON subtitle_verifications(created_at DESC, status, retry_state);

CREATE INDEX library_link_companions_lifecycle
  ON library_link_companions(link_version_id, state);
