ALTER TABLE release_candidates
  ADD COLUMN first_observed_at timestamptz,
  ADD COLUMN last_observed_at timestamptz,
  ADD COLUMN first_seen_sequence bigint,
  ADD COLUMN observation_count bigint NOT NULL DEFAULT 1 CHECK (observation_count > 0);

CREATE SEQUENCE release_candidate_first_seen_sequence;

WITH ordered AS (
  SELECT id,row_number() OVER (ORDER BY created_at,id) AS sequence
  FROM release_candidates
)
UPDATE release_candidates candidate
SET first_observed_at=candidate.created_at,
    last_observed_at=candidate.created_at,
    first_seen_sequence=ordered.sequence
FROM ordered
WHERE ordered.id=candidate.id;

SELECT setval(
  'release_candidate_first_seen_sequence',
  COALESCE((SELECT max(first_seen_sequence) FROM release_candidates),1),
  EXISTS(SELECT 1 FROM release_candidates)
);

ALTER SEQUENCE release_candidate_first_seen_sequence
  OWNED BY release_candidates.first_seen_sequence;

ALTER TABLE release_candidates
  ALTER COLUMN first_observed_at SET NOT NULL,
  ALTER COLUMN first_observed_at SET DEFAULT now(),
  ALTER COLUMN last_observed_at SET NOT NULL,
  ALTER COLUMN last_observed_at SET DEFAULT now(),
  ALTER COLUMN first_seen_sequence SET NOT NULL,
  ALTER COLUMN first_seen_sequence SET DEFAULT nextval('release_candidate_first_seen_sequence');

CREATE UNIQUE INDEX release_candidates_first_seen_sequence
  ON release_candidates(first_seen_sequence);

CREATE FUNCTION enforce_release_candidate_immutable_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.workflow_id IS DISTINCT FROM OLD.workflow_id
     OR NEW.indexer_instance_id IS DISTINCT FROM OLD.indexer_instance_id
     OR NEW.native_key IS DISTINCT FROM OLD.native_key
     OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR NEW.first_observed_at IS DISTINCT FROM OLD.first_observed_at
     OR NEW.first_seen_sequence IS DISTINCT FROM OLD.first_seen_sequence THEN
    RAISE EXCEPTION 'release candidate immutable identity cannot change';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER release_candidates_immutable_identity
BEFORE UPDATE ON release_candidates
FOR EACH ROW EXECUTE FUNCTION enforce_release_candidate_immutable_identity();

CREATE TABLE search_run_indexers (
  search_run_id uuid NOT NULL REFERENCES search_runs(id) ON DELETE CASCADE,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE RESTRICT,
  state text NOT NULL CHECK (state IN ('planned','succeeded','deferred','failed')),
  retry_at timestamptz,
  detail text,
  completed_at timestamptz,
  PRIMARY KEY (search_run_id,indexer_instance_id)
);

ALTER TABLE search_runs DROP CONSTRAINT search_runs_mode_check;
ALTER TABLE search_runs ADD CONSTRAINT search_runs_mode_check
  CHECK (mode IN ('feed','targeted','manual','commissioning','recovery'));

CREATE TABLE acquisition_rounds (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  round_ordinal integer NOT NULL CHECK (round_ordinal > 0),
  state text NOT NULL CHECK (state IN ('open','exhausted','plex_confirmed','cancelled')),
  attempt_limit_snapshot integer NOT NULL CHECK (attempt_limit_snapshot BETWEEN 1 AND 2),
  novelty_cutoff_sequence bigint NOT NULL DEFAULT 0 CHECK (novelty_cutoff_sequence >= 0),
  policy_version bigint NOT NULL CHECK (policy_version > 0),
  policy_snapshot jsonb NOT NULL,
  upgrade_baseline_link_versions text NOT NULL DEFAULT '',
  opened_at timestamptz NOT NULL,
  exhausted_at timestamptz,
  closed_at timestamptz,
  close_reason text,
  UNIQUE (workflow_id,round_ordinal),
  UNIQUE (workflow_id,id)
);

CREATE UNIQUE INDEX acquisition_rounds_one_open
  ON acquisition_rounds(workflow_id)
  WHERE state='open';

CREATE FUNCTION enforce_acquisition_round_immutable_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.workflow_id IS DISTINCT FROM OLD.workflow_id
     OR NEW.round_ordinal IS DISTINCT FROM OLD.round_ordinal
     OR NEW.attempt_limit_snapshot IS DISTINCT FROM OLD.attempt_limit_snapshot
     OR NEW.policy_version IS DISTINCT FROM OLD.policy_version
     OR NEW.policy_snapshot IS DISTINCT FROM OLD.policy_snapshot
     OR NEW.upgrade_baseline_link_versions IS DISTINCT FROM OLD.upgrade_baseline_link_versions
     OR NEW.opened_at IS DISTINCT FROM OLD.opened_at THEN
    RAISE EXCEPTION 'acquisition round immutable snapshot cannot change';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER acquisition_rounds_immutable_snapshot
BEFORE UPDATE ON acquisition_rounds
FOR EACH ROW EXECUTE FUNCTION enforce_acquisition_round_immutable_snapshot();

INSERT INTO acquisition_rounds(
  id,workflow_id,round_ordinal,state,attempt_limit_snapshot,
  novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,
  opened_at,exhausted_at,closed_at,close_reason
)
SELECT
  md5('media-manager-acquisition-round-1:' || workflow.id::text)::uuid,
  workflow.id,
  1,
  CASE
    WHEN workflow.state='plex_confirmed' THEN 'plex_confirmed'
    WHEN workflow.state='cancelled' THEN 'cancelled'
    WHEN max(job.attempt_generation) >= 2 AND workflow.state IN ('waiting','needs_attention','held','blocked') THEN 'exhausted'
    ELSE 'open'
  END,
  2,
  COALESCE((SELECT max(candidate.first_seen_sequence) FROM release_candidates candidate WHERE candidate.workflow_id=workflow.id),0),
  profile.policy_version,
  profile.policy,
  COALESCE((SELECT string_agg(version.id::text,',' ORDER BY version.id)
   FROM library_link_versions version
   JOIN workflows published ON published.id=version.workflow_id
   WHERE published.media_id=workflow.media_id AND published.variant=workflow.variant AND version.state='current'),''),
  COALESCE(min(job.submission_started_at),workflow.created_at),
  CASE WHEN max(job.attempt_generation) >= 2 AND workflow.state IN ('waiting','needs_attention','held','blocked') THEN max(COALESCE(job.last_reconciled_at,job.submission_started_at)) END,
  CASE
    WHEN workflow.state IN ('plex_confirmed','cancelled') THEN workflow.updated_at
    WHEN max(job.attempt_generation) >= 2 AND workflow.state IN ('waiting','needs_attention','held','blocked')
      THEN max(COALESCE(job.last_reconciled_at,job.submitted_at,job.submission_started_at))
  END,
  CASE
    WHEN workflow.state='plex_confirmed' THEN 'migrated confirmed workflow'
    WHEN workflow.state='cancelled' THEN 'migrated cancelled workflow'
    WHEN max(job.attempt_generation) >= 2 AND workflow.state IN ('waiting','needs_attention','held','blocked') THEN 'migrated exhausted workflow; owner review required'
  END
FROM workflows workflow
LEFT JOIN altmount_jobs job ON job.workflow_id=workflow.id
JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
GROUP BY workflow.id,workflow.state,workflow.created_at,workflow.updated_at,profile.policy_version,profile.policy;

ALTER TABLE altmount_jobs
  ADD COLUMN round_id uuid,
  ADD COLUMN round_slot integer,
  ADD COLUMN acquisition_outcome text,
  ADD COLUMN acquisition_closed_at timestamptz;

UPDATE altmount_jobs job
SET round_id=round.id,
    round_slot=job.attempt_generation,
    acquisition_outcome=CASE
      WHEN job.state='import_complete' THEN 'output_verified'
      WHEN job.state='needs_attention' AND job.error_code IN (
        'altmount_identity_unresolved','altmount_identity_ambiguous',
        'altmount_output_unavailable','altmount_job_attention',
        'season_expectation_missing','output_inspection_failed'
      ) THEN 'infrastructure_attention'
      WHEN job.state='needs_attention' THEN 'legacy_attention'
      WHEN job.state='failed' THEN 'execution_failed'
      WHEN job.state IN ('cancelled','removed') THEN job.state
    END,
    acquisition_closed_at=CASE
      WHEN job.state='import_complete'
        OR job.state='needs_attention' AND COALESCE(job.error_code,'') NOT IN (
          'altmount_identity_unresolved','altmount_identity_ambiguous',
          'altmount_output_unavailable','altmount_job_attention',
          'season_expectation_missing','output_inspection_failed'
        )
        OR job.state IN ('failed','cancelled','removed')
      THEN COALESCE(job.last_reconciled_at,job.submitted_at,job.submission_started_at)
    END
FROM acquisition_rounds round
WHERE round.workflow_id=job.workflow_id AND round.round_ordinal=1;

ALTER TABLE altmount_jobs
  ALTER COLUMN round_id SET NOT NULL,
  ALTER COLUMN round_slot SET NOT NULL,
  ADD CONSTRAINT altmount_jobs_round_fk
    FOREIGN KEY (workflow_id,round_id) REFERENCES acquisition_rounds(workflow_id,id) ON DELETE RESTRICT,
  ADD CONSTRAINT altmount_jobs_round_slot_check CHECK (round_slot BETWEEN 1 AND 2),
  ADD CONSTRAINT altmount_jobs_round_slot_unique UNIQUE (round_id,round_slot);

CREATE TABLE workflow_attempted_releases (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  candidate_id uuid NOT NULL,
  first_job_id uuid NOT NULL REFERENCES altmount_jobs(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id,candidate_id),
  FOREIGN KEY (workflow_id,candidate_id) REFERENCES release_candidates(workflow_id,id)
);

INSERT INTO workflow_attempted_releases(workflow_id,candidate_id,first_job_id,created_at)
SELECT DISTINCT ON (job.workflow_id,job.candidate_id)
  job.workflow_id,job.candidate_id,job.id,job.submission_started_at
FROM altmount_jobs job
ORDER BY job.workflow_id,job.candidate_id,job.attempt_generation,job.id;

CREATE TABLE workflow_submitted_packages (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  package_sha256 bytea NOT NULL CHECK (octet_length(package_sha256)=32),
  first_job_id uuid NOT NULL REFERENCES altmount_jobs(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id,package_sha256)
);

INSERT INTO workflow_submitted_packages(workflow_id,package_sha256,first_job_id,created_at)
SELECT DISTINCT ON (job.workflow_id,job.nzb_package_sha256)
  job.workflow_id,job.nzb_package_sha256,job.id,job.submission_started_at
FROM altmount_jobs job
WHERE job.nzb_package_sha256 IS NOT NULL
ORDER BY job.workflow_id,job.nzb_package_sha256,job.attempt_generation,job.id;

CREATE UNIQUE INDEX altmount_jobs_one_active_acquisition
  ON altmount_jobs(workflow_id)
  WHERE state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested');

CREATE TABLE workflow_package_exclusions (
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  package_sha256 bytea NOT NULL CHECK (octet_length(package_sha256)=32),
  job_id uuid NOT NULL REFERENCES altmount_jobs(id) ON DELETE RESTRICT,
  candidate_id uuid NOT NULL,
  failure_kind text NOT NULL,
  reason text NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (workflow_id,package_sha256),
  FOREIGN KEY (workflow_id,candidate_id) REFERENCES release_candidates(workflow_id,id)
);

ALTER TABLE workflow_release_exclusions
  DROP CONSTRAINT workflow_release_exclusions_failure_kind_check;

ALTER TABLE workflow_release_exclusions
  ADD CONSTRAINT workflow_release_exclusions_failure_kind_check
  CHECK (failure_kind IN (
    'subtitle_verification','release_retrieval','duplicate_package','output_verification',
    'season_pack_incomplete','movie_primary_ambiguous','output_empty',
    'media_identity_mismatch','media_corrupt','password_protected'
  ));

CREATE TABLE workflow_retry_discovery (
  workflow_id uuid PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
  exhausted_round_id uuid NOT NULL UNIQUE REFERENCES acquisition_rounds(id) ON DELETE CASCADE,
  state text NOT NULL CHECK (state IN ('waiting','ready','paused')),
  rescan_generation bigint NOT NULL CHECK (rescan_generation > 0),
  next_rescan_at timestamptz,
  last_scan_at timestamptz,
  last_scan_complete boolean,
  complete_no_new_scans integer NOT NULL DEFAULT 0 CHECK (complete_no_new_scans >= 0),
  pending_indexer_ids uuid[],
  ready_candidate_id uuid,
  ready_first_seen_sequence bigint,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  FOREIGN KEY (workflow_id,ready_candidate_id) REFERENCES release_candidates(workflow_id,id),
  CHECK ((state='ready') = (ready_candidate_id IS NOT NULL AND ready_first_seen_sequence IS NOT NULL))
);

CREATE INDEX workflow_retry_discovery_due
  ON workflow_retry_discovery(next_rescan_at,workflow_id)
  WHERE state='waiting' AND next_rescan_at IS NOT NULL;

UPDATE altmount_jobs
SET error_code=COALESCE(error_code,'altmount_submission_rejected'),
    acquisition_outcome='execution_failed'
WHERE state='failed';

UPDATE workflows workflow
SET attention_code='altmount_submission_rejected',
    attention_detail=COALESCE(workflow.attention_detail,'AltMount definitively rejected the NZB; resolve this submission failure before another release.')
WHERE workflow.state='needs_attention'
  AND EXISTS(
    SELECT 1 FROM altmount_jobs job
    WHERE job.id=workflow.grab_attempt_id AND job.state='failed'
  );
