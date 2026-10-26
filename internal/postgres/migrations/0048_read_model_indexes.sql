-- Owner read models sort recent observations and newest search attempts. These
-- indexes keep the bounded list queries from scanning and sorting the entire
-- production history on every refresh.
CREATE INDEX IF NOT EXISTS search_runs_workflow_started_id_idx
  ON search_runs (workflow_id, started_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS release_observations_observed_at_id_idx
  ON release_observations (observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS release_candidates_observation_id_idx
  ON release_candidates (observation_id);

CREATE INDEX IF NOT EXISTS release_candidates_workflow_id_idx
  ON release_candidates (workflow_id);
