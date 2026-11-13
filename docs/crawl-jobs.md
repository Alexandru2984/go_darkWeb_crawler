# Personal crawl queue and safe imports

Schema **12**, personal export format **5**. Open **Queue** to manage your own
account's work; administrative global-data access does not extend these reads
or controls to other accounts.

## Controls and limits

The overview shows pending counts, due work and the configured submission limit
(default 500, zero disables it). Pending records from all sources count against
new manual submissions, imports, retries and schedules. Automatic link discovery
and retry/revival can exceed that submission limit; it is not a total storage
quota or a throughput guarantee. Worker availability and domain politeness still
control actual start time.

Pause is per account and persistent. It prevents new claims, including automatic
and watched-site recrawls. A request already claimed may finish, and new
submissions remain queued while paused. Resume preserves queued work and its
dates. Claims share-lock the owner preference before locking a node, so a
committed pause cannot be crossed and privacy updates keep the same lock order.

Use the status filter and pagination to inspect queued, running, failed,
completed, blocked and cancelled records. Each row includes due/start time,
failed attempts, depth and any available generic failure code. Older attempts
may lack a diagnostic. Raw error strings are not stored. Successful crawls reset
failure counts; robots/invalid-address refusals are excluded from automatic
failed-node revival.

- **Run now** resets a nonrunning, unblocked record to pending and clears its
  retry counter. Requeueing an existing pending record needs no extra quota slot.
- **Schedule** accepts a local date and time within the next 30 days. The browser
  sends an explicit timezone; API output uses UTC. A due time makes a job
  eligible, rather than promising an exact start. The database session timezone
  is handled explicitly so a different server timezone cannot shift the instant.
- **Cancel future crawl** requires an inline confirmation. It preserves stored
  content, notes, tags, bookmarks and watches. Cancelled records are not claimed,
  automatically revived or resurrected by late save/failure callbacks. Retry or
  schedule a cancelled record to resume its checks.

Cancellation/requeue serialize with worker claims on the node. Running and
blocked jobs return conflict instead of having their state overwritten. Manual
retries enforce the same submission quota and current blacklist as enqueues.
Cancellation changes scheduling, not the existence of a watch; stopping a watch
and deleting its history remain separate actions on Changes.

## Batch import

Paste up to 20 URLs, one per nonempty line, and choose **Preview import**. The
read-only snapshot reports invalid v3 addresses, duplicates after canonicalizing
fragments, existing personal records, blocked domains and submission capacity.
Preview writes nothing and starts no requests.

**Queue accepted URLs** submits only the entries the preview marked ready.
The server rechecks policy, duplicates and capacity at acceptance, so a newly
unblocked URL excluded from the preview cannot be added without a new review.
Capacity may change between preview and acceptance; per-entry results show what
was actually queued. Existing records require an explicit retry/reschedule and
are never silently reactivated by import.

Acceptance locks domain advisory keys in numeric order (including deduplication
of hash collisions), then the account quota, and commits as one transaction.
Unexpected database failure rolls back the entire batch. Quota exhaustion is an
expected per-entry result: the eligible subset that fits is committed and the
response returns 429 with accurate `added`, `skipped`, `items` and a legacy
`error` message. Rate limiting charges one unit per submitted URL, once.

## Privacy and API

Sensitive URLs stay in JSON bodies, never route/query strings or browser storage.
Cookie writes require CSRF proof; all reads and writes require authentication,
return no-store responses and ignore administrator privilege for ownership.
Control writes are limited to 60/minute/account; jobs and preview share the
60/minute search budget. Existing crawl submission rate limiting remains 20
URLs/minute for ordinary accounts.

| Method | Endpoint | Body/purpose |
| --- | --- | --- |
| GET | `/api/queue` | Own summary, pause state, due count and submission limit |
| POST | `/api/queue/jobs` | `{status}`, pagination-only `page`/`limit` in URL |
| POST | `/api/queue/pause` | Required boolean `{paused}` |
| POST | `/api/queue/action` | `{url,action}` for `cancel`/`retry`, or `schedule` with RFC3339 `scheduled_at` |
| POST | `/api/crawl/bulk/preview` | `{urls:[...]}`, read-only acceptance preview |
| POST | `/api/crawl/bulk` | Revalidated transactional acceptance |

Strict JSON rejects unknown fields and trailing documents. URL/action bodies
allow 16 KiB and batches 256 KiB, sufficient for escaped Unicode while retaining
independent 2,048-byte URL and 20-entry limits. Empty job results are arrays.
Status filters and saved snapshots include `cancelled`.

Personal export format 5 streams the complete account-scoped `crawl_jobs`
metadata and includes `crawl_preferences.paused`. Erasing crawl history or the
account cascades the job metadata with its records; the pause preference remains
until explicitly changed or the account is erased. No independent job-history
store, third-party service or notification email is added.

## Operations and recovery

Two private aggregate metrics distinguish immediately available work and
unpaused non-policy failures from stored backlog:
`onionspider_crawl_ready_nodes` and `onionspider_crawl_failed_nodes`.
Inflight requests count as work even if their owner pauses. Future/paused work
and policy refusals do not cause false stalled-crawler alerts. Existing alert
rules fall back to legacy queue metrics if the new metrics are absent. No
account identifiers, URLs or new metric labels are exposed.

Build the API statically with `make build`, stage frontend assets outside the
live dist and publish assets before the entry HTML. Require a fresh encrypted
Google Drive snapshot and its paired TOTP keyring. Migration 12 Down erases pause
intent and cancellation status and must never be used for automatic recovery.
The untouched schema-11 binary cannot start against schema 12.

The compatible fallback is based on `07d260e`, with the current database source
and migrations plus API `privacy_handlers.go`, `crawl_handlers.go`,
`queue_handlers.go`, `deps.go` and `nodes_handlers.go`. Its claim method is
explicitly replaced with a no-work result: recovery temporarily stops all crawl
execution while preserving schedules, cancellation and account pause intent.
The fallback UI displays Recovery mode and keeps export format 5 available.
Forward recovery restores execution under the stored account preferences.
Complete mixed source, static binaries, frontend builder and prior monitoring
rules are archived with the release. Verify a schema-12 Google Drive restore
after publishing. Treat prolonged recovery mode as an operational incident.
