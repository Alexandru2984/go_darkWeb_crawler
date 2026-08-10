-- Time-based two-factor authentication.
--
-- totp_secret holds a pending or active base32 seed. It is only meaningful
-- together with totp_enabled: enrolment writes the secret first, and the second
-- factor does not take effect until the user proves they can generate a code
-- from it. Storing an unconfirmed secret as "enabled" would lock out anyone who
-- abandoned enrolment halfway.
--
-- totp_last_step records the counter step of the last accepted code. A TOTP
-- code stays valid for its whole period plus the accepted drift, so without
-- this a code observed once can be replayed until it expires. Every successful
-- verification must move this forward, and codes at or below it are refused.
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret       VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_confirmed_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step    BIGINT  NOT NULL DEFAULT 0;

-- Single-use fallbacks for a lost authenticator. Only the digest is stored:
-- these are high-entropy random values, so SHA-256 is sufficient and a database
-- or backup compromise yields nothing usable, exactly as with the existing
-- verification and reset credentials.
CREATE TABLE IF NOT EXISTS recovery_codes (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  VARCHAR(64) NOT NULL,
    used_at    TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Redemption looks a code up by (user, digest) and must not be able to match
-- another account's code.
CREATE UNIQUE INDEX IF NOT EXISTS idx_recovery_codes_user_hash
    ON recovery_codes(user_id, code_hash);
