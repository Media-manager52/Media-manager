UPDATE integration_instances
SET enabled = false,
    is_default = false,
    config = jsonb_set(config, '{mode_verified}', 'false'::jsonb, true),
    updated_at = now()
WHERE kind = 'newznab';

ALTER TABLE integration_instances
  ADD CONSTRAINT newznab_enabled_only_after_commission
  CHECK (kind <> 'newznab' OR NOT enabled OR COALESCE(config->'mode_verified' = 'true'::jsonb, false));
