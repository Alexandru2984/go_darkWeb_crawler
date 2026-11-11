# Backups and restore

The production database is backed up daily by
`onion-spider-backup.timer` at 03:10 UTC with up to ten minutes of jitter.
The installed job uses the existing rclone Google Drive crypt remote.

| Setting | Production value |
| --- | --- |
| Local directory | `/home/micu/onion-spider-backups` |
| Local retention | 7 verified archives |
| Offsite directory | `gdrive-vps-crypt:onion-spider` |
| Offsite retention | 30 active archives |
| Archive encryption | GPG AES256 with AEAD and SHA512 S2K |
| Drive encryption | Existing rclone crypt configuration, including file and directory names |
| Offsite configuration | `/etc/onion-spider/backup.env` |
| rclone configuration | `/home/micu/.config/rclone/rclone.conf` |
| Recovery records | `/var/lib/onion-spider-backup` |
| Restore drill | Monthly, first day at 04:15 UTC with up to fifteen minutes of jitter |

Retention counts active archives. Manual backups occupy retention slots as well
as scheduled backups. Google Drive uses the existing rclone trash setting.
Archives removed from the active set go to Drive trash, where account-side
retention and version policies also apply.

## Backup sequence

The script reads database credentials from the application environment file.
The connection URI and password stay out of subprocess arguments. The password
is removed from the environment after the database dump.

The job streams pg_dump through gzip and GPG. It decrypts the temporary archive
through `gzip --test`, then atomically publishes the local filename. An exclusive
lock serializes backup creation, rotation and offsite publication.

The offsite helper copies the retained archives into the dedicated Drive
directory. Snapshot uploads use rclone's immutable flag. It verifies the copied
archives by downloading and comparing their contents while they remain
GPG-encrypted.

After successful archive verification, the helper writes and reads back
`latest.json`. This recovery record identifies the verified filename, byte
length, SHA-256 digest and verification time. It then rotates matching snapshot
filenames within this application's directory. The current recovery pointer is
preserved even if the system clock has moved backwards.

An offsite error fails the service. Verified local archives remain available
under the local retention policy. A subsequent run retries the retained local
archives. Remote retention runs only after verification succeeds.

The success record at
`/var/lib/onion-spider-backup/offsite-success.json` records the last completed
offsite publication. Its timestamp provides evidence of backup freshness.

## Encryption keys and account access

The GPG passphrase lives at `/etc/onion-spider/backup.passphrase`, owned by root
with mode 0600. systemd passes it through LoadCredential.

The existing rclone configuration contains the Google authorization and crypt
keys. It has mode 0600. The jobs can refresh the OAuth token in its existing
configuration directory.

Recovery after losing the VPS requires independently recoverable copies of the
GPG passphrase and rclone crypt credentials, together with access to the Google
account. Keep these in the operator's external secret store. Key custody outside
the VPS has to be verified by the operator. The backup job uploads encrypted
archives and their recovery metadata.

## Isolated restore verification

Run the installed verification job with

```bash
sudo systemctl start onion-spider-restore-check.service
systemctl show onion-spider-restore-check.service -p Result -p ExecMainStatus
```

The job downloads the Drive recovery record and its archive, checks its size and
SHA-256 digest, then streams GPG decryption and decompression into PostgreSQL.
SQL errors stop the restore.

The PostgreSQL container uses the pinned image from the backup environment
file. It has a read-only root filesystem, no external network, no published
ports, no host filesystem mounts and no Linux capabilities. Restored data lives
in a 2 GiB tmpfs. Container memory is bounded at 2 GiB. Archive downloads are
bounded at 2 GiB.

The job checks the migration version and dirty flag, core application tables
and validation of database constraints. It records aggregate table counts,
archive identity and elapsed time in
`/var/lib/onion-spider-backup/restore-success.json`.

The container is removed after success or failure. systemd also runs cleanup
after an interrupted job. Cleanup validates the stored container name and its
ownership label, and preserves an active restore's lock.

Raw SQL and SQL diagnostics stay inside the private temporary workspace.
Routine service logs contain completion or failure status.

The elapsed time in the restore record covers archive download, integrity
checks, database initialization, restore and schema checks. Host provisioning,
credential recovery and application promotion require their own recovery
exercise.

## Install or update the jobs

The example environment file selects the existing Google Drive crypt remote.
It carries configuration paths and retention settings. The encryption passphrase
and rclone credentials remain in their existing protected files.

```bash
sudo install -m 0600 deploy/systemd/onion-spider-backup.env.example /etc/onion-spider/backup.env
sudo install -m 0644 deploy/systemd/onion-spider-backup.service /etc/systemd/system/
sudo install -m 0644 deploy/systemd/onion-spider-backup.timer /etc/systemd/system/
sudo install -m 0644 deploy/systemd/onion-spider-restore-check.service /etc/systemd/system/
sudo install -m 0644 deploy/systemd/onion-spider-restore-check.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now onion-spider-backup.timer onion-spider-restore-check.timer
```

The restore job uses Docker. The service account needs access to the Docker
daemon. Install the pinned restore image before an unattended drill.

```bash
sudo systemctl start onion-spider-backup.service
sudo systemctl start onion-spider-restore-check.service
systemctl list-timers onion-spider-backup.timer onion-spider-restore-check.timer
```

If the backup environment file has not been installed and BACKUP_REMOTE is
unset, the original local backup workflow remains available.

## Manual recovery from Google Drive

Use a protected working directory on the recovery machine. Restore the rclone
credentials and obtain the GPG passphrase through the external secret store.

```bash
umask 077
mkdir -p /var/tmp/onion-spider-recovery
cd /var/tmp/onion-spider-recovery
rclone copyto gdrive-vps-crypt:onion-spider/latest.json latest.json
```

Read the filename, byte length and SHA-256 from the recovery record. Download
that exact archive and compare its digest before decrypting it.

```bash
rclone copyto "gdrive-vps-crypt:onion-spider/ARCHIVE_FILENAME" ./ARCHIVE_FILENAME
sha256sum ./ARCHIVE_FILENAME
sudo -u postgres createdb onion_spider_restore
set -o pipefail
gpg --batch --pinentry-mode loopback \
  --passphrase-file /path/to/recovered/backup.passphrase \
  --decrypt ./ARCHIVE_FILENAME \
  | gzip --decompress \
  | sudo -u postgres psql -X --set ON_ERROR_STOP=1 --quiet onion_spider_restore
sudo -u postgres psql -X -d onion_spider_restore \
  -c 'SELECT version, dirty FROM schema_migrations'
```

Validate the restored application tables and compare any recorded counts.
Review the recovered data before promoting the database. Record the incident
timeline and recovery evidence in the incident runbook.

For database promotion, stop the application, preserve the original database
under a separate name and connect the application to the verified restored
database. Verify readiness and authenticated application flows before reopening
traffic.

## Monitoring and checks

Check the latest service result and the timestamps in the offsite and restore
success records. A failed job leaves the previous success record intact.

```bash
systemctl show onion-spider-backup.service -p Result -p ExecMainStatus
systemctl show onion-spider-restore-check.service -p Result -p ExecMainStatus
journalctl -u onion-spider-backup.service -u onion-spider-restore-check.service -n 30
```

Operator alert delivery remains part of the monitoring work in the production
roadmap. The daily schedule gives a nominal one-day backup interval. The last
verified offsite timestamp determines the actual recovery point.

Backup regressions run locally and in CI with a disposable crypt remote backed
by local disk.

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_backup.py' -v
shellcheck scripts/backup.sh
```

The transport verification behaviour follows the rclone copy and download-check
interfaces. [rclone copy](https://rclone.org/commands/rclone_copy/),
[rclone check](https://rclone.org/commands/rclone_check/).

## Verified recovery record

Verified on 2026-11-11. The scheduled offsite run completed at 03:15:38 UTC,
and the latest archived snapshot was restored at 14:39:57 UTC in 362.5 seconds.
The restored database reported migration version 8, dirty=false and zero
unvalidated constraints. The isolated container and its cleanup marker were
removed.

The application remained ready throughout the drill. The security review
records the verification evidence and remaining recovery responsibilities.
