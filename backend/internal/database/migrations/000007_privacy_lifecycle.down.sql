-- Rolling back drops the policy, not the data it governed: any crawl record the
-- retention sweeper already deleted is gone for good. Reverting this migration
-- only means accounts stop expressing a preference and revert to keeping
-- everything, which is the pre-migration behaviour.
DROP INDEX IF EXISTS idx_nodes_user_discovered;
DROP INDEX IF EXISTS idx_users_deletion_due;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_retention_days_nonneg;

ALTER TABLE users DROP COLUMN IF EXISTS deletion_scheduled_for;
ALTER TABLE users DROP COLUMN IF EXISTS deletion_requested_at;
ALTER TABLE users DROP COLUMN IF EXISTS store_content;
ALTER TABLE users DROP COLUMN IF EXISTS retention_days;
