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
| Medium | Four dedicated scrape targets, 17 rules and operator email delivery are verified. Monitoring still shares the application's VPS | Add an independent external heartbeat and off-host notification path for complete VPS loss |
| Medium | Google Drive publication and isolated database restore are verified. Independent recovery of the GPG passphrase and rclone crypt keys after total VPS loss remains an operator responsibility | Verify the external secret store, Google account recovery and a full host-recovery exercise |
| High | TOTP seeds now use an independent keyring and account-bound authenticated encryption. Full API/VPS compromise can still expose keys and live data; a database writer can alter authorization | Retain protected key custody, review runtime isolation and exercise the documented recovery/rotation process; TOTP encryption does not protect all application content |
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
metrics. The dedicated monitoring stack is deployed and the local notification
pipeline was measured at 2.05 seconds with an immediate synthetic rule. The
operator confirmed receipt of one authorized test email. Operator incident
response time remains unverified. Production rules have
longer configured holds; the synthetic result is not their detection latency.

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

## Account security release and recovery review

Release `79f4622` was published on 2026-11-11 at 21:10:58 UTC. The installed API
SHA-256 is `0c2c419bbad9b80794ce78f64377944338ce3463c51503f50e9b967e7acbcaae`.
The previous frontend, previous binary, tested crypto-compatible binary and
manifest are retained in the root-restricted directory
`/home/micu/.local/state/onion-spider/releases/20261111T211058Z-accounts`.
The compatible mode was checked before normal startup. The published service
uses normal encryption mode, a root-only keyring delivered by LoadCredential,
schema version 8 and zero automatic restarts. Public status and security pages
returned 200. Unauthenticated password changes returned 401 through both vhosts.

Password changes require the current password and MFA when enrolled, invalidate
reset links and revoke every device atomically. The responsive form confirms
the password, supports viewing the new value and returns to sign-in with a
confirmation message. A browser-discovered redirect race was fixed before
publication.

Pending/active TOTP seeds now use AES-256-GCM envelopes bound to account ID and
key identifier. Legacy migration and key rotation preserve enrolment, session,
recovery-code and replay state. Every new write is encrypted, including in the
explicit compatibility mode retained for rollback. Missing keys or corrupt
records fail closed. No production TOTP seed records existed at the rollout
precheck; migration and rotation of existing enrolled records were exercised in
isolated fixtures. Accounts are not automatically enrolled in MFA.

The existing Google Drive backup includes a separately GPG-encrypted keyring
companion. Publication verifies both ciphertexts and retention preserves their
pairing. The first paired backup and restore passed before publication. A fresh
post-publication backup completed at 21:12:17 UTC. Its isolated restore completed
at 21:18:17 UTC in 281.14 seconds, with schema version 8, no dirty migration or
unvalidated constraints, and the downloaded TOTP keyring successfully verified.
The restore uses the installed API binary's offline verifier without accessing
the host's current keyring; the snapshot contained zero TOTP seed records.

The first restore attempt failed during streaming and preserved the preceding
success record. Review identified a startup race: the Docker image's temporary
PostgreSQL initialization server accepted Unix-socket connections before the
final restart. Readiness now waits on container-local TCP. A repeat using the
final readiness check completed successfully. Stream diagnostics report exit
codes and SQLSTATE without exposing SQL or credential material.

Verification included all backend packages with the race detector and an
isolated PostgreSQL database, vet, golangci-lint, clean source/binary govulncheck,
26 frontend unit tests, strict lint, a production build and a clean npm audit.
The 25 Python regressions cover backup, key management and monitoring. A nonroot
read-only API container loaded a private group-readable keyring and reached
readiness. Chromium checked widths 320/375/768/1280, password changes with and
without MFA, copied-session rejection, enrolment, key rotation, TOTP login and
unchanged recovery-code counts after rotation.

Review boundary: this mitigates disclosure of TOTP seeds through database-only
access. Full API/VPS compromise can expose runtime keys; database write access
can alter authorization. The same GPG passphrase protects the database and
keyring backups, so offsite-store plus passphrase access remains a full recovery
boundary. No new vendor was introduced. Operator key custody and an independent
incident-response exercise remain open; financial loss and legal applicability
were not inferred. The recovery and rollback scenarios were rehearsed against
disposable data. See [the account-security runbook](account-security.md).

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
independent host-loss detection, external recovery-key custody and full
host-recovery work listed above. GitHub-hosted CI has not been triggered by a
push during this session.


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

## Monitoring and HTTP runtime patch stage

The API was updated to Go 1.26.11 after govulncheck's updated database identified
GO-2026-6859 in Go 1.26.10. This addresses the HTTP/1 MaxBytesReader header race
described by the advisory. The source and compiled release binary both passed
govulncheck after rebuilding. CI and the API Docker build select the patched
version. [Go release history](https://go.dev/doc/devel/release#go1.26.11),
[Go vulnerability record](https://vuln.go.dev/ID/GO-2026-6859.json).

Release `4044eb8` was published on 2026-11-11 at 15:42:37 UTC. The tested
binary's SHA-256 is
`d34b6ba29f3edf30e354a6b32c8b7957b92661cd8ff71b2868bb39431af29019`.
The preceding binary and manifest are retained under the root-restricted path
`/home/micu/.local/state/onion-spider/releases/20261111T154237Z-monitoring`.
Readiness, public status, local status and the Tor vhost status passed. The API
had zero automatic restarts after publication. No migration was required.

Queue counts now explicitly include empty statuses with zero values, preventing
stale gauges. The freshness gauge advances only after a successful query. A
database regression covers a queue state disappearing after completion.

Two previous PromQL rules compared series carrying different status/result
labels, preventing the intended match. The replacement rules match labels
explicitly and detect crawler inactivity even before a success counter exists.

The deployed stack uses four healthy scrape targets and 17 rules for API and
database readiness, crawler throughput, stale metrics, offsite backup age,
restore-check age, invalid records, failed services, disabled timers, collector
failure, disk space and notification infrastructure. Configuration belongs to
this project and survives deployments of the other projects on the VPS.

The private collector requires bearer authentication, receives its token with
LoadCredential, exposes numeric aggregates and cannot access the Docker socket.
Its live sandbox could read the four recovery units and valid success records.
Prometheus and Alertmanager run with nonroot users, read-only roots, no
capabilities and resource limits. Ports 9090, 9093 and 19190 are bound to
127.0.0.1; Alertmanager's cluster listener is disabled.

Verification completed:

- All Go packages passed with the race detector and a disposable PostgreSQL
  database under Go 1.26.11. Vet and golangci-lint passed.
- All 24 rule scenarios and eight collector regressions passed, alongside the
  ten existing backup regressions.
- Config validation ran using the same pinned images as production. Shellcheck
  and systemd unit validation passed. The monitoring checks are included in CI.
- A temporary synthetic rule reached a localhost webhook through the actual
  Prometheus/Alertmanager stack in 2.05 seconds. Temporary rules and routes were
  removed afterward. The local webhook probe sent no external email.

The operator authorized alerts and one email test to `984.alexmihai@proton.me`.
The email route is installed with required STARTTLS on port 587. On activation,
the remaining synthetic test alert generated the single authorized email.
Alertmanager recorded one completed notification, zero failures and 1.51 seconds
of notification latency. The test was resolved; resolution email is disabled.
A fallback route suppresses leftover local webhook probe alerts after cleanup.
The operator confirmed receipt; no second email test was sent. Read-only provider
verification returned HTTP 401 with `restricted_api_key`, indicating a send-only
credential. No additional API permission was requested. Monitoring on this VPS
cannot independently report total VPS or network loss.

Residual upstream finding: the current official Prometheus 3.16.0 and
Alertmanager 0.35.0 images were built with Go 1.27.3. Binary scans report
GO-2026-6859, fixed in Go 1.27.4. The HTTP services are restricted to loopback,
which limits reachability but does not remove the vulnerable runtime. Replace
these digest-pinned images when patched upstream builds are available. This
finding does not apply to the newly patched Onion Spider API binary.
