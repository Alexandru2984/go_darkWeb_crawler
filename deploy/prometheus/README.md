# Prometheus and alerting

Metrics, alert rules, and the notification path for Onion Spider.

`/etc/prometheus/prometheus.yml` is **host-wide and shared** with the other
services on this box, so this repo does not own it. The files here are copied
into `/etc/prometheus/` and referenced from that config.

## Files

- `onion-spider-rules.yml` — alert rules (this repo owns them).
- `alertmanager.yml` — routing and the email receiver. Host-wide; shared with
  any other service that starts alerting.

## Install

```bash
sudo cp deploy/prometheus/onion-spider-rules.yml /etc/prometheus/
sudo cp deploy/prometheus/alertmanager.yml     /etc/prometheus/

# prometheus.yml must reference the rules:
#   rule_files:
#     - "onion-spider-rules.yml"
#   scrape_configs:
#     - job_name: 'onion-spider'
#       static_configs:
#         - targets: ['localhost:8900']

sudo promtool check rules /etc/prometheus/onion-spider-rules.yml
sudo promtool check config /etc/prometheus/prometheus.yml
sudo amtool check-config /etc/prometheus/alertmanager.yml
sudo systemctl reload prometheus
sudo systemctl restart prometheus-alertmanager
```

## Why these alerts exist

Between 2026-05-05 and 2026-07-20 the crawler did no work at all — every node
ended in `failed`, nothing was left pending, and the service sat idle for over
two months. Nothing detected it, because nothing was *wrong* in a way a health
check can see: the process was up, the database answered, the API returned 200.

So the rules alert on the **absence of work**, not on liveness. `/healthz` says
the process runs and `/readyz` says the database answers; neither has any
opinion about whether the crawler is crawling, which was the part that broke.

`OnionSpiderQueueStalled` matches that exact signature — nothing pending while
failures pile up. If it ever fires, the revive sweeper is broken or not running.

## The SMTP password

Not in `alertmanager.yml`, so the file is safe to commit. Alertmanager reads it
from `/etc/prometheus/alertmanager-smtp.pass` (`root:prometheus`, `0640`) via
`smtp_auth_password_file`.

## Alertmanager is bound to loopback

`/etc/default/prometheus-alertmanager` pins `--web.listen-address=127.0.0.1:9093`.
It listens on all interfaces by default, and its API can list every firing
problem on the host and silence alerts — worth keeping off the network even
though ufw already blocks 9093.

## Testing the pipeline

An untested alert is worth as little as an untested backup. Post one by hand and
confirm it is actually delivered — SMTP accepting a message is not the same as a
mailbox receiving it:

```bash
curl -X POST http://127.0.0.1:9093/api/v2/alerts -H 'Content-Type: application/json' -d '[{
  "labels": {"alertname":"PipelineTest","severity":"critical","service":"onion-spider"},
  "annotations": {"summary":"test"},
  "startsAt": "'$(date -u +%Y-%m-%dT%H:%M:%S.000Z)'"
}]'

sudo amtool --alertmanager.url=http://127.0.0.1:9093 alert query
sudo journalctl -u prometheus-alertmanager -n 20 | grep -i notify
```

Verified 2026-07-21, end to end into the mailbox
(`postfix/lmtp ... status=sent (250 2.0.0 ... Saved)`).

Resolve it afterwards by re-posting the same labels with an `endsAt` in the past.

## Recipient

Alerts go to `postmaster@micutu.com`.

The first attempt used `micu@micutu.com` — the value of `ADMIN_EMAIL` in the
app's config — and was rejected with `550 5.1.1 ... User unknown in virtual
mailbox table`. That address is not a mailbox. (`ADMIN_EMAIL` is only compared
against a registering user's address for the admin bootstrap, so the app itself
never sends mail there and is unaffected.)

**This only works if somebody reads that mailbox.** The failure being corrected
here is a problem going unnoticed for two months; routing alerts to an inbox
nobody opens reproduces it exactly. If `postmaster@` is not somewhere you look,
change `to:` in `alertmanager.yml` to an address you actually read.
