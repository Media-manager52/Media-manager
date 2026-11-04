-- An integration's secret entered on the website: AES-256-GCM ciphertext
-- bound to the integration, never returned by any route. The integration's
-- secret_ref becomes 'sealed:integration:<id>'; references to environment
-- variables keep working unchanged.
CREATE TABLE integration_secrets (
  integration_id uuid PRIMARY KEY REFERENCES integration_instances(id) ON DELETE CASCADE,
  ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 29 AND 8192),
  updated_at timestamptz NOT NULL
);
