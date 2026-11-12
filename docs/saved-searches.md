# Private saved searches

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
of crawl-record expiry. Personal JSON export version **2** includes the
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
| POST | `/api/saved-searches` | `{name, q, category, status}` creates an entry |
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

## Schema and rollback

Migration **9** adds the `saved_searches` table, per-account name/indexes and an
account foreign key with `ON DELETE CASCADE`. It does not rewrite crawl data.
The original API with migrations only through version 8 cannot start against
version 9. **Do not use the untouched previous executable or run Down as an
automatic rollback**: Down removes saved searches.

The release provides a tested `api.compatible` executable. It uses base commit
`21fcd42` plus these files from backend commit `8ad1b9f`:

- `internal/database/saved_searches.go`
- `internal/database/migrations/000009_saved_searches.up.sql`
- `internal/database/migrations/000009_saved_searches.down.sql`
- `internal/api/privacy_handlers.go`
- `internal/api/nodes_handlers.go`

The fallback embeds migration 9 and preserves personal export version 2, bulk
erasure and Unicode-query validation. Its previous frontend hides saved-search
controls until forward recovery. Existing entries and all account/crawl data
are retained. The compiled fallback and its complete source archive accompany
the release manifest, previous frontend and forward artifacts in the
root-restricted release directory. The isolated browser test exercised fallback
export and forward recovery while retaining an existing saved search.

Use `make build` for the host API, which verifies a static executable. The
compatibility artifact uses `CGO_ENABLED=0 go build -buildvcs=false -trimpath
-ldflags='-s -w'`; `-buildvcs=false` reflects the explicit mixed source tree.
Keep the production systemd execution sandbox in place.

Normal backups use the existing encrypted Google Drive workflow, including the
paired TOTP keyring. A fresh snapshot is required before publication; verify a
new schema-9 snapshot through the isolated restore drill after publication.
