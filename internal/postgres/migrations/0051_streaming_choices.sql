-- Viewer release choices behind a streaming stream list. They used to live
-- only in process memory, so every restart (each deploy) turned a stream list
-- the player still showed into "release choice expired". A row holds only
-- what retrieval needs: the indexer instance, a fingerprint of that instance's
-- configuration and secret reference (never a secret), and the release's
-- native id, title and sanitized locator reference. Rows expire with the
-- stream list (six hours) and are pruned on the next write.
CREATE TABLE IF NOT EXISTS streaming_choices (
    token                   uuid PRIMARY KEY,
    request                 jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
    integration_id          uuid NOT NULL REFERENCES integration_instances(id) ON DELETE CASCADE,
    integration_fingerprint bytea NOT NULL CHECK (octet_length(integration_fingerprint) = 32),
    native_guid             text NOT NULL CHECK (native_guid <> ''),
    title                   text NOT NULL,
    locator_ref             text NOT NULL,
    created_at              timestamptz NOT NULL,
    expires_at              timestamptz NOT NULL,
    CONSTRAINT streaming_choices_times CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS streaming_choices_expiry_idx
    ON streaming_choices(expires_at);
