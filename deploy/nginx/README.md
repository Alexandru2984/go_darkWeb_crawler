# nginx (production)

Version-controlled copy of the live config for `go.micutu.com`. The live file
is `/etc/nginx/sites-enabled/onion_spider`.

## Files

- `onion_spider.conf` — the site config (TLS, security headers, API proxy).
  This repo **owns** this file; the live copy is a deploy target.
- `snippets/cloudflare-realip.conf` — restores the real client IP when traffic
  is proxied through Cloudflare. **Why it matters:** the Go backend trusts
  `X-Real-IP` for per-IP rate limiting, login lockout and the audit log. Behind
  Cloudflare without this, every request is attributed to a Cloudflare edge IP,
  which both enables rate-limit bypass/sharing and causes false lockouts.

  ⚠️ This repo does **not** own the snippet. `/etc/nginx/snippets/cloudflare-realip.conf`
  is host-wide (shared with other vhosts) and is regenerated from Cloudflare's
  published ranges by `update-cloudflare-ips.sh`, which lives outside this
  project. The copy here is a **read-only mirror for reference**. Copying it
  over the live file would freeze the ranges at whatever this repo last
  recorded, so a Cloudflare prefix added later would stop being trusted — and
  clients behind it would all collapse into one rate-limit bucket. Never
  `cp` this one outward; diff it instead.

## Apply

```bash
# Site config only — the repo owns this file.
sudo cp deploy/nginx/onion_spider.conf /etc/nginx/sites-enabled/onion_spider

sudo nginx -t          # validate before reloading
sudo systemctl reload nginx
```

To check whether the externally-managed snippet has drifted from the mirror
(comments and `real_ip_recursive` are expected to differ; the
`set_real_ip_from` set is what matters):

```bash
diff <(grep -oE 'set_real_ip_from [^;]+' /etc/nginx/snippets/cloudflare-realip.conf | sort) \
     <(grep -oE 'set_real_ip_from [^;]+' deploy/nginx/snippets/cloudflare-realip.conf | sort)
```

## Verify the real IP fix

After reload, hit the site through Cloudflare and confirm the audit log /
access log shows real client IPs (not 104.x / 172.64.x Cloudflare ranges).

If the orange cloud (CF proxy) is **off** for this hostname, the snippet is a
harmless no-op — `set_real_ip_from` only rewrites IPs for connections coming
from the listed ranges.

## Refreshing Cloudflare ranges

Handled outside this repo by `update-cloudflare-ips.sh` (see the warning above),
which regenerates the host-wide snippet from <https://www.cloudflare.com/ips/>.

This now runs weekly via `cloudflare-ips-update.timer` (units in
`deploy/systemd/`). It used to be hand-run, and by the time that was checked the
installed list had already drifted stale — which is the failure this schedule
exists to prevent. When the list is missing a prefix Cloudflare has added,
clients arriving through it keep the edge IP as `$remote_addr`, so per-IP rate
limiting and login lockout apply to the edge rather than the client and
unrelated users share a single bucket.

```bash
systemctl list-timers cloudflare-ips-update.timer      # when it next runs
sudo /home/micu/swift+vapor/scripts/update-cloudflare-ips.sh --check   # is it stale now?
```
