# Security threat model

Last reviewed: 2026-08-09

Scope: Onion Spider API, crawler, web UI, PostgreSQL data, Tor integration,
nginx/Cloudflare edge, systemd deployment, email and backups.

## Decision and security objective

Current release verdict: **RED — MITIGATE BEFORE SHIP** until the production
deployment gate at the end of this document is completed and verified.

The objective is strong confidentiality and tenant isolation under realistic
attack conditions. It is not an “unbreakable” claim. In particular:

- Cloudflare terminates TLS for the clearnet hostname and can technically see
  credentials, searches and requested onion URLs in plaintext at its edge.
- The VPS and the hosted crawler must see a target URL while crawling it. A
  hosted server-side crawler therefore cannot be fully zero-knowledge.
- The onion-service entry point avoids the Cloudflare TLS boundary and gives
  Tor-authenticated end-to-end transport to this VPS, but a compromised VPS or
  application can still access live application data.

The privacy target is consequently: collect less, retain less, prevent
cross-tenant access, remove plaintext telemetry, encrypt recoverable copies,
make compromise visible quickly, and give users a Tor-native path that does not
cross a CDN.

## Architecture and trust boundaries

```text
clearnet browser -> Cloudflare TLS/WAF -> nginx -> Go API -> PostgreSQL
Tor browser ----------------> onion service -> nginx -> Go API -> PostgreSQL
                                                   |
                                                   +-> Tor SOCKS -> onion sites
                                                   +-> SMTP server -> account email
Prometheus <- localhost /metrics                  backups -> encrypted local files
```

Trust boundaries:

1. Internet to Cloudflare: hostile input and traffic volume.
2. Cloudflare to origin: only current Cloudflare network ranges may reach the
   clearnet vhost. `CF-Connecting-IP` is trusted only across this boundary.
3. Tor onion service to onion nginx vhost: no Cloudflare; all Tor clients reach
   the same local source address, so IP-based controls are weak here.
4. nginx to Go on `127.0.0.1:8900`: nginx owns body limits, edge policy and
   public routing; the API still validates all input independently.
5. Go to PostgreSQL: the DB contains identity, crawl history and content and is
   the highest-value confidentiality target.
6. Go to Tor: all crawler egress must use SOCKS; redirects to clearnet or to a
   different onion service are rejected.
7. Go to SMTP: reset and verification links cross the mail provider boundary.
8. Host to backup storage: backups are encrypted, but are currently retained
   only on the same VPS.

## Assets and data classification

| Class | Examples | Sensitivity | Current protection |
|---|---|---:|---|
| Authentication | password hashes, JWT signing key, session JWTs, reset/verification credentials | Critical | Argon2id t=3/64 MiB/p=4 with transparent upgrade from bcrypt; HS256 pinned; short-lived JWT; opaque tokens hashed; secrets mode 0600 |
| Identity | account email and role | High | tenant-scoped DB access; removed from JWT; email/IP pseudonymized in audit events |
| User intent | submitted onion URLs, paths, query strings, searches, graph edges | Critical | authenticated access; no application/nginx request-value logging after current fixes; `no-store` |
| Crawled material | titles, page text, metadata, extracted emails/keys/crypto addresses | High | DB access control and export limits; not yet encrypted at application layer or automatically expired |
| Operational data | request route/status/duration, HMAC audit references, aggregate metrics | Medium | bounded/pseudonymized telemetry; API metrics loopback-only |
| Recovery data | encrypted SQL dumps containing all classes above | Critical | GPG AES256/AEAD, root-held passphrase, mode 0600, seven local copies; no automated offsite copy yet |

Production snapshot on 2026-08-09: three verified users (one admin), about
31,700 tenant-owned nodes and 88,000 edges. A complete DB or backup compromise
therefore reveals the full hosted dataset and the association between each
account and its crawl history. It does not reveal plaintext passwords, current
verification/reset credentials, or raw IP/email values from newly written
audit events.

## STRIDE register

Likelihood and impact are qualitative (`L`, `M`, `H`) and assume an
internet-exposed service with a small current user base but unusually sensitive
browsing intent.

| ID | STRIDE | Threat / abuse case | Likelihood | Impact | Implemented controls | Residual work |
|---|---|---|---:|---:|---|---|
| S1 | Spoofing | Credential stuffing or password reuse takes over an account | M | H | Argon2id (memory-hard, so GPU cracking of a stolen hash is far costlier than bcrypt), constant-time unknown-user path, lockout, per-source limits, verified email | Passkeys/TOTP, breached-password screening, session inventory and anomaly alerting |
| S2 | Spoofing | Forged or confused JWT (`none`, another HMAC algorithm, wrong issuer/audience, overlong token) | L | H | exact HS256, required `iss/aud/exp/iat/nbf`, max lifetime, DB token version, tests | Planned signing-key rotation procedure and per-session revocation |
| S3 | Spoofing | Fake `CF-Connecting-IP` or direct-origin request bypasses WAF/rate limits | M | H | trusted CIDRs only; clearnet vhost returns 444 outside Cloudflare ranges | Deploy repo nginx config; enable authenticated origin pulls or mTLS; alert on rejected origin attempts |
| T1 | Tampering | Malicious onion HTML, XML or compression input exploits parsers | M | H | no JS execution, MIME gate, decompressed read limits, content truncation, parser dependency scanning, hardened systemd | Fuzz targets, worker process isolation, seccomp/AppArmor profile, parser CPU/memory budgets |
| T2 | Tampering | SQL or cross-tenant identifier manipulation changes another tenant’s graph/queue | L | H | parameterized SQL, `(url,user_id)` keys, DB-loaded authorization, tenant-bound GraphML joins and integration tests | PostgreSQL row-level security as a second authorization layer |
| T3 | Tampering | Dependency, CI action or container base is replaced upstream | M | H | lockfiles, CodeQL, govulncheck, npm audit, Dependabot | Pin Actions to commit SHA, pin image digests, SBOM, provenance/signatures, secret and image scans |
| R1 | Repudiation | Attacker performs account actions that cannot be attributed | M | M | structured auth audit with stable HMAC references; route/status metrics | Alert rules for auth anomalies; protected audit sink; clock verification; documented audit retention |
| R2 | Repudiation | Operator cannot reconstruct a production change or incident | M | H | Git commits, systemd journal, migration table, encrypted backups | Deployment log, release artifact hashes, centralized append-only security events |
| I1 | Information disclosure | Raw onion URLs/searches/emails/IPs leak through nginx or application logs | H (historical) | H | access log disabled; central redaction/HMAC; route-pattern logging | Rotate/purge historical journals under evidence-preservation policy; continuous canary test |
| I2 | Information disclosure | Cloudflare, browser telemetry or a CDN cache observes sensitive clearnet traffic | H | H | `no-store`, strict CSP, no app analytics beacon, Tor onion endpoint | Disable Cloudflare NEL/browser analytics; document onion URL prominently; assess CDN removal or application-layer request encryption |
| I3 | Information disclosure | IDOR/cross-tenant export returns another user’s data | M | H | DB role lookup, tenant filters, GraphML join fix, integration tests | Add RLS and a full endpoint-by-endpoint authorization matrix test |
| I4 | Information disclosure | DB, `.env`, local backup or old secret copy is stolen | M | H | service sandbox, mode 0600, hashed one-time tokens, encrypted dumps | Remove/secure stale secret copies, rotate credentials, application-level envelope encryption, encrypted offsite backup |
| I5 | Information disclosure | XSS reads returned bearer credentials or renders crawled HTML | M | H | Vue escaping, `innerText`, CSP, no raw HTML rendering, browser login omits JWT body, HttpOnly cookie | Remove remaining inline-style allowance, add CSP reporting without third-party leakage, recurring DOM-XSS tests |
| D1 | Denial of service | Queue flood, expensive search/export, parser bomb or slow onion consumes workers/RAM | H | M/H | body/response limits, timeouts, queue fairness, rate/concurrency limits, bounded exports, systemd restart, per-account pending-queue quota enforced inside the enqueue transaction, bulk submissions charged per URL | Job budgets, circuit breakers, memory/CPU limits and load tests |
| D2 | Denial of service | Account lockout is weaponized against a known email | M | M | bounded lockout and generic responses | Progressive backoff, MFA-aware recovery, alerting; avoid permanent administrative lock |
| D4 | Denial of service | One onion visitor exhausts the limit shared by every other onion visitor | H | M | authenticated requests charged to the account, not the address; pre-auth limits namespaced per front door and sized per front door on onion; per-email lockout and per-recipient caps unchanged | Proof-of-work or another address-free per-client signal so the onion aggregate can be tightened again |
| D3 | Denial of service | Tor/Postgres/disk outage stalls crawler while health endpoint stays green | H (observed) | H | queue sweeper, work-based Prometheus alerts, readiness check, encrypted backups | Alert delivery test, offsite restore drill, database/disk SLOs |
| E1 | Elevation | Two instances race during first-admin registration or DB error fails open | L | H | serialized advisory-lock bootstrap, fail-closed DB handling, role validation | Replace public bootstrap with one-time offline admin command before reopening registration |
| E2 | Elevation | Compromised crawler process reaches SSH keys, other apps or host kernel | M | Critical | loopback bind; repository systemd unit scores 1.1; dynamic UID; static artifact-only mount; whole-filesystem noexec; no capabilities; protected home/system/kernel/devices; syscall filtering | Deploy and verify the new unit, move releases to a root-owned tree, isolate Tor, add AppArmor/SELinux |

## Blast radius and rough loss model

Worst credible cases:

- Stolen normal session: one account’s identity, submitted URLs, results and
  exports for up to four hours; write actions possible until revocation.
- Stolen admin session: all hosted crawl data plus blacklist administration;
  it does not grant shell or raw DB access.
- SQL injection/application DB credential theft: all account emails, password
  hashes, crawl history, crawled material and audit references; data tampering
  and deletion are possible.
- VPS root or backup-passphrase compromise: all current data and secrets,
  historical local backups, SMTP/Tor control credentials and the onion service
  key material available elsewhere on the host.
- Cloudflare compromise/legal access: clearnet requests while they traverse the
  edge, including login and search content; the Tor onion endpoint is outside
  this path.

Provisional FAIR-style estimate, for prioritization only:

- Assumed material confidentiality incident frequency before remaining P0/P1
  work: 10–20% per year.
- Assumed direct response, rebuild, legal assessment and user-support cost for
  this small service: EUR 5,000–25,000 per event, excluding fines and harm to
  users.
- Provisional annualized loss expectancy: roughly EUR 500–5,000.

These figures are deliberately ranges. They must be replaced with actual
revenue, response labor, legal counsel, contractual exposure and cyber-insurance
inputs before being used as a business forecast. User safety and disclosure of
highly sensitive browsing intent can dominate the monetary estimate.

## Detection coverage and MTTD

| Signal | Current detection | Current MTTD | Target |
|---|---|---:|---:|
| API/process unavailable | Prometheus `up` alert | 5–10 min plus notification latency | <= 10 min |
| Crawler stalled with work queued | work-rate alert | 60–90 min | <= 30 min |
| Empty queue with large failed set | queue alert | about 2 h | <= 60 min |
| High crawl failure ratio | ratio alert | 1–2 h | <= 60 min |
| Disk exhaustion | node metric alerts | 5–30 min after threshold | <= 15 min critical |
| Credential stuffing / repeated lockouts | audit rows only; no alert | Manual/indefinite | <= 15 min |
| Admin login or unusual bulk export | no dedicated event/alert | Manual/indefinite | <= 15 min |
| Cross-tenant access attempt | generic 404/403 only | Manual/indefinite | <= 15 min for repeated attempts |
| Direct-origin bypass attempt | nginx currently unalerted | Manual/indefinite | <= 5 min |
| Secret/DB exfiltration | no egress or DB anomaly detector | Manual/indefinite | <= 15 min high-confidence |
| Backup failure | systemd journal, no proven page | Up to next manual review | <= 30 min |

Until the indefinite rows have alerts delivered to a channel the operator
actually reads, incident response depends too heavily on manual discovery.

## Regulatory and notification clock

This service processes account identifiers and potentially identifying or
high-risk browsing intent. Treat a confidentiality, integrity or availability
incident involving those records as a possible personal-data breach.

GDPR Article 33 requires notification to the competent supervisory authority
without undue delay and, where feasible, within 72 hours after awareness unless
the breach is unlikely to risk individuals’ rights and freedoms. Article 34 can
also require communication to affected people when high risk is likely. Record
the awareness time immediately; do not wait for full certainty. See the
[official regulation](https://eur-lex.europa.eu/eli/reg/2016/679/2016-05-04)
and [EDPB breach guidance](https://www.edpb.europa.eu/our-work-tools/our-documents/guidelines/guidelines-92022-personal-data-breach-notification-under_en).

Legal counsel/controller identity, competent DPA, DPO/contact and contractual
notification duties are **not yet recorded**. This is a release blocker for a
commercial or public launch.

## Vendors and external dependencies

| Party | Data/access | Security dependency | Open diligence item |
|---|---|---|---|
| VPS provider | disks, memory, network metadata, snapshots; potential privileged host access | physical/hypervisor security and account protection | Provider/region, DPA, encryption, support-access logs and account MFA |
| Cloudflare | clearnet plaintext requests after TLS termination, client IP and traffic metadata | WAF, DNS, origin routing, account/API-token security | DPA/settings review; disable NEL/analytics; AOP/mTLS; least-privilege API token and MFA |
| SMTP/mail host | recipient email and single-use link | delivery security, mailbox/account protection | DPA/retention, TLS enforcement, account MFA, bounce/log retention |
| GitHub/Actions | source, CI metadata, build logs and repository secrets | organization/repository controls and action supply chain | MFA, branch protection, environments, runner permissions, SHA-pinned actions |
| Go/npm registries | dependency and request metadata during builds/audits | package integrity and availability | Locking, checksum policy, internal cache, malicious-package response |
| Let’s Encrypt | clearnet hostname/certificate transparency metadata | certificate issuance and renewal | CAA, renewal alert, account/key protection |
| Tor network/onion sites | network timing and hostile response content; no account identity intentionally sent | onion protocol, client configuration, parser isolation | Dedicated Tor instance/user, egress validation, threat-intel handling |

Contracts, DPAs and vendor incident-notification SLAs have not been verified by
this code audit. They need an owner and recorded review date.

## Production release gate

Do not call the release production-final until all items below are evidenced:

- [x] Known reachable dependency vulnerabilities patched and scanners clean.
- [x] Sensitive request values removed from new application/nginx logs.
- [x] Direct-origin Cloudflare bypass fixed in the repository configuration.
- [x] JWT, one-time token, tenant export, onion validation and admin bootstrap
  boundaries covered by tests.
- [x] Incident-response runbook exists.
- [ ] Release binary and nginx config deployed with tested rollback.
- [ ] Direct-origin request fails externally while Cloudflare and onion paths
  remain healthy.
- [ ] Existing sessions intentionally invalidated and pending credentials
  migrated without plaintext retention.
- [ ] Security alert delivery is tested end-to-end; high-risk auth/export/origin
  events have an owner and target MTTD.
- [ ] Historical sensitive journals and redundant secret copies are handled
  under an evidence/rotation plan.
- [ ] Encrypted offsite backup and isolated restore drill succeed.
- [ ] Cloudflare NEL/browser analytics and vendor privacy settings are reviewed.
- [ ] Data retention, account deletion/export and privacy notice are shipped
  before opening registration broadly.

After these gates, the expected verdict is **AMBER — ship only with explicit
risk acceptance**, not “secure forever.” Re-run this review after material auth,
storage, crawler, proxy or vendor changes.
