# Private saved searches

Current extensions and schema-11 recovery are documented in
[bookmarks and changes](bookmarks-and-changes.md). The release-specific
migration and rollback notes below describe the earlier deployment.

Open **Dashboard → List & search → Saved searches**. Choose the current query,
category and crawl status, enter a name, then choose **Save search**. Saving is
explicit: typing, searching and changing filters do not create stored history.
Each entry can be applied, renamed or deleted. **Clear** clears query text;
**Reset all filters** also clears category/status and returns to the first page.

Entries are private to the owning account, including when an administrator
views global crawl results. They persist across devices after sign-in. Maximums
are 50 entries per account, 80 Unicode characters per name and 200 per query.
Names are case-insensitively unique within an account. Applying a snapshot
updates all filters together and sends one result request.

## Privacy and lifecycle

Names and criteria are stored as plaintext in PostgreSQL, like other research
preferences. Application authorization isolates accounts; this is not encryption
against the VPS/database operator. Names and query terms are never put in
resource URLs or browser storage. API responses use `no-store`; handlers do not
log their values. Vue renders user-supplied names as text.
Private query/name inputs disable autocomplete, capitalization and spellcheck.

Saved searches are preferences, retained until explicit deletion independently
of crawl-record expiry. Personal JSON export version **3** includes the
`saved_searches` array. **Privacy → Saved searches** deletes all of the account's
entries after current-password proof and MFA when enabled. Individual deletion
keeps crawl records. Final account erasure cascades to saved entries.

Deletion removes live records. Existing encrypted archives follow the Google
Drive backup and provider retention policies; deletion does not rewrite old
archives. A historical restore can reintroduce data deleted after its snapshot,
so reconcile subsequent deletion requests during recovery.

## API

All endpoints require a valid session. Cookie writes require the existing CSRF
header. The caller's current account ID determines scope; client-supplied account
IDs or roles are rejected. Administrators have no cross-account exception.

| Method | Path | Body / result |
| --- | --- | --- |
| GET | `/api/saved-searches` | `{searches: [...], limit: 50}` |
| POST | `/api/saved-searches` | `{name, q, category, status, title, host, discovered_from, discovered_to, tag, sort}` creates an entry |
| POST | `/api/saved-searches/{id}` | `{name}` renames the caller's entry |
| DELETE | `/api/saved-searches/{id}` | Deletes the caller's entry |

An empty query saves category/status-only filters. Fields are bounded and
validated on the server, and capped JSON decoding rejects unknown fields and
additional documents. Unknown and other-account IDs both return 404. Duplicate
names and full quotas return 409. Mutations share a 60/minute account limit.

Creation locks the parent account row before counting entries, keeping the
quota intact under concurrent writes. Bulk erasure uses the same lock. The
existing search endpoint also accepts up to 200 Unicode characters and rejects
NUL, instead of incorrectly treating the character limit as a byte limit.

## Current research criteria and rollback

Saved searches now retain title, host, discovery dates, private collection and
sort order as well as query/category/status. See the
[collection and research runbook](collections-and-research.md) for semantics,
rename propagation, missing-collection behaviour and current recovery steps.

Migration 9 introduced saved searches; migration 10 extends their criteria.
The current fallback must embed migration 10 and preserve all fields and export
version 3. An untouched older executable cannot start against the current
schema. Never apply Down as an automatic rollback, since it removes data.

The matching fallback UI blocks applying advanced saved criteria while keeping
them available for export. Applying just the older fields would broaden a saved
search. Basic searches remain usable until forward recovery restores the new
controls. Release manifests retain both executables, matching frontends and
compatibility source under the root-restricted release directory.

Use `make build` for static host executables. Backups continue through the
existing encrypted Google Drive workflow, including the paired TOTP keyring.
