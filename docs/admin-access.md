# Administrator access and invitations

## Owner sign-in

The operator's login is the email on the existing `users` row with role `admin`.
Alertmanager recipients and SMTP sender settings serve different purposes and do
not create or update a login. `ADMIN_EMAIL` only selects the first administrator
during public-registration bootstrap; changing it does not migrate an existing
account. Keep public registration closed on this installation.

An explicitly authorized operator recovery on 2026-11-12 moved the existing
owner's login to their approved Proton address, preserving user ID and research
data. Previous sessions were revoked, the previous password invalidated, and
one password-choice link was sent through SMTP. The root-owned environment was
aligned with the new address. No password or link token was committed or printed.

Use the **Onion Spider password reset** message to choose a password, then sign
in at `/login`. Reset links expire after one hour. If needed, request a fresh
link through **Forgot password** for the approved address. Password reset does
not remove an already enrolled authenticator.

Open **Security**, enable two-factor authentication and save the recovery codes.
Administrative permissions are available after enrolling and signing in again.
An administrator without the required factor sees an actionable enrolment page
at `/admin`, while the API rejects administrative reads and writes.

## Creating accounts

Open **Admin** and enter the recipient's email, your administrator password and
a fresh authenticator or unused recovery code. **Create and invite** creates a
pending regular user and sends an **Onion Spider account invitation** message.
The initial account password is random and unknown. The recipient chooses their
own password at `/activate-account`; activation verifies their address and does
not grant administrative privileges.

The account list is paginated and shows role, activation status and MFA status.
For a pending regular account, **New invitation** prepares the form; sending
requires another credential check. Invitations expire after 24 hours, work once
and store only a SHA-256 token digest. Resending invalidates the previous link.
Allow five minutes between invitations to the same account. The recipient
cooldown is checked under a database lock, including across API instances.

SMTP acceptance is reported after the send call. A transport error can happen
after a provider accepted a message, so a failure says delivery could not be
confirmed. The account stays pending and remains visible for recovery. Check
delivery before explicitly resending after the cooldown. There are no automatic
email retries. Missing SMTP configuration also reports a failure for invitations.

The shared sensitive-action limit allows ten password checks per account per
hour. Invitation writes lock and recheck the actor's live administrative role,
session credential version and required MFA state before committing. Cookie
requests use the existing CSRF protection. The client cannot select a role.

## Blacklist

The same Admin page lists blocked v3 .onion hostnames. Add a domain to prevent
future crawler visits across accounts; existing research records are retained.
Removing a block requires an explicit confirmation in the interface. Backend
validation and the administrative MFA policy apply to both actions.

## Link privacy and recovery

Reset, verification and invitation links carry their credential in the URL
fragment. Startup consumes it in memory **before constructing browser history**;
otherwise the router's initial URL snapshot can restore the fragment after it
was scrubbed. The credential is posted in a request body and never stored in
local storage. Reloading a consumed page requires reopening the email link.

Build the host API with `make build`. It sets `CGO_ENABLED=0` and verifies that
the staged executable is statically linked. The production systemd sandbox
allows execution of the API only; a dynamically linked build needs a loader
outside that allowlist and exits with `203/EXEC`. Verify the staged artifact
before replacing the live file. Keep this sandbox restriction in place.

The release adds no schema migration. Existing user/password/reset formats stay
compatible with the prior API. A rollback to an API predating invitations stops
the new activation endpoint; retain pending accounts and restore this release
before asking recipients to retry activation. Use the saved frontend matching
the API when rolling back. Release manifests and previous artifacts are in the
root-restricted `/home/micu/.local/state/onion-spider/releases/` directory.

Operational verification must use disposable accounts and a captured test
mailer, never send test invitations to production users. Inspect account
metadata only; do not print hashes, reset/verification tokens, TOTP seeds or
SMTP secrets. Backups continue through the existing encrypted Google Drive
workflow, including the paired TOTP keyring snapshot.
