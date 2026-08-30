# Running Eve Flipper in Docker (Linux server / Unraid)

Eve Flipper's web build ships as a Linux container image on GitHub Container Registry,
intended for self-hosting the backend on a home server (Unraid, a NAS, a spare Linux
box) so any browser on your LAN can reach the trading UI.

Image: `ghcr.io/stevarms/eve-flipper:latest`
Exposed port: `13370/tcp`
Persistent state: everything the app writes lives under `/data` — mount one volume.

---

## 1. ESI SSO for a non-localhost host

Release builds bake `http://localhost:13370/api/auth/callback` in as the default
callback, which only works when you browse the app from the same machine that
runs it. For a server install the SSO redirect must come back to the host you
actually type in your browser bar — and CCP enforces two hard rules that make
this fiddlier than it sounds:

- **One callback URL per ESI application** (single-line field on
  <https://developers.eveonline.com/>).
- **`http://` is only accepted for the literal string `localhost`.** Any other
  host — LAN IPs, mDNS names, real hostnames — must use `https://`.

The clean way to run both a local dev build and a server install without
constantly swapping callback URLs is **two separate ESI applications**:

| App                | Callback URL                                                |
|--------------------|-------------------------------------------------------------|
| Dev (localhost)    | `http://localhost:13370/api/auth/callback`                  |
| Server / Unraid    | `https://<host-you-reach-the-app-at>/api/auth/callback`     |

Create the second application on developers.eveonline.com, then set
`ESI_CLIENT_ID`, `ESI_CLIENT_SECRET`, and `ESI_CALLBACK_URL` env vars on the
container to that application's values. Because it must be HTTPS, front the
container with a reverse proxy that terminates TLS (SWAG, Nginx Proxy Manager,
Caddy on Unraid; or Cloudflare Tunnel if you're fine with routing through
their edge). A self-signed cert works if you're OK clicking through the
browser warning once — CCP only compares the URL string, not the cert chain.

---

## 2. Run the container

### Unraid — one-URL install (recommended)

The repo ships a prefilled Unraid template:

1. Unraid webUI → **Docker** tab → **Add Container**.
2. In the **Template** dropdown at the top, paste:
   ```
   https://raw.githubusercontent.com/stevarms/Eve-flipper/master/unraid/eve-flipper.xml
   ```
3. Every field (image, port, `/data` mount, `ESI_CALLBACK_URL`) is prefilled — adjust the callback URL to match the hostname you'll browse to, then **Apply**.

Unraid auto-checks GHCR for the `:latest` tag from then on; new releases show up as an "update available" badge on the Docker tab.

### Unraid — manual "Add Container → Advanced view"

If you'd rather fill fields by hand:

| Field       | Value                                                            |
|-------------|------------------------------------------------------------------|
| Repository  | `ghcr.io/stevarms/eve-flipper:latest`                            |
| Network     | `bridge`                                                         |
| Port        | Container `13370` → Host `13370`                                 |
| Path        | Container `/data` → Host `/mnt/user/appdata/eve-flipper` (RW)    |
| Env         | `ESI_CALLBACK_URL=http://unraid.local:13370/api/auth/callback`   |

### Plain `docker run`

```bash
docker run -d --name eve-flipper \
  -p 13370:13370 \
  -v eveflipper_data:/data \
  -e ESI_CALLBACK_URL=http://unraid.local:13370/api/auth/callback \
  --restart unless-stopped \
  ghcr.io/stevarms/eve-flipper:latest
```

### docker-compose

```yaml
services:
  eve-flipper:
    image: ghcr.io/stevarms/eve-flipper:latest
    container_name: eve-flipper
    restart: unless-stopped
    ports:
      - "13370:13370"
    volumes:
      - eveflipper_data:/data
    environment:
      ESI_CALLBACK_URL: http://unraid.local:13370/api/auth/callback

volumes:
  eveflipper_data:
```

---

## 3. First launch

Open `http://<host>:13370/` in a browser. The Static Data Export (~50 MB) is
downloaded and unpacked into `/data/data/sde/` on first start — allow a couple
of minutes before the scanner and industry features are usable. `docker logs
eve-flipper` shows progress:

```
[SDE] Downloading data... first launch can take a few minutes
[SDE] Scanner ready
```

Logs go to stdout only — the file-logger writes next to the binary, but the
distroless runtime has no writable exe dir, so file logging is disabled by
design in the container. `docker logs` is the source of truth.

---

## 4. What's stored on the `/data` volume

| Path (in container)                       | What it is                                     |
|-------------------------------------------|------------------------------------------------|
| `/data/flipper.db` + `-shm` / `-wal`      | SQLite database (config, scans, ledger, sessions) |
| `/data/data/sde/`                         | Unpacked Static Data Export                    |
| `/data/data/sde.zip`                      | Cached SDE archive                             |
| `/data/data/ship_packaged_volumes.json`   | Cached ship packaged-volume overrides          |
| `/data/.config/EveFlipper/vault_machine.key` | Local key that encrypts ESI refresh tokens at rest |

Back up the whole `/data` mount to preserve character logins, scan history, and
industry plans. Losing `vault_machine.key` means every character has to re-auth
through EVE SSO (no other data is lost).

---

## 5. Upgrading

```bash
docker pull ghcr.io/stevarms/eve-flipper:latest
docker stop eve-flipper && docker rm eve-flipper
docker run -d --name eve-flipper ...   # same command as before, same volume
```

The `/data` volume carries state across upgrades. SQLite schema migrations run
automatically on startup. On Unraid, the "update ready" badge does this
sequence for you — click **Update**.

---

## 6. Notes and limitations

- **Runtime env vars the container respects** — `ESI_CALLBACK_URL` (required for
  any non-localhost host), `ESI_CLIENT_ID` / `ESI_CLIENT_SECRET` (only needed on
  unofficial builds without baked-in credentials), and `EVEFLIPPER_ALLOWED_ORIGINS`
  if you front the container with a reverse proxy on a different origin.
- **Multi-user / metered mode (`EVEFLIPPER_HOSTED=1`) is not covered here.** The
  image behaves identically to the desktop binary — no quotas, no LLM gating.
- **No TLS built in.** LAN plain HTTP is fine for a home server; for external
  access, front the container with a reverse proxy (SWAG / Nginx Proxy Manager /
  Caddy) that terminates HTTPS and forwards to `13370`, then register the HTTPS
  callback URL on the ESI developer portal.
- **amd64 only.** The image is not published for arm64; add it to the release
  workflow's `platforms:` list if you need Pi-class hosts.
