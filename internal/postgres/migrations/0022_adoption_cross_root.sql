ALTER TABLE adoption_operations
  ADD COLUMN source_root_id uuid REFERENCES library_roots(id) ON DELETE RESTRICT,
  ADD COLUMN destination_root_id uuid REFERENCES library_roots(id) ON DELETE RESTRICT,
  ADD COLUMN transfer_mode text,
  ADD COLUMN source_sha256 bytea;

UPDATE adoption_operations operation
SET source_root_id = batch.default_root_id,
    destination_root_id = batch.default_root_id,
    transfer_mode = CASE WHEN operation.kind = 'unknown_preserved' OR operation.source_relative = operation.destination_relative THEN 'preserve' ELSE 'rename' END,
    source_sha256 = decode(repeat('00', 32), 'hex')
FROM adoption_batches batch
WHERE batch.id = operation.batch_id;

ALTER TABLE adoption_operations
  ALTER COLUMN source_root_id SET NOT NULL,
  ALTER COLUMN destination_root_id SET NOT NULL,
  ALTER COLUMN transfer_mode SET NOT NULL,
  ALTER COLUMN source_sha256 SET NOT NULL,
  ADD CONSTRAINT adoption_operations_transfer_mode_valid CHECK (transfer_mode IN ('preserve','rename','copy_remove')),
  ADD CONSTRAINT adoption_operations_source_sha256_valid CHECK (octet_length(source_sha256) = 32),
  ADD CONSTRAINT adoption_operations_transfer_roots_consistent CHECK (
    transfer_mode = 'copy_remove' OR source_root_id = destination_root_id
  );
