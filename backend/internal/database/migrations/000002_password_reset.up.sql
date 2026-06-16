-- Password reset + session revocation.
--
-- reset_token / reset_expires_at mirror the email-verification columns and back
-- the "forgot password" flow. token_version is the session-revocation lever:
-- it is embedded in every JWT at login and compared against the DB on each
-- authenticated request. Bumping it (on password reset or explicit logout-all)
-- instantly invalidates every token issued beforehand, without rotating the
-- global JWT_SECRET (which would log out every user at once).
--
-- Idempotent: ADD COLUMN IF NOT EXISTS so re-running against a DB that already
-- has these columns is a no-op.
ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_token       VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_expires_at  TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version     INT NOT NULL DEFAULT 0;

-- Lookups during reset confirmation hit reset_token directly.
CREATE INDEX IF NOT EXISTS idx_users_reset_token ON users(reset_token);
