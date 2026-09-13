-- Migration 0035: indexer commissioning live gate (Milestone A).
--
-- B1 RATIONALE (retry_after_deferred is retained-gate):
-- retry_after_deferred is structurally a PRE-SEND classification. Migration
-- 0034 admits it in the deferred_before_send outcome set, and a deferral
-- decided before transmission never crosses the boundary and therefore never
-- creates a gate. Migration 0034 contains no CHECK preventing
-- retry_after_deferred from appearing on a 'completed' row, and this migration
-- may not modify migration 0034. A completed step carrying it is therefore
-- representable but semantically incoherent: it asserts a pre-send deferral on
-- a row whose transmitted_at is set. Treating that as proof of resolved
-- post-transmission accounting would clear a safety gate on the strength of a
-- contradiction. It is classified retained-gate so the contradiction fails
-- closed.
--
-- Additive only. No column is dropped, renamed or retyped. No existing
-- constraint, index, function or trigger is modified or replaced. No UPDATE,
-- no INSERT, no backfill: all gates start NULL and all last_outcome_at start
-- NULL. NULL last_outcome_at means "the time of the last outcome is not
-- known", which is the truthful state for every row existing at schema 34.
--
-- Dependency note: the same-indexer trigger check below joins steps -> runs.
-- That binding is frozen by migration 0034's own triggers (runs
-- .indexer_instance_id is in the run trigger's immutable plan-identity list;
-- steps.run_id is in the step trigger's immutable request-identity list). If
-- a future migration relaxes either immutability, the gate invariant must be
-- re-derived.
ALTER TABLE indexer_budget_state ADD COLUMN last_outcome_at timestamptz;
ALTER TABLE indexer_budget_state ADD COLUMN commissioning_gate_step_id uuid;
ALTER TABLE indexer_budget_state ADD CONSTRAINT indexer_budget_state_gate_step_fk
  FOREIGN KEY (commissioning_gate_step_id)
  REFERENCES indexer_commissioning_steps(id)
  ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE indexer_budget_state ADD CONSTRAINT indexer_budget_state_outcome_time
  CHECK (last_outcome_at IS NULL OR last_outcome IS NOT NULL);
CREATE UNIQUE INDEX indexer_budget_state_one_gate_per_step
  ON indexer_budget_state(commissioning_gate_step_id)
  WHERE commissioning_gate_step_id IS NOT NULL;
CREATE FUNCTION enforce_indexer_budget_commissioning_gate()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
  gate_step_state pg_catalog.text;
  gate_step_outcome pg_catalog.text;
  gate_step_transmitted_at pg_catalog.timestamptz;
  gate_step_indexer pg_catalog.uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.commissioning_gate_step_id IS NOT NULL THEN
      RAISE EXCEPTION 'indexer budget state with an unresolved commissioning gate cannot be deleted';
    END IF;
    RETURN OLD;
  END IF;
  -- last_outcome_at monotonicity applies to EVERY UPDATE (SET, CLEAR and
  -- unchanged-gate paths alike): the gate-dispatch branches below must not
  -- return before these checks run.
  IF TG_OP = 'UPDATE' THEN
    IF OLD.last_outcome_at IS NOT NULL AND NEW.last_outcome_at IS NULL THEN
      RAISE EXCEPTION 'indexer commissioning outcome time cannot return to unknown';
    END IF;
    IF OLD.last_outcome_at IS NOT NULL AND NEW.last_outcome_at IS NOT NULL
      AND NEW.last_outcome_at < OLD.last_outcome_at THEN
      RAISE EXCEPTION 'indexer commissioning outcome time cannot regress';
    END IF;
    -- A gated row is bound to its indexer: while a gate is held, the row
    -- must not be moved to another indexer while retaining the old gate.
    IF OLD.commissioning_gate_step_id IS NOT NULL
      AND NEW.indexer_instance_id IS DISTINCT FROM OLD.indexer_instance_id THEN
      RAISE EXCEPTION 'indexer budget state with an unresolved commissioning gate cannot change indexer';
    END IF;
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.commissioning_gate_step_id IS NULL THEN
      RETURN NEW;
    END IF;
    EXECUTE pg_catalog.format(
      'SELECT step.state, step.safe_outcome, step.transmitted_at, run.indexer_instance_id
         FROM %I.indexer_commissioning_steps step
         JOIN %I.indexer_commissioning_runs run ON run.id = step.run_id
        WHERE step.id = $1',
      TG_TABLE_SCHEMA, TG_TABLE_SCHEMA
    ) INTO gate_step_state, gate_step_outcome, gate_step_transmitted_at, gate_step_indexer
      USING NEW.commissioning_gate_step_id;
    IF gate_step_state IS NULL THEN
      RAISE EXCEPTION 'commissioning gate must reference a step of the same indexer';
    END IF;
    IF gate_step_indexer IS DISTINCT FROM NEW.indexer_instance_id THEN
      RAISE EXCEPTION 'commissioning gate must reference a step of the same indexer';
    END IF;
    IF gate_step_state <> 'transmitting' OR gate_step_transmitted_at IS NULL THEN
      RAISE EXCEPTION 'commissioning gate may be established only at the transmission boundary';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.commissioning_gate_step_id IS DISTINCT FROM OLD.commissioning_gate_step_id THEN
    IF OLD.commissioning_gate_step_id IS NULL AND NEW.commissioning_gate_step_id IS NOT NULL THEN
      EXECUTE pg_catalog.format(
        'SELECT step.state, step.safe_outcome, step.transmitted_at, run.indexer_instance_id
           FROM %I.indexer_commissioning_steps step
           JOIN %I.indexer_commissioning_runs run ON run.id = step.run_id
          WHERE step.id = $1',
        TG_TABLE_SCHEMA, TG_TABLE_SCHEMA
      ) INTO gate_step_state, gate_step_outcome, gate_step_transmitted_at, gate_step_indexer
        USING NEW.commissioning_gate_step_id;
      IF gate_step_state IS NULL THEN
        RAISE EXCEPTION 'commissioning gate must reference a step of the same indexer';
      END IF;
      IF gate_step_indexer IS DISTINCT FROM NEW.indexer_instance_id THEN
        RAISE EXCEPTION 'commissioning gate must reference a step of the same indexer';
      END IF;
      IF gate_step_state <> 'transmitting' OR gate_step_transmitted_at IS NULL THEN
        RAISE EXCEPTION 'commissioning gate may be established only at the transmission boundary';
      END IF;
      RETURN NEW;
    END IF;
    IF OLD.commissioning_gate_step_id IS NOT NULL AND NEW.commissioning_gate_step_id IS NOT NULL THEN
      RAISE EXCEPTION 'unresolved commissioning gate cannot be replaced';
    END IF;
    IF OLD.commissioning_gate_step_id IS NOT NULL AND NEW.commissioning_gate_step_id IS NULL THEN
      EXECUTE pg_catalog.format(
        'SELECT step.state, step.safe_outcome
           FROM %I.indexer_commissioning_steps step
          WHERE step.id = $1',
        TG_TABLE_SCHEMA
      ) INTO gate_step_state, gate_step_outcome
        USING OLD.commissioning_gate_step_id;
      IF gate_step_state IS DISTINCT FROM 'completed' THEN
        RAISE EXCEPTION 'commissioning gate clearance requires safely resolved accounting';
      END IF;
      IF gate_step_outcome NOT IN (
        'caps_observed','recipe_verified','no_results','no_plausible_match',
        'unsupported','permission_restricted','contradicted',
        'malformed_response','semantic_ambiguity',
        'rate_limited','quota_exhausted','authentication_failure',
        'configuration_stale',
        'safety_credential_echo','safety_unsafe_redirect','safety_unsafe_text',
        'safety_body_limit','safety_xml_limit'
      ) THEN
        RAISE EXCEPTION 'commissioning gate clearance requires safely resolved accounting';
      END IF;
      RETURN NEW;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER indexer_budget_state_commissioning_gate
BEFORE INSERT OR UPDATE OR DELETE ON indexer_budget_state
FOR EACH ROW EXECUTE FUNCTION enforce_indexer_budget_commissioning_gate();
