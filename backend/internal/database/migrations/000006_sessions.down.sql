-- Rolling back drops the per-session revocation records. Tokens already issued
-- keep working until they expire or token_version is bumped, which is the
-- coarse behaviour that existed before this migration.
DROP TABLE IF EXISTS sessions;
