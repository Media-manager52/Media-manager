UPDATE integration_instances
SET config = config
  || CASE
    WHEN config ? 'movie_category_ids' THEN '{}'::jsonb
    ELSE jsonb_build_object(
      'movie_category_ids',
      CASE
        WHEN jsonb_typeof(config->'categories') = 'array' THEN config->'categories'
        ELSE '[]'::jsonb
      END
    )
  END
  || CASE
    WHEN config ? 'series_category_ids' THEN '{}'::jsonb
    ELSE jsonb_build_object(
      'series_category_ids',
      CASE
        WHEN jsonb_typeof(config->'categories') = 'array' THEN config->'categories'
        ELSE '[]'::jsonb
      END
    )
  END
  || jsonb_build_object('category_selection_review_required', true)
WHERE kind = 'newznab'
  AND jsonb_typeof(config) = 'object';

CREATE FUNCTION media_manager_newznab_endpoint_config_sha256(configuration jsonb, secret_reference text)
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
STRICT
SET search_path = pg_catalog
AS $$
DECLARE
  endpoint text := COALESCE(configuration->>'base_url','');
  endpoint_parts text[];
  normalized_scheme text;
  normalized_hostname text;
  effective_port text;
  base_path text;
  contract_key text := COALESCE(btrim(configuration->>'provider_contract_key'),'');
BEGIN
  IF jsonb_typeof(configuration) <> 'object'
     OR octet_length(endpoint) > 2048
     OR endpoint <> btrim(endpoint)
     OR position(chr(92) in endpoint) > 0
     OR position('%' in regexp_replace(endpoint,'%[0-9A-Fa-f]{2}','','g')) > 0
     OR secret_reference !~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$'
     OR (contract_key <> '' AND contract_key !~ '^[a-z][a-z0-9._:-]{0,99}$') THEN
    RETURN NULL;
  END IF;

  endpoint_parts := regexp_match(
    endpoint,
    '^(https?)://(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(:([0-9]{1,5}))?(/[^?#[:space:][:cntrl:]]*)?$',
    'i'
  );
  IF endpoint_parts IS NULL THEN
    RETURN NULL;
  END IF;

  normalized_scheme := lower(endpoint_parts[1]);
  IF normalized_scheme = 'http'
     AND COALESCE(configuration->'allow_http','false'::jsonb) <> 'true'::jsonb THEN
    RETURN NULL;
  END IF;

  normalized_hostname := lower(endpoint_parts[2]);
  IF left(normalized_hostname,1) = '[' AND right(normalized_hostname,1) = ']' THEN
    normalized_hostname := substr(normalized_hostname,2,length(normalized_hostname)-2);
    BEGIN
      PERFORM normalized_hostname::inet;
    EXCEPTION WHEN invalid_text_representation THEN
      RETURN NULL;
    END;
  ELSE
    normalized_hostname := rtrim(normalized_hostname,'.');
  END IF;
  IF normalized_hostname = '' OR octet_length(normalized_hostname) > 253 THEN
    RETURN NULL;
  END IF;

  effective_port := NULLIF(endpoint_parts[4],'');
  IF effective_port IS NULL THEN
    effective_port := CASE normalized_scheme WHEN 'https' THEN '443' ELSE '80' END;
  ELSE
    IF effective_port::integer < 1 OR effective_port::integer > 65535 THEN
      RETURN NULL;
    END IF;
    effective_port := (effective_port::integer)::text;
  END IF;
  base_path := COALESCE(endpoint_parts[5],'');
  IF right(base_path,1) = '/' THEN
    base_path := left(base_path,length(base_path)-1);
  END IF;

  RETURN sha256(convert_to(jsonb_build_object(
    'scheme', normalized_scheme,
    'hostname', normalized_hostname,
    'effective_port', effective_port,
    'base_path', base_path,
    'allow_http', COALESCE(configuration->'allow_http','false'::jsonb) = 'true'::jsonb,
    'authentication', 'query_api_key',
    'secret_reference', secret_reference,
    'provider_contract_key', contract_key,
    'provider_contract_version', CASE WHEN contract_key = '' THEN 0 ELSE 1 END
  )::text,'UTF8'));
END
$$;

CREATE TABLE indexer_capability_profiles (
  id uuid PRIMARY KEY,
  indexer_instance_id uuid NOT NULL REFERENCES integration_instances(id) ON DELETE RESTRICT,
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  lifecycle_state text NOT NULL CHECK (lifecycle_state IN ('draft','active','superseded','invalidated')),
  profile_document jsonb NOT NULL CHECK (jsonb_typeof(profile_document) = 'object'),
  document_sha256 bytea NOT NULL CHECK (
    octet_length(document_sha256) = 32
    AND document_sha256 = sha256(convert_to(profile_document::text,'UTF8'))
  ),
  endpoint_config_sha256 bytea NOT NULL CHECK (octet_length(endpoint_config_sha256) = 32),
  created_at timestamptz NOT NULL,
  activated_at timestamptz,
  superseded_at timestamptz,
  invalidated_at timestamptz,
  invalidation_reason text CHECK (invalidation_reason IS NULL OR invalidation_reason ~ '^[a-z][a-z0-9._:-]{0,99}$'),
  UNIQUE (indexer_instance_id,revision),
  UNIQUE (indexer_instance_id,id),
  CHECK ((profile_document->'schema_version' = to_jsonb(schema_version)) IS TRUE),
  CHECK (
    (lifecycle_state = 'draft' AND activated_at IS NULL AND superseded_at IS NULL AND invalidated_at IS NULL AND invalidation_reason IS NULL)
    OR (lifecycle_state = 'active' AND activated_at IS NOT NULL AND superseded_at IS NULL AND invalidated_at IS NULL AND invalidation_reason IS NULL)
    OR (lifecycle_state = 'superseded' AND activated_at IS NOT NULL AND superseded_at IS NOT NULL AND invalidated_at IS NULL AND invalidation_reason IS NULL)
    OR (lifecycle_state = 'invalidated' AND superseded_at IS NULL AND invalidated_at IS NOT NULL AND invalidation_reason IS NOT NULL)
  )
);

CREATE UNIQUE INDEX indexer_capability_profiles_one_active
  ON indexer_capability_profiles(indexer_instance_id)
  WHERE lifecycle_state = 'active';

CREATE FUNCTION enforce_indexer_capability_profile_history()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
  integration_configuration jsonb;
  integration_secret_reference text;
  current_endpoint_digest bytea;
  integration_rows integer := 0;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.lifecycle_state <> 'draft' THEN
      RAISE EXCEPTION 'indexer capability profiles must begin as drafts';
    END IF;
    RETURN NEW;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'indexer capability profiles are append-only';
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.indexer_instance_id IS DISTINCT FROM OLD.indexer_instance_id
     OR NEW.revision IS DISTINCT FROM OLD.revision
     OR NEW.schema_version IS DISTINCT FROM OLD.schema_version
     OR NEW.profile_document IS DISTINCT FROM OLD.profile_document
     OR NEW.document_sha256 IS DISTINCT FROM OLD.document_sha256
     OR NEW.endpoint_config_sha256 IS DISTINCT FROM OLD.endpoint_config_sha256
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'indexer capability profile documents are immutable';
  END IF;

  IF OLD.lifecycle_state = 'draft'
     AND NEW.lifecycle_state NOT IN ('draft','active','invalidated') THEN
    RAISE EXCEPTION 'invalid indexer capability profile lifecycle transition';
  ELSIF OLD.lifecycle_state = 'active'
     AND NEW.lifecycle_state NOT IN ('active','superseded','invalidated') THEN
    RAISE EXCEPTION 'invalid indexer capability profile lifecycle transition';
  ELSIF OLD.lifecycle_state IN ('superseded','invalidated')
     AND NEW.lifecycle_state IS DISTINCT FROM OLD.lifecycle_state THEN
    RAISE EXCEPTION 'terminal indexer capability profile cannot transition';
  END IF;

  IF OLD.lifecycle_state = 'draft' AND NEW.lifecycle_state = 'active' THEN
    EXECUTE format(
      'SELECT config,secret_ref FROM %I.integration_instances WHERE id=$1 AND kind=''newznab''',
      TG_TABLE_SCHEMA
    ) INTO integration_configuration,integration_secret_reference
      USING NEW.indexer_instance_id;
    GET DIAGNOSTICS integration_rows = ROW_COUNT;
    IF integration_rows = 1 THEN
      EXECUTE format(
        'SELECT %I.media_manager_newznab_endpoint_config_sha256($1,$2)',
        TG_TABLE_SCHEMA
      ) INTO current_endpoint_digest
        USING integration_configuration,integration_secret_reference;
    END IF;
    IF integration_rows <> 1
       OR COALESCE((integration_configuration->>'category_selection_review_required')::boolean,true)
       OR current_endpoint_digest IS NULL
       OR NEW.endpoint_config_sha256 IS DISTINCT FROM current_endpoint_digest
       OR COALESCE(btrim(NEW.profile_document->'provider_contract'->>'key'),'')
         IS DISTINCT FROM COALESCE(btrim(integration_configuration->>'provider_contract_key'),'')
       OR (COALESCE(btrim(NEW.profile_document->'provider_contract'->>'key'),'') <> ''
         AND COALESCE((NEW.profile_document->'provider_contract'->>'version')::integer,0) <> 1)
       OR NOT EXISTS (
         SELECT 1
         FROM jsonb_array_elements(COALESCE(NEW.profile_document->'search_recipes','[]'::jsonb)) recipe
         WHERE recipe->>'role' = 'primary'
           AND recipe->'verification'->>'state' = 'verified'
       ) THEN
      RAISE EXCEPTION 'indexer capability profile activation prerequisites are not satisfied';
    END IF;
  END IF;

  IF NEW.lifecycle_state IS NOT DISTINCT FROM OLD.lifecycle_state
     AND (NEW.activated_at IS DISTINCT FROM OLD.activated_at
       OR NEW.superseded_at IS DISTINCT FROM OLD.superseded_at
       OR NEW.invalidated_at IS DISTINCT FROM OLD.invalidated_at
       OR NEW.invalidation_reason IS DISTINCT FROM OLD.invalidation_reason) THEN
    RAISE EXCEPTION 'indexer capability profile lifecycle evidence is immutable';
  END IF;

  IF NEW.activated_at IS DISTINCT FROM OLD.activated_at
     AND NOT (OLD.lifecycle_state = 'draft'
       AND NEW.lifecycle_state = 'active'
       AND OLD.activated_at IS NULL
       AND NEW.activated_at IS NOT NULL) THEN
    RAISE EXCEPTION 'indexer capability profile activation evidence cannot change';
  END IF;
  IF NEW.superseded_at IS DISTINCT FROM OLD.superseded_at
     AND NOT (OLD.lifecycle_state = 'active'
       AND NEW.lifecycle_state = 'superseded'
       AND OLD.superseded_at IS NULL
       AND NEW.superseded_at IS NOT NULL) THEN
    RAISE EXCEPTION 'indexer capability profile supersession evidence cannot change';
  END IF;
  IF (NEW.invalidated_at IS DISTINCT FROM OLD.invalidated_at
       OR NEW.invalidation_reason IS DISTINCT FROM OLD.invalidation_reason)
     AND NOT (OLD.lifecycle_state IN ('draft','active')
       AND NEW.lifecycle_state = 'invalidated'
       AND OLD.invalidated_at IS NULL
       AND OLD.invalidation_reason IS NULL
       AND NEW.invalidated_at IS NOT NULL
       AND NEW.invalidation_reason IS NOT NULL) THEN
    RAISE EXCEPTION 'indexer capability profile invalidation evidence cannot change';
  END IF;

  IF NEW.lifecycle_state = 'active'
     AND NEW.activated_at IS NULL THEN
    RAISE EXCEPTION 'active indexer capability profile requires activation evidence';
  END IF;
  IF NEW.lifecycle_state = 'superseded' AND NEW.superseded_at IS NULL THEN
    RAISE EXCEPTION 'superseded indexer capability profile requires a timestamp';
  END IF;
  IF NEW.lifecycle_state = 'invalidated'
     AND (NEW.invalidated_at IS NULL OR NEW.invalidation_reason IS NULL) THEN
    RAISE EXCEPTION 'invalidated indexer capability profile requires evidence';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER indexer_capability_profiles_append_only
BEFORE INSERT OR UPDATE OR DELETE ON indexer_capability_profiles
FOR EACH ROW EXECUTE FUNCTION enforce_indexer_capability_profile_history();

CREATE TABLE indexer_operation_status (
  indexer_instance_id uuid NOT NULL,
  capability_profile_id uuid NOT NULL,
  operation_key text NOT NULL CHECK (operation_key IN ('caps','search','details','nfo','file_list','feed','retrieve')),
  recipe_key text NOT NULL DEFAULT '' CHECK (recipe_key = '' OR recipe_key ~ '^[a-z][a-z0-9._:-]{0,99}$'),
  availability text NOT NULL DEFAULT 'unknown' CHECK (availability IN ('unknown','ready','deferred','degraded')),
  last_outcome text CHECK (last_outcome IS NULL OR last_outcome ~ '^[a-z][a-z0-9._:-]{0,99}$'),
  safe_code text CHECK (safe_code IS NULL OR safe_code ~ '^[a-z][a-z0-9._:-]{0,99}$'),
  last_failure_scope text CHECK (last_failure_scope IS NULL OR last_failure_scope IN ('instance','operation')),
  consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
  retry_at timestamptz,
  last_success_at timestamptz,
  last_failure_at timestamptz,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (capability_profile_id,operation_key,recipe_key),
  FOREIGN KEY (indexer_instance_id,capability_profile_id)
    REFERENCES indexer_capability_profiles(indexer_instance_id,id) ON DELETE RESTRICT
);

CREATE FUNCTION enforce_indexer_operation_status_target()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
  profile_document jsonb;
  profile_activated_at timestamptz;
  profile_rows integer := 0;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.indexer_instance_id IS DISTINCT FROM OLD.indexer_instance_id
       OR NEW.capability_profile_id IS DISTINCT FROM OLD.capability_profile_id
       OR NEW.operation_key IS DISTINCT FROM OLD.operation_key
       OR NEW.recipe_key IS DISTINCT FROM OLD.recipe_key THEN
      RAISE EXCEPTION 'indexer operation status identity is immutable';
    END IF;
  END IF;

  EXECUTE format(
    'SELECT capability.profile_document,capability.activated_at FROM %I.indexer_capability_profiles capability WHERE capability.indexer_instance_id=$1 AND capability.id=$2',
    TG_TABLE_SCHEMA
  ) INTO profile_document,profile_activated_at
    USING NEW.indexer_instance_id,NEW.capability_profile_id;
  GET DIAGNOSTICS profile_rows = ROW_COUNT;

  IF profile_rows <> 1 OR profile_activated_at IS NULL THEN
    RAISE EXCEPTION 'indexer operation status requires an activated capability profile';
  END IF;
  IF NEW.recipe_key <> '' THEN
    IF NEW.operation_key <> 'search' OR NOT EXISTS (
      SELECT 1
      FROM jsonb_array_elements(COALESCE(profile_document->'search_recipes','[]'::jsonb)) recipe
      WHERE recipe->>'key' = NEW.recipe_key
    ) THEN
      RAISE EXCEPTION 'indexer operation status recipe does not belong to its capability profile';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER indexer_operation_status_exact_target
BEFORE INSERT OR UPDATE ON indexer_operation_status
FOR EACH ROW EXECUTE FUNCTION enforce_indexer_operation_status_target();

WITH legacy_profile AS (
  SELECT jsonb_build_object(
    'schema_version', 1,
    'caps', jsonb_build_object(),
    'search_recipes', jsonb_build_array(),
    'operations', jsonb_build_array(),
    'retrieval', jsonb_build_object(),
    'mapping_refs', jsonb_build_array(),
    'evidence', jsonb_build_array()
  ) AS document
)
INSERT INTO indexer_capability_profiles(
  id,indexer_instance_id,revision,schema_version,lifecycle_state,
  profile_document,document_sha256,endpoint_config_sha256,created_at
)
SELECT uuidv7(),integration.id,1,1,'draft',
  legacy_profile.document,
  sha256(convert_to(legacy_profile.document::text,'UTF8')),
  media_manager_newznab_endpoint_config_sha256(integration.config,integration.secret_ref),
  now()
FROM integration_instances integration
CROSS JOIN legacy_profile
WHERE integration.kind = 'newznab'
  AND media_manager_newznab_endpoint_config_sha256(integration.config,integration.secret_ref) IS NOT NULL;
