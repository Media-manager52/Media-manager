-- Permit explicitly configured bounded budgets. Existing round snapshots and
-- jobs remain untouched, and the immutable-snapshot trigger stays enforced.
ALTER TABLE acquisition_rounds DROP CONSTRAINT acquisition_rounds_attempt_limit_snapshot_check;
ALTER TABLE acquisition_rounds ADD CONSTRAINT acquisition_rounds_attempt_limit_snapshot_check
  CHECK (attempt_limit_snapshot BETWEEN 1 AND 6);
ALTER TABLE altmount_jobs DROP CONSTRAINT altmount_jobs_round_slot_check;
ALTER TABLE altmount_jobs ADD CONSTRAINT altmount_jobs_round_slot_check
  CHECK (round_slot BETWEEN 1 AND 6);
