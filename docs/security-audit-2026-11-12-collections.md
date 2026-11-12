# Collections and research-filter review — 2026-11-12

This stage follows the [saved-search release](security-audit-2026-11-12-saved-searches.md).
The [collection/research runbook](collections-and-research.md) covers usage,
retention, limits, API semantics and rollback.

## Findings and behaviour

The existing tag transaction counted memberships without locking a shared row,
so concurrent additions could both observe a free final slot. Re-adding a tag
at capacity also incorrectly failed before checking whether it already existed.
Tag writes now lock the account, check existing membership first, enforce the
25/site and 100/account limits, and hold the referenced node through commit.
Tag names use Unicode character bounds matching PostgreSQL instead of a byte
limit advertised as characters.

Collections reuse personal tags, avoiding a second membership store. Reads,
membership changes, renames and deletion always use the authenticated account.
An administrator's global crawl access grants no cross-account exception.
Renames reject existing destination groups and update saved filters atomically.
Deleting a group retains its saved criterion so applying it cannot broaden
results. Bulk tag erasure requires current-password and enrolled MFA proof.

Title/host/discovery-date/collection filters and result order are evaluated
before pagination. Research values stay in bounded JSON bodies even without a
content query. Title wildcards are escaped literally, hostnames use the existing
cryptographic onion validation, dates are checked before SQL casts, and sort
expressions come from fixed server choices. SQL values are parameterized.
Advanced drafts are applied explicitly instead of issuing requests per keystroke.
Saved searches preserve every filter and the personal export format is now 3.

Direct `SaveNode` insertion previously omitted the canonical host. Production
inspection found no missing hosts at review time, since the normal queue path
already fills them. Saving now fills/repairs the host on new, changed and
unchanged-content paths, keeping exact-host filtering reliable for direct saves.

Browser checks found tablet navigation could place sign-out beyond the viewport.
The drawer now remains available until the expanded menu fits. Result tables
also followed viewport size inside a narrower collection panel; container-based
layout now selects cards when the panel cannot fit a table. Empty-page recovery
after removing its last member returns to page one, and pagination follows the
server's actual has-more header.

## Validation

- Full Go/PostgreSQL tests passed with the race detector. Additional targeted
  runs passed after final changes. Tests cover composed filters before paging,
  inclusive discovery dates, literal title wildcards, Unicode/NUL/date/sort
  bounds, admin/user isolation, atomic final quota slots, idempotence at capacity,
  rename collisions, saved-filter propagation and missing-group restriction.
- Personal export scope and password/MFA-protected collection erasure were
  exercised. An unchanged-content write repaired its missing host in a forced
  regression fixture. Empty browse results have a stable JSON array shape.
- Go vet, golangci-lint, source and static-binary govulncheck scans passed.
  The compatible fallback binary was scanned too. Frontend lint, 27 unit tests,
  production builds and npm audit passed. No dependency was added.
- Chromium used the actual static API and disposable PostgreSQL accounts.
  It exercised creation/addition/rename/deletion, collision refusal, full filter
  snapshots, draft reset, one browse request per saved apply, markup escaping,
  exact 20/21-row pagination, member removal and empty-page recovery, account
  and administrator isolation, export v3, CSRF and password proof.
- The UI was checked at 320/375/768/1280 px for overflow and clipped controls,
  including navigation. Mobile screenshots were visually reviewed. No JavaScript
  errors were recorded.
- The untouched schema-9 API failed safely against schema 10 in isolation.
  The compatible API and matching fallback frontend retained all criteria,
  exported them and blocked advanced Apply rather than dropping conditions.
  Forward recovery restored Apply. A SQL dump/restore round trip retained
  advanced criteria and remaining memberships with no unvalidated constraints.
- Staged implementation changes passed Gitleaks. Production credentials and
  research data were not used for UI tests, and no test email was sent.
  No GitHub push or hosted CI run was performed.

## Publication

Backend `3257d50` and frontend `774eb6e` were published at 21:46:25 UTC.
Release artifacts and the manifest are retained under the root-restricted path
`/home/micu/.local/state/onion-spider/releases/20261112T214625Z-collections`.
It contains forward/previous executables, the schema-compatible fallback and
its source archive, matching frontends and the fallback frontend build recipe.
Automatic rollback keeps migration 10 in place and never runs destructive Down.

The pre-publication encrypted Google Drive snapshot completed at 21:34:59 UTC
and included its paired TOTP keyring. Readiness, nginx validation and live
schema checks passed: version 10, dirty=false. Public registration remains
closed. Local onion-vhost checks rejected unauthenticated research/collection
POSTs with 401 and no-store; all four monitoring targets were healthy.

Cloudflare browser checks confirmed the Collections route requires sign-in,
new research/collection endpoints reject anonymous POSTs with 401/no-store,
registration remains closed and all three email-link routes still scrub their
fragments. No JavaScript errors were recorded.

The post-publication encrypted snapshot completed at 21:48:31 UTC. Its isolated
Google Drive restore completed at 21:56:02 UTC with schema 10, dirty=false,
zero unvalidated constraints and a verified paired TOTP keyring. Restoration
did not write to the production database. Disposable test databases and API/web
listeners were removed after verification.
