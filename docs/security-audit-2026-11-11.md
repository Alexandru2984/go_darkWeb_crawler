# Security audit and release review

Reviewed on 2026-11-11. The application uses Go, PostgreSQL and Vue 3.

This review covers the repository, application authorization, crawler egress,
content storage, browser flows, container builds and the two nginx entry points.
The existing working-tree changes to Alertmanager belong to the operator.

## Findings addressed in this release

| Priority | Finding | Resolution |
| --- | --- | --- |
| High | Admin MFA enforcement covered blacklist operations while other handlers selected global data through the raw admin role | Database-loaded effective privileges now apply the MFA policy to lists, search, details, edges, statistics, queue and exports |
| High | Sessions issued before MFA enrolment could keep administrative access after enrolment | Enrolment and removal increment the credential version and revoke sessions atomically. Session validation also checks the enrolment timestamp for previously enrolled accounts |
| High | A stolen session could begin MFA enrolment without proving the password | Setup requires the current password. Setup, confirmation and removal share an account rate limit |
| High | Concurrent enrolment requests could confirm a secret that had been replaced between validation and persistence | Staging and confirmation compare credential versions. Confirmation also compares the exact secret validated by the request |
| High | Distributed requests could run unbounded concurrent memory-hard password operations | Four shared slots cap password work across login, registration, reset, MFA management and privacy re-authentication. Saturation returns 503 and Retry-After |
| High | The account-deletion sweeper used an earlier list without checking whether deletion had subsequently been cancelled | The transactional delete checks the current deadline. Audit cleanup commits with the deletion. Regression tests cover cancellation and rescheduling |
| High | Known dependency advisories affected the HTML parser and other dependencies | Updated Go, x/net, Excelize and npm dependencies. Source and release-binary vulnerability checks pass |
| Medium | Metadata-only writes could race a privacy preference change and retained entities extracted from page bodies | Crawl persistence holds the user preference through the transaction and removes extracted entities from new metadata-only records |
| Medium | Re-enabling storage did not restore an unchanged page because its digest already existed | A subsequent crawl restores the body while preserving the content-change result |
| Medium | Page-content purging left body-derived entities in metadata | Purging removes the body, digest and extracted entities |
| Medium | Personal exports could be spooled into nginx temporary files | Both export endpoints stream through nginx. Interrupted personal exports abort instead of completing a partial document |
| Medium | The root Docker ignore file did not apply to the separate Compose build contexts | Backend and frontend contexts now have explicit source allowlists |
| Medium | The Compose API listened on its container loopback, preventing frontend access | A validated BIND_ADDRESS defaults to host loopback. Compose sets a container network listener without publishing an API host port |
| Medium | Container base images included older nginx and an unsupported Alpine branch | The frontend uses a digest-pinned nginx 1.30.6 image. The Tor image uses Alpine 3.24 |
| Medium | Nginx deployment templates had drifted from the installed ACME and origin-guard configuration | The source configuration now matches the installed vhost. The release preserved the existing ACME locations and origin guard |
| Medium | CI missed an existing fixture false positive and failed current lint checks | Added one reviewed exact Gitleaks fingerprint, fixed lint findings, enforced npm audit and added release-binary scanning |
| Product correctness | Expired sessions left protected views mounted, failed re-authentication ended otherwise valid sessions, and network failures could leave loading state unresolved | The interface handles these conditions explicitly. Cancelled requests keep their cancellation semantics |
| Product correctness | Delayed annotation responses could replace the note displayed for a subsequently selected site | Dialog requests are cancelled and obsolete responses are discarded |

The parser issue is directly relevant because crawled HTML reaches
golang.org/x/net/html. The initial npm audit reported 12 affected package
entries, including development dependencies and a Vue server-rendering advisory.
That count describes dependency findings. The application uses client-side Vue
rendering. [Go HTML parser advisory](https://pkg.go.dev/vuln/GO-2026-6860),
[Vue advisory](https://github.com/advisories/GHSA-g2v6-rqmx-r4w6).

The x/crypto revision is pinned to
v0.58.1-0.20261009181718-2f1f834330b2. Comparison with v0.58.0 showed removal
of the deprecated OpenPGP directory. The application imports Argon2id and bcrypt.
Both source and binary scanners now report no vulnerabilities.
[OpenPGP advisory](https://pkg.go.dev/vuln/GO-2026-5932).

## Remaining operational work

| Priority | Observation | Acceptance evidence |
| --- | --- | --- |
| High | No Onion Spider scrape target was present in either inspected Prometheus instance. The systemd monitoring services described by the existing runbook were inactive | A healthy scrape target, loaded project rules and verified alert routing to the operator |
| Medium | Google Drive publication and isolated database restore are verified. Independent recovery of the GPG passphrase and rclone crypt keys after total VPS loss remains an operator responsibility | Verify the external secret store, Google account recovery and a full host-recovery exercise |
| High | TOTP seeds and live application data remain readable to a database administrator | Encryption of TOTP seeds using a separate managed key, rotation coverage and a documented recovery process |
| Medium | Cloudflare processes clearnet TLS and its account-side privacy settings were not audited through an authenticated connector | Operator review of edge logging, browser reporting, analytics and retention settings |
| Medium | Distribution coverage for upstream nginx advisories was not verified | Match installed Ubuntu package patches to applicable advisories before treating upstream version comparisons as a vulnerability finding |
| Medium | A private plaintext environment backup remains on disk with restricted permissions | Review its necessity, keep recovery material encrypted and rotate credentials when removing a potentially exposed copy |
| Medium | CI actions use version tags and GitHub-hosted execution was not triggered during this local review | Pin action revisions, push the reviewed commits and retain the resulting green runs |
| Product | Production registration is closed | Decide the invitation or registration workflow before promoting the service to new account holders |
| Product | Browser verification covered Chromium. Firefox, Safari, screen-reader use and automated accessibility checks remain open | Cross-browser and accessibility results against the public page and authenticated routes |

## Threat model

The highest-impact boundaries are account authentication, global administrative
reads and storage of hostile page content.

- Spoofing and elevation of privilege can turn a stolen session into access to
  the account's records or administrative data. Current mitigations include
  live authorization, MFA, credential-version checks and device revocation.
- Information disclosure can expose account identities, crawl history, notes,
  stored pages and TOTP seeds through a database or VPS compromise.
- Denial of service can target the HTML parser, password hashing, exports and
  the shared crawler queue. Parser patches, bounded password work, export
  concurrency, request limits and account queue quotas address these paths.

A full VPS compromise can affect all registered accounts and local backups.
Account counts and monetary loss were not estimated in this review.

## Detection and incident response

The repository contains an incident-response runbook and aggregate application
metrics. Operational alert delivery currently needs verification against the
actual monitoring stack. A measured detection time cannot be asserted.

The automated session-abuse exercise used disposable accounts. It enrolled MFA,
rejected an older copied session, authenticated with a one-use recovery code and
checked redirect handling after expiry. A timed operator exercise covering
credential rotation, containment and restoration remains outstanding.

Legal applicability, vendor contracts and notification obligations require the
operator's assessment. This technical review does not establish a compliance
certification. The relevant vendor boundaries include Cloudflare, SMTP, the VPS
provider, Google Drive, software registries, GitHub and the Tor network. No new external
telemetry service was introduced.

## Verification

- All backend packages passed with the race detector and a disposable PostgreSQL
  database. Database tests ran with TEST_DATABASE_URL set.
- Go vet and golangci-lint passed.
- Source and compiled release-binary govulncheck scans reported no vulnerabilities.
- Frontend lint passed with zero warnings. All 26 unit tests passed.
- npm audit reported zero vulnerabilities.
- Backend, frontend and Tor container builds succeeded.
- An isolated Docker frontend reached the API over its private network. The API
  had no published host port.
- Chromium exercised cookie login, filtered pagination, safe page-text rendering,
  the details dialog, queue state, MFA enrolment, copied-session rejection,
  recovery-code login, network failure recovery and session-expiry redirects.
- A delayed annotation response was unable to replace the next site's note.
- Public demo search, category filtering, watch toggling and reset worked.
- The public page and queue had no document overflow at 320, 375, 768 and 1280 px.
- Gitleaks history scanning passed after reviewing the existing public test
  password and recording its exact commit/file/rule/line fingerprint.

These checks cover the reviewed code paths and the recorded environment.
Production publication results and rollback artifacts are recorded below.

## Production publication

Release 24d4557d9a131f456e80d3e2f8a24e4a235503ca was published on
2026-11-11 at 02:20:48 UTC.

The encrypted backup service completed with Result=success immediately before
publication. The previous API binary, full frontend and both nginx vhosts are
preserved in the restricted directory
`/home/micu/.local/state/onion-spider/releases/20261111T022048Z`.
Its manifest records the source revision and old/new binary digests.

The installed API binary matches the tested artifact by SHA-256. Nginx
configuration validation and API readiness passed. The service was active with
zero automatic restarts after publication.

Verified through the public hostname using curl and Chromium

- Status and public registration configuration returned 200.
- The product page returned 200 and its live demo category filter worked.
- The sign-in page showed that registration is closed.
- Both unauthenticated export endpoints returned 401.
- The private application shell retained its noindex metadata.
- The sitemap contained only the public product page.
- API responses retained no-store and the expected security headers.
- A direct-origin request was closed without an HTTP response.
- Requests to the onion service over Tor returned 200 for status and the product
  page.

Authenticated account flows were exercised against the isolated fixture
environment. Production user credentials were not used.

The application release is deployed. Operational readiness remains dependent on
alerting, external recovery-key custody and full host-recovery work listed above. GitHub-hosted CI has not been triggered
by a push during this session.


## Google Drive backup and recovery stage

The installed backup job publishes archives to the existing
`gdrive-vps-crypt:onion-spider` destination. GPG encryption is applied before the
rclone crypt transport. Verification downloads and compares the encrypted
archives before updating the recovery pointer or rotating the remote set.

The daily run completed automatically on 2026-11-11 at 03:15:38 UTC. It kept
seven local archives and eight active offsite archives. Offsite retention is
configured for thirty active archives. The existing Drive trash policy remains
part of the account-side retention boundary. Client-side immutable upload guards
against accidental overwrite. Server-enforced object locking was not configured.

The recovery record identifies a verified filename, byte length and SHA-256
digest. A monthly timer restores that archive inside a container with no
external network or host mounts. Database files are held in a bounded tmpfs.
Interrupted drills have a labelled-container cleanup path protected by a lock.

The first end-to-end remote restore completed on 2026-11-11 at 02:55:44 UTC in
247.43 seconds. A second drill restored the latest scheduled archive,
`onion_spider-2026-11-11_03-14-48.sql.gz.gpg`, at 14:39:57 UTC in 362.5 seconds.
Both restored schemas were version 8 with dirty=false and no unvalidated
constraints. These durations cover the database drills.

The latest archive's downloaded SHA-256 matched the verified recovery record.
The final success record was written after container cleanup completed. No
restore container or pending cleanup marker remained. Both timers were enabled,
the installed units matched their repository versions and the application
readiness check remained successful.

Ten local regression tests cover retention, retry, read-back failure, invalid
destinations, archive tampering, cleanup ownership and failure, atomic success
records and keeping connection credentials out of process arguments. Shellcheck
and systemd unit validation passed. The same regression suite is included in CI.

The backup connection parser now reads the environment file directly. It keeps
the PostgreSQL URI out of Python's arguments, preserves configured TLS options
and removes the database password from the environment before offsite work.

The backup provider and crypt remote reuse the operator's existing configuration.
External key custody and full host recovery require the operator's verification.
