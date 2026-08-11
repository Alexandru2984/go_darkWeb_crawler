-- Private annotations and change watching.
--
-- Everything here is per-account and scoped by user_id, not just by node_id.
-- Two accounts can hold the same .onion in their own graphs (that is what the
-- (url, user_id) key on nodes is for), and what one of them wrote about it is
-- none of the other's business. Every lookup in this file is keyed by the pair.

-- ── tags ─────────────────────────────────────────────────────────────────────
-- Short labels the account puts on a site. The value is the user's own words
-- about someone else's site, which makes it more sensitive than the crawl
-- record it hangs off, not less.
CREATE TABLE IF NOT EXISTS node_tags (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id    INT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    tag        VARCHAR(40) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Applying the same tag twice is not an error the interface should invent, so
-- the insert relies on this to be a no-op rather than a duplicate row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_node_tags_unique ON node_tags(user_id, node_id, tag);

-- "Everything I tagged X", and the tag list with counts.
CREATE INDEX IF NOT EXISTS idx_node_tags_by_tag ON node_tags(user_id, tag);

-- ── notes ────────────────────────────────────────────────────────────────────
-- One free-text note per account per site. Keyed by the pair rather than given
-- its own id: there is nothing to say about a second note on the same site, and
-- a composite primary key makes "one note" a property of the schema instead of
-- a rule the application has to keep remembering.
CREATE TABLE IF NOT EXISTS node_notes (
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id    INT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, node_id)
);

-- ── watches ──────────────────────────────────────────────────────────────────
-- A site the account wants to be told about when it changes.
--
-- last_hash is the watch's own copy of the digest it last reported on, kept
-- separately from nodes.content_hash on purpose. The crawler updates the node's
-- digest as part of storing the crawl; if the watch read that same column, a
-- failure between storing the crawl and recording the event would lose the
-- notification permanently — the digest would already say "current" and the next
-- crawl would find nothing changed. With its own watermark the watch simply
-- notices the difference again on the next pass.
--
-- NULL means never observed: the first crawl after starting a watch establishes
-- the baseline and reports nothing, because "this page differs from the nothing
-- I knew before" is not a change the user asked to hear about.
-- last_reachable is a separate column rather than a sentinel inside last_status.
-- A crawl that fails at the network level has no HTTP status at all, so folding
-- the two would mean reading "no status" as either "never observed" or "was
-- answering fine" — and both readings produce wrong events: a site that has been
-- down for a week reports going down on every pass, or never reports coming
-- back. NULL here means never observed, which is the only state that suppresses
-- an event entirely.
CREATE TABLE IF NOT EXISTS watches (
    id              SERIAL PRIMARY KEY,
    user_id         INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id         INT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    interval_days   INT NOT NULL DEFAULT 1,
    last_hash       TEXT,
    last_status     INT,
    last_reachable  BOOLEAN,
    last_checked_at TIMESTAMP,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_watches_unique ON watches(user_id, node_id);
CREATE INDEX IF NOT EXISTS idx_watches_user ON watches(user_id, created_at DESC);

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'watches_interval_sane') THEN
    ALTER TABLE watches ADD CONSTRAINT watches_interval_sane
      CHECK (interval_days >= 1 AND interval_days <= 365);
  END IF;
END $$;

-- ── watch events ─────────────────────────────────────────────────────────────
-- The change feed. Deliberately a table rather than an email: a message saying
-- "the site you are watching changed" tells whoever handles that mailbox what
-- this account is interested in, and the whole point of this service is that
-- nobody outside it learns that. The feed is read after signing in.
CREATE TABLE IF NOT EXISTS watch_events (
    id          SERIAL PRIMARY KEY,
    watch_id    INT NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    user_id     INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        VARCHAR(20) NOT NULL,
    status_code INT,
    detected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    seen_at     TIMESTAMP
);

-- The feed, newest first, and the unread count beside it.
CREATE INDEX IF NOT EXISTS idx_watch_events_feed ON watch_events(user_id, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_watch_events_unseen ON watch_events(user_id) WHERE seen_at IS NULL;
