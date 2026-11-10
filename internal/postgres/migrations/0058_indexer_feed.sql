-- Newest-uploads watching. Each enabled Newznab indexer's list of newest
-- uploads is read a few times an hour and compared with the titles that are
-- still waiting. The feed never selects a release: a match only brings the
-- title's own, already scheduled search forward, and that search applies every
-- language, quality and identity rule as before.

-- What the reader last did for one indexer, so pacing and failure backoff
-- survive a restart.
CREATE TABLE indexer_feed_state (
  indexer_instance_id uuid PRIMARY KEY REFERENCES integration_instances(id) ON DELETE CASCADE,
  last_read_at timestamptz,
  next_read_at timestamptz,
  interval_seconds integer NOT NULL DEFAULT 0 CHECK (interval_seconds >= 0),
  last_outcome text NOT NULL DEFAULT '',
  consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
  last_item_count integer NOT NULL DEFAULT 0,
  last_new_count integer NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL
);

-- One new upload that looked like a waiting title, and what was done about
-- it. The unique key makes a re-read of the same upload a no-op.
CREATE TABLE indexer_feed_matches (
  id uuid PRIMARY KEY,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE CASCADE,
  native_guid text NOT NULL,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  release_title text NOT NULL,
  seen_at timestamptz NOT NULL,
  outcome text NOT NULL DEFAULT 'pending' CHECK (outcome IN ('pending','search_advanced','search_due','discovery_advanced','not_applicable','expired')),
  settled_at timestamptz,
  UNIQUE (indexer_instance_id, native_guid, workflow_id)
);

CREATE INDEX indexer_feed_matches_pending ON indexer_feed_matches (workflow_id, seen_at) WHERE outcome = 'pending';
CREATE INDEX indexer_feed_matches_seen ON indexer_feed_matches (seen_at);
