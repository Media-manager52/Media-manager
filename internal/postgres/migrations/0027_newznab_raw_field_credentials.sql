CREATE OR REPLACE FUNCTION media_manager_url_decode_component(input text)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $function$
DECLARE
  result bytea := ''::bytea;
  position integer := 1;
  current text;
  encoded text;
BEGIN
  WHILE position <= char_length(input) LOOP
    current := substr(input, position, 1);
    IF current = '%' THEN
      IF position + 2 > char_length(input) THEN
        RETURN NULL;
      END IF;
      encoded := substr(input, position + 1, 2);
      IF encoded !~ '^[0-9A-Fa-f]{2}$' THEN
        RETURN NULL;
      END IF;
      result := result || decode(encoded, 'hex');
      position := position + 3;
    ELSIF current = '+' THEN
      result := result || decode('20', 'hex');
      position := position + 1;
    ELSE
      result := result || convert_to(current, 'UTF8');
      position := position + 1;
    END IF;
  END LOOP;
  RETURN convert_from(result, 'UTF8');
EXCEPTION WHEN others THEN
  RETURN NULL;
END
$function$;

CREATE OR REPLACE FUNCTION media_manager_url_encode_component(input text)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $function$
DECLARE
  source bytea := convert_to(input, 'UTF8');
  result text := '';
  position integer := 0;
  value integer;
BEGIN
  WHILE position < length(source) LOOP
    value := get_byte(source, position);
    IF (value BETWEEN 65 AND 90) OR (value BETWEEN 97 AND 122) OR (value BETWEEN 48 AND 57) OR value IN (45, 46, 95, 126) THEN
      result := result || chr(value);
    ELSIF value = 32 THEN
      result := result || '+';
    ELSE
      result := result || '%' || upper(lpad(to_hex(value), 2, '0'));
    END IF;
    position := position + 1;
  END LOOP;
  RETURN result;
END
$function$;

CREATE OR REPLACE FUNCTION media_manager_is_credential_query_name(input text)
RETURNS boolean
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $function$
DECLARE
  value text := btrim(input);
  decoded text;
  attempt integer := 0;
  canonical text;
BEGIN
  WHILE attempt < 4 LOOP
    decoded := media_manager_url_decode_component(value);
    EXIT WHEN decoded IS NULL OR decoded = value;
    value := decoded;
    attempt := attempt + 1;
  END LOOP;
  canonical := lower(regexp_replace(value, '[^[:alnum:]]', '', 'g'));
  RETURN canonical = ANY (ARRAY[
    'apikey','token','apitoken','accesstoken','authtoken','authorization',
    'password','passwd','secret','apisecret','clientsecret','credential','credentials'
  ]);
END
$function$;

CREATE OR REPLACE FUNCTION media_manager_sanitize_newznab_query(input text, depth integer)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $function$
DECLARE
  parts text[] := string_to_array(input, '&');
  part text;
  separator integer;
  raw_name text;
  raw_value text;
  decoded text;
  sanitized text;
  result text := '';
  retained integer := 0;
BEGIN
  IF cardinality(parts) > 4096 THEN
    RETURN '';
  END IF;
  FOREACH part IN ARRAY parts LOOP
    separator := strpos(part, '=');
    IF separator > 0 THEN
      raw_name := substr(part, 1, separator - 1);
      raw_value := substr(part, separator + 1);
    ELSE
      raw_name := part;
      raw_value := NULL;
    END IF;
    IF media_manager_is_credential_query_name(raw_name) THEN
      CONTINUE;
    END IF;
    IF raw_value IS NOT NULL AND depth + 1 < 4 THEN
      decoded := media_manager_url_decode_component(raw_value);
      IF decoded IS NOT NULL THEN
        sanitized := media_manager_sanitize_newznab_value(decoded, depth + 1);
        IF sanitized IS DISTINCT FROM decoded THEN
          part := raw_name || '=' || media_manager_url_encode_component(sanitized);
        END IF;
      END IF;
    END IF;
    IF retained > 0 THEN
      result := result || '&';
    END IF;
    result := result || part;
    retained := retained + 1;
  END LOOP;
  RETURN result;
END
$function$;

CREATE OR REPLACE FUNCTION media_manager_sanitize_newznab_value(input text, depth integer DEFAULT 0)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $function$
DECLARE
  query_at integer;
  fragment_offset integer;
  query_end integer;
  prefix text;
  raw_query text;
  sanitized text;
  decoded text;
BEGIN
  IF octet_length(input) > 1048576 THEN
    RETURN '[provider URL metadata removed: safety bound exceeded]';
  END IF;
  IF depth >= 4 THEN
    RETURN input;
  END IF;
  query_at := strpos(input, '?');
  IF query_at > 0 THEN
    prefix := substr(input, 1, query_at - 1);
    IF prefix = '' OR prefix ~* '^(https?://|//|/)' THEN
      fragment_offset := strpos(substr(input, query_at + 1), '#');
      IF fragment_offset > 0 THEN
        query_end := query_at + fragment_offset;
        raw_query := substr(input, query_at + 1, query_end - query_at - 1);
      ELSE
        query_end := char_length(input) + 1;
        raw_query := substr(input, query_at + 1);
      END IF;
      sanitized := media_manager_sanitize_newznab_query(raw_query, depth);
      IF sanitized IS DISTINCT FROM raw_query THEN
        RETURN prefix || CASE WHEN sanitized = '' THEN '' ELSE '?' || sanitized END || substr(input, query_end);
      END IF;
      RETURN input;
    END IF;
  END IF;
  IF strpos(input, '=') > 0 THEN
    sanitized := media_manager_sanitize_newznab_query(input, depth);
    IF sanitized IS DISTINCT FROM input THEN
      RETURN sanitized;
    END IF;
  END IF;
  decoded := media_manager_url_decode_component(input);
  IF decoded IS NOT NULL AND decoded IS DISTINCT FROM input THEN
    sanitized := media_manager_sanitize_newznab_value(decoded, depth + 1);
    IF sanitized IS DISTINCT FROM decoded THEN
      RETURN media_manager_url_encode_component(sanitized);
    END IF;
  END IF;
  RETURN input;
END
$function$;

CREATE OR REPLACE FUNCTION media_manager_sanitize_newznab_raw_fields(input jsonb)
RETURNS jsonb
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $function$
DECLARE
  result jsonb;
BEGIN
  IF jsonb_typeof(input) <> 'array' THEN
    RETURN input;
  END IF;
  SELECT COALESCE(jsonb_agg(
    CASE
      WHEN jsonb_typeof(item.value) = 'object' AND jsonb_typeof(item.value->'value') = 'string'
        THEN jsonb_set(item.value, '{value}', to_jsonb(media_manager_sanitize_newznab_value(item.value->>'value')), false)
      ELSE item.value
    END ORDER BY item.ordinality
  ), '[]'::jsonb)
  INTO result
  FROM jsonb_array_elements(input) WITH ORDINALITY AS item(value, ordinality);
  RETURN result;
END
$function$;

CREATE TABLE IF NOT EXISTS security_migration_aggregates (
  migration_version bigint PRIMARY KEY,
  observations_before bigint NOT NULL,
  affected_rows_before bigint NOT NULL,
  rows_updated bigint NOT NULL,
  observations_after bigint NOT NULL,
  affected_rows_after bigint NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);

DO $migration$
DECLARE
  observations_before bigint;
  affected_before bigint;
  updated_rows bigint;
  observations_after bigint;
  affected_after bigint;
BEGIN
  SELECT count(*), count(*) FILTER (
    WHERE raw_fields IS DISTINCT FROM media_manager_sanitize_newznab_raw_fields(raw_fields)
  )
  INTO observations_before, affected_before
  FROM release_observations;

  UPDATE release_observations
  SET raw_fields = media_manager_sanitize_newznab_raw_fields(raw_fields)
  WHERE raw_fields IS DISTINCT FROM media_manager_sanitize_newznab_raw_fields(raw_fields);
  GET DIAGNOSTICS updated_rows = ROW_COUNT;

  SELECT count(*), count(*) FILTER (
    WHERE raw_fields IS DISTINCT FROM media_manager_sanitize_newznab_raw_fields(raw_fields)
  )
  INTO observations_after, affected_after
  FROM release_observations;

  INSERT INTO security_migration_aggregates(
    migration_version, observations_before, affected_rows_before, rows_updated,
    observations_after, affected_rows_after
  ) VALUES (27, observations_before, affected_before, updated_rows, observations_after, affected_after)
  ON CONFLICT (migration_version) DO NOTHING;
END
$migration$;

CREATE OR REPLACE FUNCTION media_manager_sanitize_newznab_raw_fields_trigger()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
  NEW.raw_fields := media_manager_sanitize_newznab_raw_fields(NEW.raw_fields);
  RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS release_observations_sanitize_raw_fields ON release_observations;
CREATE TRIGGER release_observations_sanitize_raw_fields
BEFORE INSERT OR UPDATE OF raw_fields ON release_observations
FOR EACH ROW EXECUTE FUNCTION media_manager_sanitize_newznab_raw_fields_trigger();
