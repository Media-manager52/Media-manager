-- A newest-uploads match may now open the next bounded round for a title
-- whose round is used up and whose discovery already holds an eligible
-- release (the owner's "start next round", at most once a day per title).
-- The match records that as its own outcome.
ALTER TABLE indexer_feed_matches DROP CONSTRAINT indexer_feed_matches_outcome_check;
ALTER TABLE indexer_feed_matches ADD CONSTRAINT indexer_feed_matches_outcome_check
  CHECK (outcome IN ('pending','search_advanced','search_due','discovery_advanced','round_opened','not_applicable','expired'));

-- Matches that brought a discovery forward are looked at again while they are
-- fresh, in case that discovery found a release.
CREATE INDEX indexer_feed_matches_discovery ON indexer_feed_matches (settled_at) WHERE outcome = 'discovery_advanced';
