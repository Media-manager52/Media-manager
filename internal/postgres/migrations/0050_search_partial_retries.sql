-- Durable partial-search completion state. A retry may reuse only evidence
-- produced by providers that completed under the exact same search intent.
-- Candidate and observation IDs are both stored: the candidate identity is
-- stable, while the immutable observation must still be the candidate's
-- current evidence pointer before it can be reused.
CREATE TABLE IF NOT EXISTS search_partial_retries (
    id                  uuid PRIMARY KEY,
    workflow_id         uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    round_id            uuid NOT NULL,
    mode                text NOT NULL CHECK (mode IN ('targeted', 'recovery')),
    intent_fingerprint  bytea NOT NULL CHECK (octet_length(intent_fingerprint) = 32),
    planned_indexer_ids uuid[] NOT NULL CHECK (cardinality(planned_indexer_ids) > 0),
    completed_indexers  jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(completed_indexers) = 'object'),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL,
    CONSTRAINT search_partial_retries_round_fk
        FOREIGN KEY (workflow_id, round_id)
        REFERENCES acquisition_rounds(workflow_id, id) ON DELETE CASCADE,
    CONSTRAINT search_partial_retries_intent_key
        UNIQUE (workflow_id, round_id, mode, intent_fingerprint),
    CONSTRAINT search_partial_retries_times CHECK (updated_at >= created_at AND expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS search_partial_retries_expiry_idx
    ON search_partial_retries(expires_at);
