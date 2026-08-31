CREATE OR REPLACE FUNCTION media_manager_sanitize_newznab_query_scoped(input text, depth integer, remove_r boolean)
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
  decoded_name text;
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
    decoded_name := media_manager_url_decode_component(raw_name);
    IF media_manager_is_credential_query_name(raw_name) OR (remove_r AND lower(COALESCE(decoded_name, raw_name)) = 'r') THEN
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
  remove_r boolean;
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
      remove_r := prefix ~* '^https?://([A-Za-z0-9-]+\.)*drunkenslug\.com(:[0-9]+)?(/|$)';
      sanitized := media_manager_sanitize_newznab_query_scoped(raw_query, depth, remove_r);
      IF sanitized IS DISTINCT FROM raw_query THEN
        RETURN prefix || CASE WHEN sanitized = '' THEN '' ELSE '?' || sanitized END || substr(input, query_end);
      END IF;
      RETURN input;
    END IF;
  END IF;
  IF strpos(input, '=') > 0 THEN
    sanitized := media_manager_sanitize_newznab_query_scoped(input, depth, false);
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
  ) VALUES (28, observations_before, affected_before, updated_rows, observations_after, affected_after)
  ON CONFLICT (migration_version) DO NOTHING;
END
$migration$;
