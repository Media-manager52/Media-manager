-- Recover non-terminal workflows whose acquisition round was already exhausted
-- before the paced retry-discovery mechanism existed.
--
-- Migration 0032 marked such rounds exhausted and left the workflow in an
-- attention state, but it did not create a workflow_retry_discovery row. That
-- left the workflow unable to search (the round is exhausted) and unable to
-- open the next round (which requires a ready discovered candidate), so it was
-- a genuine dead end with no automatic or owner-driven recovery.
--
-- This migration restores the documented invariant uniformly: schedule the
-- same deterministic low-priority discovery that any exhausted round receives
-- and park the workflow in the existing round_exhausted_waiting_new_identity
-- state. Retained output, exclusions, jobs, and every historical row are left
-- untouched. Only the owner can open the next round after discovery finds a
-- newly observed eligible release.
CREATE TEMP TABLE legacy_exhausted_round_recovery ON COMMIT DROP AS
SELECT DISTINCT ON (workflow.id)
  workflow.id AS workflow_id,
  workflow.state AS from_state,
  workflow.revision AS revision,
  round.id AS round_id,
  now() AS migrated_at,
  now() + (interval '24 hours' * (0.8 + (abs(hashtext(workflow.id::text)) % 401)::numeric / 1000.0)) AS next_rescan_at
FROM workflows workflow
JOIN acquisition_rounds round
  ON round.workflow_id = workflow.id AND round.state = 'exhausted'
WHERE workflow.terminal_at IS NULL
  AND workflow.state IN ('needs_attention','waiting','held','blocked')
  AND NOT EXISTS (
    SELECT 1 FROM workflow_retry_discovery discovery
    WHERE discovery.workflow_id = workflow.id
  )
ORDER BY workflow.id, round.round_ordinal DESC;

INSERT INTO workflow_retry_discovery(
  workflow_id,exhausted_round_id,state,rescan_generation,next_rescan_at,
  complete_no_new_scans,pending_indexer_ids,created_at,updated_at
)
SELECT workflow_id,round_id,'waiting',1,next_rescan_at,0,NULL,migrated_at,migrated_at
FROM legacy_exhausted_round_recovery;

INSERT INTO outbox_events(
  id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at
)
SELECT
  gen_random_uuid(),
  'workflow_retry_discovery',
  workflow_id,
  1,
  'workflow.discovery_rescan_requested',
  jsonb_build_object('workflow_id', workflow_id, 'rescan_generation', 1),
  migrated_at,
  next_rescan_at
FROM legacy_exhausted_round_recovery;

INSERT INTO workflow_transitions(
  workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at
)
SELECT
  workflow_id,
  revision + 1,
  from_state,
  'waiting',
  'legacy exhausted acquisition round recovery; paced discovery scheduled',
  'migration',
  migrated_at
FROM legacy_exhausted_round_recovery;

UPDATE workflows workflow
SET state='waiting',
    revision=workflow.revision + 1,
    attention_code='round_exhausted_waiting_new_identity',
    attention_detail='Two release-attributable acquisition slots were used before paced discovery existed. Scheduled discovery is now waiting for a newly observed eligible release; opening another round requires the owner.',
    updated_at=legacy.migrated_at
FROM legacy_exhausted_round_recovery legacy
WHERE workflow.id = legacy.workflow_id;
