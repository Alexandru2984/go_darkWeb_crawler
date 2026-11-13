ALTER TABLE users ADD COLUMN crawl_paused BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE nodes ADD COLUMN last_failure_code TEXT NOT NULL DEFAULT ''
    CHECK(last_failure_code IN ('','network','robots','invalid_url','storage','http_error'));
CREATE INDEX idx_nodes_owner_schedule ON nodes(user_id,processing_status,next_crawl_at,id);
ALTER TABLE saved_searches DROP CONSTRAINT saved_searches_status_check;
ALTER TABLE saved_searches ADD CONSTRAINT saved_searches_status_check
    CHECK(status IN ('','pending','crawling','completed','failed','blocked','cancelled'));
