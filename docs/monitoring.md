# Monitoring

The API exposes aggregate Prometheus metrics on loopback port 8900. Public nginx
proxies only `/api/`; the metrics endpoint is not publicly routed.

The dedicated Onion Spider Prometheus instance collects the API, the private
backup/readiness collector, Alertmanager and itself every 30 seconds. Rules
evaluate every 30 seconds. Installation, credentials, email delivery and
incident commands are in [the monitoring runbook](../deploy/prometheus/README.md).

## Operational signals

| Metric | Meaning |
| --- | --- |
| `onionspider_queue_nodes` | Queue counts by bounded status, including explicit zeroes |
| `onionspider_queue_last_refresh_timestamp_seconds` | Last successful database refresh |
| `onionspider_crawls_total` | Successful/failed crawl attempts |
| `onionspider_links_discovered_total` | Links extracted from successful fetches |
| `onionspider_api_ready` | Fixed local readiness request succeeds |
| `onionspider_backup_state_valid` | Valid offsite/restore success record |
| `onionspider_backup_last_success_timestamp_seconds` | Verified publication / isolated restore completion |
| `onionspider_backup_unit_failed` | Last backup or restore service result indicates failure |
| `onionspider_backup_timer_enabled` | Timer enabled and active |
| `onionspider_backup_units_readable` | All four recovery units can be inspected |
| `onionspider_host_disk_available_bytes` | Available root filesystem space |

The collector exposes no archive names, remote paths, hashes, credentials, email
addresses or account data.

## Detection behaviour

The crawler previously sat idle for months while health checks stayed green.
Queue alerts inspect available work and completions, match labels deliberately
and handle a success counter that does not exist yet. An idle, empty queue does
not trigger the no-completions alert. Queue metrics reset disappearing states to
zero and report refresh freshness.

Failed backup/restore jobs have a two-minute alert hold. Disabled timers, failed
scrapes and API readiness failures have a five-minute hold. Offsite backup age
above 26 hours and restore-check age above 40 days have a ten-minute hold.
Scheduling, scraping, evaluation and notification grouping add delay. These are
configured thresholds, not measured operator response times.

Email notifications are enabled for the operator-approved Proton address. One
test completed without SMTP errors and the operator confirmed receipt.
Monitoring on the same VPS does not provide an independent path for detecting
complete host loss.
