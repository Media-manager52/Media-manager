-- The latest progress AltMount itself reported for a job. These columns are
-- observations only: they never drive workflow state, and a missing value
-- means AltMount did not report one (the UI then shows no percentage).
ALTER TABLE altmount_jobs
  ADD COLUMN progress_percent smallint CHECK (progress_percent BETWEEN 0 AND 100),
  ADD COLUMN progress_stage text CHECK (progress_stage IS NULL OR length(progress_stage) <= 64),
  ADD COLUMN progress_observed_at timestamptz;
