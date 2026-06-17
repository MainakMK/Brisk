# Brisk — Deploying the Control Plane + Dashboard on a REAL Server (Docker)

> **What this is.** A complete, self-contained runbook for moving the Brisk **control plane**
> (the "server panel": API + dashboard + Postgres/TimescaleDB + NATS) off your laptop and onto a
> real, internet-reachable server, all in Docker. Written so a future deploy can be done by
> following this file alone — no need to re-read the codebase.
>
> **Audience:** you, later. Plain steps + exact env vars + the gotchas that will actually bite.
>
> **Scope.** This covers the **control plane / panel** only. The **edges (PoPs)** are bare-metal and
> already deployed; the only edge change here is repointing each agent at the new public control
> plane (Step 8). Edge bring-up itself lives in the build spec; agent rollout lives in
> `docs/features/Brisk_Agent_Rollout_Process.html`.

---

## 0. Mental model (read this once)

Brisk has **two independent halves** — by design, one can be down without taking the other down:

| Half | What it is | Runs where | Public? |
|------|-----------|-----------|---------|
| **Data plane** | nginx + `brisk-agent` on each edge (NY/FRA/BLR) | bare-metal edges | yes — serves your CDN traffic |
| **Control plane ("panel")** | API (`brisk-control`) + dashboard + Postgres/Timescale + NATS | **one Docker host** (this doc) | the API + dashboard need to be reachable |

The edges **pull** from the control plane:
- each agent **polls** `AGENT_CONTROL_PLANE_URL` for config + heartbeats + (self-update) releases,
- each agent **subscribes** to NATS (`AGENT_NATS_URL`) for instant cache purges.

**On your laptop today**, edges reach the control plane through a **reverse SSH tunnel**
(`brisk-tunnels`), because a laptop has no public address. **On a real server you delete that
whole tunnel concept** — the edges connect **directly** to the server's public HTTPS URL. That is
the single biggest change.

```
  TODAY (laptop):   edge ──reverse SSH tunnel──> laptop:8080 (brisk-control)
  REAL SERVER:      edge ──HTTPS over internet──> control.yourdomain.com ──> brisk-control:8080
                    (NO tunnels container at all)
```

---

## 1. What you're deploying (the 4 containers)

From `brisk-control/docker-compose.yml`:

| Service | Image / build | Port (local today) | Real-server exposure |
|---------|---------------|--------------------|----------------------|
| `timescaledb` | `timescale/timescaledb:2.24.0-pg17` (pinned) | `127.0.0.1:5432` | **internal only** — never public |
| `nats` | `nats:2.10-alpine` (`-js` JetStream) | `127.0.0.1:4222` | edges need it for purge → **TLS-exposed** (Step 6) |
| `brisk-control` | built from `brisk-control/Dockerfile` (distroless static Go, `EXPOSE 8080`) | `8080` | behind HTTPS reverse proxy → `control.yourdomain.com` |
| `brisk-dashboard` | **dev** image today (`Dockerfile.dev`, Vite HMR, source bind-mount). **For a real server use the PROD image** `brisk-dashboard/Dockerfile` (builds static `dist/`, serves via nginx, `EXPOSE 80`) | `5173` (dev) | behind HTTPS → `panel.yourdomain.com` |

DB schema is created automatically: `brisk-control` runs **goose migrations on startup** (you'll
see `running migrations` → `goose: successfully migrated database to version: N` in its logs).

---

## 2. Prerequisites

- A Linux server (Ubuntu 24.04 LTS recommended), 2 vCPU / 4 GB+ is plenty for the panel.
- **Docker Engine + Docker Compose v2** installed (`docker compose version`).
  - *Use real Linux Docker, NOT Docker Desktop.* The "Loading…/port hangs" bug you hit on Windows
    (`docker-windows-stale-wslrelay-port-hang` in memory) is a **Docker-Desktop-on-Windows** issue —
    it does not exist on a Linux server. One less thing to worry about.
- A domain you control, with the ability to add DNS records. Decide two hostnames:
  - `control.yourdomain.com` → the API (edges + dashboard talk to this)
  - `panel.yourdomain.com` → the dashboard UI (your browser)
  - (you can also use one host + path-routing, but two hosts is simpler — this doc assumes two)
- Ports **80 + 443 open** to the internet on the server's firewall. **5432 (Postgres) and 4222
  (NATS-plain) stay closed**; only the TLS NATS port (Step 6) is opened.
- The Brisk repo on the server (git clone or copy `brisk-control/` + `brisk-dashboard/`).
- (Optional) your laptop DB backup if you want to carry over existing zones/servers/history:
  `brisk-control/backups/brisk-pre-phase8-*.sql.gz` (Step 9).

---

## 3. DNS records

Point both hostnames at the server's public IP:

```
control.yourdomain.com.   A   <SERVER_PUBLIC_IP>
panel.yourdomain.com.     A   <SERVER_PUBLIC_IP>
```

(If NATS gets its own hostname per Step 6: `nats.yourdomain.com A <SERVER_PUBLIC_IP>` too.)

---

## 4. The `.env` file (the heart of it)

Create `brisk-control/.env` on the server. **This file is gitignored and holds all secrets — never
commit it.** Every value below maps to an env var the control plane reads (`internal/config`), wired
through `docker-compose.yml`'s `environment:` block.

```dotenv
# ─── Database ───────────────────────────────────────────────────────────────
DB_PASSWORD=<long-random-string>           # Postgres password (internal only)

# ─── Admin bootstrap (first login) ──────────────────────────────────────────
BRISK_ADMIN_EMAIL=you@yourdomain.com       # seeded once on first start
BRISK_ADMIN_PASSWORD=<strong-password>     # change after first login

# ─── Public URLs / CORS / cookies (CRITICAL on a real server) ───────────────
# URL the EDGES use to reach the control plane (baked into each agent's config when
# you provision/repoint them). MUST be the public HTTPS API host.
AGENT_CONTROL_PLANE_URL=https://control.yourdomain.com
# URL the EDGES use to reach NATS for purge. See Step 6 for exposing NATS over TLS.
AGENT_NATS_URL=tls://nats.yourdomain.com:4222
# The dashboard origin allowed to call the API with credentials (CORS allow-list).
BRISK_DASHBOARD_ORIGIN=https://panel.yourdomain.com
# Session cookie MUST be Secure behind HTTPS (it was false for local http dev).
BRISK_COOKIE_SECURE=true

# ─── Bunny geo-DNS (routing the CDN hostnames) ──────────────────────────────
BUNNY_API_KEY=<bunny-api-key>
BUNNY_DNS_ZONE_ID=<zone-id>
BUNNY_DNS_ZONE=<your-cdn-base-zone>        # e.g. a2zjav.com (current live base)
BRISK_CDN_RECORD=cdn                        # the record under the zone
BRISK_DNS_TTL=15
BRISK_DNS_ROUTING_MODE=geographic          # geographic | latency
BRISK_DNS_MONITOR=true                      # Bunny native ping monitor (failover backstop)
BRISK_DNS_MONITOR_TYPE=ping

# ─── Self-driven health checker (fast failover) ─────────────────────────────
BRISK_HEALTH_ENABLED=true
BRISK_HEALTH_INTERVAL=5
BRISK_HEALTH_TIMEOUT=3
BRISK_HEALTH_FAIL_THRESHOLD=2
BRISK_HEALTH_RISE_THRESHOLD=3
BRISK_HEALTH_PATH=/healthz
BRISK_HEALTH_SCHEME=https
BRISK_HEALTH_PORT=0

# ─── Managed wildcard TLS for the CDN edges (lego + Bunny DNS-01) ────────────
# This is for the *edge* certs, NOT the panel's own cert (the panel cert is the
# reverse proxy's job, Step 5). Flip STAGING=false only at real cutover.
BRISK_TLS_MANAGED=true
BRISK_TLS_EMAIL=you@yourdomain.com
BRISK_TLS_STAGING=false
BRISK_TLS_DOMAINS=*.cdn.yourdomain.com
BRISK_CUSTOM_TLS_STAGING=false

# ─── Agent self-service rollout (Phase 8) ───────────────────────────────────
BRISK_AGENT_PUBKEYS=<your-ed25519-PUBLIC-key-base64>   # public half only; signs are verified
BRISK_ROLLOUT_ENABLED=true

# ─── Load steering (#3) — optional, off unless you want it ──────────────────
BRISK_LOAD_STEER_ENABLED=false

# ─── Dashboard build/runtime ────────────────────────────────────────────────
# PROD dashboard bakes this at BUILD time (see Step 5). Setting it here documents intent.
VITE_API_URL=https://control.yourdomain.com
```

> **Carry over from the laptop:** `BUNNY_*`, `BRISK_TLS_EMAIL`, `BRISK_AGENT_PUBKEYS` (public key),
> and the admin creds should match what you already use. The **private** ed25519 signing key NEVER
> goes here — it lives in your password manager / GitHub secret only.

---

## 5. HTTPS reverse proxy (the panel's own TLS)

The `brisk-control` container serves **plain HTTP on :8080**, and the prod dashboard serves **plain
HTTP on :80** inside its container. You put a TLS-terminating reverse proxy in front. **Caddy** is
the lowest-effort (automatic Let's Encrypt). Run it as a 5th container or on the host.

**`Caddyfile`:**
```caddyfile
control.yourdomain.com {
    reverse_proxy brisk-control:8080
}
panel.yourdomain.com {
    reverse_proxy brisk-dashboard:80
}
```

Add Caddy to the compose (same Docker network as the others so service names resolve):
```yaml
  caddy:
    image: caddy:2-alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on: [brisk-control, brisk-dashboard]
# and add caddy_data: / caddy_config: under the top-level volumes:
```

Then **remove the host port publishes** that exposed the API/dashboard directly:
- `brisk-control`: change `ports: ["8080:8080"]` → **delete it** (Caddy reaches it over the internal
  network as `brisk-control:8080`). Keeping it bound publicly is a security hole.
- `brisk-dashboard`: same — the prod image is reached as `brisk-dashboard:80` by Caddy; no host
  publish needed.
- `timescaledb` / `nats`: keep their `127.0.0.1:` binds (internal only).

> Alternative to Caddy: nginx + certbot, or your cloud's load balancer with ACM/Let's Encrypt. Any
> TLS terminator that forwards to `:8080` and `:80` works. The two rules that matter: **HTTPS on
> both hostnames**, and **`control.` forwards to brisk-control:8080**.

---

## 6. Exposing NATS to the edges (purge channel)

On the laptop, NATS was reached through the reverse tunnel. On a real server the edges need a public
way in. Two options:

- **Option A — TLS NATS (recommended).** Give NATS a server cert and open `4222` with TLS. Set
  `AGENT_NATS_URL=tls://nats.yourdomain.com:4222`. Configure the `nats` container with a TLS block
  (mount a cert; NATS supports `-c nats.conf` with `tls { cert_file, key_file }`). Open 4222/tcp in
  the firewall.
- **Option B — skip instant purge for now.** Set `AGENT_NATS_URL=` (empty). Edges then won't get the
  instant NATS purge; cache still expires by TTL and you can still trigger purges that apply on the
  next config pull. Simplest to start; add Option A later. (The data plane is unaffected either way.)

> Do **not** expose NATS plaintext (`nats://...:4222`) to the internet. Either TLS it or leave it
> internal/empty.

---

## 7. Build + bring up the stack

On the server, from `brisk-control/`:

```bash
# 1) Build the PROD dashboard image with the public API URL baked in.
#    (The compose dev service uses Dockerfile.dev + localhost — for a real server,
#     either swap the brisk-dashboard service to use ../brisk-dashboard/Dockerfile
#     with build args, or build it standalone and reference the image.)
docker build -t brisk-dashboard:prod \
  --build-arg VITE_API_URL=https://control.yourdomain.com \
  ../brisk-dashboard

# 2) In docker-compose.yml, point the brisk-dashboard service at the prod image
#    instead of Dockerfile.dev, and drop its source bind-mount + 5173 publish:
#       brisk-dashboard:
#         image: brisk-dashboard:prod
#         depends_on: [brisk-control]
#    (Caddy serves it; no host port needed.)

# 3) Bring everything up.
docker compose up -d --build

# 4) Watch the control plane boot + migrate.
docker compose logs -f brisk-control
#   expect: "running migrations" → "successfully migrated database to version: N"
#           → "rollout engine started" (if BRISK_ROLLOUT_ENABLED=true)
#           → "brisk-control listening" addr=:8080
```

> **Why the dashboard URL is baked at build time:** the prod dashboard is static files; the browser
> calls `VITE_API_URL` directly (see `brisk-dashboard/src/lib/api.ts`:
> `BASE_URL = import.meta.env.VITE_API_URL ?? "http://localhost:8080"`). If you forget the build-arg,
> the deployed panel will try to call `localhost:8080` from each visitor's browser and hang on
> "Loading…". **This is the #1 deploy mistake — set the build-arg.**

---

## 8. Point the edges at the new control plane

The edges currently target the laptop tunnel (`http://127.0.0.1:18080`). Repoint each edge's agent:

For **each** edge (NY, FRA, BLR), edit `/etc/brisk/agent.yaml`:
```yaml
control_plane_url: "https://control.yourdomain.com"
nats_url: "tls://nats.yourdomain.com:4222"   # or "" if you chose Option B
```
then `systemctl restart brisk-agent` and confirm it reconnects:
```bash
journalctl -u brisk-agent -n 20 --no-pager | grep -i heartbeat   # expect "heartbeat ok"
```

> **Newly-provisioned** edges get these URLs automatically from `AGENT_CONTROL_PLANE_URL` /
> `AGENT_NATS_URL` in the server `.env` — only the **existing 3** need the manual edit above.
>
> Do this **one edge at a time** and verify each reconnects before the next, so you always have
> live PoPs reporting in. The data plane keeps serving throughout (agent restart is ~6s; nginx is
> untouched). After all three, the dashboard **Servers** page should show all 3 **online** on the
> public control plane.
>
> Once the edges are on the real server, the laptop's `brisk-tunnels` containers are **no longer
> used** — stop/remove them.

---

## 9. (Optional) carry over existing data

If you want the new server to start with your existing zones / servers / history instead of empty:

```bash
# On the server, after the stack is up and migrated, restore the laptop dump.
gunzip -c brisk-pre-phase8-YYYYMMDD-HHMMSS.sql.gz | \
  docker exec -i brisk-control-timescaledb-1 psql -U brisk -d brisk
# then restart the control plane so it re-reads:
docker compose restart brisk-control
```
> If you'd rather start clean (re-add servers + zones via the dashboard), skip this — the admin
> bootstrap (`BRISK_ADMIN_EMAIL`/`PASSWORD`) lets you log in to an empty panel and rebuild.

---

## 10. Verify (acceptance checklist)

```bash
# API reachable over HTTPS (expect 401 = up + auth-required, NOT a timeout/000):
curl -s -o /dev/null -w '%{http_code}\n' https://control.yourdomain.com/api/v1/admin/tokens   # 401

# Dashboard loads:
#   open https://panel.yourdomain.com in a browser → login with BRISK_ADMIN_* → it should NOT
#   hang on "Loading…" (if it does: VITE_API_URL build-arg was wrong, or CORS/cookie mismatch).

# Edges online (from the panel Servers page, or):
docker exec brisk-control-timescaledb-1 psql -U brisk -d brisk -tAc \
  "select edge_id, status, agent_version from servers order by edge_id;"   # all 'online'

# Heartbeats arriving:
docker compose logs --since 30s brisk-control | grep -c heartbeat   # > 0
```

Tick all of:
- [ ] `https://control.yourdomain.com` returns 401 (not a hang) — API public + TLS OK
- [ ] `https://panel.yourdomain.com` loads the login page, login works (no "Loading…")
- [ ] all 3 edges show **online** + correct agent version
- [ ] a test cache purge from the panel reaches the edges (if NATS Option A) or applies on next pull
- [ ] `5432` and plain `4222` are **NOT** reachable from the public internet (`nmap`/firewall check)

---

## 11. Operations (day-2)

- **Update the control-plane code:** `git pull` → `docker compose up -d --build brisk-control`
  (migrations run automatically). On Linux there's **no stale-port bug** — no `wslrelay` dance.
- **Update the dashboard:** rebuild the prod image with the same `VITE_API_URL` build-arg →
  `docker compose up -d brisk-dashboard`.
- **Backups:** schedule `pg_dump` of the `brisk` DB (zones, servers, tokens, history) off-box.
  ```bash
  docker exec brisk-control-timescaledb-1 pg_dump -U brisk -d brisk --no-owner | gzip > brisk-$(date +%F).sql.gz
  ```
- **Restart policy:** add `restart: unless-stopped` to every service so the panel survives reboots.
- **Logs:** `docker compose logs -f brisk-control`.
- **Agent self-update from here:** once the panel is on a real server you can also turn on the
  **GitHub auto-deploy** (see `docs/future/Brisk_Agent_AutoDeploy_When_CP_On_Real_Server.md`) — that
  doc's prerequisite ("control plane on a real public server") is satisfied by THIS deploy.

---

## 12. Security checklist (don't skip)

- [ ] `BRISK_COOKIE_SECURE=true` (sessions only over HTTPS)
- [ ] `BRISK_DASHBOARD_ORIGIN` exactly matches the panel's HTTPS origin (CORS)
- [ ] Postgres (`5432`) + plain NATS (`4222`) **not** published publicly — only `127.0.0.1` or
      internal Docker network; the only public ports are 80/443 (+ TLS-NATS if Option A)
- [ ] `.env`, `tunnels/.env`, and any `*.key` are gitignored and never committed
- [ ] The ed25519 **private signing key** is NOT on the server — only the **public** key in
      `BRISK_AGENT_PUBKEYS`
- [ ] Change `BRISK_ADMIN_PASSWORD` after first login; consider a non-default admin email
- [ ] Strong random `DB_PASSWORD`
- [ ] Firewall: default-deny inbound except 80/443 (+ 4222/tcp only if TLS-NATS)

---

## 13. Quick reference — what changes vs. the laptop

| Thing | Laptop (today) | Real server (this doc) |
|-------|----------------|------------------------|
| Edge → control plane | reverse SSH tunnel (`brisk-tunnels`) | direct HTTPS to `control.yourdomain.com` |
| `brisk-tunnels` containers | required | **deleted** (not used) |
| Dashboard image | dev (`Dockerfile.dev`, Vite HMR) | **prod** (`Dockerfile`, static + nginx) |
| `VITE_API_URL` | `http://localhost:8080` | `https://control.yourdomain.com` (baked at build) |
| TLS for the panel | none (http) | reverse proxy (Caddy) + Let's Encrypt |
| `BRISK_COOKIE_SECURE` | `false` | `true` |
| `8080` / `5173` host publish | yes | **removed** (Caddy fronts them) |
| NATS reach | tunnel | TLS-exposed or empty |
| Windows port-hang bug | possible | **n/a** (Linux Docker) |

---

**Related docs**
- Auto-deploy via GitHub (after this): `docs/future/Brisk_Agent_AutoDeploy_When_CP_On_Real_Server.md`
- Agent rollout to edges (how edge deploys work): `docs/features/Brisk_Agent_Rollout_Process.html`
- Changing the CDN base domain: `docs/features/Brisk_CDN_Domain_Migration_Runbook.html`
- Windows-only port-hang gotcha (does NOT apply to a Linux server): memory
  `docker-windows-stale-wslrelay-port-hang`
