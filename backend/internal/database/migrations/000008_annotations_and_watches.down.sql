-- Dropping these destroys the account's own writing — tags, notes and the
-- change history it collected. That is not recoverable from the crawl data,
-- which is why this migration is only ever run deliberately.
DROP TABLE IF EXISTS watch_events;
DROP TABLE IF EXISTS watches;
DROP TABLE IF EXISTS node_notes;
DROP TABLE IF EXISTS node_tags;
