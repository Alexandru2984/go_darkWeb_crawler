-- Never use Down for automatic recovery: it erases the operator's pause intent.
UPDATE nodes SET processing_status='failed' WHERE processing_status='cancelled';
UPDATE saved_searches SET status='failed' WHERE status='cancelled';
ALTER TABLE saved_searches DROP CONSTRAINT saved_searches_status_check;
ALTER TABLE saved_searches ADD CONSTRAINT saved_searches_status_check
    CHECK(status IN ('','pending','crawling','completed','failed','blocked'));
DROP INDEX idx_nodes_owner_schedule;
ALTER TABLE nodes DROP COLUMN last_failure_code;
ALTER TABLE users DROP COLUMN crawl_paused;
