-- One indexer answer to a whole-season search, shared by the episode
-- workflows of that season. A back-catalogue season with many missing
-- episodes used to send one search per episode to every indexer; the answer
-- to "season N" contains all of them. Rows are short-lived (hours) and only
-- point at release_observations, which stay the evidence. complete is false
-- when the indexer had more results than were read; such an answer is never
-- shared and the episodes search on their own.
CREATE TABLE season_search_responses (
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE CASCADE,
  request_fingerprint bytea NOT NULL CHECK (length(request_fingerprint) = 32),
  fetched_at timestamptz NOT NULL,
  complete boolean NOT NULL,
  observation_ids uuid[] NOT NULL DEFAULT '{}',
  locator_refs text[] NOT NULL DEFAULT '{}',
  CHECK (cardinality(observation_ids) = cardinality(locator_refs)),
  PRIMARY KEY (indexer_instance_id, request_fingerprint)
);
CREATE INDEX season_search_responses_fetched ON season_search_responses (fetched_at);
