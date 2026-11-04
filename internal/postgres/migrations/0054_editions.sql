-- A title can be kept in two editions: the standard edition (every existing
-- library) and a 4K edition (a library root marked '4k'). Each edition is
-- acquired, published, upgraded and repaired on its own: a 4K publication never
-- displaces the standard one, and the other way round.
--
-- A root's edition is fixed when it is created. A workflow copies the edition
-- of its root, so "one active workflow per title" becomes "one per title and
-- edition". Every existing root and workflow is standard, so nothing changes
-- for them.
ALTER TABLE library_roots
  ADD COLUMN edition text NOT NULL DEFAULT 'standard' CHECK (edition IN ('standard', '4k'));

-- A 4K root is only chosen for 4K requests; it can never be a kind's default.
ALTER TABLE library_roots
  ADD CONSTRAINT library_roots_4k_not_default CHECK (edition = 'standard' OR NOT is_default);

ALTER TABLE workflows
  ADD COLUMN edition text NOT NULL DEFAULT 'standard' CHECK (edition IN ('standard', '4k'));

UPDATE workflows workflow SET edition = root.edition
  FROM library_roots root WHERE root.id = workflow.root_id AND workflow.edition <> root.edition;

DROP INDEX IF EXISTS workflows_one_active_variant;
CREATE UNIQUE INDEX workflows_one_active_variant ON workflows(media_id, variant, edition) WHERE terminal_at IS NULL;

-- The database itself keeps a workflow's edition equal to its root's: the pair
-- references the root, so a workflow cannot be inserted with another edition
-- and a root's edition cannot change once a workflow uses it.
ALTER TABLE library_roots ADD CONSTRAINT library_roots_id_edition UNIQUE (id, edition);
ALTER TABLE workflows ADD CONSTRAINT workflows_root_edition
  FOREIGN KEY (root_id, edition) REFERENCES library_roots(id, edition);

-- A file verified for a 4K edition that is not actually 2160p is blamed on the
-- release: it is excluded and the round tries another candidate.
ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN (
    'subtitle_verification','release_retrieval','duplicate_package','output_verification',
    'season_pack_incomplete','movie_primary_ambiguous','output_empty',
    'media_identity_mismatch','media_corrupt','password_protected',
    'archive_unsupported','no_playable_media','release_incomplete',
    'resolution_mismatch'
  ));

-- A new workflow takes its root's edition, so every insert path stays correct
-- without naming the column; the foreign key above still refuses any later
-- change that would split them.
CREATE FUNCTION workflows_take_root_edition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  SELECT edition INTO NEW.edition FROM library_roots WHERE id = NEW.root_id;
  IF NEW.edition IS NULL THEN
    NEW.edition := 'standard';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER workflows_take_root_edition BEFORE INSERT ON workflows
  FOR EACH ROW EXECUTE FUNCTION workflows_take_root_edition();
