-- Destructive to saved filter details: normal rollback keeps these columns.
ALTER TABLE saved_searches DROP COLUMN result_sort, DROP COLUMN collection_tag,
    DROP COLUMN discovered_to, DROP COLUMN discovered_from,
    DROP COLUMN host_filter, DROP COLUMN title_filter;
