# Saved-search security and release review — 2026-11-12

This release follows the [administrator access review](security-audit-2026-11-12-admin.md).
Usage, retention and rollback are in the [saved-search runbook](saved-searches.md).

## Changes and threat boundary

Accounts can explicitly save query/category/status snapshots, apply them,
rename them and delete them. Names and terms stay in request bodies and are
rendered as text. Every SQL read/write is scoped to the current user; global
administrator access never bypasses saved-search ownership. Unknown and foreign
IDs are indistinguishable. Cookie writes use CSRF protection, strict bounded
JSON and a separate account mutation limit.

Account-row locking serializes quota checks and bulk erasure. Per-account
storage is limited to 50 entries, names to 80 Unicode characters and queries to
200. The existing search endpoint previously counted bytes despite advertising
a character limit; Unicode character bounds and NUL rejection now match saved
queries. Search failure logs no longer receive the query as an explicit field.
Browser autocomplete, capitalization and spellcheck are disabled for private
query and saved-name fields.

Saved entries are retained preferences, independent of crawl expiry. Personal
export format 2 includes them before unbounded sections start streaming.
Password/MFA-protected bulk deletion and account-deletion cascade cover them.
Names and terms remain plaintext in PostgreSQL, accessible to the host/database
operator, and historical backups follow their existing retention policy.

Migration 9 is additive. Testing confirmed the untouched version-8 API cannot
start against version 9. The retained fallback embeds migration 9 and backports
personal export, purge and query validation. Automatic rollback preserves the
new table instead of applying the destructive Down migration. The previous
interface temporarily hides saved-search controls until forward recovery.

## Validation

- Full Go tests with PostgreSQL and the race detector passed. Targeted tests
  passed after the final changes, including ownership for ordinary users and
  administrators, duplicate names, Unicode/NUL boundaries, concurrent quota
  enforcement, account cascade, export scope, password/MFA proof and account
  mutation-limit isolation. A forced database failure proved query text did not
  reach error logs.
- `go vet`, golangci-lint, frontend lint, 27 existing frontend unit tests and
  production builds passed. Source, final static API and compatible fallback
  govulncheck scans found no vulnerabilities; npm audit also passed. No new
  dependency was added.
- Chromium exercised the actual static API and a disposable PostgreSQL
  database: save/apply/rename/delete, one request per complete filter snapshot,
  reset, escaped markup, Unicode bounds, cross-device persistence, account
  isolation, export v2, CSRF rejection and reauthenticated bulk deletion.
  It also confirmed private-field browser attributes and absence of query/name
  writes to localStorage/sessionStorage.
- Responsive checks covered 320/375/768/1280 px, document overflow and clipped
  controls. A mobile screenshot was visually reviewed. No JavaScript errors
  were recorded.
- Compatibility fallback export and forward recovery retained an existing
  saved search. A separate SQL dump/restore round trip preserved schema 9,
  the query and filters, with dirty=false and no unvalidated constraints.
- Each staged implementation change passed Gitleaks. No hosted CI run or
  GitHub push was performed. Production user credentials were not used in
  tests, and this stage sent no test emails.

## Publication

Backend `8ad1b9f` and interface `9b98897` were published at 12:35:31 UTC.
Browser-field hardening `284c5ef` was subsequently published without restarting
the API. The root-restricted release directory is
`/home/micu/.local/state/onion-spider/releases/20261112T123531Z-saved-searches`.
Its manifest records both source revisions and digests. It retains the forward
API/frontend, original executable, compatible fallback/source archive and prior
frontend.

The pre-publication encrypted Google Drive backup completed at 12:33:02 UTC.
A new schema-9 snapshot completed at 12:37:58 UTC, including the paired TOTP
keyring. Readiness and nginx validation passed. The live schema reports version
9 and dirty=false. Loopback API and local onion vhost deny unauthenticated
saved-search access with 401 and `no-store`.

Chromium checks through Cloudflare confirmed closed registration, 401/no-store
for private saved searches, and unchanged fragment scrubbing for reset,
verification and invitation pages. No JavaScript errors occurred. All four
private Prometheus targets were healthy after publication.

The post-publication Google Drive restore completed at 12:43:55 UTC. Its
isolated database reported schema 9, dirty=false, zero unvalidated constraints
and a verified paired TOTP keyring. The production database was not used for
restoration, and no production queries were created for testing.
