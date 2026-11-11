#!/usr/bin/env python3
"""Publish encrypted backups and verify a Google Drive restore in isolation."""

import argparse
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import uuid

SNAPSHOT_NAME = re.compile(r"onion_spider-\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.sql\.gz\.gpg")
CONTAINER_NAME = re.compile(r"onion-spider-restore-[a-f0-9]{12}")
RESTORE_LABEL = "org.onion-spider.restore-check"
RESTORE_MAX_BYTES = 2 * 1024 ** 3
RCLONE_OPTIONS = [
    "--contimeout", "10s", "--timeout", "2m",
    "--retries", "3", "--low-level-retries", "5", "--log-level", "ERROR",
]


def utc_now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def atomic_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as output:
            temporary = Path(output.name)
            json.dump(value, output, sort_keys=True)
            output.write("\n")
        os.replace(temporary, path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def file_digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def rclone(*args, capture=False):
    result = subprocess.run(
        ["rclone", *args, *RCLONE_OPTIONS],
        check=True, text=True, stdout=subprocess.PIPE if capture else None,
        timeout=25 * 60,
    )
    return result.stdout


def remote_path():
    remote = os.environ.get("BACKUP_REMOTE", "")
    name, separator, path = remote.partition(":")
    parts = path.strip("/").split("/")
    if (not separator or not re.fullmatch(r"[A-Za-z0-9_.-]+", name)
            or not path.strip("/") or any(part in (".", "..", "") for part in parts)):
        raise ValueError("BACKUP_REMOTE must name a remote and a dedicated directory")
    return remote.rstrip("/")


def state_directory():
    path = Path(os.environ.get("BACKUP_STATE_DIR", "/var/lib/onion-spider-backup"))
    if not path.is_absolute() or path == Path("/"):
        raise ValueError("BACKUP_STATE_DIR must be an absolute application directory")
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    return path


def publish(latest_name):
    """The backup.sh caller holds its backup lock throughout publication."""
    remote = remote_path()
    directory = Path(os.environ.get("BACKUP_OUTPUT_DIR", "/home/micu/onion-spider-backups"))
    retention = int(os.environ.get("BACKUP_REMOTE_RETENTION", "30"))
    if not 1 <= retention <= 365:
        raise ValueError("BACKUP_REMOTE_RETENTION must be between 1 and 365")
    if not SNAPSHOT_NAME.fullmatch(latest_name):
        raise ValueError("Invalid snapshot filename")
    sources = sorted(
        item.name for item in directory.iterdir()
        if SNAPSHOT_NAME.fullmatch(item.name) and item.is_file() and not item.is_symlink()
    )
    if latest_name not in sources:
        raise ValueError("The new snapshot is missing from the backup directory")

    state = state_directory()
    with tempfile.TemporaryDirectory(prefix="onion-spider-publish-") as work:
        files = Path(work) / "files.txt"
        files.write_text("\n".join(sources) + "\n")
        rclone("copy", str(directory), remote, "--files-from", str(files), "--immutable")
        # Download comparison works with both ordinary and crypt remotes.
        # All bytes being compared are still the GPG-encrypted archive.
        rclone("check", str(directory), remote, "--files-from", str(files),
               "--download", "--one-way", "--checkers", "2")
        latest = directory / latest_name
        metadata = {
            "format_version": 1,
            "filename": latest_name,
            "sha256": file_digest(latest),
            "size_bytes": latest.stat().st_size,
            "verified_at": utc_now(),
        }
        marker = Path(work) / "latest.json"
        atomic_json(marker, metadata)
        rclone("copyto", str(marker), remote + "/latest.json")
        if json.loads(rclone("cat", remote + "/latest.json", "--head", "8192",
                             capture=True)) != metadata:
            raise ValueError("Remote recovery metadata failed read-back verification")

        listing = rclone("lsf", remote, "--files-only", "--max-depth", "1", capture=True)
        snapshots = [name for name in listing.splitlines() if SNAPSHOT_NAME.fullmatch(name)]
        if latest_name not in snapshots or len(snapshots) != len(set(snapshots)):
            raise ValueError("Remote snapshot listing is incomplete or ambiguous")
        others = sorted((name for name in snapshots if name != latest_name), reverse=True)
        keep = {latest_name, *others[:retention - 1]}
        for name in sorted(set(snapshots) - keep):
            rclone("deletefile", remote + "/" + name)

    atomic_json(state / "offsite-success.json", {
        "completed_at": utc_now(), "remote": remote,
        "latest": metadata, "retained_snapshots": len(keep),
    })
    print(f"backup: offsite verified, {len(keep)} snapshots retained", flush=True)


def cleanup_container(state):
    """Also used by systemd after a timeout or a killed restore process."""
    marker = state / "restore-container.json"
    if not marker.exists():
        return
    name = json.loads(marker.read_text())["name"]
    if not CONTAINER_NAME.fullmatch(name):
        raise ValueError("Invalid restore container name")
    present = subprocess.run(
        ["docker", "ps", "--all", "--filter", "name=" + name, "--format", "{{.Names}}"],
        check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=30,
    )
    if name in present.stdout.splitlines():
        inspected = subprocess.run(
            ["docker", "inspect", "--format",
             '{{index .Config.Labels "' + RESTORE_LABEL + '"}}', name],
            check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=30,
        )
        if inspected.stdout.strip() != "true":
            raise ValueError("Container is missing the restore-check ownership label")
        subprocess.run(["docker", "rm", "--force", name], check=True,
                       stdout=subprocess.DEVNULL, timeout=30)
    marker.unlink()


def cleanup_idle_container(state):
    with (state / "restore-check.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        cleanup_container(state)


def restore_sql(container, archive, passphrase, work):
    command = [
        "gpg", "--homedir", str(work / "gnupg"), "--no-options", "--batch",
        "--pinentry-mode", "loopback", "--no-symkey-cache",
        "--passphrase-file", str(passphrase), "--decrypt", str(archive),
    ]
    (work / "gnupg").mkdir(mode=0o700)
    processes = []
    # Diagnostics can contain SQL fragments. Keep them inside the private
    # temporary directory and emit a generic failure to the service journal.
    with (work / "restore-errors.log").open("wb") as errors:
        try:
            decrypted = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=errors)
            processes.append(decrypted)
            unpacked = subprocess.Popen(
                ["gzip", "--decompress"], stdin=decrypted.stdout,
                stdout=subprocess.PIPE, stderr=errors,
            )
            processes.append(unpacked)
            decrypted.stdout.close()
            restored = subprocess.Popen(
                ["docker", "exec", "-i", container, "psql", "-X", "--quiet",
                 "--set", "ON_ERROR_STOP=1", "--set", "VERBOSITY=sqlstate",
                 "-U", "restore", "-d", "onion_spider_restore"],
                stdin=unpacked.stdout, stdout=subprocess.DEVNULL, stderr=errors,
            )
            processes.append(restored)
            unpacked.stdout.close()
            codes = [process.wait(timeout=15 * 60) for process in reversed(processes)]
            if any(code != 0 for code in codes):
                errors.flush()
                diagnostic = (work / "restore-errors.log").read_text(errors="replace")
                state = re.search(r"ERROR:\s+([A-Z0-9]{5})\s*$", diagnostic, re.MULTILINE)
                sqlstate = state.group(1) if state else "unavailable"
                print(f"backup: stream failure (psql={codes[0]}, gzip={codes[1]}, gpg={codes[2]}, SQLSTATE={sqlstate})", flush=True)
                raise RuntimeError("Archive decryption, decompression or SQL restore failed")
        finally:
            for process in reversed(processes):
                if process.poll() is None:
                    process.kill()
                process.wait()


def check_restore():
    remote = remote_path()
    state = state_directory()
    passphrase = Path(os.environ.get(
        "BACKUP_PASSPHRASE_FILE",
        str(Path(os.environ.get("CREDENTIALS_DIRECTORY", "/nonexistent")) / "backup-passphrase"),
    ))
    if not passphrase.is_file():
        raise ValueError("The backup passphrase credential is unavailable")
    metadata = json.loads(rclone("cat", remote + "/latest.json", "--head", "8192", capture=True))
    if not isinstance(metadata, dict):
        raise ValueError("Invalid remote recovery metadata")
    name = metadata.get("filename", "")
    digest = metadata.get("sha256", "")
    if (metadata.get("format_version") != 1 or not isinstance(name, str)
            or not SNAPSHOT_NAME.fullmatch(name) or not isinstance(digest, str)
            or not re.fullmatch(r"[a-f0-9]{64}", digest)
            or type(metadata.get("size_bytes")) is not int
            or not 0 < metadata["size_bytes"] <= RESTORE_MAX_BYTES):
        raise ValueError("Invalid remote recovery metadata")

    started = time.monotonic()
    with (state / "restore-check.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        cleanup_container(state)
        with tempfile.TemporaryDirectory(prefix="onion-spider-restore-") as temporary:
            work = Path(temporary)
            archive = work / name
            rclone("copyto", remote + "/" + name, str(archive), "--max-transfer", "2G")
            if (archive.stat().st_size != metadata["size_bytes"]
                    or file_digest(archive) != metadata["sha256"]):
                raise ValueError("Downloaded archive failed size or SHA-256 verification")

            container = "onion-spider-restore-" + uuid.uuid4().hex[:12]
            atomic_json(state / "restore-container.json", {"name": container})
            try:
                subprocess.run([
                    "docker", "run", "--detach", "--rm", "--name", container,
                    "--label", RESTORE_LABEL + "=true",
                    "--network", "none", "--read-only", "--user", "postgres",
                    "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
                    "--memory", "2g", "--cpus", "2", "--pids-limit", "256",
                    "--tmpfs", "/var/lib/postgresql:rw,nosuid,nodev,noexec,size=2g,mode=1777",
                    "--tmpfs", "/var/run/postgresql:rw,nosuid,nodev,noexec,size=16m,mode=1777",
                    "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m,mode=1777",
                    "-e", "POSTGRES_HOST_AUTH_METHOD=trust",
                    "-e", "POSTGRES_USER=restore", "-e", "POSTGRES_DB=onion_spider_restore",
                    os.environ.get("BACKUP_RESTORE_IMAGE", "postgres:18.6-alpine"),
                ], check=True, stdout=subprocess.DEVNULL, timeout=90)

                for _ in range(60):
                    ready = subprocess.run(
                        ["docker", "exec", container, "pg_isready", "-h", "127.0.0.1", "-U", "restore",
                         "-d", "onion_spider_restore"],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10,
                    )
                    if ready.returncode == 0:
                        break
                    time.sleep(1)
                else:
                    raise RuntimeError("The isolated restore database did not become ready")

                restore_sql(container, archive, passphrase, work)
                query = """
                    SELECT json_build_object(
                        'schema_version', (SELECT version FROM schema_migrations),
                        'schema_dirty', (SELECT dirty FROM schema_migrations),
                        'users', (SELECT count(*) FROM users),
                        'nodes', (SELECT count(*) FROM nodes),
                        'edges', (SELECT count(*) FROM edges),
                        'sessions', (SELECT count(*) FROM sessions),
                        'notes', (SELECT count(*) FROM node_notes),
                        'watches', (SELECT count(*) FROM watches),
                        'unvalidated_constraints', (SELECT count(*) FROM pg_constraint
                                                   WHERE NOT convalidated));
                """
                checked = subprocess.run(
                    ["docker", "exec", container, "psql", "-X", "--tuples-only",
                     "--no-align", "--set", "ON_ERROR_STOP=1", "-U", "restore",
                     "-d", "onion_spider_restore", "-c", query],
                    check=True, text=True, stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL, timeout=60,
                )
                result = json.loads(checked.stdout)
                if (result["schema_dirty"] is not False
                        or type(result["schema_version"]) is not int
                        or result["schema_version"] < 1 or result["unvalidated_constraints"] != 0):
                    raise ValueError("Restored schema or constraints failed validation")
                result.update({
                    "completed_at": utc_now(), "remote": remote, "filename": name,
                    "sha256": metadata["sha256"], "elapsed_seconds": round(time.monotonic() - started, 2),
                })
            finally:
                cleanup_container(state)
            atomic_json(state / "restore-success.json", result)
            print("backup: remote archive restored and schema validated in isolation", flush=True)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    upload = commands.add_parser("upload")
    upload.add_argument("filename")
    commands.add_parser("restore-check")
    commands.add_parser("cleanup")
    args = parser.parse_args()
    try:
        if args.command == "upload":
            publish(args.filename)
        elif args.command == "restore-check":
            check_restore()
        else:
            cleanup_idle_container(state_directory())
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        # Exception text can include transport diagnostics. Report only its type.
        print(f"backup: {args.command} failed ({type(error).__name__})", flush=True)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
