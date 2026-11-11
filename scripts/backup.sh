#!/usr/bin/env bash
#
# Daily PostgreSQL backup for Onion Spider.
#
# Streams pg_dump through gzip and authenticated GPG encryption into a
# timestamped file under $BACKUP_OUTPUT_DIR, then rotates so only the newest
# $BACKUP_RETENTION dumps remain. No plaintext dump ever touches the disk.
#
# WHY IT IS ENCRYPTED: the dump contains every user's email address alongside
# the full record of which .onion services their account has crawled. That is
# the most sensitive artifact this project produces, and a backup is the copy
# most likely to be moved somewhere with weaker access control than the live
# database.
#
# THE PASSPHRASE IS NOT RECOVERABLE. It is deliberately not derived from
# anything on this host — a backup encrypted with a host-bound key is worthless
# in the case backups exist for, which is losing the host. Keep a copy off this
# machine (password manager). Without it these files cannot be decrypted.
#
# Triggered by onion-spider-backup.timer. The passphrase arrives as the systemd
# credential `backup-passphrase`; for a manual run, point
# BACKUP_PASSPHRASE_FILE at the same file.
#
#   Restore verification: systemctl start onion-spider-restore-check.service
#   Manual recovery: see docs/backup-restore.md
#
# Exit codes:
#   0 — local backup verified and configured offsite publication completed
#       (or another run held the lock)
#   1 — fatal (missing env/passphrase, dump failure, verification failure)

set -euo pipefail
umask 077

ENV_FILE="${BACKUP_ENV_FILE:-/home/micu/go/backend/.env}"
BACKUP_DIR="${BACKUP_OUTPUT_DIR:-/home/micu/onion-spider-backups}"
RETENTION="${BACKUP_RETENTION:-7}"
LOCKFILE="${BACKUP_DIR}/.backup.lock"

if [[ "$BACKUP_DIR" != /* || "$BACKUP_DIR" == "/" ]]; then
    echo "backup: BACKUP_OUTPUT_DIR must be a specific absolute directory." >&2
    exit 1
fi
if [[ ! "$RETENTION" =~ ^[1-9][0-9]{0,2}$ ]] || (( RETENTION > 365 )); then
    echo "backup: BACKUP_RETENTION must be an integer from 1 to 365." >&2
    exit 1
fi

# --- passphrase -------------------------------------------------------------
FROM_SYSTEMD_CREDENTIAL=0
if [[ -n "${BACKUP_PASSPHRASE_FILE:-}" ]]; then
    PASSPHRASE_FILE="$BACKUP_PASSPHRASE_FILE"
elif [[ -n "${CREDENTIALS_DIRECTORY:-}" ]]; then
    PASSPHRASE_FILE="${CREDENTIALS_DIRECTORY}/backup-passphrase"
    FROM_SYSTEMD_CREDENTIAL=1
else
    echo "backup: no passphrase credential; run via the systemd unit or set BACKUP_PASSPHRASE_FILE." >&2
    exit 1
fi
if [[ ! -f "$PASSPHRASE_FILE" ]]; then
    echo "backup: passphrase file is missing or not a regular file." >&2
    exit 1
fi
# A backup whose passphrase is readable by other users is not encrypted in any
# way that matters — fail rather than produce something that only looks
# protected.
#
# This check is skipped for a systemd-supplied credential, which is deliberately
# mode 0440 root:root: systemd isolates it through the containing directory
# (dr-xr-x--- root:root) plus an ACL for the service user, not through the
# file's own group bit. Reading the mode alone would reject a correctly
# protected file. We validate what we are handed directly, and defer to systemd
# for what systemd owns.
if (( ! FROM_SYSTEMD_CREDENTIAL )); then
    PASSPHRASE_MODE="$(stat -c '%a' "$PASSPHRASE_FILE")"
    if (( (8#$PASSPHRASE_MODE & 077) != 0 )); then
        echo "backup: passphrase file must not be readable by group or others." >&2
        exit 1
    fi
fi
if ! awk 'NR == 1 { if (length($0) < 32) exit 1; next } { exit 1 } END { if (NR != 1) exit 1 }' "$PASSPHRASE_FILE"; then
    echo "backup: passphrase must be exactly one line of at least 32 characters." >&2
    exit 1
fi

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

# Single-instance guard: a manual run overlapping the timer would otherwise have
# both writing and rotating at once. Non-blocking — if another run holds the
# lock we exit 0 and let it finish.
exec 9>"$LOCKFILE"
chmod 600 "$LOCKFILE"
if ! flock -n 9; then
    echo "backup: another run is in progress, skipping." >&2
    exit 0
fi

# --- connection details -----------------------------------------------------
if [[ ! -f "$ENV_FILE" ]]; then
    echo "backup: env file $ENV_FILE not found." >&2
    exit 1
fi

# Only the configuration file path enters argv. Connection credentials are
# read inside Python and returned as shell-quoted environment assignments.
PG_SETTINGS="$(python3 - "$ENV_FILE" <<'PYENV'
import pathlib
import shlex
import sys
import urllib.parse

try:
    lines = pathlib.Path(sys.argv[1]).read_text().splitlines()
    value = next(line.partition("=")[2].strip() for line in lines
                 if line.startswith("DATABASE_URL="))
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
        value = value[1:-1]
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme not in ("postgres", "postgresql") or not parsed.hostname:
        raise ValueError("invalid connection")
    settings = {
        "PGHOST": parsed.hostname,
        "PGPORT": str(parsed.port or 5432),
        "PGUSER": urllib.parse.unquote(parsed.username or ""),
        "PGPASSWORD": urllib.parse.unquote(parsed.password or ""),
        "PGDATABASE": urllib.parse.unquote(parsed.path.lstrip("/")),
    }
    parameters = urllib.parse.parse_qs(parsed.query)
    for option, variable in (("sslmode", "PGSSLMODE"),
                             ("connect_timeout", "PGCONNECT_TIMEOUT"),
                             ("sslrootcert", "PGSSLROOTCERT"),
                             ("sslcert", "PGSSLCERT"),
                             ("sslkey", "PGSSLKEY")):
        if option in parameters:
            settings[variable] = parameters[option][-1]
    if not settings["PGDATABASE"]:
        raise ValueError("missing database")
except (OSError, StopIteration, ValueError):
    sys.exit("backup: invalid or missing DATABASE_URL in the environment file")

for key, value in settings.items():
    print(key + "=" + shlex.quote(value))
PYENV
)"
eval "$PG_SETTINGS"
unset PG_SETTINGS
export PGHOST PGPORT PGUSER PGPASSWORD PGDATABASE
export PGSSLMODE PGCONNECT_TIMEOUT PGSSLROOTCERT PGSSLCERT PGSSLKEY
: "${PGDATABASE:?backup: could not parse a database name out of DATABASE_URL}"

STAMP="$(date -u +%Y-%m-%d_%H-%M-%S)"
OUT="${BACKUP_DIR}/onion_spider-${STAMP}.sql.gz.gpg"
[[ ! -e "$OUT" ]] || { echo "backup: refusing to overwrite $OUT" >&2; exit 1; }
TMP="$(mktemp --tmpdir="$BACKUP_DIR" ".onion_spider-${STAMP}.partial.XXXXXX")"
GNUPGHOME="$(mktemp -d "${TMPDIR:-/tmp}/onion-spider-backup.XXXXXX")"
export GNUPGHOME
cleanup() {
    rm -f -- "$TMP"
    rm -rf -- "$GNUPGHOME"
}
trap cleanup EXIT

echo "backup: dumping ${PGDATABASE}@${PGHOST}:${PGPORT} -> $OUT"

# pipefail carries a failure in pg_dump or gzip out through the pipeline, so a
# truncated dump cannot be silently encrypted and kept as if it were complete.
pg_dump --no-owner --no-privileges --format=plain \
    | gzip --best \
    | gpg --homedir "$GNUPGHOME" --no-options --batch --yes \
        --pinentry-mode loopback --no-symkey-cache \
        --passphrase-file "$PASSPHRASE_FILE" \
        --symmetric --force-aead --cipher-algo AES256 \
        --s2k-mode 3 --s2k-digest-algo SHA512 --compress-algo none \
        --output "$TMP"

unset PGPASSWORD

# Prove the file is restorable before it joins the retained set: decrypt it and
# run the result through `gzip --test`. An unverified backup is a guess, and the
# moment you find out otherwise is the worst possible one.
gpg --homedir "$GNUPGHOME" --no-options --batch --yes \
    --pinentry-mode loopback --no-symkey-cache \
    --passphrase-file "$PASSPHRASE_FILE" \
    --decrypt "$TMP" 2>/dev/null \
    | gzip --test

mv "$TMP" "$OUT"
chmod 600 "$OUT"
trap - EXIT
rm -rf -- "$GNUPGHOME"

SIZE="$(stat -c '%s' "$OUT")"
# Names are generated with a sortable UTC stamp, so rotation can work off a
# glob and never has to parse `ls` output or trust arbitrary filenames.
mapfile -t ALL < <(printf '%s\n' "${BACKUP_DIR}"/onion_spider-*.sql.gz.gpg | sort)
COUNT="${#ALL[@]}"
if (( COUNT > RETENTION )); then
    for old in "${ALL[@]:0:COUNT-RETENTION}"; do
        echo "backup: rotating out $old"
        rm -f -- "$old"
    done
fi

echo "backup: local complete (${SIZE} bytes, $(( COUNT > RETENTION ? RETENTION : COUNT )) kept)"

if [[ -n "${BACKUP_REMOTE:-}" ]]; then
    SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
    python3 "${SCRIPT_DIR}/backup_offsite.py" upload "$(basename -- "$OUT")"
fi
