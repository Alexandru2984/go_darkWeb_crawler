"""Tests for the private operational collector use local fixtures and HTTP."""

import datetime
import http.client
import json
import os
from pathlib import Path
import secrets
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch

import monitoring_exporter as monitor


class MonitoringTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="onion-spider-monitor-test-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.now = int(time.time())
        self.stamp = datetime.datetime.fromtimestamp(
            self.now, datetime.timezone.utc,
        ).isoformat()

    def write_state(self, kind, **extra):
        data = {"completed_at": self.stamp, **extra}
        (self.directory / f"{kind}-success.json").write_text(json.dumps(data))

    def units(self):
        result = {}
        for name in (*monitor.SERVICES, *monitor.TIMERS):
            result[name] = {
                "Id": name, "LoadState": "loaded", "Result": "success",
                "ExecMainStatus": "0", "ActiveState": "active" if name in monitor.TIMERS else "inactive",
                "UnitFileState": "enabled" if name in monitor.TIMERS else "static",
            }
        return result

    def test_successful_records_publish_timestamps(self):
        self.write_state("offsite", latest={"format_version": 1, "size_bytes": 1024})
        self.write_state("restore", schema_dirty=False, unvalidated_constraints=0, schema_version=8)
        self.assertEqual(monitor.read_backup_state(self.directory, "offsite", self.now), (1, self.now, 1024))
        self.assertEqual(monitor.read_backup_state(self.directory, "restore", self.now), (1, self.now, 0))

    def test_missing_corrupt_and_future_records_are_invalid(self):
        self.assertEqual(monitor.read_backup_state(self.directory, "offsite", self.now), (0, 0, 0))
        path = self.directory / "offsite-success.json"
        for value in (b"{", b"[]" , b"x" * 65537):
            path.write_bytes(value)
            self.assertEqual(monitor.read_backup_state(self.directory, "offsite", self.now), (0, 0, 0))
        self.stamp = datetime.datetime.fromtimestamp(
            self.now + 3600, datetime.timezone.utc,
        ).isoformat()
        self.write_state("offsite", latest={"format_version": 1, "size_bytes": 100})
        self.assertEqual(monitor.read_backup_state(self.directory, "offsite", self.now), (0, 0, 0))

    def test_failed_restore_does_not_look_valid(self):
        self.write_state("restore", schema_dirty=True, unvalidated_constraints=0, schema_version=8)
        self.assertEqual(monitor.read_backup_state(self.directory, "restore", self.now), (0, 0, 0))

    def test_metrics_show_failed_jobs_and_disabled_timers_without_private_fields(self):
        private = "private-address@example.invalid"
        self.write_state("offsite", latest={"format_version": 1, "size_bytes": 10, "filename": private})
        self.write_state("restore", schema_dirty=False, unvalidated_constraints=0, schema_version=8)
        units = self.units()
        units[monitor.SERVICES[0]].update(Result="exit-code", ExecMainStatus="1")
        units[monitor.TIMERS[1]].update(UnitFileState="disabled", ActiveState="inactive")
        with patch.dict(os.environ, {"MONITOR_BACKUP_STATE_DIR": str(self.directory)}):
            with patch.object(monitor, "read_units", return_value=units):
                with patch.object(monitor.urllib.request, "build_opener", side_effect=OSError("offline")):
                    body = monitor.collect_metrics().decode()
        self.assertIn(f'onionspider_backup_unit_failed{{unit="{monitor.SERVICES[0]}"}} 1', body)
        self.assertIn(f'onionspider_backup_timer_enabled{{unit="{monitor.TIMERS[1]}"}} 0', body)
        self.assertIn("onionspider_api_ready 0", body)
        self.assertNotIn(private, body)
        self.assertNotIn(str(self.directory), body)

    def server(self, collector):
        token = secrets.token_hex(32).encode()
        server = monitor.MetricsServer(("127.0.0.1", 0), token, collector)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        connection = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=5)
        self.addCleanup(connection.close)
        return token, connection

    def test_unauthorized_requests_cannot_collect_metrics(self):
        collector = Mock(return_value=b"onionspider_test 1\n")
        _, connection = self.server(collector)
        connection.request("GET", "/metrics")
        response = connection.getresponse()
        self.assertEqual(response.status, 401)
        response.read()
        collector.assert_not_called()

    def test_authorized_scrape_succeeds_and_is_not_cacheable(self):
        collector = Mock(return_value=b"onionspider_test 1\n")
        token, connection = self.server(collector)
        connection.request("GET", "/metrics", headers={"Authorization": "Bearer " + token.decode()})
        response = connection.getresponse()
        self.assertEqual(response.status, 200)
        self.assertEqual(response.getheader("Cache-Control"), "no-store")
        self.assertEqual(response.read(), b"onionspider_test 1\n")
        collector.assert_called_once()

    def test_health_response_contains_no_operational_data(self):
        collector = Mock(return_value=b"private")
        _, connection = self.server(collector)
        connection.request("GET", "/healthz")
        response = connection.getresponse()
        self.assertEqual(response.status, 200)
        self.assertEqual(response.read(), b"ok\n")
        collector.assert_not_called()

    def test_collection_failure_is_visible_to_prometheus(self):
        collector = Mock(side_effect=OSError("private details"))
        token, connection = self.server(collector)
        connection.request("GET", "/metrics", headers={"Authorization": "Bearer " + token.decode()})
        response = connection.getresponse()
        self.assertEqual(response.status, 503)
        self.assertNotIn(b"private details", response.read())


if __name__ == "__main__":
    unittest.main()
