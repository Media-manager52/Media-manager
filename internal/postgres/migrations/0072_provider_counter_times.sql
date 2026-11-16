-- Partial provider answers must not erase or refresh another counter. Limits
-- without a counter have their own age; probes are spaced by actual reservation.
ALTER TABLE indexer_provider_limits
  ADD COLUMN query_observed_at timestamptz,
  ADD COLUMN grab_observed_at timestamptz,
  ADD COLUMN query_limit_observed_at timestamptz,
  ADD COLUMN grab_limit_observed_at timestamptz,
  ADD COLUMN query_last_probe_at timestamptz,
  ADD COLUMN grab_last_probe_at timestamptz,
  ADD COLUMN query_counter_max integer CHECK (query_counter_max>0),
  ADD COLUMN grab_counter_max integer CHECK (grab_counter_max>0);

UPDATE indexer_provider_limits SET
  query_observed_at=CASE WHEN query_used IS NOT NULL THEN observed_at END,
  grab_observed_at=CASE WHEN grab_used IS NOT NULL THEN observed_at END,
  query_limit_observed_at=CASE WHEN query_max IS NOT NULL THEN observed_at END,
  grab_limit_observed_at=CASE WHEN grab_max IS NOT NULL THEN observed_at END,
  query_counter_max=CASE WHEN query_used IS NOT NULL THEN query_max END,
  grab_counter_max=CASE WHEN grab_used IS NOT NULL THEN grab_max END;

-- Carry forward actual requests made after an exhausted reading, so upgrading
-- cannot grant another probe immediately after the previous binary sent one.
UPDATE indexer_provider_limits limits SET
  query_last_probe_at=(SELECT max(reserved_at) FROM indexer_request_reservations
    WHERE indexer_instance_id=limits.indexer_instance_id AND request_family='query'
      AND reserved_at>=limits.observed_at AND limits.query_used>=limits.query_max),
  grab_last_probe_at=(SELECT max(reserved_at) FROM indexer_request_reservations
    WHERE indexer_instance_id=limits.indexer_instance_id AND request_family='grab'
      AND reserved_at>=limits.observed_at AND limits.grab_used>=limits.grab_max);
