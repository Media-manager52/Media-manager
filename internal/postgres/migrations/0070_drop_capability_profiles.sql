-- Search capability profiles are removed. Only the removed commissioning v2
-- could activate one, so search always used the dialect the normal commission
-- check stored. The table holds only draft profiles (migration 0033 created an
-- empty one per Newznab indexer), and indexer_operation_status was never
-- written.
--
-- The migration refuses to run if any profile was ever activated or any
-- operation status was recorded, so no used profile is dropped silently.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM indexer_capability_profiles WHERE lifecycle_state <> 'draft')
     OR EXISTS (SELECT 1 FROM indexer_operation_status) THEN
    RAISE EXCEPTION 'an indexer capability profile was used; export it before upgrading';
  END IF;
END;
$$;

DROP TABLE indexer_operation_status;
DROP FUNCTION enforce_indexer_operation_status_target();
DROP TABLE indexer_capability_profiles;
DROP FUNCTION enforce_indexer_capability_profile_history();
DROP FUNCTION media_manager_newznab_endpoint_config_sha256(jsonb, text);
