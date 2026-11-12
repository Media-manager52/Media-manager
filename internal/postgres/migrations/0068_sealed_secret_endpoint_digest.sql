-- An indexer whose API key was saved on the website has the secret
-- reference sealed:integration:<id> (schema 55). The endpoint digest only
-- accepted environment-variable names, so it was NULL for such an indexer and
-- commissioning could never start or activate a capability profile for it.
-- The digest now also accepts the exact sealed reference shape. Environment
-- references hash exactly as before, so every stored digest stays valid.
CREATE OR REPLACE FUNCTION media_manager_newznab_endpoint_config_sha256(configuration jsonb, secret_reference text)
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
STRICT
SET search_path = pg_catalog
AS $$
DECLARE
  endpoint pg_catalog.text := COALESCE(configuration->>'base_url','');
  endpoint_parts pg_catalog.text[];
  normalized_scheme pg_catalog.text;
  normalized_hostname pg_catalog.text;
  effective_port pg_catalog.text;
  base_path pg_catalog.text;
  contract_key pg_catalog.text := COALESCE(btrim(configuration->>'provider_contract_key'),'');
BEGIN
  IF jsonb_typeof(configuration) <> 'object'
     OR octet_length(endpoint) > 2048
     OR endpoint <> btrim(endpoint)
     OR position(chr(92) in endpoint) > 0
     OR position('%' in regexp_replace(endpoint,'%[0-9A-Fa-f]{2}','','g')) > 0
     OR NOT (secret_reference ~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$'
             OR secret_reference ~ '^sealed:integration:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
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
     AND COALESCE(configuration->'allow_http','false'::pg_catalog.jsonb) <> 'true'::pg_catalog.jsonb THEN
    RETURN NULL;
  END IF;

  normalized_hostname := lower(endpoint_parts[2]);
  IF left(normalized_hostname,1) = '[' AND right(normalized_hostname,1) = ']' THEN
    normalized_hostname := substr(normalized_hostname,2,length(normalized_hostname)-2);
    BEGIN
      PERFORM normalized_hostname::pg_catalog.inet;
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
    IF effective_port::pg_catalog.int4 < 1 OR effective_port::pg_catalog.int4 > 65535 THEN
      RETURN NULL;
    END IF;
    effective_port := (effective_port::pg_catalog.int4)::pg_catalog.text;
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
    'allow_http', COALESCE(configuration->'allow_http','false'::pg_catalog.jsonb) = 'true'::pg_catalog.jsonb,
    'authentication', 'query_api_key',
    'secret_reference', secret_reference,
    'provider_contract_key', contract_key,
    'provider_contract_version', CASE WHEN contract_key = '' THEN 0 ELSE 1 END
  )::pg_catalog.text,'UTF8'));
END
$$;
