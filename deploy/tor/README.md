# Tor

`torrc` and `Dockerfile` here are for the **docker-compose dev stack**, where
Tor runs as its own container.

In **production** Tor is the host's own `tor@default` daemon, shared with other
services on the box, so there is no file in this repo to deploy — `/etc/tor/torrc`
is host-wide and owns configuration this project has no business overwriting
(other projects' `HiddenServiceDir` entries live in it).

## What this project needs from the host Tor

**SOCKS port** — `127.0.0.1:9050`, the default. No configuration needed.

Leave the default isolation flags alone. `IsolateSOCKSAuth` is on by default and
the crawler depends on it: it derives a SOCKS username/password per destination
host so Tor builds one circuit per site (see `internal/proxy/client.go`). If a
`SocksPort` line is ever added with explicit flags that omit `IsolateSOCKSAuth`,
that isolation silently stops working — unrelated onion services would start
sharing circuits again, with nothing in the app's logs to indicate it.

**Control port** — optional, for `SIGNAL NEWNYM` (circuit rotation after a run
of failures). Append to `/etc/tor/torrc`:

```
ControlPort 127.0.0.1:9051
HashedControlPassword <output of: tor --hash-password YOUR_PASSWORD>
```

then set `TOR_CONTROL_ADDR` and `TOR_CONTROL_PASSWORD` in `backend/.env` and
restart the API.

### Why password auth and not the cookie

`CookieAuthentication 1` writes a cookie readable by the `debian-tor` group, so
using it requires adding this app's user to that group. That grants **every**
process running as that user full control of the Tor daemon — including the
ability to read or add hidden services belonging to unrelated projects on the
same host. The password keeps the capability scoped to this app's `0600` .env.

### Verifying

```bash
sudo -u debian-tor tor --verify-config     # before reloading
sudo systemctl reload tor@default          # reload, not restart: keeps HS descriptors
sudo cat /var/lib/tor/*/hostname           # onion addresses must be unchanged
```

The API logs `tor controller active` plus `tor_circuit_renewed` at startup when
the control port is reachable, and `tor control port unavailable; circuit
renewal disabled` when it is not. The latter is a warning, not a failure — the
crawler runs either way, it just cannot rotate away from a bad circuit, so a
failing guard path stalls the queue until the process is restarted.
