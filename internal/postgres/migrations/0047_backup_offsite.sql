ALTER TABLE backup_records
  ADD COLUMN offsite_state text CHECK (offsite_state IS NULL OR offsite_state IN ('uploaded','failed')),
  ADD COLUMN offsite_key text,
  ADD COLUMN offsite_error text,
  ADD COLUMN offsite_uploaded_at timestamptz;
