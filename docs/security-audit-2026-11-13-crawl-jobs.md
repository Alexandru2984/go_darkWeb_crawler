# Crawl control and import security review

Date: 2026-11-13. Scope: per-account pause, cancellation and scheduling; retry and
import quotas; private job metadata/export; truthful scheduling alerts; schema-12
release/recovery. Existing nginx/Cloudflare origin restrictions, authentication,
MFA policy and rclone Google Drive provider remain in use. No new vendor or
notification destination is introduced.

## Findings and mitigations

1. Manual recrawl checked state and updated it in separate statements, allowing
   it to overwrite a claim made in between. It also bypassed the submission
   quota. Retry/schedule now lock domain policy, account quota and the owned row
   in a consistent order. A running or blocked node conflicts; other transitions
   verify quota/blacklist and commit atomically. Existing pending rows are
   rescheduled without consuming another slot.
2. A full 20-entry batch was charged once for the request and again for its URLs,
   making it cost 21 units against a 20-unit ordinary-account budget. Charging
   now happens once per submitted URL. Duplicate ON CONFLICT no-ops were counted
   as added; actual inserts now determine the count and existing submissions
   remain idempotent at capacity.
3. Batch acceptance previously performed a sequence of separate transactions.
   New acceptance shares one transaction, policy locks and the quota decision.
   Numeric advisory keys are sorted/deduplicated so hash collisions cannot invert
   lock order. Unexpected failure rolls the entire batch back. Quota limits are
   explicit per-entry outcomes with a partial eligible subset and legacy error
   compatibility. Read-only previews write nothing, and the UI accepts only the
   subset it actually displayed as ready; changes in capacity/policy are checked
   again on acceptance.
4. Pause is persistent and claims lock the owner preference before the node.
   This serializes a committed pause with new claims without holding a long
   network transaction. Cancel and claim serialize on the node, while running
   requests are explicitly allowed to finish. Cancelled records are excluded
   from claims/revival and protected from late save/failure callbacks. A new
   blacklist cannot erase their cancellation intent, and they cannot use that
   state to bypass policy on retry.
5. Successful writes now replenish retry counts, including unchanged-content
   writes. Failure reasons are a fixed vocabulary rather than raw errors;
   robots/invalid-address refusals are not revived automatically. Historical
   attempts can lack a reason. Personal export format 5 streams all job metadata
   and includes the pause preference. History/account erasure removes metadata
   with the existing records; a pause preference remains until changed/deleted.
6. Queue summaries and job controls always use the caller's own account, even
   for an administrator. All sensitive URLs stay in JSON bodies. Authentication,
   cookie CSRF, strict JSON/body/field bounds, independent write limits and
   parameterized SQL protect the new endpoints. No new account/URL labels or raw
   research logs are introduced.
7. Scheduled/paused records would otherwise cause false stalled-crawler alerts.
   Two private aggregate gauges now distinguish currently actionable work and
   unpaused, non-policy failures. Rules use them when present and retain a legacy
   fallback. Active requests still count after pause; due automatic recrawls
   count even without pending entries. All zero statuses include cancellation,
   so erased cancelled work cannot leave a stale queue gauge.

## Threat model, detection and response

Top threats are cross-account state tampering (high impact), quota/deadlock
resource exhaustion (availability impact), and disclosure of research addresses
through logs or client persistence (high confidentiality impact). Owner-scoped
queries, serial state changes, bounded batches/rates and private request bodies
address those paths. Full compromise of the existing API/database role can
still expose the hosted research data within that role's access; this feature
is not an encryption boundary against a compromised server. Financial loss and
incident detection times have not been measured and are not invented here.

Existing private readiness, queue-refresh, crawl-progress and backup rules
provide operational detection. The progress rule retains its one-hour period
and exhausted-queue rule its two-hour period; these do not measure or guarantee
detection of every unauthorized state change. Route/status counters and
UID/action/count logs support incident investigation without URL disclosure.
The incident runbook includes a queue-abuse scenario and an isolated tabletop;
no production data was manipulated for it. Existing incident/legal assessment
procedures continue to apply, without adding a compliance-certification claim.

## Validation

- Full Go/PostgreSQL suite passed with the race detector. Tests cover fair
  multi-account claims, committing pause, cancel/claim concurrency, active-job
  conflicts, manual retry quotas, precise scheduled instants in a non-UTC DB
  session, final-slot import races, canonical deduplication, forced SQL rollback,
  blacklist/cancellation interaction, failure-code lifecycle, complete private
  export, ownership including administrators, strict bodies and URL charging.
- Go vet and golangci-lint passed. Frontend lint, 27 unit tests, production builds
  and both npm audit scopes passed. Source and static API/recovery binaries are
  scanned before publication; staged/history secret scans use the CI scope.
- Chromium exercised the actual static API and isolated PostgreSQL accounts.
  It covered pause/resume, 20/21 pagination and empty-page recovery, separate
  mobile date/time inputs, schedule/run-now/cancel, retained annotations/watch,
  generic failures, active/blocked refusal, read-only preview, accepted subset,
  changed-capacity handling, quota/rate/CSRF, administrator/owner isolation and
  export format 5. All controls were checked at 320/375/768/1280 px for clipping
  and horizontal overflow, with no JavaScript errors. A mobile capture was
  reviewed before publication.
- All 27 Prometheus scenarios passed, including intentional pause/future work,
  policy refusal and due recrawls without pending rows. There remain 17 alert
  rules. No test alert or email was sent.
- Recovery testing proved the untouched schema-11 binary cannot start on schema
  12, the compatible API preserves export/preferences/metadata while claiming
  no work even when an account is enabled, and forward recovery restores the
  controls under stored pause state. Recovery mode is visibly identified.

## Practical limits and remaining work

Pause/cancellation govern future claims, not interruption of an already running
HTTP request. Due time is eligibility, not an exact-start guarantee. The quota
is a manual-submission cap rather than total storage or automatic-discovery cap.
Preview cannot reserve capacity. Export sections are streamed sequentially,
not one frozen transactional snapshot. Broader browser/accessibility testing,
independent external detection, host recovery/key custody, monitoring-runtime
patch tracking and CI/SEO expansion remain in the production roadmap.

## Production evidence

- Feature release `20261113T183430Z-jobs`: backend `4b345e4`, frontend
  `83fabe1`, schema **12**. Executable checksum (SHA-256):
  `64226f9def3fb3afaa436ade4e631643eea6d715088ca079806b8f88d626bb85`.
  Previous/compatible APIs, frontends, complete compatibility source, frontend
  builder recipe and prior/new monitoring rules are stored with the release.
- Dashboard acceptance guidance is published from `c642d84` in frontend-only
  release `20261113T183950Z-crawl-submit-feedback`. Current entry HTML SHA-256:
  `433af4270701de3faab92f41db2fb64dc2d95eb01400101cd1f92697489526dd`.
  It uses the API message for existing URLs instead of claiming a new queued job;
  the prior entry and its assets remain available for rollback.
- Fresh encrypted pre-release Drive backup completed at
  `2026-11-13T18:30:57.186483+00:00`; the post-feature snapshot completed at
  `2026-11-13T18:37:54.325823+00:00`. Isolated Drive restore completed at
  `2026-11-13T18:43:02.677622+00:00`: schema **12**, dirty **false**,
  unvalidated constraints **0**, TOTP keyring verification **true**.
- Production API readiness and nginx checks passed. Public Chromium verified
  the current entry, authentication redirect, protected queue endpoints (401),
  closed registration, no exposed private scheduling metrics and no mobile
  login overflow/JavaScript errors. Private monitoring reported **4/4** healthy
  targets, **17** healthy rules with the new scheduling expressions, and **0**
  pending/firing alerts. Runtime rules match the committed rule file.
- Isolated test accounts/database and temporary keyring directories were
  removed. No real account's credentials or scheduling preferences were changed
  by the validation exercise, and no test alert/email was sent. Existing
  operator Alertmanager edits remain outside these commits. No push was made.
