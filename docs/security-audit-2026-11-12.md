# Recovery-code security release

Published on 2026-11-12, extending the [prior audit](security-audit-2026-11-11.md)
and [account-security runbook](account-security.md).

## Findings and behaviour

The prior generator issued ten characters, giving less than 50 bits even before
its byte-modulo sampling bias. Recovery codes use fast SHA-256 digests, so
limited entropy weakens their margin against offline guessing after a database
disclosure. Password checks and online throttling still apply. No exploitation
is asserted by this review.

New codes sample 28 independent characters uniformly from a 31-character
alphabet using crypto/rand.Int. Three grouping hyphens produce 31 display
characters and approximately 138.7 bits of randomness. This exceeds the 64-bit
minimum discussed for saved recovery codes in
[NIST SP 800-63B-4](https://pages.nist.gov/800-63-4/sp800-63b.html), without
establishing NIST compliance for the application.

MFA users can replace recovery codes with the current password and a fresh
authenticator code. A recovery code cannot mint a new set. Replacement, TOTP
consumption, credential-version advancement and session revocation are atomic.
Concurrent requests and failed inserts preserve a complete valid set. The
authenticator and enrolment timestamp stay intact; the pseudonymous auth audit
records replacement.

Codes appear once in memory. Existing printed codes remain valid until explicit
replacement and are not automatically strengthened. The UI highlights two or
fewer remaining codes and accepts the longer format across sign-in and password
re-authentication. Older open tabs should reload to update their input bounds.

## Publication

Release `7daa6f2` was published at 01:23:38 UTC. API SHA-256:
`2e70713fdb01750d64e0362c66f75babddfd3ba3ea4cd384a583c6e333e99536`.
The previous crypto-capable API, previous frontend, compatible rollback frontend
and manifest are retained under the root-restricted path
`/home/micu/.local/state/onion-spider/releases/20261112T012338Z-recovery-codes`.
The rollback UI uses the preceding account workflow with longer code inputs.
Existing hashed assets remain available to already-open clients.

No schema, TOTP-envelope, keyring or recovery-digest format changed. A fresh
paired Google Drive backup was verified at 01:17:09 UTC. The unchanged keyring
was verified by the earlier isolated restore. Systemd isolation and the loopback
listener are retained. Production status returned 200, both public and Tor-vhost
unauthorized replacement requests returned 401, and the service had zero
automatic restarts. All four monitoring targets were healthy.

## Verification

- All backend packages passed with the race detector against disposable
  PostgreSQL; vet and golangci-lint passed.
- Tests cover wrong/missing credentials, required MFA, refusal to regenerate
  with a recovery code, replay, concurrency, incomplete/duplicate insert rollback,
  session revocation, legacy printed-code login and new-code login.
- Source and final binary govulncheck scans reported no vulnerabilities.
- Strict frontend lint, all 26 frontend unit tests and production build passed.
  No new dependency was added.
- Chromium used the actual API binary and an isolated database. It exercised
  low-code guidance, authenticator-backed replacement, copied-session and
  old-code rejection, 31-character code login and replay rejection. Form and
  code-display widths 320/375/768/1280 had no document overflow.
- Staged changes passed Gitleaks. Local checks ran without a GitHub push or
  hosted CI execution.

Authenticated tests used disposable accounts. Production user credentials were
not used, and no test email was sent in this stage.
