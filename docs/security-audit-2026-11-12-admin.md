# Administrator access review — 2026-11-12

This release addresses the missing operational administrator workflow identified
after the [account-security release](security-audit-2026-11-12.md). The
[access runbook](admin-access.md) describes owner sign-in and invitations.

## Findings and changes

The alert recipient was configured separately from the owner's login. Public
registration was closed, an existing administrator still used the previous
address, and the UI did not provide an administration page. Following explicit
operator approval, the same account was moved to the approved address without
changing its ID or deleting research data. The old password and sessions were
invalidated, one password-choice email was sent, and the owner subsequently
completed password reset and signed in. Credentials were not inspected.

The new Admin page lists accounts in bounded pages, creates pending regular
users through reauthenticated invitations, recovers pending invitations through
explicit resend and manages the existing crawler blacklist. Public registration
stays closed. Backend authorization checks the live role and required MFA; the
page guides administrators who need to enrol. Invitation writes lock the actor
and recheck the session credential version, role and MFA before commit.

Recipients choose their own password using a 24-hour, one-use email credential.
Only its SHA-256 digest is stored. Activation verifies the address, invalidates
older reset credentials and advances the session version in the same write.
The request cannot grant an administrator role. A database-serialized five-minute
recipient cooldown bounds resend races, alongside existing account rate limits
and bounded password work. Failed or ambiguous SMTP delivery leaves a visible
pending account; the interface offers explicit resend after the cooldown.

Browser testing found that consuming a URL fragment in `main.js` was too late:
importing the router had already constructed browser history and captured the
original URL, so initial navigation could restore its credential. Consumption
now precedes history construction. Reset, verification and invitation pages
were checked in Chromium. Fragments were already excluded from HTTP requests;
the fix additionally prevents them surviving the initial navigation in the
address bar. The rollback frontend preserves this fix for its existing routes.

The common capped JSON decoder now rejects extra JSON documents after the
first object, as well as unknown fields. This applies to the new credential
requests and existing endpoints using that decoder.

## Validation

- The full Go suite passed with PostgreSQL integration fixtures and the race
  detector. Targeted administrator/invitation tests passed again after the
  session-version guard was finalized. Cases include missing/ordinary/stale
  administrator access, required MFA, password plus second-factor proof,
  strict request fields, closed registration, activation replay and expiry,
  duplicate email, failed delivery, resend invalidation and concurrent resends.
- `go vet` and golangci-lint passed. Source and final static binary govulncheck
  scans reported no vulnerabilities. Production and development npm audits
  reported no vulnerabilities; no dependency was added.
- Frontend lint, 27 unit tests and the production build passed. Chromium used
  real API handlers, an isolated PostgreSQL container and a captured test
  mailer. It exercised MFA enrolment and gating, invitation, resend cooldown,
  failed delivery with a visible pending account, password-choice activation,
  recipient login, user-role isolation and obsolete-link rejection.
- Chromium exercised blacklist validation, addition and explicit removal
  confirmation only against the disposable database. Widths 320/375/768/1280
  were checked for document overflow and clipped controls. A visual review
  caught and corrected a long button label clipped on a narrow screen.
- The fallback frontend was built and separately checked for reset and
  verification fragment scrubbing. Staged implementation changes passed
  Gitleaks. No GitHub push or hosted CI run was performed.

The first publication attempt used a dynamically linked local build, which the
existing systemd execution allowlist rejected with `203/EXEC`. The readiness
check restored the previous API. The static artifact was rebuilt using the
project's `make build` target and verified before the next publication; the
sandbox was retained. A fresh encrypted Google Drive backup with its paired
TOTP keyring is required by the publication preflight.

Invitation tests never sent real mail. The single owner recovery message was
explicitly authorized. Production checks use unauthenticated requests and
account metadata, not the owner's credentials.

## Publication and operational checks

Implementation commits `9fea51e` and `2076e6a` were published at 11:12:08 UTC on
2026-11-12. The release manifest, both API executables and matching frontend
artifacts are retained under the root-restricted directory
`/home/micu/.local/state/onion-spider/releases/20261112T111208Z-administration`.
The manifest records executable and frontend-index digests. The earlier failed
publication is retained separately with `published: false` and `rolled_back:
true` for diagnosis.

The pre-publication encrypted Google Drive backup completed at 11:11:33 UTC and
included its paired keyring. The API passed readiness, nginx configuration
validation and live digest comparison. Public registration remained closed.
Direct loopback and the local onion vhost returned configuration successfully
and rejected unauthenticated account inventory with 401 and `no-store`.

Chromium checks through the public Cloudflare domain confirmed all three email
pages scrubbed their fragments, a guest Admin visit required sign-in, the
configuration endpoint reported closed registration, and account inventory
returned 401 with `no-store`. No JavaScript errors occurred. The Python HTTP
client received Cloudflare 1010 responses, so browser results were checked
explicitly without changing Cloudflare policy. All four private Prometheus
targets were healthy and there were no active monitoring alerts after release.

The existing owner account is verified and administrative; password reset was
completed. MFA remains unenrolled and must be enabled by the owner before
administrative functions become available under the production MFA policy.
