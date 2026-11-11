# Account credentials and TOTP recovery

## Password changes

Account security includes a password-change form with current password,
confirmation and a single-use authenticator/recovery code when MFA is enabled.
The account re-authentication limit and shared password-work budget apply.

The database compares both the credential version and prior hash, then commits
the new password, reset-link invalidation and session revocation together.
Concurrent resets or MFA changes cause a conflict. Successful changes sign out
every device, discard unconfirmed MFA setup and preserve enrolled MFA and unused
recovery codes. The browser returns to sign-in with a confirmation message.

## TOTP storage and threat boundary

Pending and confirmed seeds use AES-256-GCM envelopes with independently random
nonces. The account ID and envelope header are authenticated, preventing a
ciphertext from being moved to another account or assigned a different key ID.
Plain seeds are not sent as database query parameters on new writes.

The keyring is independent of JWT signing and PostgreSQL. Production stores it
at `/etc/onion-spider/totp-keyring.json`, root-owned mode 0600, delivered to the
API and backup service through systemd `LoadCredential`. Local development can
set `TOTP_KEYRING_FILE` to a private file. Missing/unknown keys and corrupt data
stop startup. MFA is never silently disabled.

This protects seeds in a database-only disclosure. An attacker controlling the
API process or VPS can access decryption keys. A database writer can still alter
authorization or password hashes. No external key-management vendor was added.
[Go AEAD API](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce),
[OWASP storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html).

## Initialize and rotate

```bash
sudo python3 scripts/totp_keys.py init
```

Initialization refuses to overwrite existing files. The tool creates a random
256-bit key, publishes the file atomically, flushes it to disk and prints no key
material. Do not derive it from a password, JWT key or VPS identifier.

Before first encryption, install the updated backup unit and complete a verified
Google Drive backup containing the encrypted keyring. Normal API startup
atomically validates and rewraps existing seeds before serving requests, keeping
enrolment timestamps, replay counters, sessions and recovery-code state intact.

Run rotation serially:

```bash
sudo python3 scripts/totp_keys.py rotate
sudo systemctl start onion-spider-backup.service
# Continue only after backup success and Google Drive verification.
sudo systemctl restart onion-spider.service
sudo systemctl start onion-spider-backup.service
sudo systemctl start onion-spider-restore-check.service
```

The first backup preserves the new key before it is used. Startup rewraps old
envelopes; the second backup captures the rewrapped database and paired keyring.
Old keys are retained. The tool stops at 16 keys and never retires one
automatically. Preserve keys required by older databases/backups. Follow the Go
AEAD limit of fewer than 2^32 encryptions per key, rotating well before it.

For local Docker development:

```bash
python3 scripts/totp_keys.py init --path .secrets/totp-keyring.json --group "$(id -g)"
# Match TOTP_KEYRING_PATH and TOTP_KEYRING_GID in the private .env.docker file.
docker compose --env-file .env.docker up --build
```

The API receives only the read-only key-file mount and configured supplementary
reader group. Missing host paths fail instead of creating a directory. `.secrets`
and `.env.docker` are ignored by Git and excluded from the backend build context.

## Backup and recovery

Every new production dump has a paired
`onion_spider-keyring-<timestamp>.json.gpg` file. GPG encryption precedes the
existing Google Drive crypt transport. Keys are outside the SQL dump. The same
GPG passphrase protects both archives; access to that passphrase and the offsite
store is a full backup-recovery boundary.

`latest.json` binds both ciphertexts by filename, size and SHA-256. Publication
verifies them by downloading, and retention removes companions with their
database archives. Production backup fails if its keyring credential is missing.

The restore drill downloads both files, decrypts the keyring and pipes it with
restored seed records to the API binary's `--verify-totp-backup` mode. That mode
opens no listener, connects to no DB, uses only downloaded keys and writes no
plaintext key file. It authenticates restored ciphertext and account binding.
Encrypted seeds without their recovery keys fail the drill. Success is written
after verification and container cleanup.

For host recovery, install the matching recovered keyring as root-only
`/etc/onion-spider/totp-keyring.json` before starting the API. Retain the GPG
passphrase, rclone crypt keys and Google account recovery independently of this
VPS. Off-host custody still needs operator verification.

## Rollback and incident response

First deployment checks the new binary with `--totp-compatibility`: read both
valid legacy and authenticated encrypted records, encrypt every new write and
skip bulk rewrapping. This tested binary becomes the rollback artifact before
normal startup migrates retained seeds.

Once encrypted records exist, rollback must use a compatible binary with the
current complete keyring. Do not start pre-encryption releases or restore an old
keyring missing newer keys. Compatibility mode is a recovery option; normal
operation validates and encrypts all seeds.

If keys are missing/corrupt, recover the correct protected keyring before
restart. Do not disable MFA or replace it with a random key. Preserve evidence
and use the existing incident-response runbook. API startup failures trigger the
availability alert; backup/restore failures have a two-minute hold. These holds
do not measure operator incident response time.
