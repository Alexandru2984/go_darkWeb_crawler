-- Initial schema. Designed to be idempotent so adopting golang-migrate against
-- the existing prod database is a no-op: every statement uses IF NOT EXISTS or
-- a DO block that checks pg_constraint before adding constraints. On a fresh
-- install this builds the full schema from scratch; on prod it just records v1.

-- ── users ────────────────────────────────────────────────────────────────────
-- Referenced by nodes / edges via user_id FK, so created first.
CREATE TABLE IF NOT EXISTS users (
    id                       SERIAL PRIMARY KEY,
    email                    VARCHAR(255) UNIQUE NOT NULL,
    password_hash            VARCHAR(255) NOT NULL,
    role                     VARCHAR(50)  DEFAULT 'user',
    is_verified              BOOLEAN      DEFAULT FALSE,
    verification_token       VARCHAR(255),
    verification_expires_at  TIMESTAMP,
    created_at               TIMESTAMP    DEFAULT CURRENT_TIMESTAMP
);

-- ── nodes ────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS nodes (
    id                     SERIAL PRIMARY KEY,
    url                    TEXT NOT NULL,
    title                  TEXT,
    status_code            INT,
    server_header          VARCHAR(100),
    metadata               JSONB,
    content                TEXT,
    retry_count            INT       DEFAULT 0,
    next_crawl_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    processing_status      VARCHAR(20) DEFAULT 'pending',
    depth                  INT       DEFAULT 0,
    search_vector          TSVECTOR,
    discovered_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_crawled_at        TIMESTAMP,
    content_hash           TEXT,
    re_crawl_interval_days INT       DEFAULT 7,
    category               VARCHAR(30) DEFAULT 'unknown',
    crawl_started_at       TIMESTAMP,
    host                   TEXT,
    user_id                INT NOT NULL DEFAULT 1 REFERENCES users(id) ON DELETE CASCADE
);

-- The old single-tenant UNIQUE(url) is replaced by the (url, user_id) tuple so
-- two users can independently track the same .onion.
ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_url_key CASCADE;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'nodes_url_user_key') THEN
    ALTER TABLE nodes ADD CONSTRAINT nodes_url_user_key UNIQUE (url, user_id);
  END IF;
END $$;

-- ── edges ────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS edges (
    source_url    TEXT NOT NULL,
    target_url    TEXT NOT NULL,
    user_id       INT NOT NULL DEFAULT 1 REFERENCES users(id) ON DELETE CASCADE,
    discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- The PK and FK on edges were rewritten when multi-tenancy landed; recreate
-- them only if they don't already exist on this database.
ALTER TABLE edges DROP CONSTRAINT IF EXISTS edges_pkey CASCADE;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'edges_pkey') THEN
    ALTER TABLE edges ADD CONSTRAINT edges_pkey PRIMARY KEY (source_url, target_url, user_id);
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'edges_source_url_fkey') THEN
    ALTER TABLE edges ADD CONSTRAINT edges_source_url_fkey
      FOREIGN KEY (source_url, user_id) REFERENCES nodes(url, user_id) ON DELETE CASCADE;
  END IF;
END $$;

-- ── auth_audit ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS auth_audit (
    id         SERIAL PRIMARY KEY,
    event      TEXT NOT NULL,
    email      TEXT,
    ip         TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ── blacklist ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS blacklist (
    domain   TEXT PRIMARY KEY,
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ── indexes ──────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_nodes_search_vector     ON nodes USING GIN(search_vector);
CREATE INDEX IF NOT EXISTS idx_nodes_status            ON nodes(processing_status, next_crawl_at);
CREATE INDEX IF NOT EXISTS idx_nodes_category          ON nodes(category);
CREATE INDEX IF NOT EXISTS idx_nodes_host              ON nodes(host);
CREATE INDEX IF NOT EXISTS idx_edges_target            ON edges(target_url);
CREATE INDEX IF NOT EXISTS idx_auth_audit_created      ON auth_audit(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_auth_audit_email_event  ON auth_audit(email, event, created_at DESC);

-- ── search_vector trigger ────────────────────────────────────────────────────
-- Recompute the tsvector only when title or content actually change, so a row
-- update that only bumps last_crawled_at doesn't pay the tokenizer cost.
CREATE OR REPLACE FUNCTION nodes_search_vector_update() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'INSERT'
     OR NEW.title   IS DISTINCT FROM OLD.title
     OR NEW.content IS DISTINCT FROM OLD.content THEN
    NEW.search_vector := to_tsvector('english',
      COALESCE(NEW.title, '') || ' ' || COALESCE(NEW.content, ''));
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS nodes_search_vector_trigger ON nodes;
CREATE TRIGGER nodes_search_vector_trigger
  BEFORE INSERT OR UPDATE ON nodes
  FOR EACH ROW EXECUTE FUNCTION nodes_search_vector_update();

-- ── historical backfill ──────────────────────────────────────────────────────
-- Populate `host` on rows discovered before the column existed. Idempotent:
-- the WHERE only matches NULL hosts, which is empty on a freshly-built schema.
UPDATE nodes SET host = (regexp_match(url, '^https?://([^/?#]+)'))[1] WHERE host IS NULL;
