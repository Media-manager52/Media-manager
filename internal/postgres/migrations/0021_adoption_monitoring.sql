ALTER TABLE adoption_batches
  ADD COLUMN replacement_search_authorized_at timestamptz,
  ADD COLUMN replacement_search_authorized_by text,
  ADD CONSTRAINT adoption_batches_replacement_search_authorization CHECK (
    (replacement_search_authorized_at IS NULL AND replacement_search_authorized_by IS NULL) OR
    (replacement_search_authorized_at IS NOT NULL AND replacement_search_authorized_by = 'owner')
  );

CREATE TABLE adoption_monitoring_applications (
  batch_id uuid NOT NULL REFERENCES adoption_batches(id) ON DELETE RESTRICT,
  media_id uuid NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
  variant text NOT NULL CHECK (variant <> ''),
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE RESTRICT,
  search_immediately boolean NOT NULL,
  replacement_authorized boolean NOT NULL,
  applied_at timestamptz NOT NULL,
  PRIMARY KEY (batch_id, media_id, variant)
);
