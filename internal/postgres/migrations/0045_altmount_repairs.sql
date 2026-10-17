-- A repair replaces a published file whose AltMount source lost Usenet
-- articles (AltMount health status "corrupted"). The repair workflow acquires
-- a replacement for exactly that movie or episode; when Plex confirms the
-- replacement, the broken link is retired. Nothing in AltMount is touched.
--
-- The broken file is either a Media Manager link version, or a library symlink
-- Media Manager did not publish (older content) identified through Plex.
CREATE TABLE altmount_repairs (
  id uuid PRIMARY KEY,
  altmount_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE RESTRICT,
  altmount_health_id bigint NOT NULL,
  altmount_file_path text NOT NULL CHECK (altmount_file_path <> ''),
  broken_link_version_id uuid REFERENCES library_link_versions(id) ON DELETE RESTRICT,
  unmanaged_root_id uuid REFERENCES library_roots(id) ON DELETE RESTRICT,
  -- The library folder holding the broken symlink (container path and the
  -- same folder as Plex sees it); a Plex library can have several folders.
  unmanaged_location text,
  unmanaged_plex_location text,
  unmanaged_relative text,
  unmanaged_target text,
  media_id uuid REFERENCES media_items(id) ON DELETE RESTRICT,
  variant text NOT NULL DEFAULT '',
  repair_workflow_id uuid REFERENCES workflows(id) ON DELETE RESTRICT,
  broken_release_title text NOT NULL DEFAULT '',
  state text NOT NULL CHECK (state IN ('open', 'repaired', 'needs_attention')),
  detail text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  resolved_at timestamptz,
  CHECK ((state = 'open' AND resolved_at IS NULL) OR state <> 'open'),
  CHECK ((broken_link_version_id IS NULL) <> (unmanaged_root_id IS NULL)),
  CHECK (unmanaged_root_id IS NULL OR (unmanaged_relative <> '' AND unmanaged_target LIKE '/%' AND unmanaged_location LIKE '/%' AND unmanaged_plex_location LIKE '/%')),
  CHECK (state <> 'open' OR (media_id IS NOT NULL AND repair_workflow_id IS NOT NULL))
);

-- One repair per broken file at a time.
CREATE UNIQUE INDEX altmount_repairs_one_open_per_link ON altmount_repairs(broken_link_version_id) WHERE state IN ('open', 'needs_attention') AND broken_link_version_id IS NOT NULL;
CREATE UNIQUE INDEX altmount_repairs_one_open_per_unmanaged ON altmount_repairs(unmanaged_location, unmanaged_relative) WHERE state IN ('open', 'needs_attention') AND unmanaged_root_id IS NOT NULL;
CREATE INDEX altmount_repairs_workflow ON altmount_repairs(repair_workflow_id) WHERE state = 'open';
