-- Reverse of 000001_init.up.sql. Drops every object the up created, in the
-- order that respects FK dependencies. Use with caution — running this on
-- prod DELETES ALL DATA in the onion-spider schema.

DROP TRIGGER  IF EXISTS nodes_search_vector_trigger ON nodes;
DROP FUNCTION IF EXISTS nodes_search_vector_update();

DROP INDEX IF EXISTS idx_auth_audit_email_event;
DROP INDEX IF EXISTS idx_auth_audit_created;
DROP INDEX IF EXISTS idx_edges_target;
DROP INDEX IF EXISTS idx_nodes_host;
DROP INDEX IF EXISTS idx_nodes_category;
DROP INDEX IF EXISTS idx_nodes_status;
DROP INDEX IF EXISTS idx_nodes_search_vector;

DROP TABLE IF EXISTS blacklist;
DROP TABLE IF EXISTS auth_audit;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS users;
