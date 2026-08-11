-- Per-account data lifecycle: how long crawl records are kept, whether page
-- content is stored at all, and a scheduled path out of the service.
--
-- retention_days is the age after which an account's crawl records are deleted
-- automatically. Zero means "keep indefinitely", which is the behaviour every
-- existing account already has — so the default preserves it and nothing is
-- silently reaped by adding this column.
--
-- store_content is the metadata-only switch. Page text is by far the most
-- sensitive thing this service holds: it is a copy of someone else's site,
-- retained on our disk and in our backups. An account that only needs the graph
-- (which hosts exist, what links to what, status codes) has no reason to make
-- us hold that copy, and turning it off removes a whole class of exposure that
-- no amount of access control on the stored copy can match.
--
-- deletion_requested_at / deletion_scheduled_for implement a grace period. The
-- request is recorded immediately but acted on later, so a deletion triggered by
-- a stolen session (or by a misclick) can still be reversed by the real owner
-- during the window. Both columns are set together; either being NULL means no
-- deletion is pending.
ALTER TABLE users ADD COLUMN IF NOT EXISTS retention_days         INT     NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS store_content          BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deletion_requested_at  TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deletion_scheduled_for TIMESTAMP;

-- A negative retention would make the cutoff a future timestamp and delete
-- everything the account owns on the next sweep. The API validates the range,
-- but the sweeper is destructive enough that the database should refuse the
-- value outright rather than trust every future caller.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_retention_days_nonneg') THEN
    ALTER TABLE users ADD CONSTRAINT users_retention_days_nonneg CHECK (retention_days >= 0);
  END IF;
END $$;

-- The deletion sweeper asks only for rows that are due. Partial index so it
-- stays a tiny lookup regardless of how many accounts exist, since in the
-- normal case no account has a deletion pending at all.
CREATE INDEX IF NOT EXISTS idx_users_deletion_due
    ON users(deletion_scheduled_for)
    WHERE deletion_scheduled_for IS NOT NULL;

-- The retention sweeper scans one account's rows by age. Without this it is a
-- sequential scan of the whole nodes table once per account per run.
CREATE INDEX IF NOT EXISTS idx_nodes_user_discovered ON nodes(user_id, discovered_at);
