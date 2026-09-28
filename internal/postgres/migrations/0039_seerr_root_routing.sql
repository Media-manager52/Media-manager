-- Seerr request routing: honor the root folder and profile the requester chose in
-- Seerr instead of always falling back to the default library root.
--
-- seerr_request_snapshots keeps the routing provenance observed from the Seerr
-- request (serverId/profileId/rootFolder/is4k). seerr_library_routes maps a
-- Seerr root folder, per instance and media kind, to a Media Manager library
-- root and an optional quality-profile override. When a request carries a root
-- folder without an enabled route, intake pauses with needs_configuration
-- instead of silently publishing into the wrong library.

ALTER TABLE seerr_request_snapshots
  ADD COLUMN seerr_server_id integer,
  ADD COLUMN seerr_profile_id integer,
  ADD COLUMN seerr_root_folder text,
  ADD COLUMN seerr_is_4k boolean NOT NULL DEFAULT false;

CREATE TABLE seerr_library_routes (
  id uuid PRIMARY KEY,
  seerr_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE CASCADE,
  media_kind text NOT NULL CHECK (media_kind IN ('movie', 'series')),
  root_folder text NOT NULL,
  library_root_id uuid NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
  quality_profile_id uuid REFERENCES quality_profiles(id) ON DELETE SET NULL,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (seerr_instance_id, media_kind, root_folder)
);

CREATE INDEX seerr_library_routes_lookup ON seerr_library_routes(seerr_instance_id, media_kind, enabled);
CREATE INDEX seerr_request_snapshots_root_folder ON seerr_request_snapshots(seerr_instance_id, media_kind, seerr_root_folder);
