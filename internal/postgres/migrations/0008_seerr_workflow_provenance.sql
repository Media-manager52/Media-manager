CREATE TABLE seerr_request_workflows (
  seerr_instance_id uuid NOT NULL,
  request_id text NOT NULL,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  attached_snapshot_revision bigint NOT NULL CHECK (attached_snapshot_revision > 0),
  owns_workflow boolean NOT NULL,
  PRIMARY KEY (seerr_instance_id, request_id, workflow_id),
  FOREIGN KEY (seerr_instance_id, request_id)
    REFERENCES seerr_request_snapshots(seerr_instance_id, request_id)
    ON DELETE CASCADE
);

CREATE INDEX seerr_request_workflows_by_workflow
  ON seerr_request_workflows(workflow_id);
