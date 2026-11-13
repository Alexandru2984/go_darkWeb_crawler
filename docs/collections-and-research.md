# Private collections and research filters

Current extensions and schema-11 recovery are documented in
[bookmarks and changes](bookmarks-and-changes.md). The release-specific
migration and rollback notes below describe the earlier deployment.

Open **Collections** to group sites already present in your own crawl graph.
Create a collection with its first site, add more sites, rename it or remove it
with explicit confirmation. Each tag is a collection membership, so existing
tags appear automatically. A site can belong to several collections. Removing
its last member removes that collection; there are no separate empty groups.

Names are normalized to lowercase with collapsed whitespace and support up to
40 Unicode characters. Each account can have 100 collections and each site can
have 25 tags. Re-adding an existing membership succeeds at capacity. Account-row
locking enforces both limits under concurrent requests.

Collection reads and writes are always personal, including for administrators.
Having global crawl access does not permit tagging another account's site or
reading its private groups. Names and site URLs travel in JSON bodies, never in
collection resource paths, browser storage or shareable filter URLs.

## Filtering and saved searches

In **Dashboard → More filters & sorting**, choose a title substring, validated
v3 onion hostname, discovery-date range, private collection and result order.
Choose **Apply filters** to update results. Editing a draft does not issue
requests for each keystroke, and saving is disabled until the draft is applied.
Reset clears drafts as well as all applied filters.

- Title filtering is case-insensitive and treats `%`, `_` and backslashes as
  literal characters, not user-selected SQL wildcards.
- A hostname identifies the exact onion host. It is validated locally and does
  not initiate a network request or a crawl. HTTP/HTTPS web ports are normalized
  away.
- Dates include the full selected discovery days. These use the stored discovery
  calendar; historical timestamps have no timezone offset. The range is checked
  before SQL execution.
- Automatic sorting uses content relevance when a query exists and newest
  discovery otherwise. Explicit orders include newest, oldest, title and
  relevance; relevance without a content query falls back to newest.
- Every filter is applied before pagination. Collection filters always select
  the caller's own graph even when the administrator normally sees global data.

Saved searches retain all criteria. Renaming a collection updates matching
saved filters in the same transaction and never changes another account's data.
Renaming into another existing collection returns a conflict instead of silently
merging groups. Deleting a collection leaves its saved filter restrictive: it
returns no sites until that tag is restored or the saved search is replaced.
The interface displays a missing collection instead of clearing its filter.

## Export, retention and deletion

Personal JSON format **3** includes collection summaries, memberships within
annotations and every saved filter field. Collections are retained tags and do
not have independent metadata. They disappear when their crawl records expire
or are erased; final account deletion cascades to tags and saved searches.

**Privacy → Collections and tags** removes all memberships after current-password
proof and MFA when enrolled. Crawl records, notes, watches and saved criteria
are managed separately. Deleting saved searches is the separate way to remove
the retained collection names from those preferences. Individual collection
removal requires confirmation and keeps the underlying sites.

Research preferences and annotations remain plaintext in PostgreSQL. Account
isolation does not hide them from the VPS/database operator. Deleted live data
can remain in encrypted archives until backup/provider retention expires; a
historical restore must reconcile later deletion requests.

## API and bounds

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/browse?page=1&limit=30` | JSON query/category/status plus title, host, discovered_from, discovered_to, tag and sort |
| GET | `/api/tags` | At most 100 personal collection names with membership counts |
| POST | `/api/collections/add` | `{url, tag}` creates/reuses a group and adds an owned site |
| POST | `/api/collections/rename` | `{tag, name}` renames a personal group |
| POST | `/api/collections/delete` | `{tag}` removes its memberships |

All require authentication. Cookie POSTs require CSRF proof. Browsing shares the
60/minute account search budget. Collection writes and tag additions share a
separate 60/minute budget. Strict JSON rejects unknown fields and trailing
documents. Browse and saved-creation bodies are capped at 8 KiB; all fields
have independent bounds. SQL values are parameterized and result ordering comes
from fixed server-side choices. Empty browse results are JSON arrays.

## Deployment and rollback

Migration **10** adds saved title/host/date/tag/sort columns with empty defaults;
existing queries retain their original scope. Host and account/discovery indexes
already exist. The normal API is built with `make build` and must remain static
for the systemd execution sandbox.

Do not run Down or install the untouched schema-9 executable after migration 10.
The tested fallback uses base `fefab66`, with these backend files from `3257d50`:
`db.go`, `annotations.go`, `collections.go`, `research_filters.go`,
`saved_searches.go` under `internal/database`, both migration-10 SQL files and
`internal/api/privacy_handlers.go`. The complete mixed source tree is archived
beside the compiled `api.compatible` artifact.

The fallback reads schema 10 and preserves personal export format 3 and all
stored criteria. Its matching frontend blocks applying advanced saved searches
and explains their temporary unavailability; applying only the older fields
would silently broaden results. Basic searches remain usable, and forward
recovery restores the new controls. Native sign-out navigation and result-list
responsive fixes are also retained in the fallback UI.

Publication requires a fresh encrypted Google Drive snapshot, paired TOTP
keyring verification, static executable checks and readiness. New migrations
stay in place during automatic rollback. After publication, create a schema-10
snapshot and verify it using the isolated Google Drive restore service.
