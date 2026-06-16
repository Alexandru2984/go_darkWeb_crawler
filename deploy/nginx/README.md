# nginx (production)

Version-controlled copy of the live config for `go.micutu.com`. The live file
is `/etc/nginx/sites-enabled/onion_spider`.

## Files

- `onion_spider.conf` — the site config (TLS, security headers, API proxy).
- `snippets/cloudflare-realip.conf` — restores the real client IP when traffic
  is proxied through Cloudflare. **Why it matters:** the Go backend trusts
  `X-Real-IP` for per-IP rate limiting, login lockout and the audit log. Behind
  Cloudflare without this, every request is attributed to a Cloudflare edge IP,
  which both enables rate-limit bypass/sharing and causes false lockouts.

## Apply

```bash
sudo cp deploy/nginx/snippets/cloudflare-realip.conf /etc/nginx/snippets/
sudo cp deploy/nginx/onion_spider.conf /etc/nginx/sites-available/onion_spider
# (sites-enabled/onion_spider is typically a symlink to sites-available)

sudo nginx -t          # validate before reloading
sudo systemctl reload nginx
```

## Verify the real IP fix

After reload, hit the site through Cloudflare and confirm the audit log /
access log shows real client IPs (not 104.x / 172.64.x Cloudflare ranges).

If the orange cloud (CF proxy) is **off** for this hostname, the snippet is a
harmless no-op — `set_real_ip_from` only rewrites IPs for connections coming
from the listed ranges.

## Refreshing Cloudflare ranges

Cloudflare adds prefixes over time. Refresh from <https://www.cloudflare.com/ips/>
(a monthly cron that regenerates `cloudflare-realip.conf` and reloads nginx is
ideal).
