#!/usr/bin/env python3
"""Initialize or rotate a private TOTP keyring without displaying key material."""

import argparse
import base64
import datetime
import fcntl
import json
import os
from pathlib import Path
import re
import secrets
import tempfile


def read_keyring(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o007:
        raise ValueError("Keyring must be a private regular file")
    with path.open("rb") as source:
        raw = source.read(65537)
    if len(raw) > 65536:
        raise ValueError("Keyring exceeds size limit")
    data = json.loads(raw)
    if not isinstance(data, dict) or set(data) != {"format_version", "active", "keys"}:
        raise ValueError("Invalid keyring")
    keys = data["keys"]
    if (type(data["format_version"]) is not int or data["format_version"] != 1 or not isinstance(keys, dict)
            or not 1 <= len(keys) <= 16 or data["active"] not in keys):
        raise ValueError("Invalid keyring")
    for name, value in keys.items():
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,32}", name) or len(base64.b64decode(value, validate=True)) != 32:
            raise ValueError("Invalid encryption key")
    return data


def update(path, action, group=None):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if path.parent.stat().st_mode & 0o022:
        raise ValueError("Keyring directory must not be group/world writable")
    with (path.parent / ".totp-keyring.lock").open("a") as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        if action == "init":
            if path.exists() or path.is_symlink():
                raise ValueError("Existing keyring will not be overwritten")
            data = {"format_version": 1, "keys": {}}
        else:
            data = read_keyring(path)
            if len(data["keys"]) >= 16:
                raise ValueError("Retain old backup keys outside this file before further rotation")
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        name = "k" + stamp + "-" + secrets.token_hex(4)
        data["keys"][name] = base64.b64encode(secrets.token_bytes(32)).decode()
        data["active"] = name
        previous = path.stat() if path.exists() else None
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as output:
                temporary = Path(output.name)
                json.dump(data, output, sort_keys=True)
                output.write("\n")
                output.flush()
                os.fsync(output.fileno())
            owner = previous.st_uid if previous else os.getuid()
            target_group = group if group is not None else (previous.st_gid if previous else os.getgid())
            os.chown(temporary, owner, target_group)
            mode = 0o640 if group is not None or (previous and previous.st_mode & 0o040) else 0o600
            temporary.chmod(mode)
            if action == "init":
                os.link(temporary, path)
            else:
                os.replace(temporary, path)
            directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
        return len(data["keys"])


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("init", "rotate"))
    parser.add_argument("--path", type=Path, default=Path("/etc/onion-spider/totp-keyring.json"))
    parser.add_argument("--group", type=int, help="Optional reader group, for local Docker development")
    args = parser.parse_args()
    try:
        count = update(args.path, args.action, args.group)
    except (OSError, ValueError, KeyError, TypeError):
        parser.exit(1, "totp keys: operation failed; key material was not displayed\n")
    print(f"totp keys: {args.action} complete, {count} keys retained; back up before restarting the API")


if __name__ == "__main__":
    main()
