-- Credentials are AES-256-GCM sealed by the API with a separate server key.
-- Provider secrets never enter integration config, read models, or audit detail.
CREATE TABLE oldboys_credentials (
    singleton boolean PRIMARY KEY CHECK(singleton),
    sealed bytea NOT NULL CHECK(octet_length(sealed) BETWEEN 28 AND 32768),
    updated_at timestamptz NOT NULL
);
