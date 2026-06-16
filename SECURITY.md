# Security Policy

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Report privately via one of:

- GitHub's **"Report a vulnerability"** button under the repository's *Security*
  tab (preferred — creates a private advisory), or
- email **security@micutu.com**.

Include enough detail to reproduce: affected endpoint/component, a proof of
concept if possible, and the impact you observed. We aim to acknowledge reports
within 72 hours.

Please give us a reasonable window to ship a fix before any public disclosure.

## Scope

In scope:

- The Go backend (`backend/`) — API, auth, crawler, exports.
- The Vue frontend (`frontend/`).
- Deployment configuration in this repository (`docker-compose.yml`,
  `Dockerfile`s, `deploy/`).

Out of scope:

- Findings that require a compromised host or physical access.
- The Tor network itself and third-party `.onion` services being crawled.
- Denial of service via raw traffic volume (rate limiting is best-effort and
  the service sits behind a CDN/reverse proxy).

## Supported Versions

Only the latest `main` is supported. There are no long-term support branches.
