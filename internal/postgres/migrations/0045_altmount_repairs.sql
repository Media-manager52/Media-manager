-- A repair replaces a published file whose AltMount source lost Usenet
-- articles (AltMount health status "corrupted"). The repair workflow acquires
-- a replacement for exactly that movie or episode; when Plex confirms the
-- replacement, the broken link is displaced. Nothing in AltMount is touched.
CREATE TABLE altmount_repairs (
  id uuid PRIMARY KEY,
  altmount_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE RESTRICT,
  altmount_health_id bigint NOT NULL,
  altmount_file_path text NOT NULL CHECK (altmount_file_path <> ''),
  broken_link_version_id uuid NOT NULL REFERENCES library_link_versions(id) ON DELETE RESTRICT,
  media_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
  variant text NOT NULL,
  repair_workflow_id uuid REFERENCES workflows(id) ON DELETE RESTRICT,
  broken_release_title text NOT NULL DEFAULT '',
  state text NOT NULL CHECK (state IN ('open', 'repaired', 'needs_attention')),
  detail text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  resolved_at timestamptz,
  CHECK ((state = 'open' AND resolved_at IS NULL) OR state <> 'open')
);

-- One repair per broken published file at a time.
CREATE UNIQUE INDEX altmount_repairs_one_open_per_link ON altmount_repairs(broken_link_version_id) WHERE state IN ('open', 'needs_attention');
CREATE INDEX altmount_repairs_workflow ON altmount_repairs(repair_workflow_id) WHERE state = 'open';
