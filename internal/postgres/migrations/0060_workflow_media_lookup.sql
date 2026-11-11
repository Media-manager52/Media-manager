-- Reads that ask "which workflows belong to this title" (Bibliotek's missing
-- seasons, the wanted list, a title's files) looked up every workflow of a
-- title, ended ones included. Only open workflows had an index, so each lookup
-- scanned the whole table once per title. This index serves all of them.
CREATE INDEX workflows_media_variant ON workflows (media_id, variant, edition);

-- A newly found release for a title whose round is used up now opens the next
-- round by itself (at most once a day per title). When that is refused (for
-- example the profile changed or the retained-output limit is reached) the
-- discovery generation is remembered, so the same refusal is not asked again
-- every minute; a later discovery generation is asked afresh.
ALTER TABLE workflow_retry_discovery ADD COLUMN automatic_round_refused_generation bigint;
