CREATE UNIQUE INDEX idx_nodes_owner_id ON nodes(user_id,id);
CREATE TABLE node_bookmarks (
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INT NOT NULL,
    review_state TEXT NOT NULL DEFAULT 'unreviewed' CHECK(review_state IN ('unreviewed','reviewed','follow_up')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(user_id,node_id),
    FOREIGN KEY(user_id,node_id) REFERENCES nodes(user_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_bookmarks_owner_created ON node_bookmarks(user_id,created_at DESC,node_id DESC);
ALTER TABLE saved_searches
    ADD COLUMN note_filter TEXT NOT NULL DEFAULT '' CHECK(char_length(note_filter)<=200),
    ADD COLUMN bookmarked_only BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN review_filter TEXT NOT NULL DEFAULT '' CHECK(review_filter IN ('','unreviewed','reviewed','follow_up'));
ALTER TABLE saved_searches DROP CONSTRAINT saved_searches_result_sort_check;
ALTER TABLE saved_searches ADD CONSTRAINT saved_searches_result_sort_check
    CHECK(result_sort IN ('','recent','oldest','title','relevance','bookmarked'));
