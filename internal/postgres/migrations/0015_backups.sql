CREATE TABLE backup_records (
  id uuid PRIMARY KEY,
  state text NOT NULL CHECK (state IN ('queued','running','verified','failed')),
  format text NOT NULL CHECK (format='postgres_custom_aes256gcm_v1'),
  filename text,
  size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes >= 0),
  sha256 text CHECK (sha256 IS NULL OR sha256 ~ '^[a-f0-9]{64}$'),
  schema_version bigint,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text,
  created_at timestamptz NOT NULL,
  started_at timestamptz,
  completed_at timestamptz
);
CREATE INDEX backup_records_created_idx ON backup_records(created_at DESC);
