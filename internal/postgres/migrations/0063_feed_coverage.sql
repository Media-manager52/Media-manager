-- The newest-uploads reader remembers the upload IDs it saw last, so a read
-- after a restart continues where the last one stopped instead of starting
-- over, and it counts reads that could not reach an upload it had seen
-- before (uploads may have been missed between them).
ALTER TABLE indexer_feed_state
  ADD COLUMN recent_guids text[] NOT NULL DEFAULT '{}',
  ADD COLUMN reads_total bigint NOT NULL DEFAULT 0,
  ADD COLUMN gap_reads_total bigint NOT NULL DEFAULT 0,
  ADD COLUMN last_read_complete boolean NOT NULL DEFAULT true;
