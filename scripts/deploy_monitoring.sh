#!/usr/bin/env bash
# Install only Onion Spider's private monitoring stack. Run from this checkout
# with sudo. Configuration validation precedes replacement of live files.
set -euo pipefail
umask 077

if (( EUID != 0 )); then
    echo 'monitoring: run this installer with sudo' >&2
    exit 1
fi
REPO="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
STATE=/var/lib/onion-spider-monitoring
CONFIG="$STATE/config"
SECRETS=/etc/onion-spider/monitoring
PROM_IMAGE=prom/prometheus:v3.16.0@sha256:e04987641b57704f408e31f6373ba4138ecf27cb4a165d24aabb6c38fddd0f06
AM_IMAGE=prom/alertmanager:v0.35.0@sha256:e638797febd034838eca8b98cdbd46f951e9bf7f178777aa8669167406a9946c
COMPOSE=(docker compose --env-file /dev/null -f "$REPO/deploy/prometheus/docker-compose.yml")

install -d -o root -g micu -m 0750 "$STATE" "$CONFIG"
install -d -o root -g root -m 0700 "$SECRETS"

# Parse the SMTP credential as data; never source the app environment, put a
# secret in argv, or print it. Existing credentials survive repeated installs.
python3 - "$REPO/backend/.env" "$SECRETS" <<'PY'
import grp
import os
from pathlib import Path
import secrets
import sys

directory = Path(sys.argv[2])
group = grp.getgrnam("micu").gr_gid
for name in ("metrics-token", "smtp-password"):
    path = directory / name
    if not path.exists():
        if name == "metrics-token":
            value = secrets.token_hex(32)
        else:
            values = {}
            for line in Path(sys.argv[1]).read_text().splitlines():
                key, separator, raw = line.partition("=")
                if separator:
                    value = raw.strip()
                    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
                        value = value[1:-1]
                    values[key.strip()] = value
            value = values.get("SMTP_PASS", "")
            if not value or "\n" in value or "\r" in value:
                sys.exit("monitoring: SMTP_PASS is missing or invalid")
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as output:
            output.write(value + "\n")
    if not path.is_file() or path.is_symlink():
        sys.exit("monitoring: credential must be a regular file")
    os.chown(path, 0, group)
    os.chmod(path, 0o640)
PY

STAGE="$(mktemp -d "$STATE/.stage.XXXXXX")"
chown root:micu "$STAGE"
chmod 0750 "$STAGE"
ROLLBACK=0
cleanup() {
    result=$?
    if (( result != 0 && ROLLBACK == 1 )); then
        echo 'monitoring: deployment failed; restoring previous configuration' >&2
        for name in prometheus.yml alertmanager.yml onion-spider-rules.yml; do
            if [[ -f "$STAGE/previous/$name" ]]; then
                install -o root -g micu -m 0640 "$STAGE/previous/$name" "$CONFIG/$name.next"
                mv -f "$CONFIG/$name.next" "$CONFIG/$name"
            fi
        done
        "${COMPOSE[@]}" kill --signal SIGHUP prometheus alertmanager >/dev/null 2>&1 || true
    fi
    rm -rf -- "$STAGE"
    exit "$result"
}
trap cleanup EXIT

install -o root -g micu -m 0640 "$REPO/deploy/prometheus/config/"*.yml "$STAGE/"
install -o root -g micu -m 0640 "$REPO/deploy/prometheus/onion-spider-rules.yml" "$STAGE/"
"${COMPOSE[@]}" config --quiet
docker run --rm --network none --read-only --user 65534:1001 --cap-drop ALL \
    --security-opt no-new-privileges:true --entrypoint /bin/promtool \
    -v "$STAGE:/etc/prometheus/config:ro" \
    -v "$SECRETS/metrics-token:/run/secrets/metrics-token:ro" \
    "$PROM_IMAGE" check config /etc/prometheus/config/prometheus.yml
docker run --rm --network none --read-only --user 65534:1001 --cap-drop ALL \
    --security-opt no-new-privileges:true --entrypoint /bin/amtool \
    -v "$STAGE:/etc/alertmanager/config:ro" \
    -v "$SECRETS/smtp-password:/run/secrets/smtp-password:ro" \
    "$AM_IMAGE" check-config /etc/alertmanager/config/alertmanager.yml
systemd-analyze verify "$REPO/deploy/systemd/onion-spider-monitoring-exporter.service"

install -d -m 0700 "$STAGE/previous"
for name in prometheus.yml alertmanager.yml onion-spider-rules.yml; do
    if [[ -f "$CONFIG/$name" ]]; then
        cp -p "$CONFIG/$name" "$STAGE/previous/$name"
    fi
done
ROLLBACK=1
for name in prometheus.yml alertmanager.yml onion-spider-rules.yml; do
    install -o root -g micu -m 0640 "$STAGE/$name" "$CONFIG/$name.next"
    mv -f "$CONFIG/$name.next" "$CONFIG/$name"
done
install -o root -g root -m 0644 "$REPO/deploy/systemd/onion-spider-monitoring-exporter.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable onion-spider-monitoring-exporter.service
systemctl restart onion-spider-monitoring-exporter.service
"${COMPOSE[@]}" up --detach --wait --wait-timeout 60

wait_ready() {
    local port="$1"
    for _ in {1..30}; do
        if curl --noproxy '*' --fail --silent --max-time 2 "http://127.0.0.1:$port/-/ready" >/dev/null; then
            return 0
        fi
        sleep 1
    done
    return 1
}
wait_ready 9090
wait_ready 9093
# Directory mounts observe atomic replacements; HUP also updates already
# running containers when image/Compose options did not change.
"${COMPOSE[@]}" kill --signal SIGHUP prometheus alertmanager
wait_ready 9090
wait_ready 9093
curl --noproxy '*' --fail --silent --max-time 3 http://127.0.0.1:19190/healthz >/dev/null
ROLLBACK=0
echo 'monitoring: private exporter, Prometheus and Alertmanager are ready'
