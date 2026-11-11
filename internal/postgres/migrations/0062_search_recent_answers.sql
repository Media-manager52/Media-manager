-- The provider evidence of a workflow's last complete targeted search that
-- selected a release. When that release then fails verification, the retry
-- picks the next release from these answers instead of asking every indexer
-- again minutes later. completed_indexers has the same shape as
-- search_partial_retries.completed_indexers and is validated the same way
-- before it is reused. Rows are short-lived (hours).
CREATE TABLE search_recent_answers (
  workflow_id uuid PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
  round_id uuid NOT NULL,
  planned_indexer_ids uuid[] NOT NULL CHECK (cardinality(planned_indexer_ids) > 0),
  completed_indexers jsonb NOT NULL CHECK (jsonb_typeof(completed_indexers) = 'object'),
  fetched_at timestamptz NOT NULL,
  FOREIGN KEY (workflow_id, round_id) REFERENCES acquisition_rounds(workflow_id, id) ON DELETE CASCADE
);
CREATE INDEX search_recent_answers_fetched ON search_recent_answers (fetched_at);
