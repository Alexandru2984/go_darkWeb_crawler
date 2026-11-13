# Privacy-first production roadmap

This is the implementation order, not a promise that every idea belongs in the
product. Security and privacy gates take precedence over feature count.

## Current plan reviewed on 2026-11-13

The baseline audit is recorded in
[the security review](security-audit-2026-11-11.md), with the latest
[crawl-control follow-up](security-audit-2026-11-13-crawl-jobs.md). Earlier
delivery notes below describe their original release dates.

### Delivered in the current implementation

- [x] Add personal queue pause/resume, safe cancellation, run-now and local-time
  scheduling, quota/failure visibility and read-only batch preview with explicit
  acceptance. Serialize retry/cancel against worker claims, apply submission
  quotas to retries, preserve complete job metadata in export format 5 and
  provide a fail-closed schema-12 recovery mode. Make crawler alerts respect
  paused/future work and policy refusals.

- [x] Add a private bookmark reading list with review states, per-account atomic
  quotas, optional note previews, literal note search and complete saved filter
  snapshots. Add filtered/paginated change feeds, selection-only read marking,
  complete streamed history in export format 4 and password/MFA-protected bulk
  bookmark/history erasure. Verify Unicode note bounds, responsive layouts and
  data-preserving schema-11 recovery.

- [x] Add a private collection workspace backed by tags, atomic collection and
  membership limits, rename propagation to saved searches and guarded removal.
  Add private title/domain/discovery-date filters and stable sorting, apply
  drafts explicitly, preserve all saved criteria and provide export format 3.
- [x] Keep sign-out navigation available on tablets and size result tables to
  their actual container, including narrower collection workspaces.

- [x] Add private named saved searches with apply/rename/delete, bounded storage,
  concurrent quota enforcement and complete filter reset. Include saved queries
  in personal export, reauthenticated bulk erasure and account deletion; verify
  responsive display and a schema-compatible rollback.

- [x] Repair operator-approved owner access while retaining the existing account
  and data. Add a responsive MFA-gated Admin page, paginated account inventory,
  reauthenticated account invitations, one-time password-choice activation,
  explicit delivery failures, resend recovery and crawler blacklist controls.
- [x] Consume email credentials before browser-history construction so the
  router cannot restore reset, verification or invitation fragments.

- [x] Apply MFA policy to every administrative data path and revoke older
  sessions when authentication factors change.
- [x] Require the current password for MFA setup, protect concurrent enrolment
  and bound password work across accounts.
- [x] Honour account-deletion cancellation and enforce metadata-only writes.
- [x] Purge extracted page entities and restore text after storage is re-enabled.
- [x] Patch runtime dependencies, isolate Docker build contexts and repair
  container-to-container API access.
- [x] Add a responsive queue page and a crawl-record details dialog.
- [x] Filter category and status before pagination in both list and search.
- [x] Handle expired sessions, offline errors and obsolete annotation requests.
- [x] Reflect server registration availability on the sign-in page.
- [x] Add a public product page with an interactive fictional-data demo.
- [x] Add canonical metadata, a public sitemap and local robots policy.
- [x] Enforce npm security auditing, strict lint and source/binary Go scanning.
- [x] Deploy encrypted daily Google Drive backups with 7 local and 30 active
  offsite archives, verified recovery metadata and a monthly isolated restore
  drill. Ten backup regressions also run in CI.
- [x] Deploy private project-owned monitoring with four healthy scrape targets,
  17 alert rules, authenticated backup/readiness collection and a verified local
  notification pipeline. Enable operator-approved email routing and confirm
  receipt of one test. Add 24 rule scenarios and eight collector tests to CI.
- [x] Correct stale queue gauges and patch the API runtime to Go 1.26.11 for
  GO-2026-6859; publish the tested binary with a rollback artifact.
- [x] Add password changes requiring current credentials and MFA, with atomic
  session/reset-token invalidation and responsive account-security controls.
- [x] Encrypt pending and active TOTP seeds using an independent rotatable
  keyring, account-bound authenticated envelopes and recoverable Google Drive
  keyring snapshots. Test legacy migration, replay protection and browser use
  after key rotation.
- [x] Add authenticated recovery-code replacement, low-code guidance and codes
  with over 128 bits of uniform randomness. Test races, failed inserts, replay,
  legacy-code compatibility and mobile display.

### Next implementation stages

| Order | Stage | Capabilities and completion criteria |
| --- | --- | --- |
| 1 | Operational recovery and detection | Scraping, project rules, Google Drive backups, isolated restores, local notification testing and operator email delivery are verified. Add independent external detection, replace monitoring images when upstream ships the Go runtime patch, verify external recovery-key custody and exercise full host recovery. Record operator detection and restoration times |
| 2 | Account hardening | Encrypted TOTP storage/rotation, reauthenticated password changes and recovery-code replacement are deployed. Add passkeys per origin and security-event history; review privilege changes and continue covering replay/concurrent credential updates |
| 3 | Collections and search | Private collections, bookmarks/review states, note/title/domain/discovery-date filters, user-selected sorting, saved snapshots and filtered change feeds are deployed. Extend language support and configurable result presentation while keeping research terms out of request URLs |
| 4 | Research workflow | Private bookmarks, note search, tag management, change-feed filters and review states are deployed. Add keyboard review shortcuts, safe multi-select actions and first-use guidance; preserve separate-account ownership for the same URL |
| 5 | Crawl job management | Personal pause/cancel, scheduling, quota/failure visibility and previewed batch import are deployed with serialized worker claims. Add safe multi-select controls and lease-backed interruption of in-flight requests |
| 6 | Graph exploration | Add category/status filters, selected-node details, connected-neighbour loading, clustering and an accessible list of the displayed graph. Keep graph data loading bounded |
| 7 | Optional history | Add explicit opt-in snapshots, bounded retention and a text-diff view. Include snapshots in export, purge and account deletion before enabling the feature |
| 8 | Exports and automation | Add explicit export scope, row estimates, cancellable export jobs, expiry and encrypted temporary storage. Introduce separate API credentials with narrow scopes, one-time display, expiry and revocation |
| 9 | Onboarding and mobile | Administrator invitations and pending-account resend are deployed with public registration closed. Add first-use guidance, improve empty states and complete keyboard, screen-reader, Firefox and Safari checks |
| 10 | CI and release automation | Pin action revisions, add browser and accessibility checks, container runtime smoke tests and image scanning. Verify the pushed workflow and create a reproducible release/rollback path |
| 11 | Public documentation and SEO | Add public usage/security documentation, validate crawler-visible metadata, improve public-page performance and expand the demo. Keep authenticated records and account pages excluded from indexing |

Each stage should land as a focused, verified commit using the repository owner's
identity. Features that retain additional data must include export, retention and
deletion behaviour in the same stage.

The current interface keeps the graph in a separately loaded chunk. The final
build transfers about 46.4 kB gzip for the shared entry chunk and about 154.5 kB
gzip for the graph chunk. Those figures provide a baseline for further UI work.

## Product north star

- Private by default: no third-party analytics, advertising, fingerprinting or
  sensitive request logs.
- Tor-native: the onion endpoint is a first-class access path, not a hidden
  afterthought.
- Tenant-safe: every read, write, graph edge, export and background job is
  scoped at the database boundary and tested with duplicate cross-tenant data.
- Data-minimal: users control retention, export and deletion; raw content is
  optional and expires.
- Honest security: publish limitations, threat model and incident process; no
  “unhackable” or zero-knowledge claim while the hosted crawler sees URLs.
- Fast and accessible: useful on a small phone, keyboard, screen reader and slow
  Tor circuit without pulling a large graph bundle on first paint.

## Stage 0 — release blockers

Completed in the current hardening series:

- [x] Patch known reachable Go/npm vulnerabilities and verify scanners.
- [x] HMAC/redact sensitive application logs and disable nginx access logging.
- [x] Add `no-store`, tighter CSP and repository-side Cloudflare origin guard.
- [x] Minimize JWT claims; pin algorithm/issuer/audience/lifetime.
- [x] Keep browser JWT out of script-readable login JSON.
- [x] Hash verification/reset credentials with crash-safe legacy migration.
- [x] Bind GraphML edges to the owning tenant.
- [x] Cryptographically validate Tor v3 addresses and serialize blacklist
  decisions against enqueue races.
- [x] Make first-admin bootstrap atomic and fail closed.
- [x] Create threat model and incident-response runbook.

- [x] Deploy the binary and nginx config with an explicit rollback artifact.
- [x] Verify Cloudflare path, onion path, direct-origin rejection, cookies,
  headers, migrations, metrics and redacted logs externally.
- [x] Narrow the systemd bind mount to the release artifact.
- [x] Rotate the signing secret and destroy redundant `.env` copies. The three
  `.env.bak*` files held a `DATABASE_URL` and `SMTP_PASS` still valid in
  production; `JWT_SECRET` was rotated first so the copies were already dead
  credentials when they were shredded. Stale build artifacts (`api`, `api_bin`,
  `onion-spider-api.prev`, `onion-spider-api.bak-*`, `onion-spider-crawler`)
  were removed from the working tree: they were unpatched builds of this
  service sitting next to the live one.

Remaining production gate:

- [ ] Purge or tightly retain historical logs containing raw URLs/searches only
  after preserving any incident evidence that is actually required.
- [x] Verify the local Prometheus/Alertmanager pipeline and backup failure rules.
- [x] Approve the recipient and confirm receipt of one test email.
- [ ] Exercise the operator incident runbook.

### Deployed and verified on 2026-08-10

Release `ee271ed`, deployed after a fresh encrypted backup, with the previous
binary and both nginx configs preserved as a rollback artifact.

Verified against production rather than assumed:

- Clearnet 200, onion 200, direct-origin request refused before HTTP.
- Onion login: twelve consecutive failures against twelve distinct addresses all
  returned 401. Before this release the sixth would have returned 429, because
  every Tor visitor shared one per-address bucket.
- Clearnet login: five attempts, then 429 — the per-address limit is unchanged.
- The per-account lockout still fires independently: five failures against the
  *same* address return 429 on either path. That control, not the address, is
  what bounds an attack on a specific account.
- Probe addresses appear in the journal only as `ref:` HMAC pseudonyms.

Operator action, not reachable from this repository:

- [ ] Disable Cloudflare Browser Insights/analytics and NEL reporting for this
  hostname. The edge currently injects `NEL` and `Report-To`, which instruct the
  browser to send network-error reports to a third-party collector for seven
  days — telemetry this product otherwise refuses to ship. Neither nginx nor the
  application can strip a header the edge adds after the origin responds, so
  this is a zone-level change: Cloudflare dashboard → Analytics → Web Analytics
  (disable), and Network Error Logging under the zone's network settings. Verify
  afterwards with `curl -sI https://go.micutu.com/ | grep -iE 'nel|report-to'`
  returning nothing.
- [ ] Consider rotating `SMTP_PASS` on the mail server: it was present in
  plaintext in the destroyed backups and is unchanged in production.

## Stage 1 — privacy lifecycle

1. **Account privacy center**
   - [x] show stored identity, sessions, retention settings and recent security
     events without raw IP addresses;
   - [x] download a machine-readable personal-data export;
   - [x] delete account with re-authentication and grace period;
   - [x] separately delete crawl history, stored page content or sign-in history.

2. **Retention engine**
   - [x] per-account retention window for nodes and edges;
   - [x] scheduled deletion with bounded batches, metrics and dry-run mode;
   - [ ] short default retention for raw content and extracted identifiers;
   - [ ] legal-hold mechanism that is explicit, audited and never silently
     enabled;
   - [ ] backup-retention documentation so deletion timelines include
     recoverable copies.

3. **Data minimization modes**
   - [x] metadata-only crawl mode that never stores page text;
   - opt-in entity extraction instead of always extracting email/crypto data;
   - optional query-string stripping per crawl/watchlist;
   - content hashing and change detection without retaining old bodies;
   - POST-based search/detail lookup so sensitive values do not enter browser,
     proxy and referrer URL surfaces.

4. **Encryption and key architecture**
   - envelope-encrypt raw content, metadata and URLs with per-tenant data keys;
   - keep key-encryption keys outside PostgreSQL and support rotation;
   - encrypt offsite backups with a key not stored on the VPS;
   - document that server-side search/crawling still requires controlled
     plaintext access at processing time;
   - explore a local-agent mode for users who require the server never to learn
     target URLs.

Acceptance: deletion/export/retention integration tests, documented key-loss
and restore behavior, no raw sensitive values in telemetry, and a published
privacy notice/data inventory.

### Delivered — privacy centre, retention engine, metadata-only mode

Migration `000007_privacy_lifecycle` adds a per-account policy: `retention_days`
(0 = keep indefinitely, which is what every existing account already had, so the
default preserves current behaviour), `store_content`, and the pair
`deletion_requested_at` / `deletion_scheduled_for`.

Decisions worth recording, because each one was a fork where the obvious choice
was the worse one:

- **Deletion is scheduled, never immediate.** A session cookie is a bearer
  credential; "delete everything" is exactly what an attacker holding one would
  reach for. The grace period (`ACCOUNT_DELETION_GRACE_DAYS`, default 7) plus a
  mail to the registered address — which such an attacker does not control — is
  what makes the account recoverable. `api.New` clamps a zero grace up to the
  default rather than treating "unset" as "delete now".
- **Re-authentication on every destructive endpoint**, password plus second
  factor when enrolled, rate-limited to ten attempts an hour per account. These
  endpoints report whether a password was correct, which makes them a guessing
  oracle for anyone already holding a session; the limiter is the equivalent of
  the login path's account lockout.
- **Cancelling a deletion is deliberately not re-authenticated.** It only ever
  preserves data, and a password prompt in front of the undo button is what
  turns a misclick into a permanent loss.
- **Clearing the sign-in history holds back the last two hours.** `auth_audit`
  is not only a record — it is the live state behind the account lockout and the
  per-recipient caps on verification and reset mail. Deleting it wholesale on
  demand would turn "clear my history" into a reset button for both.
- **Metadata-only mode still stores the content digest.** A digest is not a
  copy: keeping it means change detection and recrawl scheduling keep working
  without this service holding what the page said. `PurgeStoredContent` clears
  the digest alongside the text, because a digest of a copy we no longer hold
  would make the next crawl conclude nothing had changed and never write content
  again.
- **The personal-data export is scoped to the calling account even for
  administrators**, and never selects credential material — password digest,
  TOTP seed, recovery-code digests, verification and reset tokens. Its fallible
  lookups run before the first byte is written, so ordinary failures return an
  honest status rather than a truncated document claiming to be a 200.
- **Retention ships with `RETENTION_DRY_RUN`**, reporting matches through
  `onionspider_retention_pending` without deleting. An automatic destructive job
  meeting a database that already holds data should be watched before it is
  armed. In-flight ('crawling') rows are excluded so a worker never writes
  results to a row that was deleted underneath it.

Fixed in passing: the test fixtures in `internal/api` and `internal/database`
migrated the shared test database *before* taking the cross-package advisory
lock, so the migration test's `Down()` could drop the schema between another
package's migration and its first query. It failed as `relation "nodes" does not
exist` in roughly one full-suite run in two. Both fixtures now lock first, and
the migration test restores the schema before releasing the lock.

## Stage 2 — account and authorization security

- [x] TOTP with single-use recovery codes; enforced for administrative
  endpoints. Passkeys/WebAuthn remain the preferred long-term factor.
- [x] Server-tracked sessions: session list, last-used time, device label,
  one-session revoke and revoke-all.
- [x] Argon2id password hashing with transparent bcrypt upgrade on successful
  login, and passphrases accepted up to 256 characters.
- [ ] Screen against breached passwords without sending the full password to a
  third party.
- [ ] Short access credential plus rotated refresh credential, with replay
  detection and key rotation (`kid`) support.
- Offline, one-use admin bootstrap command; remove public registration’s ability
  to assign an admin role.
- PostgreSQL row-level security and separate least-privilege DB roles for API,
  crawler, migrations and backup.
- Fine-grained permissions for read, crawl, export and administration; immutable
  audit events for privilege and session changes.
- Safer anti-abuse: progressive backoff and proof-of-work/CAPTCHA alternative
  that does not embed a privacy-hostile third party, especially for onion users
  who share a loopback source address.

## Stage 3 — production and supply chain

- Dedicated Unix user and dedicated Tor instance for Onion Spider; no shared
  home/project tree in the service namespace.
- Root-owned, versioned release directory and atomic symlink switch for deploy
  and rollback; reproducible build metadata and checksums.
- Pin GitHub Actions and container bases by digest; minimize workflow token
  permissions; protect `main` and production environments.
- Generate CycloneDX/SPDX SBOMs, scan source/secrets/filesystems/images, sign
  artifacts and verify signatures at deploy.
- Fix and test the Docker topology so the API binds to the container interface
  without weakening the host-systemd loopback bind.
- Automated encrypted offsite backup, object-lock/immutability where practical,
  monthly scratch restore and recorded RPO/RTO.
- PostgreSQL TLS or Unix socket locally, least privileges, slow-query/connection
  anomaly alerts and storage/disk capacity plans.
- Prometheus/Alertmanager on loopback with auth or firewall defense in depth;
  security alerts for admin login, lockout bursts, bulk export, origin rejection,
  migration failure and backup failure.
- Fuzzing for URL, JWT, robots, sitemap, HTML extraction and every export
  encoder; load tests for queue, search, export and decompression/parser limits.
- CAA/DNSSEC/account MFA review, Cloudflare authenticated origin pulls/mTLS and
  documented emergency DNS/origin procedure.

### Delivered — annotations and change watching

Migration `000008_annotations_and_watches` adds `node_tags`, `node_notes`,
`watches` and `watch_events`. Everything is keyed by `(user_id, node_id)`, never
by node alone: two accounts can hold the same .onion in their own graphs, and
what one wrote about it is none of the other's business.

- **Every endpoint takes the address in a POST body, including the reads.** A
  .onion address in a request line reaches browser history, the `Referer` header
  on the next navigation and every proxy log in between. For a service whose
  purpose is that nobody learns which hidden services an account follows, that
  is the wrong shape however convenient GET would be. This matches the Stage 1
  note about POST-based lookups.
- **"Not yours" and "does not exist" answer identically (404).** Distinguishing
  them turns the annotation surface into an oracle for what somebody else is
  tracking.
- **The watch keeps its own digest watermark**, separate from
  `nodes.content_hash`. The crawler advances the node's digest as part of storing
  the crawl, so a watch reading that column would find everything unchanged; and
  a failure between storing the crawl and recording the event would lose the
  notification permanently. With its own watermark, a lost write simply means the
  next crawl notices the same difference again.
- **One observation can produce more than one event.** A page rewritten while the
  site was down comes back as both *recovered* and *content changed*. An earlier
  version reported only the recovery and then advanced the digest, which
  swallowed the change permanently — caught by
  `TestChangeDuringAnOutageIsReportedOnRecovery` before it shipped.
- **`last_reachable` is its own column rather than a sentinel inside
  `last_status`.** A network failure has no HTTP status at all, so folding the
  two forces "no status" to mean either "never observed" or "was fine" — and both
  readings misfire: a week-long outage reports going down on every pass, or never
  reports coming back.
- **Reachability events fire on transitions only**, and a 5xx counts as
  unreachable: for the person watching, a 503 and a dead circuit mean the same
  thing.
- **The retention sweeper skips annotated and watched sites.** A retention window
  is a statement about crawl records the account stopped looking at; a tag, a
  note or a watch is that account saying this site matters. Reaping those on a
  timer would destroy the user's own writing — which crawling again cannot
  regenerate — as a side effect of a setting about crawl data.
- **Nothing is emailed.** A message saying "the site you are watching changed"
  tells the mail provider, and every hop, exactly what this account follows. The
  feed is read after signing in.

Annotations and watches are included in the personal-data export and removed by
account deletion, so the V2 guarantees cover them.

## Stage 4 — useful privacy-respecting features

Highest-value product capabilities:

- [x] Watchlists with configurable recrawl interval and private change alerts
  (in-app feed, never emailed).
- [x] Tags and private notes, per-item deletion, exempt from retention.
- Collections built on tags (saved filters over the tag set).
- Content diff view with explicit retention controls and no raw body history by
  default.
- Advanced search filters: status, category, date, host, title-only and saved
  local presets; cursor pagination and cancelable/debounced requests.
- Graph filters, clustering, depth controls, time slices and lazy loading so the
  browser never downloads the whole graph by default.
- Crawl job page with per-user quota, priority, pause/cancel, error reason and
  estimated queue position.
- Bulk import preview/deduplication and scoped export with expiry, row estimate
  and explicit confirmation for sensitive content.
- NDJSON/CSV/XLSX/GraphML export jobs generated asynchronously, encrypted for
  large downloads and deleted automatically.
- API keys with narrow scopes, expiry, last-used time and one-time display;
  never reuse browser sessions for automation.
- Webhooks signed with rotating secrets, allowlisted destinations and strict
  egress protection; disabled by default.
- Optional local-only notifications and self-hosted SMTP; no push vendor by
  default.
- Transparent crawl provenance: first/last seen, content hash, response status,
  redirect chain and policy decision without exposing other tenants.
- Abuse controls, safe-content reporting and operator quarantine that preserve
  evidence without exposing it in the normal UI.

Do not add engagement analytics, ad SDKs, social trackers, remote fonts, remote
CAPTCHAs or a service worker that caches authenticated API responses.

## Stage 5 — responsive UI, accessibility and performance

Delivered on 2026-08-10 (release `f1b5af6`):

- [x] Split the public/auth shell from the authenticated dashboard; every route
  is lazily imported and the graph code loads only when the map is opened.
- [x] Replace the single large component with routed views and a design system:
  tokens for colour/spacing/type/touch-target, reusable field, button, card,
  chip and pill primitives, and consistent empty/error/loading states.
- [x] Mobile first at 320 px: navigation drawer, stacked controls, 44 px touch
  targets, cards instead of the dense table below 900 px, no page-level
  horizontal overflow.
- [x] Accessible forms with real labels and autocomplete, `role="alert"` on
  feedback, a polite live region for crawler status, skip link, landmarks,
  reduced-motion support and a stated text equivalent for the graph canvas.
- [x] Cancel stale searches, debounce input, and keep search terms in POST
  bodies rather than URLs.

Measured against the acceptance budget: the login route now transfers **41.9 kB
gzip** (verified against production), against roughly 187 kB before. The graph
chunk is 151.7 kB gzip and is fetched only on demand; the entry chunk contains
no `vis-network` symbols at all.

Still open in this stage:

- [ ] Virtualize long lists and move to cursor pagination.
- [ ] Automated accessibility suite and cross-browser tests in CI.

## Stage 5 detail — original plan

- Split the public landing/docs shell from the authenticated dashboard. Load
  graphing and export code only after login and only on the relevant route.
- Replace the single large component with routed views and a small design
  system: tokens for color/spacing/type, reusable form/control/table/card
  components and consistent empty/error/loading states.
- Mobile first at 320 px: navigation drawer, stacked controls, touch targets at
  least 44 px, cards for dense tables, sticky action bar and no page-level
  horizontal overflow.
- Accessible forms with real labels, autocomplete, error association and focus
  movement; keyboard-operable graph/list alternative; skip link, landmarks,
  reduced motion and WCAG AA contrast.
- Cancel stale fetches, debounce search, virtualize long lists, use cursor
  pagination and enforce performance budgets for JavaScript/CSS/first load over
  Tor.
- Add skeleton/empty/offline/error states without storing private API responses
  in persistent browser caches.
- Test desktop/mobile Chromium and Firefox/Tor Browser plus keyboard and an
  automated accessibility suite in CI.

Acceptance budgets: no initial graph dependency on the public/login route,
mobile layout at 320/375/768 px, WCAG 2.2 AA target, and a compressed initial JS
budget substantially below the current approximately 187 kB gzip bundle.

## Stage 6 — SEO without indexing private data

SEO applies only to public, non-sensitive pages. The dashboard, auth flows,
search results, node details, exports and onion endpoint stay `noindex` and
must never appear in a sitemap.

- Public landing page with a precise title/description, canonical URL, product
  explanation, privacy/security model and link to the onion-access guide.
- Public `/privacy`, `/security`, `/docs` and `/about` pages rendered as static
  HTML or SSR so crawlers do not need the dashboard JavaScript.
- `robots.txt` and `sitemap.xml` listing only public pages; per-private-route
  `X-Robots-Tag: noindex, nofollow, noarchive` at nginx as defense in depth.
- Open Graph/Twitter metadata using a locally hosted image; Organization and
  SoftwareApplication JSON-LD only where claims are accurate.
- Stable semantic headings, internal links, language/locale metadata, custom
  404, canonical redirect policy and measured Core Web Vitals.
- No public node directory, URL snippets or aggregate pages that can reveal
  what users crawl. Search visibility must never outrank privacy.

## Release cadence

Each stage is a separate reviewed commit or small commit series with tests,
migration/rollback notes and production verification evidence. A feature does
not ship merely because it works locally: its data collection, retention,
abuse, accessibility, monitoring and incident implications must be answered in
the same change.
