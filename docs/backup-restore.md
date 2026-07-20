# Backups and restore

The database is dumped daily, encrypted, and rotated by
`onion-spider-backup.timer` (03:10 UTC). Script: `scripts/backup.sh`, units in
`deploy/systemd/`.

- **Location:** `/home/micu/onion-spider-backups/onion_spider-<UTC-stamp>.sql.gz.gpg`
- **Retention:** 7 dumps (`BACKUP_RETENTION`)
- **Encryption:** GPG symmetric, AES256 with AEAD, SHA512 S2K

## The passphrase

Stored at `/etc/onion-spider/backup.passphrase` (root, `0600`) and handed to the
service via systemd `LoadCredential`, so the service user never needs read
access to the file.

> **Keep a copy off this machine.** The passphrase is intentionally not derived
> from anything on this host: a backup encrypted with a host-bound key is
> useless in the exact situation backups exist for, which is losing the host.
> If the box dies and the passphrase only ever lived on it, every dump is
> unrecoverable ciphertext.

## Why the dumps are encrypted

A dump contains every user's email address together with the complete record of
which `.onion` services their account has crawled. That is the most sensitive
artifact this project produces, and backups are the copies most likely to end
up somewhere with weaker access control than the live database — another disk,
a laptop, an object store.

## Restore

```bash
# 1. Pick a dump.
ls -la /home/micu/onion-spider-backups/

# 2. Restore into a scratch database FIRST. Never restore straight over a live
#    database you have not confirmed is beyond saving.
sudo -u postgres createdb onion_spider_restore
gpg --decrypt onion_spider-<stamp>.sql.gz.gpg \
  | gunzip \
  | sudo -u postgres psql -q onion_spider_restore

# 3. Check it is complete before trusting it.
sudo -u postgres psql -tAc \
  "SELECT 'nodes='||count(*) FROM nodes
   UNION ALL SELECT 'edges='||count(*) FROM edges
   UNION ALL SELECT 'users='||count(*) FROM users;" onion_spider_restore
sudo -u postgres psql -tAc \
  "SELECT version, dirty FROM schema_migrations;" onion_spider_restore
```

`schema_migrations` must come back with `dirty = false`, otherwise the API will
refuse to start against it.

To promote the restored copy, stop the API, rename the databases, and start it
again:

```bash
sudo systemctl stop onion-spider
sudo -u postgres psql -c 'ALTER DATABASE onion_spider RENAME TO onion_spider_broken;'
sudo -u postgres psql -c 'ALTER DATABASE onion_spider_restore RENAME TO onion_spider;'
sudo systemctl start onion-spider
curl -sS http://127.0.0.1:8900/readyz
```

## Verifying without a full restore

Each run already proves its own output before keeping it: the script decrypts
the file it just wrote and pipes it through `gzip --test`, and only then renames
it into the retained set. A dump that cannot be decrypted or is truncated never
becomes a backup.

That check does not prove the SQL loads, so do a real restore periodically:

```bash
sudo systemctl start onion-spider-backup.service   # run now, off-schedule
systemctl list-timers onion-spider-backup.timer    # when it next fires
journalctl -u onion-spider-backup.service -n 20
```

Last verified end to end on 2026-07-20: a 32 MB dump restored into a scratch
database with matching row counts (nodes 23346, edges 76397, users 3) and
`schema_migrations` at version 2, not dirty.

## Manual run outside systemd

```bash
sudo BACKUP_PASSPHRASE_FILE=/etc/onion-spider/backup.passphrase \
  /home/micu/go/scripts/backup.sh
```

The script refuses a passphrase file readable by group or others, refuses one
shorter than 32 characters, and takes a non-blocking lock so a manual run
overlapping the timer exits rather than interleaving with it.
