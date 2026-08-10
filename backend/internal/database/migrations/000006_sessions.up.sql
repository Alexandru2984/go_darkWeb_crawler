-- Server-side session inventory.
--
-- Until now the only revocation lever was token_version, which is all-or-
-- nothing: signing one stolen device out meant signing every device out. This
-- table gives each login its own row so a single session can be ended, and so
-- a user can see where their account is actually signed in.
--
-- token_id is a random identifier embedded in the JWT, not the JWT itself. The
-- token stays the bearer credential; this is only a handle for looking up and
-- revoking it, so the table is not a store of usable credentials.
--
-- device_label is a coarse family such as "Firefox on Linux", derived from the
-- User-Agent and never the raw string. The point is to let a user recognise
-- their own devices, not to build a fingerprint: the raw header is far more
-- identifying than that purpose requires, and this table outlives the session.
-- No IP address is stored, for the same reason.
CREATE TABLE IF NOT EXISTS sessions (
    id           SERIAL PRIMARY KEY,
    user_id      INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id     VARCHAR(64) NOT NULL,
    device_label VARCHAR(80),
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   TIMESTAMP NOT NULL,
    revoked_at   TIMESTAMP
);

-- Every authenticated request resolves a session by its token_id, so this
-- lookup has to be an index rather than a scan.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_token_id ON sessions(token_id);

-- Listing a user's sessions, newest first.
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, last_used_at DESC);

-- Expired rows are swept in bulk; without this the sweeper scans the table.
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
