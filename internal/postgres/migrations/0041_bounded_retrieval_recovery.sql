-- Retrieval failures consume a separate, durable budget within an existing
-- acquisition round. Historical failures stay untouched; no work is requeued.
ALTER TABLE workflow_release_exclusions
  ADD COLUMN retrieval_round_id uuid,
  ADD COLUMN retrieval_error_code text,
  ADD CONSTRAINT workflow_release_exclusions_retrieval_round_fk
    FOREIGN KEY (workflow_id,retrieval_round_id)
    REFERENCES acquisition_rounds(workflow_id,id),
  ADD CONSTRAINT workflow_release_exclusions_retrieval_detail_check
    CHECK ((retrieval_round_id IS NULL AND retrieval_error_code IS NULL)
      OR (retrieval_round_id IS NOT NULL AND retrieval_error_code IS NOT NULL
        AND failure_kind='release_retrieval'));

CREATE INDEX workflow_release_exclusions_retrieval_round
  ON workflow_release_exclusions(retrieval_round_id,indexer_instance_id)
  WHERE retrieval_round_id IS NOT NULL;
