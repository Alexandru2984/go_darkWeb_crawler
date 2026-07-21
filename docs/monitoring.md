# Monitoring

The API exposes Prometheus metrics on `/metrics`, served on the process root
rather than under `/api/`. Production nginx proxies only `/api/`, so the
endpoint is reachable on `127.0.0.1:8900` and never publicly.

## Scrape config

Rules and the notification path are documented in `deploy/prometheus/README.md`.

Prometheus runs on this host. `/etc/prometheus/prometheus.yml` is host-wide and
shared with the other services on the box, so it is not deployable from this
repo — the job below has to be added there by hand:

```yaml
  - job_name: 'onion-spider'
    scrape_interval: 30s
    scrape_timeout: 10s
    static_configs:
      - targets: ['localhost:8900']
```

```bash
sudo promtool check config /etc/prometheus/prometheus.yml
sudo systemctl reload prometheus
curl -s 'http://localhost:9090/api/v1/targets?state=active' | grep -o 'onion-spider'
```

## What to actually watch

| Metric | Why it matters |
|---|---|
| `onionspider_queue_nodes{status="pending"}` | Work waiting. Flat at zero with a large `failed` count is the stall described below. |
| `onionspider_queue_nodes{status="failed"}` | Climbing steadily means crawls are failing, usually Tor rather than the sites. |
| `onionspider_queue_nodes{status="crawling"}` | Should track worker count. Pinned at the worker count with no completions means workers are wedged. |
| `onionspider_crawls_total{result="..."}` | `success` vs `scrape_error` ratio — the clearest signal that Tor connectivity has degraded. |
| `onionspider_links_discovered_total` | Flat while crawls succeed means pages are being fetched but not parsed. |

## The failure this project has already had

Between May and July the entire queue died: every one of 23,346 nodes ended in
`failed`, nothing was left `pending`, and the crawler sat idle for over two
months. Nothing alerted, because from the outside the service looked healthy —
the API answered, `/readyz` was green, and the process was up. The only symptom
was `onionspider_queue_nodes{status="pending"}` sitting at zero.

Two things address it. The engine now revives long-failed nodes on an hourly
sweep (see `REVIVE_FAILED_AFTER_DAYS`), so a transient outage no longer
permanently kills the queue. And the metric above is worth an alert:

```yaml
- alert: OnionSpiderQueueStalled
  expr: onionspider_queue_nodes{status="pending"} == 0
        and onionspider_queue_nodes{status="failed"} > 100
  for: 2h
  annotations:
    summary: "Crawl queue is empty while failures are piling up — crawler is idle"
```

A liveness check cannot catch this. `/healthz` reports that the process is
running and `/readyz` that the database answers; neither has any opinion about
whether the crawler is doing work, which is the thing that was broken.
