-- Blacklists represent an onion domain, not a virtual port. Older releases
-- stored URL.Host (which included :port) in nodes.host, allowing the same onion
-- service to evade a domain block by changing between ports 80 and 443.
UPDATE nodes
SET host = split_part(lower(host), ':', 1)
WHERE host LIKE '%:%';
