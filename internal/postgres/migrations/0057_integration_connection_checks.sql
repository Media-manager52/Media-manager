-- The newest result of the owner's Test forbindelser, so "tested" stays
-- visible and distinct from "saved" after the page is left.
CREATE TABLE integration_connection_checks (
  integration_id uuid PRIMARY KEY REFERENCES integration_instances(id) ON DELETE CASCADE,
  ok boolean NOT NULL,
  checked_at timestamptz NOT NULL
);
