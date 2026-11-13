# Private bookmarks, notes and change-feed workflow

Current scheduling controls, export format 5 and schema-12 recovery are described
in [crawl jobs](crawl-jobs.md). The release-specific notes below describe the
schema-11 deployment.


Delivered on 2026-11-13, with schema **11** and personal-export format **4**.

## Product behavior

`/bookmarks` is an authenticated reading list with 20 results per page, an
owned-site URL form, content search, optional private-note previews and the
existing research filters. Open a site's annotation dialog to bookmark/remove
it or choose **Not reviewed**, **Reviewed** or **Follow up**. The same dialog is
available from the dashboard and collections. Up to 1,000 bookmarks per account
are supported; duplicate creation is idempotent even at capacity.

Bookmarks follow the crawl record's retention policy. Removing a bookmark keeps
the record and its notes, tags and watch; erasing the record or account cascades
to its bookmarks. A bookmark does not pin a record against retention. Watching
remains the explicit way to retain a site during automatic cleanup.

Dashboard research criteria now include a literal private-note substring,
bookmarked-only selection, bookmark review and recently-bookmarked ordering.
Review and bookmarked ordering imply bookmarked-only. Those criteria are
preserved in saved searches. Any note/tag/bookmark criterion restricts even an
administrator to their own graph. Draft filters require Apply; notes and terms
are never placed in URLs, browser storage or analytics. Note previews are hidden
in the reading-list UI until requested.

`/changes` supports type, unread, title, onion-host and collection filters before
pagination. Its badge shows the account-wide unread total. **Mark visible read**
sends only the unread IDs in the displayed page; an individual event can also
be marked read. Events arriving after the page loaded remain unread. Stopping
a watch removes its associated history, as the page now explains.

Personal export format 4 includes bounded bookmark references and streams all
watch events, without the change-feed page limit. It remains account-scoped for
administrators and excludes credential material. Privacy offers independent
bookmark and change-history erasure, each requiring current password and any
enrolled second factor. Neither operation removes watched sites or crawl
records. Saved criteria remain until separately deleted.

## API and constraints

- `POST /api/bookmarks`: `{url}`, `{url,remove:true}` or `{url,review}`. Creating
  uses an account-row lock, checks owned-node existence through commit and
  serializes quota checks. A composite foreign key also enforces node ownership.
  Review updates require an existing personal bookmark.
- `POST /api/browse`: adds `note` (200 Unicode characters), `bookmarked_only`,
  `review` and `sort:"bookmarked"`. Browse and saved-create bodies permit 16 KiB
  to accommodate escaped Unicode; fields retain independent bounds.
- `POST /api/watch/events/query`: `{kind,unread_only,title,host,tag}`, body up to
  8 KiB, pagination-only `page`/`limit` URL parameters. Returns
  `{events,unseen,has_more}`. Sensitive values remain in the body.
- `POST /api/watch/events/seen`: `{ids:[...]}`, 1–200 IDs in the range 1–2,147,483,647, body up to
  4 KiB. Empty/null/missing selections and unknown fields are rejected. A truly
  empty legacy request retains the older explicit mark-all operation.
- `POST /api/privacy/purge`: adds `bookmarks` and `watch_events` scopes.

All endpoints require authentication; cookie mutations require CSRF proof.
Bookmark writes have a separate 60/minute account limit. Filtered feed and
browse share the existing 60/minute search limit. Responses are no-store;
SQL values are bound, substring wildcards are literal, review/sort/kind values
are fixed choices, and owner predicates are mandatory in watch/node joins.
Notes now count actual Unicode characters (maximum 4,000), reject NUL and allow
up to 64 KiB of escaped JSON. Native browser spellcheck/autocomplete are disabled
for private note/tag fields.

## Release and recovery

Use `make build` for the static API required by the systemd execution sandbox.
Build frontend assets outside the root-owned live `frontend/dist`, publish
hashed assets before `index.html` and retain existing assets for open tabs.

Do not run migration 11 Down or install the untouched schema-10 binary after
migration. A compatible fallback is based on `5f50427` with the current
`internal/database` Go/SQL files and API `privacy_handlers.go`,
`annotation_handlers.go`, `saved_search_handlers.go` and `collection_handlers.go`.
The complete source, static API and frontend builder recipe are archived with
the release. It preserves all data and export format 4; its older UI disables
only saved searches containing note/bookmark/review filters so they cannot be
silently broadened. Earlier title/host/date/collection filters remain usable.
Forward recovery restores the full controls without changing the schema.

Publication requires a fresh encrypted Google Drive backup with its paired
TOTP keyring, static binaries, schema/readiness checks and the tested fallback.
After publishing, create a schema-11 snapshot and verify it through the isolated
Google Drive restore service. Rclone/Google Drive remains the offsite provider.
