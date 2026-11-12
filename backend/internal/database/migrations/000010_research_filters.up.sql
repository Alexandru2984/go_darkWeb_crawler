-- Additive saved criteria; existing saved searches keep their original scope.
ALTER TABLE saved_searches
    ADD COLUMN title_filter TEXT NOT NULL DEFAULT '' CHECK (char_length(title_filter) <= 200),
    ADD COLUMN host_filter TEXT NOT NULL DEFAULT '' CHECK (octet_length(host_filter) <= 253),
    ADD COLUMN discovered_from TEXT NOT NULL DEFAULT '' CHECK (length(discovered_from) IN (0,10)),
    ADD COLUMN discovered_to TEXT NOT NULL DEFAULT '' CHECK (length(discovered_to) IN (0,10)),
    ADD COLUMN collection_tag TEXT NOT NULL DEFAULT '' CHECK (char_length(collection_tag) <= 40),
    ADD COLUMN result_sort TEXT NOT NULL DEFAULT '' CHECK (result_sort IN ('','recent','oldest','title','relevance'));
