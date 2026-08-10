# Incident response runbook

This runbook is designed for a small/operator-run production service. It must
be usable while stressed, from a clean admin workstation, without relying on
the possibly compromised application.

Security contact: `security@micutu.com`

Primary incident commander: **assign by name before public launch**

Backup incident commander / legal contact: **assign before public launch**

## Severity and authority

| Severity | Examples | Initial response target | Authority |
|---|---|---:|---|
| SEV-1 | active data exfiltration, root/VPS compromise, DB or signing-key theft, confirmed cross-tenant disclosure | 15 min | Incident commander may stop the app, disable Cloudflare routing and revoke credentials immediately |
| SEV-2 | account/admin takeover, exploitable auth flaw, malware in build, destructive data change with no confirmed exfiltration | 30 min | Isolate affected component and revoke affected sessions |
| SEV-3 | scanning, failed exploit attempts, isolated availability degradation, dependency advisory without known exploitation | 4 h | Rate-limit/block, investigate and schedule remediation |

Safety overrides availability. If containment would destroy evidence or affect
other services on the shared VPS, stop the Onion Spider service first and
escalate before changing host-wide nginx, firewall, PostgreSQL, Tor or
Prometheus configuration.

## First 15 minutes

1. Record UTC awareness time, reporter, symptoms and the person assuming
   incident command. This starts the regulatory assessment clock.
2. Open a private incident log outside the affected VPS. Give it a unique ID.
3. Confirm scope from a clean device. Do not log in through a page suspected of
   credential theft.
4. For active compromise, contain the application without erasing evidence:

   ```bash
   sudo systemctl stop onion-spider
   systemctl status onion-spider --no-pager
   sudo ss -lntup
   ```

5. If the origin is exposed, temporarily stop the vhost/service or restrict at
   the edge. Do not improvise a broad firewall rule that can cut off SSH or
   unrelated services.
6. Notify the backup incident commander/legal contact. Never put secrets,
   complete URLs, raw database rows or user email addresses into chat tickets.

## Preserve evidence before cleanup

Collect metadata first and hash exported evidence. Store it on an encrypted,
access-controlled incident volume, not in this Git repository.

```bash
date -u --iso-8601=seconds
systemctl status onion-spider nginx tor@default postgresql --no-pager
sudo journalctl -u onion-spider --since '<UTC time>' --output=short-iso
sudo journalctl -u nginx --since '<UTC time>' --output=short-iso
git -C /home/micu/go status --short
git -C /home/micu/go log -n 20 --show-signature --oneline
sha256sum /home/micu/go/backend/onion-spider-api
sudo -u postgres psql -X -d onion_spider -c \
  "SELECT pid, usename, application_name, client_addr, state, query_start
   FROM pg_stat_activity WHERE datname=current_database();"
```

Rules:

- Do not delete logs, rotate secrets or rebuild the host until the necessary
  evidence has been preserved.
- Do not dump the entire production DB merely “for investigation.” Start with
  counts, timestamps and identifiers; copy personal data only when needed.
- Record every command, result, actor and timestamp in the incident log.
- Take a provider snapshot only if its confidentiality and access controls are
  understood; a snapshot is another complete copy of sensitive data.

## Triage questions

- What is the earliest evidence of compromise and when did the controller
  become aware?
- Which boundary failed: Cloudflare/origin, web session, API authorization,
  crawler parser, database, CI/supply chain, SMTP, backup or host?
- Was confidentiality, integrity or availability affected?
- Which accounts, tenant IDs, row counts, time range and data classes were in
  scope? Avoid listing raw URLs in the general incident channel.
- Is attacker access ongoing? Are there persistence mechanisms or modified
  artifacts?
- Are other applications on the shared VPS affected?
- Which vendors need urgent notification or evidence preservation?

## Scenario playbooks

### JWT signing key or session compromise

1. Stop the API if active abuse continues.
2. Generate a new high-entropy JWT secret on a clean machine and replace it
   through the normal secret-management path; never paste it into shell history.
3. Restart the API. Key rotation invalidates every existing JWT.
4. Increment `token_version` for specifically affected accounts if the signing
   key itself was not exposed; use all-account revocation when scope is unclear.
5. Review HMAC-referenced login, lockout, logout-all, export and write activity.
6. Require password reset and notify affected users when account takeover is
   confirmed. Add MFA before restoring a high-value admin session.

### Database credential, dump or backup disclosure

1. Stop application writes and revoke/rotate the application DB credential.
2. Terminate unauthorized DB sessions; preserve `pg_stat_activity`, PostgreSQL
   logs and provider access records first.
3. Treat all emails, password hashes, tenant crawl history, page content and
   extracted entities in the affected copy as exposed. Reset and verification
   tokens stored by current releases are digests, not reusable plaintext.
4. Rotate JWT, SMTP, Tor-control and backup credentials if they could be reached
   from the same host/account.
5. Restore only from a verified pre-compromise backup into an isolated database,
   compare schema/version/counts, then promote according to `backup-restore.md`.

### Direct-origin or Cloudflare trust-boundary bypass

1. Verify from an external network with the origin IP plus the production
   Host/SNI. A direct request must not return the application.
2. Preserve nginx/error/firewall records. Apply the repository origin guard,
   validate with `nginx -t`, then reload.
3. Refresh Cloudflare IP ranges from the controlled updater and verify its
   signature/source assumptions.
4. Rotate credentials only if requests or authentication data were observed or
   the bypass was combined with another exploit.
5. Add an alert for future rejected direct-origin attempts.

### Crawler parser exploit or process escape

1. Stop Onion Spider and Tor access for its process while preserving other host
   services.
2. Hash the binary and compare it with the release artifact; inspect process,
   network and file changes from outside the service namespace.
3. Assume the process environment exposed DB/JWT/SMTP/Tor-control credentials.
4. If any host persistence or cross-service access is found, rebuild from a
   trusted image instead of cleaning in place.
5. Retain the triggering response only in an encrypted malware-analysis area;
   never open it in a normal browser.

### Destructive change, ransomware or lost VPS

1. Isolate the host/provider account and revoke infrastructure/API tokens.
2. Confirm that at least one encrypted backup predates the incident.
3. Build a clean replacement host and restore into a scratch DB first.
4. Validate row counts, migration state and application tests before DNS/routing
   cutover.
5. Do not trust backups stored only on the compromised host without independent
   integrity evidence.

## Regulatory and user communication

Maintain a breach-assessment record even when notification is judged
unnecessary. Under GDPR Article 33, notify the competent authority without
undue delay and where feasible within 72 hours of awareness unless risk to
people is unlikely; document reasons for any delay. Article 34 may require
prompt notice to affected people when high risk is likely.

The initial notice can be phased. Capture:

- nature and timeline of the breach;
- categories and approximate number of people and records;
- incident/privacy contact;
- likely consequences;
- containment and mitigation completed or planned.

Use clear language. Never claim that no data was accessed merely because there
is no log evidence; state the confidence and limitations of the evidence. Legal
counsel must confirm the competent authority and any Romanian, contractual,
provider or law-enforcement obligations.

Official references: [GDPR Articles 33–34](https://eur-lex.europa.eu/eli/reg/2016/679/2016-05-04)
and [EDPB Guidelines 9/2022](https://www.edpb.europa.eu/our-work-tools/our-documents/guidelines/guidelines-92022-personal-data-breach-notification-under_en).

## Recovery gate

Do not return traffic until:

- the exploited path is understood or safely removed;
- secrets reachable by the attacker are rotated;
- known persistence is excluded or the host is rebuilt;
- DB migrations are clean and restore integrity is verified;
- direct-origin, authentication, tenant-isolation and health smoke tests pass;
- enhanced monitoring is live for the incident pattern;
- incident commander and legal/privacy owner approve recovery.

Restore gradually, monitor error/auth/export rates and keep a rollback artifact.

## Post-incident and tabletop

Within five business days, produce a blameless report with timeline, root cause,
blast radius, detection gap, user/regulatory decisions and corrective owners.
Track actions to closure.

Run at least quarterly tabletop exercises for:

1. stolen JWT/DB secret with active admin-session abuse;
2. cross-tenant export reported by a user;
3. malicious onion response suspected of escaping the parser;
4. total VPS loss when local backups are unavailable.

Measure time to acknowledge, contain, identify affected records, make the
notification decision, restore and detect recurrence. A runbook not exercised
against a timer is an untested assumption.
