-- Destructive to bookmarks and saved workflow criteria. Never use for automatic rollback.
ALTER TABLE saved_searches DROP CONSTRAINT saved_searches_result_sort_check;
UPDATE saved_searches SET result_sort='' WHERE result_sort='bookmarked';
ALTER TABLE saved_searches ADD CONSTRAINT saved_searches_result_sort_check CHECK(result_sort IN ('','recent','oldest','title','relevance'));
ALTER TABLE saved_searches DROP COLUMN review_filter,DROP COLUMN bookmarked_only,DROP COLUMN note_filter;
DROP TABLE node_bookmarks;
DROP INDEX idx_nodes_owner_id;
