#!/usr/bin/env python3
"""Expose bounded operational metrics for Onion Spider on a private listener."""

import datetime
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import time
import urllib.error
import urllib.request

SERVICES = ("onion-spider-backup.service", "onion-spider-restore-check.service")
TIMERS = ("onion-spider-backup.timer", "onion-spider-restore-check.timer")


def read_backup_state(directory, kind, now):
    filename = "offsite-success.json" if kind == "offsite" else "restore-success.json"
    try:
        with (directory / filename).open("rb") as source:
            raw = source.read(65537)
        if len(raw) > 65536:
            raise ValueError("State exceeds size limit")
        data = json.loads(raw)
        stamp = datetime.datetime.fromisoformat(data["completed_at"])
        timestamp = stamp.timestamp()
        if stamp.tzinfo is None or not 0 < timestamp <= now + 300:
            raise ValueError("Invalid completion time")
        if kind == "offsite":
            latest = data["latest"]
            size = latest["size_bytes"]
            if latest["format_version"] != 1 or type(size) is not int or size <= 0:
                raise ValueError("Invalid offsite record")
        else:
            size = 0
            if (data["schema_dirty"] is not False or data["unvalidated_constraints"] != 0
                    or type(data["schema_version"]) is not int or data["schema_version"] < 1):
                raise ValueError("Invalid restore record")
        return 1, timestamp, size
    except (OSError, ValueError, KeyError, TypeError, OverflowError):
        return 0, 0, 0


def read_units():
    executable = shutil.which("systemctl")
    if executable is None:
        raise OSError("systemctl unavailable")
    result = subprocess.run(
        [executable, "show", "--no-pager", "--no-ask-password",
         "--property=Id,LoadState,ActiveState,Result,ExecMainStatus,UnitFileState",
         *SERVICES, *TIMERS],
        check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        timeout=3,
    )
    units = {}
    for block in result.stdout.strip().split("\n\n"):
        values = dict(line.split("=", 1) for line in block.splitlines() if "=" in line)
        if values.get("Id") in (*SERVICES, *TIMERS):
            units[values["Id"]] = values
    if len(units) != 4 or any(item.get("LoadState") != "loaded" for item in units.values()):
        raise ValueError("Expected backup units are unavailable")
    return units


def collect_metrics():
    now = time.time()
    samples = [f"onionspider_monitor_collected_timestamp_seconds {now}"]
    directory = Path(os.environ.get("MONITOR_BACKUP_STATE_DIR", "/var/lib/onion-spider-backup"))
    for kind in ("offsite", "restore"):
        valid, stamp, size = read_backup_state(directory, kind, now)
        samples.extend([
            f'onionspider_backup_state_valid{{kind="{kind}"}} {valid}',
            f'onionspider_backup_last_success_timestamp_seconds{{kind="{kind}"}} {stamp}',
        ])
        if kind == "offsite":
            samples.append(f"onionspider_backup_archive_bytes {size}")

    try:
        units = read_units()
        samples.append("onionspider_backup_units_readable 1")
        for name in SERVICES:
            values = units[name]
            failed = values.get("Result") not in ("", "success") or values.get("ExecMainStatus") != "0"
            samples.append(f'onionspider_backup_unit_failed{{unit="{name}"}} {int(failed)}')
        for name in TIMERS:
            values = units[name]
            enabled = (values.get("UnitFileState") in ("enabled", "enabled-runtime")
                       and values.get("ActiveState") == "active")
            samples.append(f'onionspider_backup_timer_enabled{{unit="{name}"}} {int(enabled)}')
    except (OSError, ValueError, subprocess.SubprocessError):
        samples.append("onionspider_backup_units_readable 0")

    try:
        # This destination is fixed. Collector requests never follow redirects.
        request = urllib.request.Request("http://127.0.0.1:8900/readyz")
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        with opener.open(request, timeout=3) as response:
            ready = response.status == 200 and response.read(16) == b"ready"
    except (OSError, urllib.error.URLError):
        ready = False
    samples.append(f"onionspider_api_ready {int(ready)}")

    space = os.statvfs("/")
    samples.extend([
        f"onionspider_host_disk_available_bytes {space.f_bavail * space.f_frsize}",
        f"onionspider_host_disk_size_bytes {space.f_blocks * space.f_frsize}",
    ])
    return ("\n".join(samples) + "\n").encode("ascii")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, new_url):
        return None


class MetricsHandler(BaseHTTPRequestHandler):
    def setup(self):
        super().setup()
        self.connection.settimeout(5)

    def log_message(self, format_string, *args):
        # Request paths and peer addresses are unnecessary for operational metrics.
        pass

    def reply(self, status, body, content_type="text/plain; charset=utf-8"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            self.reply(200, b"ok\n")
            return
        if self.path != "/metrics":
            self.reply(404, b"Not found\n")
            return
        scheme, _, token = self.headers.get("Authorization", "").partition(" ")
        if scheme.lower() != "bearer" or not hmac.compare_digest(
                token.encode("utf-8"), self.server.token):
            self.reply(401, b"Unauthorized\n")
            return
        try:
            body = self.server.collector()
        except (OSError, ValueError, subprocess.SubprocessError):
            self.reply(503, b"Metric collection failed\n")
            return
        self.reply(200, body, "text/plain; version=0.0.4; charset=utf-8")


class MetricsServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, token, collector=collect_metrics):
        self.token = token
        self.collector = collector
        super().__init__(address, MetricsHandler)

    def handle_error(self, request, client_address):
        print("monitoring: request failed", flush=True)


def main():
    credential_directory = os.environ.get("CREDENTIALS_DIRECTORY", "/nonexistent")
    token_file = Path(credential_directory) / "metrics-token"
    token = token_file.read_bytes().strip()
    if len(token) < 32:
        raise ValueError("Metrics credential is missing or too short")
    address = os.environ.get("MONITOR_BIND_ADDRESS", "127.0.0.1")
    port = int(os.environ.get("MONITOR_PORT", "19190"))
    with MetricsServer((address, port), token) as server:
        server.serve_forever()


if __name__ == "__main__":
    main()
