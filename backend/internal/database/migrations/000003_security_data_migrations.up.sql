-- Records irreversible, application-level data migrations. This lets the Go
-- process transform sensitive values transactionally without requiring the
-- pgcrypto extension or elevated database privileges.
CREATE TABLE security_data_migrations (
    name       TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
