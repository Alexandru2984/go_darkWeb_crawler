-- Downgrading removes the second factor entirely. That weakens every enrolled
-- account, so it is deliberately explicit rather than a silent side effect of
-- rolling back a release.
DROP TABLE IF EXISTS recovery_codes;

ALTER TABLE users DROP COLUMN IF EXISTS totp_last_step;
ALTER TABLE users DROP COLUMN IF EXISTS totp_confirmed_at;
ALTER TABLE users DROP COLUMN IF EXISTS totp_enabled;
ALTER TABLE users DROP COLUMN IF EXISTS totp_secret;
