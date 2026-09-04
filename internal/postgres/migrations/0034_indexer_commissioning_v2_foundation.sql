CREATE TABLE indexer_commissioning_runs (
  id uuid PRIMARY KEY,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE RESTRICT,
  state text NOT NULL CHECK (state IN (
    'awaiting_caps_authorization','caps_authorized','awaiting_category_review',
    'awaiting_search_authorization','searches_authorized','ready_for_draft',
    'owner_attention','cancel_requested','cancelled','stale','safety_blocked','completed'
  )),
  revision bigint NOT NULL CHECK (revision > 0),
  profile_schema_version smallint NOT NULL CHECK (profile_schema_version = 1),
  planner_version smallint NOT NULL CHECK (planner_version = 1),
  endpoint_config_sha256 bytea NOT NULL CHECK (octet_length(endpoint_config_sha256) = 32),
  provider_contract_key text,
  provider_contract_version smallint,
  fixture_document jsonb NOT NULL CHECK (jsonb_typeof(fixture_document) = 'object'),
  fixture_sha256 bytea NOT NULL CHECK (octet_length(fixture_sha256) = 32),
  caps_plan_document jsonb NOT NULL CHECK (jsonb_typeof(caps_plan_document) = 'object'),
  caps_plan_sha256 bytea NOT NULL CHECK (octet_length(caps_plan_sha256) = 32),
  caps_authorized_at timestamptz,
  caps_authorized_by text,
  category_selection_sha256 bytea CHECK (
    category_selection_sha256 IS NULL OR octet_length(category_selection_sha256) = 32
  ),
  movie_category_ids integer[],
  series_category_ids integer[],
  search_plan_document jsonb CHECK (
    search_plan_document IS NULL OR jsonb_typeof(search_plan_document) = 'object'
  ),
  search_plan_sha256 bytea CHECK (
    search_plan_sha256 IS NULL OR (
      octet_length(search_plan_sha256) = 32
      AND search_plan_document IS NOT NULL
    )
  ),
  search_authorized_at timestamptz,
  search_authorized_by text,
  maximum_api_calls smallint NOT NULL CHECK (maximum_api_calls BETWEEN 1 AND 5),
  planned_api_calls smallint NOT NULL CHECK (
    planned_api_calls BETWEEN 1 AND maximum_api_calls
  ),
  safe_code text CHECK (safe_code IS NULL OR safe_code IN (
    'configuration.changed','owner.cancelled','credential.echo','indeterminate.attempt'
  )),
  generated_profile_id uuid UNIQUE,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
  cancel_requested_at timestamptz,
  stale_at timestamptz,
  completed_at timestamptz,
  CHECK (
    (provider_contract_key IS NULL AND provider_contract_version IS NULL)
    OR (provider_contract_key IS NOT NULL AND provider_contract_version IS NOT NULL
      AND provider_contract_key ~ '^[a-z][a-z0-9._:-]{0,99}$'
      AND provider_contract_version = 1)
  ),
  CHECK (
    caps_authorized_by IS NULL
    OR caps_authorized_by = 'owner'
  ),
  CHECK (
    search_authorized_by IS NULL
    OR search_authorized_by = 'owner'
  ),
  CHECK (
    safe_code IS NULL
    OR (safe_code = 'configuration.changed' AND state = 'stale')
    OR (safe_code = 'owner.cancelled' AND state IN ('cancel_requested','cancelled'))
    OR (safe_code = 'credential.echo' AND state = 'safety_blocked')
    OR (safe_code = 'indeterminate.attempt' AND state = 'owner_attention')
  ),
  CHECK (
    (caps_authorized_at IS NULL) = (caps_authorized_by IS NULL)
  ),
  CHECK (
    (category_selection_sha256 IS NULL
      AND movie_category_ids IS NULL
      AND series_category_ids IS NULL)
    OR (category_selection_sha256 IS NOT NULL
      AND movie_category_ids IS NOT NULL
      AND series_category_ids IS NOT NULL
      AND cardinality(movie_category_ids) <= 128
      AND cardinality(series_category_ids) <= 128
      AND array_position(movie_category_ids,NULL) IS NULL
      AND array_position(series_category_ids,NULL) IS NULL
      AND (cardinality(movie_category_ids) = 0 OR array_ndims(movie_category_ids) = 1)
      AND (cardinality(series_category_ids) = 0 OR array_ndims(series_category_ids) = 1))
  ),
  CHECK (
    (search_plan_document IS NULL
      AND search_plan_sha256 IS NULL
      AND search_authorized_at IS NULL
      AND search_authorized_by IS NULL
      AND planned_api_calls = 1)
    OR (search_plan_document IS NOT NULL
      AND search_plan_sha256 IS NOT NULL
      AND planned_api_calls > 1
      AND (search_authorized_at IS NULL) = (search_authorized_by IS NULL))
  ),
  CHECK (
    state <> 'awaiting_caps_authorization'
    OR (caps_authorized_at IS NULL AND caps_authorized_by IS NULL
      AND category_selection_sha256 IS NULL
      AND search_plan_document IS NULL AND search_plan_sha256 IS NULL
      AND search_authorized_at IS NULL AND search_authorized_by IS NULL
      AND safe_code IS NULL AND generated_profile_id IS NULL
      AND cancel_requested_at IS NULL AND stale_at IS NULL AND completed_at IS NULL)
  ),
  CHECK (
    state NOT IN ('caps_authorized','awaiting_category_review')
    OR (category_selection_sha256 IS NULL
      AND search_plan_document IS NULL AND search_plan_sha256 IS NULL
      AND search_authorized_at IS NULL AND search_authorized_by IS NULL)
  ),
  CHECK (
    search_authorized_at IS NULL
    OR state IN ('searches_authorized','ready_for_draft','completed',
      'owner_attention','cancel_requested','cancelled','stale','safety_blocked')
  ),
  CHECK (
    state NOT IN ('caps_authorized','awaiting_category_review','awaiting_search_authorization',
      'searches_authorized','ready_for_draft','completed')
    OR caps_authorized_at IS NOT NULL
  ),
  CHECK (
    state NOT IN ('awaiting_search_authorization','searches_authorized','ready_for_draft','completed')
    OR (category_selection_sha256 IS NOT NULL AND search_plan_document IS NOT NULL)
  ),
  CHECK (
    state NOT IN ('searches_authorized','ready_for_draft','completed')
    OR search_authorized_at IS NOT NULL
  ),
  CHECK (state NOT IN ('cancel_requested','cancelled') OR cancel_requested_at IS NOT NULL),
  CHECK (
    cancel_requested_at IS NULL
    OR state IN ('cancel_requested','cancelled','owner_attention','stale','safety_blocked')
  ),
  CHECK (
    (stale_at IS NOT NULL) = (state = 'stale')
  ),
  CHECK (
    (completed_at IS NOT NULL) =
      (state IN ('owner_attention','cancelled','stale','safety_blocked','completed'))
  ),
  CHECK (
    (generated_profile_id IS NOT NULL) = (state = 'completed')
  ),
  CHECK (caps_authorized_at IS NULL OR caps_authorized_at BETWEEN created_at AND updated_at),
  CHECK (search_authorized_at IS NULL OR (
    caps_authorized_at IS NOT NULL
    AND search_authorized_at BETWEEN caps_authorized_at AND updated_at
  )),
  CHECK (cancel_requested_at IS NULL OR cancel_requested_at BETWEEN created_at AND updated_at),
  CHECK (stale_at IS NULL OR stale_at BETWEEN created_at AND updated_at),
  CHECK (completed_at IS NULL OR completed_at BETWEEN created_at AND updated_at),
  FOREIGN KEY (indexer_instance_id,generated_profile_id)
    REFERENCES indexer_capability_profiles(indexer_instance_id,id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX indexer_commissioning_runs_one_nonterminal
  ON indexer_commissioning_runs(indexer_instance_id)
  WHERE state IN (
    'awaiting_caps_authorization','caps_authorized','awaiting_category_review',
    'awaiting_search_authorization','searches_authorized','ready_for_draft','cancel_requested'
  );

CREATE FUNCTION enforce_indexer_commissioning_run_history()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
  integration_kind pg_catalog.text;
  integration_configuration pg_catalog.jsonb;
  integration_secret_reference pg_catalog.text;
  current_endpoint_digest pg_catalog.bytea;
  integration_rows pg_catalog.int4 := 0;
  category_value pg_catalog.int4;
  previous_category pg_catalog.int4;
  step_count pg_catalog.int4 := 0;
  qualifying_step_count pg_catalog.int4 := 0;
  search_step_count pg_catalog.int4 := 0;
  qualifying_search_step_count pg_catalog.int4 := 0;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'indexer commissioning runs are append-only';
  END IF;

  IF TG_OP = 'INSERT' THEN
    IF NEW.state <> 'awaiting_caps_authorization' OR NEW.revision <> 1 THEN
      RAISE EXCEPTION 'indexer commissioning runs must begin awaiting CAPS authorization';
    END IF;
    EXECUTE pg_catalog.format(
      'SELECT kind,config,secret_ref FROM %I.integration_instances WHERE id=$1',
      TG_TABLE_SCHEMA
    ) INTO integration_kind,integration_configuration,integration_secret_reference
      USING NEW.indexer_instance_id;
    GET DIAGNOSTICS integration_rows = ROW_COUNT;
    IF integration_rows = 1 AND integration_kind = 'newznab' THEN
      EXECUTE pg_catalog.format(
        'SELECT %I.media_manager_newznab_endpoint_config_sha256($1,$2)',
        TG_TABLE_SCHEMA
      ) INTO current_endpoint_digest
        USING integration_configuration,integration_secret_reference;
    END IF;
    IF integration_rows <> 1
       OR integration_kind <> 'newznab'
       OR current_endpoint_digest IS NULL
       OR NEW.endpoint_config_sha256 IS DISTINCT FROM current_endpoint_digest
       OR COALESCE(NEW.provider_contract_key,'') IS DISTINCT FROM
          COALESCE(pg_catalog.btrim(integration_configuration->>'provider_contract_key'),'')
       OR (NEW.provider_contract_key IS NOT NULL AND NEW.provider_contract_version <> 1) THEN
      RAISE EXCEPTION 'indexer commissioning run configuration binding is invalid';
    END IF;
    previous_category := NULL;
    FOREACH category_value IN ARRAY COALESCE(NEW.movie_category_ids,'{}'::pg_catalog.int4[]) LOOP
      IF category_value IS NULL OR category_value <= 0 OR (previous_category IS NOT NULL AND category_value <= previous_category) THEN
        RAISE EXCEPTION 'indexer commissioning movie categories must be sorted unique positive identifiers';
      END IF;
      previous_category := category_value;
    END LOOP;
    previous_category := NULL;
    FOREACH category_value IN ARRAY COALESCE(NEW.series_category_ids,'{}'::pg_catalog.int4[]) LOOP
      IF category_value IS NULL OR category_value <= 0 OR (previous_category IS NOT NULL AND category_value <= previous_category) THEN
        RAISE EXCEPTION 'indexer commissioning series categories must be sorted unique positive identifiers';
      END IF;
      previous_category := category_value;
    END LOOP;
    RETURN NEW;
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.indexer_instance_id IS DISTINCT FROM OLD.indexer_instance_id
     OR NEW.profile_schema_version IS DISTINCT FROM OLD.profile_schema_version
     OR NEW.planner_version IS DISTINCT FROM OLD.planner_version
     OR NEW.endpoint_config_sha256 IS DISTINCT FROM OLD.endpoint_config_sha256
     OR NEW.provider_contract_key IS DISTINCT FROM OLD.provider_contract_key
     OR NEW.provider_contract_version IS DISTINCT FROM OLD.provider_contract_version
     OR NEW.fixture_document IS DISTINCT FROM OLD.fixture_document
     OR NEW.fixture_sha256 IS DISTINCT FROM OLD.fixture_sha256
     OR NEW.caps_plan_document IS DISTINCT FROM OLD.caps_plan_document
     OR NEW.caps_plan_sha256 IS DISTINCT FROM OLD.caps_plan_sha256
     OR NEW.maximum_api_calls IS DISTINCT FROM OLD.maximum_api_calls
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'indexer commissioning run plan identity is immutable';
  END IF;
  IF NEW.revision <> OLD.revision + 1 THEN
    RAISE EXCEPTION 'indexer commissioning run revision must advance exactly once';
  END IF;
  IF NEW.updated_at <= OLD.updated_at THEN
    RAISE EXCEPTION 'indexer commissioning run updates must advance time';
  END IF;

  EXECUTE pg_catalog.format(
    'SELECT kind,config,secret_ref FROM %I.integration_instances WHERE id=$1',
    TG_TABLE_SCHEMA
  ) INTO integration_kind,integration_configuration,integration_secret_reference
    USING NEW.indexer_instance_id;
  GET DIAGNOSTICS integration_rows = ROW_COUNT;
  IF integration_rows = 1 AND integration_kind = 'newznab' THEN
    EXECUTE pg_catalog.format(
      'SELECT %I.media_manager_newznab_endpoint_config_sha256($1,$2)',
      TG_TABLE_SCHEMA
    ) INTO current_endpoint_digest
      USING integration_configuration,integration_secret_reference;
  END IF;
  IF NEW.state NOT IN ('stale','owner_attention','cancel_requested','cancelled','safety_blocked')
     AND (integration_rows <> 1
       OR integration_kind <> 'newznab'
       OR current_endpoint_digest IS NULL
       OR NEW.endpoint_config_sha256 IS DISTINCT FROM current_endpoint_digest
       OR COALESCE(NEW.provider_contract_key,'') IS DISTINCT FROM
          COALESCE(pg_catalog.btrim(integration_configuration->>'provider_contract_key'),'')
       OR (NEW.provider_contract_key IS NOT NULL AND NEW.provider_contract_version <> 1)
       OR (NEW.category_selection_sha256 IS NOT NULL AND (
         integration_configuration->'category_selection_review_required' IS DISTINCT FROM 'false'::pg_catalog.jsonb
         OR integration_configuration->'movie_category_ids' IS DISTINCT FROM pg_catalog.to_jsonb(NEW.movie_category_ids)
         OR integration_configuration->'series_category_ids' IS DISTINCT FROM pg_catalog.to_jsonb(NEW.series_category_ids)))) THEN
    RAISE EXCEPTION 'indexer commissioning run configuration changed';
  END IF;

  previous_category := NULL;
  FOREACH category_value IN ARRAY COALESCE(NEW.movie_category_ids,'{}'::pg_catalog.int4[]) LOOP
    IF category_value IS NULL OR category_value <= 0 OR (previous_category IS NOT NULL AND category_value <= previous_category) THEN
      RAISE EXCEPTION 'indexer commissioning movie categories must be sorted unique positive identifiers';
    END IF;
    previous_category := category_value;
  END LOOP;
  previous_category := NULL;
  FOREACH category_value IN ARRAY COALESCE(NEW.series_category_ids,'{}'::pg_catalog.int4[]) LOOP
    IF category_value IS NULL OR category_value <= 0 OR (previous_category IS NOT NULL AND category_value <= previous_category) THEN
      RAISE EXCEPTION 'indexer commissioning series categories must be sorted unique positive identifiers';
    END IF;
    previous_category := category_value;
  END LOOP;

  IF OLD.category_selection_sha256 IS NOT NULL
     AND (NEW.category_selection_sha256 IS DISTINCT FROM OLD.category_selection_sha256
       OR NEW.movie_category_ids IS DISTINCT FROM OLD.movie_category_ids
       OR NEW.series_category_ids IS DISTINCT FROM OLD.series_category_ids) THEN
    RAISE EXCEPTION 'indexer commissioning category selection is immutable once frozen';
  END IF;
  IF OLD.category_selection_sha256 IS NULL AND NEW.category_selection_sha256 IS NOT NULL
     AND NOT (OLD.state = 'awaiting_category_review'
       AND NEW.state = 'awaiting_search_authorization') THEN
    RAISE EXCEPTION 'indexer commissioning categories may freeze only with the search plan';
  END IF;
  IF OLD.search_plan_document IS NOT NULL
     AND (NEW.search_plan_document IS DISTINCT FROM OLD.search_plan_document
       OR NEW.search_plan_sha256 IS DISTINCT FROM OLD.search_plan_sha256
       OR NEW.planned_api_calls IS DISTINCT FROM OLD.planned_api_calls) THEN
      RAISE EXCEPTION 'indexer commissioning search plan is immutable once sealed';
  END IF;
  IF OLD.search_plan_document IS NULL
     AND (NEW.search_plan_document IS NOT NULL OR NEW.planned_api_calls IS DISTINCT FROM OLD.planned_api_calls)
     AND NOT (OLD.state = 'awaiting_category_review'
       AND NEW.state = 'awaiting_search_authorization') THEN
    RAISE EXCEPTION 'indexer commissioning search plan may seal only after category review';
  END IF;
  IF OLD.caps_authorized_at IS NOT NULL
     AND (NEW.caps_authorized_at IS DISTINCT FROM OLD.caps_authorized_at
       OR NEW.caps_authorized_by IS DISTINCT FROM OLD.caps_authorized_by) THEN
    RAISE EXCEPTION 'indexer commissioning CAPS authorization is immutable';
  END IF;
  IF OLD.caps_authorized_at IS NULL AND NEW.caps_authorized_at IS NOT NULL
     AND NOT (OLD.state = 'awaiting_caps_authorization' AND NEW.state = 'caps_authorized') THEN
    RAISE EXCEPTION 'indexer commissioning CAPS authorization is out of sequence';
  END IF;
  IF OLD.search_authorized_at IS NOT NULL
     AND (NEW.search_authorized_at IS DISTINCT FROM OLD.search_authorized_at
       OR NEW.search_authorized_by IS DISTINCT FROM OLD.search_authorized_by) THEN
    RAISE EXCEPTION 'indexer commissioning search authorization is immutable';
  END IF;
  IF OLD.search_authorized_at IS NULL AND NEW.search_authorized_at IS NOT NULL
     AND NOT (OLD.state = 'awaiting_search_authorization' AND NEW.state = 'searches_authorized') THEN
    RAISE EXCEPTION 'indexer commissioning search authorization is out of sequence';
  END IF;
  IF OLD.generated_profile_id IS NOT NULL
     AND NEW.generated_profile_id IS DISTINCT FROM OLD.generated_profile_id THEN
    RAISE EXCEPTION 'indexer commissioning generated profile link is immutable';
  END IF;
  IF OLD.generated_profile_id IS NULL AND NEW.generated_profile_id IS NOT NULL
     AND NOT (OLD.state = 'ready_for_draft' AND NEW.state = 'completed') THEN
    RAISE EXCEPTION 'indexer commissioning profile may link only during completion';
  END IF;
  IF OLD.cancel_requested_at IS NOT NULL
     AND NEW.cancel_requested_at IS DISTINCT FROM OLD.cancel_requested_at THEN
    RAISE EXCEPTION 'indexer commissioning cancellation request is immutable';
  END IF;
  IF OLD.cancel_requested_at IS NULL AND NEW.cancel_requested_at IS NOT NULL
     AND NEW.state <> 'cancel_requested' THEN
    RAISE EXCEPTION 'indexer commissioning cancellation timestamp is out of sequence';
  END IF;
  IF OLD.stale_at IS NOT NULL AND NEW.stale_at IS DISTINCT FROM OLD.stale_at THEN
    RAISE EXCEPTION 'indexer commissioning stale evidence is immutable';
  END IF;
  IF OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at THEN
    RAISE EXCEPTION 'indexer commissioning completion evidence is immutable';
  END IF;

  IF OLD.state IN ('owner_attention','cancelled','stale','safety_blocked','completed')
     AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'terminal indexer commissioning run cannot transition';
  END IF;
  IF OLD.state = 'awaiting_caps_authorization'
     AND NEW.state NOT IN ('caps_authorized','cancel_requested','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  ELSIF OLD.state = 'caps_authorized'
     AND NEW.state NOT IN ('awaiting_category_review','cancel_requested','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  ELSIF OLD.state = 'awaiting_category_review'
     AND NEW.state NOT IN ('awaiting_search_authorization','cancel_requested','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  ELSIF OLD.state = 'awaiting_search_authorization'
     AND NEW.state NOT IN ('searches_authorized','cancel_requested','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  ELSIF OLD.state = 'searches_authorized'
     AND NEW.state NOT IN ('ready_for_draft','cancel_requested','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  ELSIF OLD.state = 'ready_for_draft'
     AND NEW.state NOT IN ('completed','cancel_requested','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  ELSIF OLD.state = 'cancel_requested'
     AND NEW.state NOT IN ('cancelled','stale','safety_blocked','owner_attention') THEN
    RAISE EXCEPTION 'invalid indexer commissioning run transition';
  END IF;

  IF OLD.state = 'awaiting_caps_authorization' AND NEW.state = 'caps_authorized' THEN
    EXECUTE pg_catalog.format(
      'SELECT count(*)::pg_catalog.int4,
              count(*) FILTER (WHERE ordinal=1 AND phase=''caps'' AND target_kind=''caps'' AND state=''planned'')::pg_catalog.int4
         FROM %I.indexer_commissioning_steps WHERE run_id=$1',
      TG_TABLE_SCHEMA
    ) INTO step_count,qualifying_step_count USING NEW.id;
    IF step_count <> 1 OR qualifying_step_count <> 1 THEN
      RAISE EXCEPTION 'CAPS authorization requires the exact sealed CAPS step';
    END IF;
  ELSIF OLD.state = 'caps_authorized' AND NEW.state = 'awaiting_category_review' THEN
    EXECUTE pg_catalog.format(
      'SELECT count(*)::pg_catalog.int4,
              count(*) FILTER (WHERE ordinal=1 AND phase=''caps'' AND target_kind=''caps''
                AND state=''completed'' AND safe_outcome=''caps_observed''
                AND evidence_key IS NOT NULL AND evidence_document IS NOT NULL)::pg_catalog.int4
         FROM %I.indexer_commissioning_steps WHERE run_id=$1',
      TG_TABLE_SCHEMA
    ) INTO step_count,qualifying_step_count USING NEW.id;
    IF step_count <> 1 OR qualifying_step_count <> 1 THEN
      RAISE EXCEPTION 'category review requires completed CAPS evidence';
    END IF;
  ELSIF OLD.state = 'awaiting_search_authorization' AND NEW.state = 'searches_authorized' THEN
    EXECUTE pg_catalog.format(
      'SELECT count(*)::pg_catalog.int4,
              count(*) FILTER (WHERE ordinal=1 AND phase=''caps'' AND target_kind=''caps''
                AND state=''completed'' AND safe_outcome=''caps_observed''
                AND evidence_key IS NOT NULL AND evidence_document IS NOT NULL)::pg_catalog.int4,
              count(*) FILTER (WHERE phase=''search'')::pg_catalog.int4,
              count(*) FILTER (WHERE phase=''search'' AND state=''planned'')::pg_catalog.int4
         FROM %I.indexer_commissioning_steps WHERE run_id=$1',
      TG_TABLE_SCHEMA
    ) INTO step_count,qualifying_step_count,search_step_count,qualifying_search_step_count USING NEW.id;
    IF step_count <> NEW.planned_api_calls OR qualifying_step_count <> 1
       OR search_step_count <> NEW.planned_api_calls - 1
       OR qualifying_search_step_count <> search_step_count THEN
      RAISE EXCEPTION 'search authorization requires the exact sealed planned steps';
    END IF;
  ELSIF OLD.state = 'searches_authorized' AND NEW.state = 'ready_for_draft' THEN
    EXECUTE pg_catalog.format(
      'SELECT count(*)::pg_catalog.int4,
              count(*) FILTER (WHERE ordinal=1 AND phase=''caps'' AND target_kind=''caps''
                AND state=''completed'' AND safe_outcome=''caps_observed''
                AND evidence_key IS NOT NULL AND evidence_document IS NOT NULL)::pg_catalog.int4,
              count(*) FILTER (WHERE phase=''search'')::pg_catalog.int4,
              count(*) FILTER (WHERE phase=''search'' AND state=''completed''
                AND safe_outcome IN (''recipe_verified'',''no_results'',''no_plausible_match'',
                  ''unsupported'',''permission_restricted'',''contradicted'')
                AND raw_result_count IS NOT NULL AND plausible_identity_count IS NOT NULL
                AND evidence_key IS NOT NULL AND evidence_document IS NOT NULL)::pg_catalog.int4
         FROM %I.indexer_commissioning_steps WHERE run_id=$1',
      TG_TABLE_SCHEMA
    ) INTO step_count,qualifying_step_count,search_step_count,qualifying_search_step_count USING NEW.id;
    IF step_count <> NEW.planned_api_calls OR qualifying_step_count <> 1
       OR search_step_count <> NEW.planned_api_calls - 1
       OR qualifying_search_step_count <> search_step_count THEN
      RAISE EXCEPTION 'draft readiness requires completed exact search evidence';
    END IF;
  ELSIF OLD.state = 'cancel_requested' AND NEW.state = 'cancelled' THEN
    EXECUTE pg_catalog.format(
      'SELECT count(*)::pg_catalog.int4,
              count(*) FILTER (WHERE state IN (''completed'',''deferred_before_send'',''indeterminate'',''cancelled''))::pg_catalog.int4
         FROM %I.indexer_commissioning_steps WHERE run_id=$1',
      TG_TABLE_SCHEMA
    ) INTO step_count,qualifying_step_count USING NEW.id;
    IF step_count = 0 OR qualifying_step_count <> step_count THEN
      RAISE EXCEPTION 'run cancellation requires every step to be terminal';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER indexer_commissioning_runs_append_only
BEFORE INSERT OR UPDATE OR DELETE ON indexer_commissioning_runs
FOR EACH ROW EXECUTE FUNCTION enforce_indexer_commissioning_run_history();

CREATE TABLE indexer_commissioning_steps (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES indexer_commissioning_runs(id) ON DELETE RESTRICT,
  ordinal smallint NOT NULL CHECK (ordinal BETWEEN 1 AND 5),
  phase text NOT NULL CHECK (phase IN ('caps','search')),
  target_kind text NOT NULL CHECK (target_kind IN ('caps','search_recipe')),
  recipe_key text,
  scope text,
  role text,
  fixture_key text,
  fixture_sha256 bytea,
  typed_request_document jsonb NOT NULL CHECK (jsonb_typeof(typed_request_document) = 'object'),
  structural_request_sha256 bytea NOT NULL CHECK (octet_length(structural_request_sha256) = 32),
  category_ids integer[] NOT NULL DEFAULT '{}' CHECK (
    cardinality(category_ids) <= 128
    AND array_position(category_ids,NULL) IS NULL
    AND (cardinality(category_ids) = 0 OR array_ndims(category_ids) = 1)
  ),
  request_limit smallint NOT NULL CHECK (
    (phase = 'caps' AND request_limit = 0)
    OR (phase = 'search' AND request_limit BETWEEN 1 AND 100)
  ),
  budget_family text NOT NULL CHECK (budget_family = 'query'),
  state text NOT NULL CHECK (state IN (
    'planned','intent_committed','transmitting','completed',
    'deferred_before_send','indeterminate','cancelled'
  )),
  intent_at timestamptz,
  transmitted_at timestamptz,
  completed_at timestamptz,
  executor_lease_owner text CHECK (
    executor_lease_owner IS NULL OR executor_lease_owner ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$'
  ),
  executor_lease_until timestamptz,
  safe_outcome text CHECK (safe_outcome IS NULL OR safe_outcome IN (
    'caps_observed','recipe_verified','no_results','no_plausible_match',
    'unsupported','permission_restricted','contradicted','credential_unavailable',
    'authentication_failure','quota_exhausted','rate_limited','retry_after_deferred',
    'dns_failure','tls_failure','provider_connection_failure','malformed_response',
    'semantic_ambiguity','safety_credential_echo','safety_unsafe_redirect',
    'safety_unsafe_text','safety_body_limit','safety_xml_limit',
    'configuration_stale','indeterminate','cancelled'
  )),
  safe_code text CHECK (safe_code IS NULL OR safe_code IN (
    'configuration.changed','owner.cancelled','credential.echo','indeterminate.attempt'
  )),
  retry_at timestamptz,
  raw_result_count smallint CHECK (raw_result_count BETWEEN 0 AND request_limit),
  plausible_identity_count smallint CHECK (
    plausible_identity_count BETWEEN 0 AND raw_result_count
  ),
  safe_response_sha256 bytea CHECK (
    safe_response_sha256 IS NULL OR octet_length(safe_response_sha256) = 32
  ),
  raw_body_sha256 bytea CHECK (
    raw_body_sha256 IS NULL OR octet_length(raw_body_sha256) = 32
  ),
  evidence_key text UNIQUE CHECK (
    evidence_key IS NULL OR evidence_key ~ '^evidence[.][0-9a-f]{32}$'
  ),
  evidence_document jsonb CHECK (
    evidence_document IS NULL OR jsonb_typeof(evidence_document) = 'object'
  ),
  UNIQUE (run_id,ordinal),
  CHECK (
    (phase = 'caps' AND target_kind = 'caps'
      AND recipe_key IS NULL AND scope IS NULL AND role IS NULL
      AND fixture_key IS NULL AND fixture_sha256 IS NULL
      AND cardinality(category_ids) = 0)
    OR (phase = 'search' AND target_kind = 'search_recipe'
      AND recipe_key IS NOT NULL
      AND recipe_key ~ '^[a-z][a-z0-9._:-]{0,99}$'
      AND scope IS NOT NULL
      AND scope IN ('movie','complete_season')
      AND role IS NOT NULL
      AND role IN ('primary','fallback')
      AND fixture_key IS NOT NULL
      AND fixture_key ~ '^cfx_[0-9a-f]{32}$'
      AND fixture_sha256 IS NOT NULL
      AND octet_length(fixture_sha256) = 32)
  ),
  CHECK (
    (executor_lease_owner IS NULL) = (executor_lease_until IS NULL)
  ),
  CHECK (
    (state IN ('intent_committed','transmitting')) = (executor_lease_owner IS NOT NULL)
  ),
  CHECK (
    state <> 'planned' OR (intent_at IS NULL AND transmitted_at IS NULL AND completed_at IS NULL)
  ),
  CHECK (
    state <> 'intent_committed' OR (intent_at IS NOT NULL AND transmitted_at IS NULL AND completed_at IS NULL)
  ),
  CHECK (
    state <> 'transmitting' OR (intent_at IS NOT NULL AND transmitted_at IS NOT NULL AND completed_at IS NULL)
  ),
  CHECK (
    state <> 'deferred_before_send' OR (intent_at IS NOT NULL AND transmitted_at IS NULL AND completed_at IS NOT NULL)
  ),
  CHECK (
    state <> 'indeterminate' OR (intent_at IS NOT NULL AND transmitted_at IS NOT NULL AND completed_at IS NOT NULL)
  ),
  CHECK (
    state <> 'cancelled' OR (transmitted_at IS NULL AND completed_at IS NOT NULL)
  ),
  CHECK (
    state <> 'completed' OR (intent_at IS NOT NULL AND transmitted_at IS NOT NULL
      AND completed_at IS NOT NULL AND safe_outcome IS NOT NULL)
  ),
  CHECK (
    (completed_at IS NOT NULL) =
      (state IN ('completed','deferred_before_send','indeterminate','cancelled'))
  ),
  CHECK (
    (safe_outcome IS NOT NULL) =
      (state IN ('completed','deferred_before_send','indeterminate','cancelled'))
  ),
  CHECK (
    safe_code IS NULL
    OR (safe_outcome IS NOT NULL AND (
      (safe_code = 'configuration.changed' AND safe_outcome = 'configuration_stale')
      OR (safe_code = 'owner.cancelled' AND safe_outcome = 'cancelled')
      OR (safe_code = 'credential.echo' AND safe_outcome = 'safety_credential_echo')
      OR (safe_code = 'indeterminate.attempt' AND safe_outcome = 'indeterminate')))
  ),
  CHECK (
    (state = 'cancelled' AND safe_outcome = 'cancelled')
    OR (state <> 'cancelled' AND safe_outcome IS DISTINCT FROM 'cancelled')
  ),
  CHECK (
    (state = 'indeterminate' AND safe_outcome = 'indeterminate')
    OR (state <> 'indeterminate' AND safe_outcome IS DISTINCT FROM 'indeterminate')
  ),
  CHECK (
    safe_outcome IS NULL
    OR (safe_outcome = 'caps_observed' AND target_kind = 'caps')
    OR (safe_outcome IN ('recipe_verified','no_results','no_plausible_match') AND target_kind = 'search_recipe')
    OR safe_outcome NOT IN ('caps_observed','recipe_verified','no_results','no_plausible_match')
  ),
  CHECK (
    state <> 'cancelled' OR safe_outcome = 'cancelled'
  ),
  CHECK (
    state <> 'indeterminate' OR safe_outcome = 'indeterminate'
  ),
  CHECK (
    state <> 'deferred_before_send'
    OR safe_outcome IN ('credential_unavailable','quota_exhausted','rate_limited','retry_after_deferred','configuration_stale')
  ),
  CHECK (
    (evidence_key IS NULL AND evidence_document IS NULL)
    OR (evidence_key IS NOT NULL AND evidence_document IS NOT NULL
      AND safe_outcome IS NOT NULL
      AND raw_result_count IS NOT NULL AND plausible_identity_count IS NOT NULL)
  ),
  CHECK (
    plausible_identity_count IS NULL
    OR (raw_result_count IS NOT NULL
      AND plausible_identity_count BETWEEN 0 AND raw_result_count)
  ),
  CHECK (
    (raw_result_count IS NULL AND plausible_identity_count IS NULL)
    OR (raw_result_count IS NOT NULL AND plausible_identity_count IS NOT NULL)
  ),
  CHECK (
    evidence_key IS NULL
    OR safe_outcome IN ('caps_observed','recipe_verified','no_results',
      'no_plausible_match','unsupported','permission_restricted','contradicted')
  ),
  CHECK (
    safe_outcome NOT IN ('safety_credential_echo','safety_unsafe_redirect',
      'safety_unsafe_text','safety_body_limit','safety_xml_limit')
    OR (safe_response_sha256 IS NULL AND raw_body_sha256 IS NULL
      AND evidence_key IS NULL AND evidence_document IS NULL)
  ),
  CHECK (raw_body_sha256 IS NULL OR evidence_key IS NOT NULL),
  CHECK (
    state NOT IN ('planned','intent_committed','transmitting')
    OR (safe_outcome IS NULL AND safe_code IS NULL AND retry_at IS NULL
      AND raw_result_count IS NULL AND plausible_identity_count IS NULL
      AND safe_response_sha256 IS NULL AND raw_body_sha256 IS NULL
      AND evidence_key IS NULL AND evidence_document IS NULL)
  ),
  CHECK (retry_at IS NULL OR state = 'deferred_before_send'),
  CHECK (transmitted_at IS NULL OR (intent_at IS NOT NULL AND transmitted_at >= intent_at)),
  CHECK (completed_at IS NULL OR (
    (intent_at IS NULL OR completed_at >= intent_at)
    AND (transmitted_at IS NULL OR completed_at >= transmitted_at)
  )),
  CHECK (executor_lease_until IS NULL OR (
    intent_at IS NOT NULL AND executor_lease_until > intent_at
    AND (transmitted_at IS NULL OR executor_lease_until > transmitted_at)
  ))
);

CREATE UNIQUE INDEX indexer_commissioning_steps_one_executing
  ON indexer_commissioning_steps(run_id)
  WHERE state IN ('intent_committed','transmitting');

CREATE FUNCTION enforce_indexer_commissioning_step_history()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
  category_value pg_catalog.int4;
  previous_category pg_catalog.int4;
  planned_calls pg_catalog.int4;
  prior_incomplete_steps pg_catalog.int4 := 0;
  run_state pg_catalog.text;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'indexer commissioning steps are append-only';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.state <> 'planned' THEN
      RAISE EXCEPTION 'indexer commissioning steps must begin planned';
    END IF;
    EXECUTE pg_catalog.format(
      'SELECT planned_api_calls FROM %I.indexer_commissioning_runs WHERE id=$1',
      TG_TABLE_SCHEMA
    ) INTO planned_calls USING NEW.run_id;
    IF planned_calls IS NULL
       OR NEW.ordinal > planned_calls
       OR (NEW.phase = 'caps' AND NEW.ordinal <> 1)
       OR (NEW.phase = 'search' AND NEW.ordinal <= 1) THEN
      RAISE EXCEPTION 'indexer commissioning step does not belong to the sealed plan';
    END IF;
    previous_category := NULL;
    FOREACH category_value IN ARRAY NEW.category_ids LOOP
      IF category_value IS NULL OR category_value <= 0 OR (previous_category IS NOT NULL AND category_value <= previous_category) THEN
        RAISE EXCEPTION 'indexer commissioning step categories must be sorted unique positive identifiers';
      END IF;
      previous_category := category_value;
    END LOOP;
    RETURN NEW;
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.run_id IS DISTINCT FROM OLD.run_id
     OR NEW.ordinal IS DISTINCT FROM OLD.ordinal
     OR NEW.phase IS DISTINCT FROM OLD.phase
     OR NEW.target_kind IS DISTINCT FROM OLD.target_kind
     OR NEW.recipe_key IS DISTINCT FROM OLD.recipe_key
     OR NEW.scope IS DISTINCT FROM OLD.scope
     OR NEW.role IS DISTINCT FROM OLD.role
     OR NEW.fixture_key IS DISTINCT FROM OLD.fixture_key
     OR NEW.fixture_sha256 IS DISTINCT FROM OLD.fixture_sha256
     OR NEW.typed_request_document IS DISTINCT FROM OLD.typed_request_document
     OR NEW.structural_request_sha256 IS DISTINCT FROM OLD.structural_request_sha256
     OR NEW.category_ids IS DISTINCT FROM OLD.category_ids
     OR NEW.request_limit IS DISTINCT FROM OLD.request_limit
     OR NEW.budget_family IS DISTINCT FROM OLD.budget_family THEN
    RAISE EXCEPTION 'indexer commissioning step request identity is immutable';
  END IF;

  IF OLD.state IN ('completed','deferred_before_send','indeterminate','cancelled')
     AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'terminal indexer commissioning step is immutable';
  END IF;
  IF OLD.state = 'planned' AND NEW.state NOT IN ('intent_committed','cancelled') THEN
    RAISE EXCEPTION 'invalid indexer commissioning step transition';
  ELSIF OLD.state = 'intent_committed'
     AND NEW.state NOT IN ('transmitting','deferred_before_send','cancelled') THEN
    RAISE EXCEPTION 'invalid indexer commissioning step transition';
  ELSIF OLD.state = 'transmitting'
     AND NEW.state NOT IN ('completed','indeterminate') THEN
    RAISE EXCEPTION 'invalid indexer commissioning step transition';
  END IF;

  IF NEW.state IN ('intent_committed','transmitting') THEN
    EXECUTE pg_catalog.format(
      'SELECT state FROM %I.indexer_commissioning_runs WHERE id=$1',
      TG_TABLE_SCHEMA
    ) INTO run_state USING NEW.run_id;
    IF (NEW.phase = 'caps' AND run_state IS DISTINCT FROM 'caps_authorized')
       OR (NEW.phase = 'search' AND run_state IS DISTINCT FROM 'searches_authorized') THEN
      RAISE EXCEPTION 'commissioning step execution requires its authorized run phase';
    END IF;
  END IF;

  IF OLD.state = 'planned' AND NEW.state = 'intent_committed' THEN
    EXECUTE pg_catalog.format(
      'SELECT count(*)::pg_catalog.int4 FROM %I.indexer_commissioning_steps
        WHERE run_id=$1 AND ordinal < $2 AND NOT (
          state=''completed''
          AND evidence_key IS NOT NULL
          AND evidence_document IS NOT NULL
          AND ((phase=''caps'' AND target_kind=''caps'' AND safe_outcome=''caps_observed'')
            OR (phase=''search'' AND target_kind=''search_recipe'' AND safe_outcome IN (
              ''recipe_verified'',''no_results'',''no_plausible_match'',
              ''unsupported'',''permission_restricted'',''contradicted''))))',
      TG_TABLE_SCHEMA
    ) INTO prior_incomplete_steps USING NEW.run_id,NEW.ordinal;
    IF prior_incomplete_steps <> 0 THEN
      RAISE EXCEPTION 'commissioning steps must begin in ordinal order';
    END IF;
  END IF;
  IF NEW.state = 'cancelled' THEN
    EXECUTE pg_catalog.format(
      'SELECT state FROM %I.indexer_commissioning_runs WHERE id=$1',
      TG_TABLE_SCHEMA
    ) INTO run_state USING NEW.run_id;
    IF run_state IS DISTINCT FROM 'cancel_requested' THEN
      RAISE EXCEPTION 'commissioning step cancellation requires a run cancellation request';
    END IF;
  END IF;

  IF OLD.intent_at IS NOT NULL AND NEW.intent_at IS DISTINCT FROM OLD.intent_at THEN
    RAISE EXCEPTION 'indexer commissioning step intent evidence is immutable';
  END IF;
  IF OLD.transmitted_at IS NOT NULL AND NEW.transmitted_at IS DISTINCT FROM OLD.transmitted_at THEN
    RAISE EXCEPTION 'indexer commissioning step transmission evidence is immutable';
  END IF;
  IF OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at THEN
    RAISE EXCEPTION 'indexer commissioning step completion evidence is immutable';
  END IF;
  IF OLD.evidence_key IS NOT NULL
     AND (NEW.evidence_key IS DISTINCT FROM OLD.evidence_key
       OR NEW.evidence_document IS DISTINCT FROM OLD.evidence_document) THEN
      RAISE EXCEPTION 'indexer commissioning step evidence is immutable';
  END IF;
  IF OLD.evidence_key IS NULL AND NEW.evidence_key IS NOT NULL
     AND NOT (OLD.state = 'transmitting' AND NEW.state = 'completed') THEN
    RAISE EXCEPTION 'indexer commissioning evidence may attach only to a completed transmission';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER indexer_commissioning_steps_append_only
BEFORE INSERT OR UPDATE OR DELETE ON indexer_commissioning_steps
FOR EACH ROW EXECUTE FUNCTION enforce_indexer_commissioning_step_history();
