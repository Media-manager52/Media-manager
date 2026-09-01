CREATE TABLE indexer_request_reservations (
  id uuid PRIMARY KEY,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE CASCADE,
  request_family text NOT NULL CHECK (request_family IN ('query','grab')),
  reserved_at timestamptz NOT NULL
);

CREATE INDEX indexer_request_reservations_window
  ON indexer_request_reservations(indexer_instance_id,request_family,reserved_at);
