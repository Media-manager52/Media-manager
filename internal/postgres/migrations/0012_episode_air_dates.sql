ALTER TABLE pack_expected_episodes ADD COLUMN air_date date;
CREATE INDEX pack_expected_episodes_air_date ON pack_expected_episodes(air_date) WHERE air_date IS NOT NULL;
