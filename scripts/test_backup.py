"""Regression tests use disposable files and a crypt remote on local disk."""

import contextlib
import io
import fcntl
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import uuid

import backup_offsite
import totp_keys

SCRIPT = Path(__file__).with_name("backup.sh")


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="onion-spider-backup-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "source"
        self.source.mkdir()
        self.remote = self.root / "remote"
        self.state = self.root / "state"
        self.config = self.root / "rclone.conf"
        password = subprocess.check_output(
            ["rclone", "obscure", uuid.uuid4().hex], text=True,
        ).strip()
        self.config.write_text(
            "[fixture]\ntype = crypt\nremote = " + str(self.remote)
            + "\npassword = " + password + "\nfilename_encryption = standard\n"
        )
        self.config.chmod(0o600)
        environment = {
            "RCLONE_CONFIG": str(self.config),
            "BACKUP_REMOTE": "fixture:onion-spider",
            "BACKUP_OUTPUT_DIR": str(self.source),
            "BACKUP_STATE_DIR": str(self.state),
            "BACKUP_REMOTE_RETENTION": "2",
        }
        self.environment = patch.dict(os.environ, environment)
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def snapshot(self, day):
        name = f"onion_spider-2026-11-{day:02d}_01-00-00.sql.gz.gpg"
        (self.source / name).write_bytes(os.urandom(128))
        return name

    def invoke_rclone(self, *args):
        return subprocess.check_output(
            ["rclone", *args, "--log-level", "ERROR"], text=True,
        )

    def publish(self, filename):
        with contextlib.redirect_stdout(io.StringIO()):
            backup_offsite.publish(filename)

    def test_upload_verifies_ciphertext_and_preserves_unrelated_files(self):
        oldest, middle, newest = [self.snapshot(day) for day in (1, 2, 3)]
        unrelated = self.root / "operator-note.txt"
        unrelated.write_text("keep this unrelated file")
        self.invoke_rclone("copyto", str(unrelated), "fixture:onion-spider/operator-note.txt")
        self.publish(newest)

        names = self.invoke_rclone("lsf", "fixture:onion-spider", "--files-only").splitlines()
        self.assertNotIn(oldest, names)
        self.assertIn(middle, names)
        self.assertIn(newest, names)
        self.assertIn("operator-note.txt", names)
        marker = json.loads(self.invoke_rclone("cat", "fixture:onion-spider/latest.json"))
        self.assertEqual(marker["filename"], newest)
        self.assertEqual(marker["sha256"], backup_offsite.file_digest(self.source / newest))
        self.assertTrue((self.state / "offsite-success.json").is_file())
        self.publish(newest)  # Retrying is safe after retention removed an older copy.
        self.assertEqual((self.source / oldest).stat().st_size, 128)

    def test_clock_rollback_keeps_the_verified_recovery_pointer(self):
        older, newer = [self.snapshot(day) for day in (1, 2)]
        with patch.dict(os.environ, {"BACKUP_REMOTE_RETENTION": "1"}):
            self.publish(older)
        names = self.invoke_rclone("lsf", "fixture:onion-spider", "--files-only").splitlines()
        self.assertIn(older, names)
        self.assertNotIn(newer, names)

    def test_keyring_companions_are_verified_and_rotated_with_their_databases(self):
        oldest, newest = [self.snapshot(day) for day in (1, 2)]
        for snapshot in (oldest, newest):
            (self.source / backup_offsite.keyring_filename(snapshot)).write_bytes(os.urandom(128))
        with patch.dict(os.environ, {"BACKUP_REMOTE_RETENTION": "1", "BACKUP_TOTP_KEYRING_REQUIRED": "true"}):
            self.publish(newest)
        names = self.invoke_rclone("lsf", "fixture:onion-spider", "--files-only").splitlines()
        self.assertNotIn(backup_offsite.keyring_filename(oldest), names)
        self.assertIn(backup_offsite.keyring_filename(newest), names)
        metadata = json.loads(self.invoke_rclone("cat", "fixture:onion-spider/latest.json"))
        self.assertEqual(metadata["keyring"]["filename"], backup_offsite.keyring_filename(newest))
        self.assertEqual(metadata["keyring"]["sha256"], backup_offsite.file_digest(self.source / metadata["keyring"]["filename"]))

    def test_missing_required_keyring_is_rejected_before_upload(self):
        with patch.dict(os.environ, {"BACKUP_TOTP_KEYRING_REQUIRED": "true"}):
            with patch.object(backup_offsite, "rclone") as command:
                with self.assertRaises(ValueError):
                    self.publish(self.snapshot(1))
                command.assert_not_called()

    def test_encrypted_restore_requires_its_keyring(self):
        completed = subprocess.CompletedProcess([], 0, stdout=json.dumps([
            {"user_id": 1, "value": "totp:v1:missing:invalid"},
        ]))
        with patch.object(backup_offsite.subprocess, "run", return_value=completed) as command:
            with self.assertRaises(ValueError):
                backup_offsite.verify_restored_totp("fixture-container", None, {})
            self.assertEqual(command.call_count, 1)

    def test_tampered_keyring_is_rejected_before_decryption(self):
        snapshot = self.snapshot(1)
        name = backup_offsite.keyring_filename(snapshot)
        (self.source / name).write_bytes(os.urandom(128))
        metadata = {"keyring": {"filename": name, "sha256": "0" * 64, "size_bytes": 128}}
        work = self.root / "restore"
        work.mkdir()
        def command(*args, **kwargs):
            shutil.copyfile(self.source / name, args[2])
        with patch.object(backup_offsite, "rclone", side_effect=command):
            with patch.object(backup_offsite.subprocess, "run") as decrypt:
                with self.assertRaises(ValueError):
                    backup_offsite.download_keyring(metadata, snapshot, "fixture:onion-spider", work, self.root / "passphrase")
                decrypt.assert_not_called()

    def test_failed_readback_does_not_rotate_remote_backups_or_publish_success(self):
        filename = self.snapshot(1)
        commands = []

        def command(*args, **kwargs):
            commands.append(args)
            if args[0] == "check":
                raise subprocess.CalledProcessError(1, "rclone")

        with patch.object(backup_offsite, "rclone", side_effect=command):
            with self.assertRaises(subprocess.CalledProcessError):
                self.publish(filename)
        self.assertEqual([command[0] for command in commands], ["copy", "check"])
        self.assertFalse((self.state / "offsite-success.json").exists())

    def test_unsafe_destinations_are_rejected_before_rclone(self):
        for remote in ("fixture:", "fixture:/", "fixture:onion-spider/..", "fixture:../other", "/tmp/local"):
            with self.subTest(remote=remote), patch.dict(os.environ, {"BACKUP_REMOTE": remote}):
                with patch.object(backup_offsite, "rclone") as command:
                    with self.assertRaises(ValueError):
                        self.publish(self.snapshot(1))
                    command.assert_not_called()

    def test_tampered_download_is_rejected_before_starting_a_container(self):
        filename = self.snapshot(1)
        passphrase = self.root / "passphrase"
        passphrase.write_text(uuid.uuid4().hex)
        metadata = {"format_version": 1, "filename": filename, "sha256": "0" * 64, "size_bytes": 128}

        def command(*args, **kwargs):
            if args[0] == "cat":
                return json.dumps(metadata)
            if args[0] == "copyto":
                shutil.copyfile(self.source / filename, args[2])

        with patch.dict(os.environ, {"BACKUP_PASSPHRASE_FILE": str(passphrase)}):
            with patch.object(backup_offsite, "rclone", side_effect=command):
                with patch.object(backup_offsite.subprocess, "run") as run:
                    with self.assertRaises(ValueError):
                        backup_offsite.check_restore()
                    run.assert_not_called()

    def test_cleanup_preserves_an_active_restore(self):
        self.state.mkdir()
        marker = self.state / "restore-container.json"
        marker.write_text(json.dumps({"name": "onion-spider-restore-" + "a" * 12}))
        with (self.state / "restore-check.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with patch.object(backup_offsite.subprocess, "run") as run:
                backup_offsite.cleanup_idle_container(self.state)
                run.assert_not_called()
        self.assertTrue(marker.exists())

    def test_docker_failure_preserves_the_cleanup_marker(self):
        self.state.mkdir()
        marker = self.state / "restore-container.json"
        marker.write_text(json.dumps({"name": "onion-spider-restore-" + "b" * 12}))
        with patch.object(backup_offsite.subprocess, "run",
                          side_effect=subprocess.CalledProcessError(1, "docker")):
            with self.assertRaises(subprocess.CalledProcessError):
                backup_offsite.cleanup_idle_container(self.state)
        self.assertTrue(marker.exists())

    def test_failed_status_write_preserves_the_previous_record(self):
        destination = self.root / "status.json"
        destination.write_text('{"completed_at":"previous"}')
        with patch.object(backup_offsite.json, "dump", side_effect=OSError("disk full")):
            with self.assertRaises(OSError):
                backup_offsite.atomic_json(destination, {"completed_at": "new"})
        self.assertEqual(json.loads(destination.read_text())["completed_at"], "previous")
        self.assertEqual(set(self.root.glob("tmp*")), set())

    def test_restore_cleanup_failure_does_not_publish_success(self):
        filename = self.snapshot(1)
        passphrase = self.root / "passphrase"
        passphrase.write_text(uuid.uuid4().hex)
        metadata = {
            "format_version": 1, "filename": filename,
            "sha256": backup_offsite.file_digest(self.source / filename), "size_bytes": 128,
        }

        def command(*args, **kwargs):
            if args[0] == "cat":
                return json.dumps(metadata)
            if args[0] == "copyto":
                shutil.copyfile(self.source / filename, args[2])

        restored = {
            "schema_dirty": False, "schema_version": 8, "unvalidated_constraints": 0,
        }
        def run(*args, **kwargs):
            output = [] if "json_agg" in args[0][-1] else restored
            return subprocess.CompletedProcess([], 0, stdout=json.dumps(output))
        with patch.dict(os.environ, {"BACKUP_PASSPHRASE_FILE": str(passphrase)}):
            with patch.object(backup_offsite, "rclone", side_effect=command):
                with patch.object(backup_offsite.subprocess, "run", side_effect=run):
                    with patch.object(backup_offsite, "restore_sql"):
                        with patch.object(backup_offsite, "cleanup_container",
                                          side_effect=[None, RuntimeError("cleanup failed")]):
                            with self.assertRaises(RuntimeError):
                                backup_offsite.check_restore()
        self.assertFalse((self.state / "restore-success.json").exists())

    def test_connection_credentials_stay_out_of_subprocess_arguments(self):
        tools = self.root / "tools"
        tools.mkdir()
        trace = self.root / "python-arguments.json"
        password = uuid.uuid4().hex
        environment_file = self.root / "database.env"
        environment_file.write_text(
            f"DATABASE_URL=postgres://fixture:{password}@localhost:5432/fixture?sslmode=disable\n"
        )
        passphrase = self.root / "passphrase"
        passphrase.write_text(uuid.uuid4().hex + "\n")
        passphrase.chmod(0o600)
        keyring = self.root / "keyring.json"
        totp_keys.update(keyring, "init")

        python = tools / "python3"
        python.write_text(
            "#!/usr/bin/env python-placeholder\n"
            "import json,os,sys\n"
            "from pathlib import Path\n"
            "Path(os.environ['ARGUMENT_TRACE']).write_text(json.dumps(sys.argv[1:]))\n"
            "os.execv(os.environ['REAL_PYTHON'], [os.environ['REAL_PYTHON'], *sys.argv[1:]])\n"
        )
        real_python = shutil.which("python3")
        python.write_text(python.read_text().replace(
            "#!/usr/bin/env python-placeholder", "#!" + real_python, 1,
        ))
        python.chmod(0o700)
        pg_dump = tools / "pg_dump"
        pg_dump.write_text(
            "#!/usr/bin/env bash\n"
            "set -eu\n"
            'test "$PGPASSWORD" = "$EXPECTED_PASSWORD"\n'
            'test "$PGSSLMODE" = disable\n'
            "printf 'SELECT 1;\\n'\n"
        )
        pg_dump.chmod(0o700)
        result = subprocess.run(
            ["bash", str(SCRIPT)], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env={
                **os.environ, "PATH": str(tools) + os.pathsep + os.environ["PATH"],
                "BACKUP_REMOTE": "", "BACKUP_ENV_FILE": str(environment_file),
                "BACKUP_PASSPHRASE_FILE": str(passphrase),
                "BACKUP_TOTP_KEYRING_FILE": str(keyring), "BACKUP_TOTP_KEYRING_REQUIRED": "true",
                "EXPECTED_PASSWORD": password, "REAL_PYTHON": real_python,
                "ARGUMENT_TRACE": str(trace),
            },
            timeout=60,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        arguments = json.loads(trace.read_text())
        self.assertEqual(arguments, ["-", str(environment_file)])
        self.assertNotIn(password, "\n".join(arguments))
        archives = list(self.source.glob("*.sql.gz.gpg"))
        self.assertEqual(len(archives), 1)
        self.assertEqual(archives[0].stat().st_mode & 0o777, 0o600)
        keyring_archives = list(self.source.glob("onion_spider-keyring-*.json.gpg"))
        self.assertEqual(len(keyring_archives), 1)
        self.assertEqual(keyring_archives[0].stat().st_mode & 0o777, 0o600)
        self.assertNotIn(keyring.read_bytes(), keyring_archives[0].read_bytes())


if __name__ == "__main__":
    unittest.main()
