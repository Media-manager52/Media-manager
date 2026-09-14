-- Recover held workflows that predate the automatic held retry.
--
-- Held is a recoverable, paced no-selection state: at least one candidate was
-- observed but none satisfied the required release evidence. Before the
-- search service scheduled a retry from held, such a workflow parked with an
-- open round, no pending event, and no automatic or owner-driven trigger. This
-- migration nudges exactly those workflows onto the normal search path. The
-- running search worker then applies the same held retry logic as any newly
-- held workflow. No retained output, exclusion, job, or historical row is
-- changed.
WITH stranded_held AS (
  SELECT
    workflow.id AS workflow_id,
    workflow.revision AS revision,
    now() AS migrated_at
  FROM workflows workflow
  JOIN acquisition_rounds round
    ON round.workflow_id = workflow.id AND round.state = 'open'
  WHERE workflow.terminal_at IS NULL
    AND workflow.state = 'held'
    AND NOT EXISTS (
      SELECT 1 FROM outbox_events event
      WHERE event.aggregate_id = workflow.id
        AND event.aggregate_type = 'workflow'
        AND event.completed_at IS NULL
    )
)
INSERT INTO outbox_events(
  id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at
)
SELECT
  gen_random_uuid(),
  'workflow',
  workflow_id,
  revision,
  'workflow.search_requested',
  jsonb_build_object('workflow_id', workflow_id, 'reason', 'held recovery after search retry fix'),
  migrated_at,
  migrated_at
FROM stranded_held;
