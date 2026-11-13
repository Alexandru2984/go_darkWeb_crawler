# Security follow-up — private reading list and change workflow

Review date: 2026-11-13. Scope: bookmark ownership/quota enforcement, private-note
research, change-feed filtering/read marking, export and selective erasure.
Production deployment uses the existing loopback API, nginx/Cloudflare routing,
static-executable sandbox and encrypted Google Drive backup pipeline.

## Findings addressed

1. Personal export omitted recorded watch events and offered only the limited
   feed as a separate read. Export format 4 now streams the complete owned
   history, with a regression fixture exceeding 200 events. Small bookmark
   references are exported separately, bounded by the 1,000/account quota.
2. The UI's mark-all action could read events arriving after its fetch. The new
   UI submits only displayed unread IDs, or one event ID. SQL scopes the update
   to the caller; missing/empty/null, invalid and oversized selections fail
   rather than falling back to all. IDs outside PostgreSQL SERIAL range return
   400 before SQL, avoiding internal errors for oversized JSON integers.
   Truly empty legacy requests retain their
   prior explicit semantics for compatible clients.
3. Notes advertised a character limit but enforced bytes. The limit now counts
   4,000 Unicode code points, rejects NUL and permits fully escaped surrogate
   pairs in a bounded 64 KiB JSON body. Private note/tag browser fields disable
   autocomplete and spellcheck. Research/saved-create body limits accommodate
   full Unicode while all individual field limits remain enforced.
4. New bookmark writes resolve the caller's own node and hold it through commit.
   A composite owner/node foreign key enforces the same boundary in PostgreSQL.
   Account-row serialization prevents concurrent writes exceeding the quota;
   an existing bookmark remains idempotent at capacity. The new write limiter
   is independent of collection writes.
5. Private note/review/bookmark filters always force the caller's own graph,
   including administrators. Values are parameterized and substring wildcards
   escaped literally. Review/kind/sort use fixed choices. Saved snapshots keep
   booleans as booleans and retain every private criterion.
6. Watch reads/observations join events, watches and nodes on matching owners as
   well as IDs. Filtered results apply before pagination, with bounded pages,
   stable tie ordering, explicit load errors and aborted obsolete requests.
7. Bulk bookmark/history erasure uses existing password/MFA reauthentication
   before data mutation, preserves watched sites/records and affects only the
   caller. Crawl/account deletion cascades to bookmarks. Retention behavior and
   the fact that stopping a watch removes its history are explicit in the UI.

## Validation

- Full Go/PostgreSQL suite passed under the race detector; final targeted API
  tests exercise fully escaped 4,000-emoji notes and both purge scopes with
  enrolled MFA proof. Integration fixtures cover owner/admin boundaries,
  database-level owner enforcement, concurrent final quota slots, idempotence,
  literal note/title matching, saved criteria, filtered paging, selected IDs,
  complete history export and preservation of watches during erasure.
- Go vet and golangci-lint passed. Source/API-binary/compatible-binary
  govulncheck scans reported no known vulnerabilities. Frontend lint, 27 unit
  tests, production builds and both npm audit scopes passed; no dependency was
  added. Scoped full-history and staged Gitleaks scans found no secrets.
- Chromium exercised the actual static API and disposable PostgreSQL accounts:
  bookmark add/remove/review/pagination, opt-in previews, private filters, saved
  snapshots, filtered feeds, an event arriving after display, selected read,
  account/admin isolation, CSRF refusal, export v4 and password-protected purge.
  It checked 320/375/768/1280 px for horizontal overflow/clipped controls and
  reported no JavaScript errors. A mobile screenshot was reviewed.
- Recovery testing proved the untouched previous binary rejects schema 11,
  the compatible fallback preserves export and all private criteria, its UI
  blocks only unsupported saved applications, and forward recovery restores
  the complete controls. No destructive Down migration is part of recovery.

## Boundaries and follow-up

This feature review does not replace the broader threat model. A bookmark
follows crawl retention; only a watch pins a site during automatic cleanup.
Feed offset pagination is not a frozen snapshot: new events can shift later
pages, so Refresh remains available and read mutations use explicit IDs.
Exports stream sections sequentially rather than from one transactional snapshot.
Copies already downloaded by a user and older encrypted backup archives are
outside immediate database-erasure semantics.

Independent external uptime detection, full-host recovery/key custody,
monitoring-runtime patch tracking and passkeys remain in the production
roadmap. API/npm scan results do not imply the whole VPS or unrelated services
are free of vulnerabilities. CI/SEO expansion remains a later product stage.

## Production evidence

- Feature release: `20261113T050002Z-workflow`, source `7346d14` with backend
  `fcbce58`; static API SHA-256
  `09d0d1eb9bf7418b0e13f4e41b64276463f03e30fa53eca78f17b643b8226cb0`.
  The release contains the previous and compatible binaries/frontends, the
  complete compatibility source archive and its frontend build recipe.
- Event-ID range validation: source `bc8464d`, release
  `20261113T050441Z-event-id-validation`, current static API SHA-256
  `a156b2987dca95793915f1cf28dd50617f4adf7a775f6422f74b7e6b49afda6c`.
  This patch changes no schema/data format and retains the previous schema-11
  API plus source for immediate rollback.
- Encrypted post-feature Google Drive backup completed at
  `2026-11-13T05:01:33.118950+00:00`. Its paired-keyring isolated restore completed
  at `2026-11-13T05:06:00.309868+00:00`: schema **11**, dirty **false**,
  unvalidated constraints **0**, TOTP keyring verification **true**.
- Production API readiness and nginx checks passed; public Chromium confirmed
  the current frontend entry, authentication redirects, 401 on protected
  endpoints, public registration closed and no horizontal login overflow or
  JavaScript errors. Private monitoring reported **4/4** healthy targets,
  **17** rules and **0** pending/firing alerts.
- Disposable browser accounts/databases were removed. Real accounts and owner
  credentials were not changed; no invitation, reset or test email was sent.
  Existing operator Alertmanager changes were left outside these commits.
