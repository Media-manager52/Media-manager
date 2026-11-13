-- Commissioning v2 is removed. Its runs, steps and the unresolved-attempt
-- gate on indexer_budget_state were only written by the removed v2 executor.
-- Indexer pacing (next_allowed_at, cooldown_until, last_outcome_at and the
-- outcome-time check) stays as it is, and the v1 commission route and the
-- capability profiles are untouched.
--
-- The migration refuses to run if any v2 run or step was ever recorded, so
-- no commissioning history is dropped silently.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM indexer_commissioning_steps)
     OR EXISTS (SELECT 1 FROM indexer_commissioning_runs)
     OR EXISTS (SELECT 1 FROM indexer_budget_state WHERE commissioning_gate_step_id IS NOT NULL) THEN
    RAISE EXCEPTION 'commissioning v2 history exists; export it before upgrading';
  END IF;
END;
$$;

DROP TRIGGER indexer_budget_state_commissioning_gate ON indexer_budget_state;
DROP FUNCTION enforce_indexer_budget_commissioning_gate();
DROP INDEX indexer_budget_state_one_gate_per_step;
ALTER TABLE indexer_budget_state DROP CONSTRAINT indexer_budget_state_gate_step_fk;
ALTER TABLE indexer_budget_state DROP COLUMN commissioning_gate_step_id;

DROP TABLE indexer_commissioning_steps;
DROP TABLE indexer_commissioning_runs;
DROP FUNCTION enforce_indexer_commissioning_step_history();
DROP FUNCTION enforce_indexer_commissioning_run_history();
