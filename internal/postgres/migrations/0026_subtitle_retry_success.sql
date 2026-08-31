ALTER TABLE subtitle_verifications
  DROP CONSTRAINT subtitle_verifications_retry_state_check;

ALTER TABLE subtitle_verifications
  ADD CONSTRAINT subtitle_verifications_retry_state_check
  CHECK (retry_state IN ('not_needed','queued','succeeded','exhausted','no_alternate'));

UPDATE subtitle_verifications pending
SET retry_state='succeeded'
WHERE pending.retry_state='queued'
  AND EXISTS (
    SELECT 1
    FROM subtitle_verifications verified
    WHERE verified.workflow_id=pending.workflow_id
      AND verified.attempt_generation>pending.attempt_generation
      AND verified.status='verified'
  );
