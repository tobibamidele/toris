-- Control database bootstrap for integration tests.
-- Creates the toris_control schema that the toris daemon writes to.
-- In production this is created by 'toris daemon' via EnsureSchema().
-- For integration tests we pre-create it so tests can run against a
-- clean schema without starting the daemon.

CREATE SCHEMA IF NOT EXISTS toris_control;

-- Grant full privileges to the toris user on the schema.
GRANT ALL PRIVILEGES ON SCHEMA toris_control TO toris;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA toris_control TO toris;
ALTER DEFAULT PRIVILEGES IN SCHEMA toris_control
    GRANT ALL PRIVILEGES ON TABLES TO toris;
