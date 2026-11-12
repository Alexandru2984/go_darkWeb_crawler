-- Destructive: normal production rollback retains this table and uses the
-- schema-compatible fallback API. Never run Down as an automatic rollback.
DROP TABLE IF EXISTS saved_searches;
