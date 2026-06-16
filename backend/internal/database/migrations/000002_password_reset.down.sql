-- Reverse of 000002_password_reset.up.sql.
DROP INDEX IF EXISTS idx_users_reset_token;
ALTER TABLE users DROP COLUMN IF EXISTS token_version;
ALTER TABLE users DROP COLUMN IF EXISTS reset_expires_at;
ALTER TABLE users DROP COLUMN IF EXISTS reset_token;
