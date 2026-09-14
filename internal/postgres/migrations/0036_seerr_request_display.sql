ALTER TABLE seerr_request_snapshots
  ADD COLUMN title text,
  ADD COLUMN year smallint CHECK (year BETWEEN 1800 AND 9999),
  ADD COLUMN media_kind text CHECK (media_kind IN ('movie', 'series')),
  ADD COLUMN poster_url text,
  ADD COLUMN requested_at timestamptz,
  ADD COLUMN requester text;

CREATE INDEX seerr_request_snapshots_ordering ON seerr_request_snapshots(reconciled_at DESC, request_id DESC);
