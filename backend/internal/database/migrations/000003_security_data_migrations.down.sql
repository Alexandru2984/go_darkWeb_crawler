-- Token digests cannot be converted back to plaintext. Invalidate outstanding
-- links on downgrade so an older binary never mistakes a digest for a usable
-- verification or reset credential.
UPDATE users
SET verification_token = NULL,
    verification_expires_at = NULL,
    reset_token = NULL,
    reset_expires_at = NULL;

DROP TABLE IF EXISTS security_data_migrations;
