-- Release Intelligence is a recently-observed feed. Semantic observation reuse
-- deliberately keeps release_observations.observed_at immutable, so the feed
-- must sort by the candidate's mutable last_observed_at instead.
--
-- The migration runner executes each migration inside a transaction. A normal
-- CREATE INDEX is therefore used here; CREATE INDEX CONCURRENTLY cannot run in
-- that transaction. The index build takes a write-blocking lock on
-- release_candidates while it runs.
CREATE INDEX IF NOT EXISTS release_candidates_last_observed_id_idx
  ON release_candidates(last_observed_at DESC, id DESC);
