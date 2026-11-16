-- Three additions; none changes an existing column or constraint, so a binary
-- from before this migration keeps working on the migrated schema.

-- 1. An indexer whose newest uploads are read as separate movie and series
--    requests keeps one reader state per request ("stream"): its own progress,
--    remembered upload IDs and gap count. Indexers read with one request keep
--    using indexer_feed_state unchanged.
CREATE TABLE indexer_feed_stream_state (
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE CASCADE,
  stream text NOT NULL CHECK (stream IN ('movie','series')),
  last_read_at timestamptz,
  next_read_at timestamptz,
  interval_seconds integer NOT NULL DEFAULT 0 CHECK (interval_seconds >= 0),
  last_outcome text NOT NULL DEFAULT '',
  consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
  last_item_count integer NOT NULL DEFAULT 0,
  last_new_count integer NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL,
  recent_guids text[] NOT NULL DEFAULT '{}',
  reads_total bigint NOT NULL DEFAULT 0,
  gap_reads_total bigint NOT NULL DEFAULT 0,
  last_read_complete boolean NOT NULL DEFAULT true,
  PRIMARY KEY (indexer_instance_id, stream)
);

-- 2. The usage counters an indexer states in its own answers
--    (newznab:apilimits). They only ever tighten Media Manager's own request
--    reservations; see indexers.ProviderLimitDecision. NULL means the indexer
--    did not state that number.
CREATE TABLE indexer_provider_limits (
  indexer_instance_id uuid PRIMARY KEY REFERENCES integration_instances(id) ON DELETE CASCADE,
  observed_at timestamptz NOT NULL,
  query_used integer CHECK (query_used >= 0),
  query_max integer CHECK (query_max > 0),
  query_oldest_at timestamptz,
  grab_used integer CHECK (grab_used >= 0),
  grab_max integer CHECK (grab_max > 0),
  grab_oldest_at timestamptz
);

-- 3. Queue timing per event. first_ready_at is when the event first became
--    due; ready_wait_ms adds up the time it was due but not yet claimed;
--    active_ms adds up the time a worker held it. Time between attempts that
--    is neither (a scheduled retry delay) is the remainder. A constant default
--    makes these additions metadata-only.
ALTER TABLE outbox_events
  ADD COLUMN first_ready_at timestamptz,
  ADD COLUMN first_claimed_at timestamptz,
  ADD COLUMN last_claimed_at timestamptz,
  ADD COLUMN claim_count integer NOT NULL DEFAULT 0,
  ADD COLUMN ready_wait_ms bigint NOT NULL DEFAULT 0,
  ADD COLUMN active_ms bigint NOT NULL DEFAULT 0;
