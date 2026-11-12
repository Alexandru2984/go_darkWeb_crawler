-- Explicitly saved per-account preferences, never automatic search history.
CREATE TABLE saved_searches (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    query_text TEXT NOT NULL DEFAULT '' CHECK (char_length(query_text) <= 200),
    category TEXT NOT NULL DEFAULT '' CHECK (octet_length(category) <= 50),
    status TEXT NOT NULL DEFAULT '' CHECK (status IN ('', 'pending', 'crawling', 'completed', 'failed', 'blocked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX saved_searches_user_name ON saved_searches(user_id, lower(name));
CREATE INDEX saved_searches_user_updated ON saved_searches(user_id, updated_at DESC, id DESC);
