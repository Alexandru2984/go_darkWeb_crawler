# Private monitoring and recovery alerts

Onion Spider owns a separate monitoring stack. Other projects on this VPS
generate their monitoring configuration during deploy; direct edits there would
be lost. This stack uses its own containers, configuration and data volumes.

## Install or update

```bash
sudo bash scripts/deploy_monitoring.sh
```

The installer validates both configurations and the systemd unit before applying
changes, preserves existing credentials, atomically replaces configuration files,
reloads containers and checks readiness. If deployment fails, it restores previous
configuration files. The production preset assumes `/home/micu/go`, user `micu`
and group ID 1001; adjust the installer, Compose user and unit together on another
host. Docker Compose and systemd are required.

| Component | Private endpoint |
| --- | --- |
| API metrics | `127.0.0.1:8900/metrics` |
| Backup/readiness collector | `127.0.0.1:19190/metrics` |
| Prometheus | `127.0.0.1:9090` |
| Alertmanager | `127.0.0.1:9093` |

Containers use host networking to reach loopback services. HTTP listeners bind
explicitly to loopback; Alertmanager's cluster listener is disabled. Containers
run without capabilities, with read-only root filesystems and resource limits.
Prometheus retains 30 days or 1 GB of time-series blocks; WAL/head data can
exceed the block limit. Use SSH local forwarding to view the interfaces remotely.
Do not add these endpoints to nginx, Cloudflare Tunnel or public firewall rules.

Live YAML files reside in `/var/lib/onion-spider-monitoring/config`. Directory
mounts make atomic file replacement visible to running containers. Apply source
changes through the installer; direct runtime edits are temporary.

## Credentials and email delivery

`/etc/onion-spider/monitoring` is root-only. Its `metrics-token` and `smtp-password`
files are `root:micu`, mode 0640, for individual read-only container bind mounts.
The installer generates the token once and copies `SMTP_PASS` from the application
environment as data, without printing it, executing it or placing it in argv.
Existing credentials survive repeated installs. Rotate them deliberately and
recreate containers afterward to refresh individual secret-file bind mounts.

The collector receives its token through systemd `LoadCredential`. `/metrics`
returns 401 without bearer authentication; `/healthz` reveals only process
liveness. Do not put the token into shell history or command arguments. The
collector exposes aggregate numeric state and cannot access the Docker socket.

Alerts go to the operator-approved `984.alexmihai@proton.me` through
`smtp.resend.com:587`, with STARTTLS required, sender `noreply@micutu.com`,
user `resend`, and `smtp_auth_password_file: /run/secrets/smtp-password`.
Keep credentials out of YAML and commits. `send_resolved: false` suppresses
resolution emails. Future local webhook tests require a temporary exact-match
route above the `local-tests-only` fallback; that fallback prevents leftover
local probe alerts from being emailed when the temporary route is removed.

One authorized test email completed successfully on 2026-11-11. Alertmanager
recorded one email attempt, one completed notification and zero failures. The
operator confirmed receipt. Read-only provider verification is unavailable with
the send-only SMTP credential; no additional API permission was requested.

The top-level legacy `alertmanager.yml` and its operator-created backup are not
used by this stack; their local changes are preserved.

## Verify and diagnose

```bash
systemctl status onion-spider-monitoring-exporter.service
journalctl -u onion-spider-monitoring-exporter.service --since '10 minutes ago'
docker compose --env-file /dev/null -f deploy/prometheus/docker-compose.yml ps
curl --fail --silent 'http://127.0.0.1:9090/api/v1/targets?state=active'
curl --fail --silent 'http://127.0.0.1:9090/api/v1/rules'
curl --fail --silent 'http://127.0.0.1:9090/api/v1/alerts'
curl --fail --silent 'http://127.0.0.1:9093/api/v2/alerts'
```

All four targets must be up and all 17 rules must evaluate without errors.

| Alert | First investigation |
| --- | --- |
| API down / readiness failing | API service, PostgreSQL connectivity |
| Queue stalled / no successful crawling | Workers, Tor, failed-node revival |
| Queue refresh stale | Database locks and queue-count query errors |
| Offsite backup older than 26 hours | Backup service, rclone authorization |
| Restore check older than 40 days | Restore timer and last service result |
| Invalid backup record | Missing/corrupt success records, latest job results |
| Recovery job failed / timer disabled | Both recovery services and timers |
| Collector unavailable / units unreadable | Collector logs, credential, sandbox |
| Disk below 10% / 5% available | PostgreSQL, backups, monitoring retention |
| Alertmanager down / email failure | Container logs, SMTP TLS and provider status |

A service can fail while its last successful archive is fresh; job-result alerts
catch that separately. A successful rerun clears the failure state. Do not fake
success timestamps or delete records to silence an alert.

CI runs collector tests, installer checks, config validation and rule regressions.
The recovery tests simulate more than 40 days without waiting in real time.

```bash
python3 -m unittest discover -s scripts -p 'test_monitoring.py' -v
promtool test rules deploy/prometheus/onion-spider-rules.test.yml deploy/prometheus/onion-spider-recovery.test.yml
```

This stack runs on the VPS it observes. Host loss, connectivity loss or stopped
Prometheus cannot reliably notify through this same stack. An independent
external heartbeat remains future work. Current upstream monitoring images also
need replacement when Go runtime fixes for GO-2026-6859 ship; see the security audit.
