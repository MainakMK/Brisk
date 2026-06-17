# Brisk — Control-Plane Ops (Phase 3.7 Step 2)

How the **laptop control plane** drives the **3 live edges** running the real
`brisk-agent`, over a Docker reverse-SSH tunnel. Companion to
`Brisk_Phase3_Runbook.md` (which covers multi-PoP DNS/drain/failover).

> **Step 2 status:** edges rebuilt on **nginx.org 1.30.x** (Server: Brisk + Brotli
> + video slicing restored); purge + stats fan-out verified on the real fleet;
> control-plane-managed wildcard TLS (lego Bunny DNS-01) **issued in production and
> cut over live on 2026-06-10** — all 3 edges serve the lego cert (issuer YE1,
> `*.a2zjav.com`+apex) and acme.sh is retired. See the dated sections below.

## Architecture (current)

```
laptop (behind NAT)                         edge (public IP, Ubuntu, sshd)
┌─────────────────────────────┐  ssh -R  ┌───────────────────────────────┐
│ brisk-control :8080  ◄───────┼──────────┼ 127.0.0.1:18080 ◄ brisk-agent  │ BRISK_CONTROL_URL
│ nats :4222           ◄───────┼──────────┼ 127.0.0.1:14222 ◄ brisk-agent  │ BRISK_NATS_URL
│ timescaledb / dashboard      │          │ nginx.org 1.30.x + agent       │
│ acme: lego Bunny DNS-01      │          │ (Server: Brisk, brotli, slice) │
│ tunnels: autossh containers ─┼─ dial out (laptop initiates)              │
└─────────────────────────────┘          └───────────────────────────────┘
```

- **Control plane** runs in Docker on the laptop (`brisk-control/docker-compose.yml`):
  `brisk-control` API (:8080), TimescaleDB, NATS, dashboard (:5173). Private —
  never bound to the public internet.
- **Connectivity** = `brisk-control/tunnels/` (autossh containers). The laptop
  dials out to each edge and reverse-forwards the API + NATS onto the edge's
  loopback. Nothing installed on the laptop host or the edges. See
  `tunnels/README.md`.
- **Edges** run the real `brisk-agent` (systemd) which: renders nginx from its
  template, heartbeats, pulls config (ETag/304), subscribes for NATS purges,
  ships stats. The agent **fully owns** `/etc/nginx/nginx.conf`.

## Start / stop the laptop control plane

```bash
# start (control plane + DB + NATS + dashboard)
cd brisk-control && docker compose up -d
# start the tunnels (one autossh container per edge)
cd tunnels && docker compose up -d        # needs tunnels/.env (edge creds)
# stop control plane (edges KEEP serving — data plane is independent)
cd brisk-control && docker compose stop brisk-control
```

**Resilience (verified live):** with the control plane stopped, all 3 edges keep
serving from last-known-good config + local cache; NATS JetStream replays missed
purges on reconnect. The reconciler's **all-offline guard** keeps DNS records
enabled even if every heartbeat goes stale, so the zone never NXDOMAINs. When the
laptop comes back, agents reconnect within ~30s and DNS reconverges. **Laptop
asleep ≠ CDN down.**

## Edge nginx reality (Step 2 — nginx.org build)

The edges now run **nginx.org stable 1.30.x** (the official `nginx.org/packages`
apt repo), NOT the Ubuntu distro build. The distro build was linked with
`-Wl,-Bsymbolic-functions`, which **segfaulted `headers-more`** (`more_set_headers`,
signal 11 — confirmed by bisect in Step 1). The nginx.org build runs the dynamic
modules cleanly, so Step 2 restored the full feature set:

- **`Server: Brisk`** + `X-Brisk-Edge` / `X-Brisk-Cache` / `X-Brisk-Request-Id` /
  HSTS via **`more_set_headers`** (applies on every status, inherited into all
  locations — unlike `add_header`).
- **Brotli** (`ngx_brotli`, `Content-Encoding: br`) for text + gzip fallback.
- **Video slice** module (1 MB slices, per-`$slice_range` cache key, coalescing).
- **`user www-data;`** (worker default is `nobody`, but the cache dir is
  www-data-owned → otherwise `open() Permission denied` → 500).
- origin via **`resolver` + `set $brisk_origin` + `proxy_pass https://$brisk_origin`**
  (IPv4-only; the origin `test.mainakghosh.com` sits behind Cloudflare, so its IP
  can change — a variable proxy_pass + resolver re-resolves at request time).

### Module ABI-lock (don't skip)

`headers-more` and `ngx_brotli` are dynamic modules and must be compiled against
the **exact** running nginx version. `bootstrap.go` installs nginx.org via apt,
then ABI-compiles the modules against `nginx -v`, version-stamped. After ANY nginx
upgrade the modules must be recompiled or `load_module` fails to start nginx. The
bootstrap is idempotent — re-running it recompiles when the stamp drifts.

### Two cache correctness rules (learned the hard way, Step 2)

- **Cache key is `$host`-based** (`$host$uri` for static, `$host$request_uri` for
  HTML), NOT the origin host. The control-plane purger matches stored objects by
  `<host><path>`; an origin-host key silently breaks purge (HIT survives a purge).
- **`open_file_cache` is OFF.** It caches open FDs of cache files, so a purged
  (unlinked) object keeps serving from the cached FD for up to 60s — a stale HIT
  after a purge. Brisk proxies everything (no hot local static), so it buys nothing
  and breaks purge. Leave it off.

Also: the agent reloads via **`systemctl reload nginx`** on systemd hosts (a bare
`nginx -s reload -c <path>` can target the wrong master and silently NOT apply).

## Verified fan-out behavior (Step 2, real 3-edge fleet)

- **Purge:** `POST /zones/{id}/purge` publishes per-edge NATS subjects
  (`brisk.purge.edge.<edgeID>`); each agent deletes matching cache files. Verified:
  warm all 3 → HIT, purge → **all 3 MISS**.
- **JetStream durability:** with one edge's tunnel down, a purge is queued; on
  reconnect the durable consumer **replays** the missed purge (verified: the
  offline edge went MISS after its tunnel came back).
- **Stats:** every edge ships per-PoP metrics every ~10s; the control plane
  ingests to TimescaleDB. Verified all 3 reporting concurrently.

## Managed wildcard TLS (Step 2, Part 3 — lego Bunny DNS-01)

Wildcard certs need **DNS-01**, and a wildcard names a TXT under the shared zone
(`_acme-challenge.a2zjav.com`). Rather than have 3 edges race on that record with
acme.sh, the **control plane issues the cert once, centrally** and fans it to edges
over the existing config-pull channel. This keeps the Bunny key in one place,
removes the race, and is the natural home for Phase-4 custom-domain certs.

```
control plane                                   edge
┌──────────────────────────────┐               ┌────────────────────────────┐
│ acme.Manager (12h tick)      │               │ agent ApplyWithTLS:        │
│  └ lego + Bunny DNS-01 ──TXT──┼─ Bunny DNS    │  tls_mode=managed →        │
│  └ store tls_certs (PEM)      │               │  WriteManaged(cert,key)    │
│ agentConfig handler ─────cert─┼─ pull (ETag) ─┼─→ /etc/brisk/tls/<dom>/    │
│  (only for tls_mode=managed)  │               │  validate cover + reload   │
└──────────────────────────────┘               └────────────────────────────┘
```

- **Issuer/renewal:** `internal/acme` — ECDSA P-256, ARI-aware renew at 30-day
  margin, 12h check with capped backoff. Stored in Postgres (`tls_certs`,
  migration `00010`). The private key lives in the DB (same trust level as the
  Bunny key already in the control-plane env); the dashboard `/tls/status` returns
  **metadata only** (issuer/serial/expiry), never the key or chain.
- **Distribution:** `GET /agent/config` attaches `tls_cert`/`tls_key`/`tls_cert_serial`
  to any zone in `tls_mode=managed` whose served hostname a managed cert covers
  (wildcard `*.a2zjav.com` covers `cdn.a2zjav.com`). The cert **serial is folded
  into the config ETag**, so a renewal triggers a pull + cert write with no zone edit.
- **Agent apply:** `tls.WriteManaged` validates the shipped cert (parses + must
  cover the host) BEFORE writing; on any problem it keeps the existing cert. Writes
  are atomic; reload is the usual `nginx -t` → reload → rollback-on-fail. **A bad
  shipment never drops TLS.**

### Config (control-plane env — never logged/committed)

```
BRISK_TLS_MANAGED=true
BRISK_TLS_EMAIL=<acme account email>
BRISK_TLS_STAGING=true                 # staging first; flip to false at cutover
BRISK_TLS_DOMAINS=*.a2zjav.com,a2zjav.com
# BRISK_TLS_CERT_NAME defaults to the apex (a2zjav.com)
# BRISK_TLS_ACME_DIR  defaults to /var/lib/brisk-control/acme (account key)
# reuses the existing BUNNY_API_KEY for the DNS-01 TXT challenge
```

### Additive-then-switch cutover (keep the live site up)

acme.sh keeps serving until the very end. Order:

1. Enable managed TLS with `BRISK_TLS_STAGING=true`; restart control plane →
   migration `00010` runs, the manager issues a **staging** cert.
   `GET /api/v1/tls/status` shows it (`staging:true`, issuer `(STAGING)…`).
2. Confirm the staging cert covers the SANs, then `BRISK_TLS_STAGING=false`,
   `POST /api/v1/tls/reissue` (or restart) → **production** cert. Verify
   `/tls/status` (real issuer, ~90-day expiry).
3. **Only now** flip the zone to managed: `PUT /api/v1/zones/{id}` with
   `tls_mode=managed`. The next agent pull ships the prod cert; each edge writes it
   and reloads. Verify each edge serves the lego cert
   (`echo | openssl s_client -connect <ip>:443 -servername cdn.a2zjav.com`).
   The write is safe per-edge, so this is effectively one-edge-at-a-time.
4. **Retire acme.sh** on the edges: disable its renew timer/cron so it stops
   rewriting `/etc/brisk/tls/<dom>/`. The agent now owns the cert.

**Rollback:** set the zone `tls_mode` back to `selfsigned` (the agent leaves the
existing valid cert in place — `selfsigned` is an idempotent skip when a usable
cert exists), and re-enable acme.sh. No edge restart needed.

## Per-edge agent rollout (the proven sequence)

> One edge at a time. Keep the hand-written `nginx.conf.brisk-bak` for rollback.
> The live site stays up because the OTHER edges serve while one is drained.

1. **Model the zone** in the control plane (`POST /zones` + assign to the server);
   mint a token (`POST /servers/{id}/token/rotate`).
2. **agent.yaml** (uniform): `control_plane_url: http://127.0.0.1:18080`,
   `nats_url: nats://127.0.0.1:14222`, the token, `tls_mode: selfsigned`,
   `cache_dir: /var/cache/brisk`, zone `cdn.<zone> -> https://<origin>`.
3. **Drain** the edge (`POST /servers/{id}/drain`) → traffic shifts to peers.
4. On the edge: back up `/etc/nginx/nginx.conf` → `.brisk-bak`; **adopt the
   existing wildcard cert** into `/etc/brisk/tls/<domain>/{fullchain,privkey}.pem`
   (agent keeps it — `selfsigned` mode is an idempotent skip when a valid cert
   exists); `chown -R www-data:www-data /var/cache/brisk`.
5. Push the **linux binary** (`brisk-agent/dist/brisk-agent-linux-amd64`) +
   `agent.yaml`; install the `brisk-agent.service` systemd unit.
6. `brisk-agent --oneshot` → renders + `nginx -t` + reload (rolls back on failure).
   Verify by IP: `200 HIT`, the test image (150840 B), `X-Brisk-Edge`, TLS.
7. `systemctl enable --now brisk-agent` → heartbeat + config pull + purge listen.
8. **Undrain** (`POST /servers/{id}/undrain`).

**Per-edge rollback:** `cp /etc/nginx/nginx.conf.brisk-bak /etc/nginx/nginx.conf
&& systemctl stop brisk-agent && systemctl restart nginx` → back to the
hand-written config; then undrain.

## Admin auth (Step 3 — dashboard + control-plane API)

Two caller types, two mechanisms, one tenant-aware identity core
(`internal/identity`). The agent path (`/agent/*`) is **separate and untouched**.

- **Dashboard UI** → **session cookie**: `brisk_session` (HttpOnly, SameSite=Lax,
  Secure in prod), 12h TTL, server-side (`sessions` table, id hashed at rest),
  rotated on refresh. **CSRF** via double-submit: a readable `brisk_csrf` cookie
  echoed in `X-CSRF-Token` on every state-changing request (bound to the session).
- **Scripts/automation** → **bearer admin token**: `Authorization: Bearer
  brisk_admin_…`, created in the dashboard (shown once), hashed at rest (`admin_api_tokens`),
  revocable. Distinct scheme from agent tokens; bearer callers are CSRF-exempt.
- **Passwords**: **argon2id** (slow KDF for low-entropy human secrets) — NOT the
  SHA-256 used for high-entropy agent/admin tokens. **Login is rate-limited**
  (5 fails / IP+email / min → 15-min lockout) with **no user enumeration**
  (identical error for bad user vs bad password).
- **Authorization chokepoint** (`identity.Authorize`): admin = all; customer = only
  `resource.account_id == caller.account_id`. Enforced NOW on zones/purge (admin
  bypasses); infra routes (servers/dns/stats/health/tls) are admin-only. The
  Phase-5 customer portal narrows into this — no refactor, no cross-tenant leak.
- **Bootstrap**: the first admin is seeded from `BRISK_ADMIN_EMAIL` +
  `BRISK_ADMIN_PASSWORD` on startup, only when account id 1 has no password yet
  (a password later changed in the dashboard is never clobbered). **No hardcoded
  default.** Creds live in the gitignored `.env`.

Wired through the dashboard's single **`authHeader()`/api client** seam:
`credentials:"include"` + CSRF header + a 401 drops to the login screen.

Env (control plane): `BRISK_ADMIN_EMAIL`, `BRISK_ADMIN_PASSWORD` (gitignored .env),
`BRISK_COOKIE_SECURE` (false dev / **true prod**), `BRISK_DASHBOARD_ORIGIN`
(credentialed-CORS allow-list, e.g. `https://dash.example.com`).

## Laptop → public-VPS cutover (the runbook)

The control plane is now **auth-gated** (above) AND tunnel-private, so exposing it
is safe. The data plane is independent — edges keep serving throughout. The agents
are endpoint-agnostic: going public is fundamentally a **2-URL change**
(`BRISK_CONTROL_URL` / `BRISK_NATS_URL`, from Step 1).

**Bring-up on the VPS**
1. Deploy `brisk-control` + TimescaleDB + NATS via Docker on the VPS. Secrets via
   **env only** (never baked into images / committed): `DATABASE_URL`, `BUNNY_API_KEY`,
   `BRISK_ADMIN_*`, `BRISK_TLS_*`.
2. **TLS on the control-plane API**: terminate HTTPS in front of `:8080` (Caddy/
   nginx reverse proxy or the platform LB). Set **`BRISK_COOKIE_SECURE=true`** and
   `BRISK_DASHBOARD_ORIGIN=https://<dashboard-host>`. All auth is insecure without TLS.
3. **Secure NATS** — it must NOT be left open on a public box. Enable NATS auth
   (token/nkey) + TLS, or keep it on a private network/VPN reachable only by the
   control plane and the edges. Update `AGENT_NATS_URL` accordingly.
4. **Firewall**: expose only 443 (API/dashboard) publicly. DB (5432) and NATS
   (4222) stay private/bound to the internal network. SSH locked down.
5. **DB**: restore from a `pg_dump` of the laptop DB (accounts, zones, servers,
   tokens, `tls_certs`) so identities + the managed cert carry over. Schedule
   `pg_dump` backups.

**Cut the edges over (one at a time)**
6. Point each agent's `control_plane_url` / `nats_url` (via the control plane's
   `AGENT_CONTROL_PLANE_URL` / `AGENT_NATS_URL`, written into `agent.yaml`) at the
   VPS URLs. Stop the laptop tunnels (`cd tunnels && docker compose down`). Agents
   reconnect + re-pull. **No agent rebuild, no template change.**
7. **lego TLS continuity**: the managed cert + ACME account key move with the DB +
   the `brisk_acme` volume (or re-issue on the VPS — same Bunny key, idempotent).
   The 30-day auto-renew just continues.

**Rollback to the laptop**: bring the tunnels back up (`tunnels/docker compose up -d`),
revert the 2 URLs to `127.0.0.1:18080` / `127.0.0.1:14222`; agents reconnect to the
laptop. Edges never stopped serving.

> This is the **plan**; the actual public deploy is the user's call later (they want
> to keep iterating locally for now).

## Security sweep (Step 3)

- ✅ Login **rate-limited** (lockout) + **no user enumeration**.
- ✅ Passwords **argon2id**; agent/admin tokens **hashed at rest** + **revocable**.
- ✅ Session cookie **HttpOnly + SameSite** (+ **Secure** in prod via `BRISK_COOKIE_SECURE`);
  **CSRF** double-submit on state-changing requests.
- ✅ **Tenant scoping** enforced now (admin=all, customer=own) — portal-safe.
- ✅ Agent token path **untouched** — the 3 live agents kept heartbeating/pulling/purging
  through the whole change (verified).
- ✅ No secrets in logs/repo (Bunny key, admin password, tokens all gitignored env);
  HTTPS assumed in prod; NATS must be secured before public exposure (above).

## Multi-tenant host-based origin routing (Phase 4 Step 1)

The agent renders **one nginx `server` block per assigned zone** —
`server_name = cdn_hostname`, `proxy_pass` to that zone's own `origin_url`, the
zone's own settings (TLS/video/cache rules/Brotli/CORS) — so one edge serves many
customer sites, routed by the **Host header**. Built and **verified live on
2026-06-10** (`demo.a2zjav.com`→example.com and `cdn.a2zjav.com`→a2zjav served from
the same edge IP simultaneously, then the demo tenant was removed).

- **Per-tenant cache isolation:** every cache key includes `$host`, so two tenants'
  identical paths never collide and a purge on one never touches the other.
- **Per-zone upstream Host:** `zones.host_header` (empty = the origin's own host).
  Set it when the origin serves by a name other than the CDN host (e.g. a proxied
  origin). Carried store → API → agent-config → render.
- **Unique `cdn_hostname`:** DB UNIQUE + API 409 on duplicate (one host = one block).
- **`default_server` for unknown hosts:** a request whose Host matches no tenant
  zone gets a clean **444** (never a random tenant's content).
- **⚠️ default_server + health checks (important):** the control-plane health
  checker probes edges **by IP**, and Go sends **no SNI for an IP literal**, so
  those probes land on the `default_server`, not a tenant block. Therefore the
  `default_server` is NOT `ssl_reject_handshake` — it carries the managed wildcard
  cert (the checker uses `InsecureSkipVerify`, so the name needn't match) and
  **serves `/healthz` → 200**, with everything else → 444. Without this, every edge
  would fail health checks and get pulled from DNS. Verified: `/healthz` by IP → 200
  on all 3 edges after rollout.
- **Cert scope:** all Step-1 tenant hostnames are Brisk subdomains under the
  existing wildcard `*.a2zjav.com` (one label, e.g. `demo.a2zjav.com`). Customer-owned
  domains + per-domain SNI certs are **Step 2**. The template is structured so a
  per-block `ssl_certificate` slots in then.
- **Ops:** `brisk-agent --render` dumps the nginx.conf a given `agent.yaml` would
  produce (no side effects) — handy for reviewing multi-tenant output.

Rollout was the proven drain → deploy (`tunnels/deploy-agent.sh`) → verify → undrain,
one edge at a time; `cdn.a2zjav.com` stayed byte-identical (its `UpstreamHost` ==
its origin host; the only added block is the `default_server`).

## Custom domains + per-domain auto-TLS (Phase 4 Step 2)

The commercial gateway: a tenant points **their own domain** (e.g.
`cdn.theirsite.com`) at a Brisk zone and gets **automatic HTTPS** — no manual cert
handling, served from every edge via SNI.

**Lifecycle** (`custom_domains` table, migration 00013):
`pending_dns → verifying → issuing → active → (renewing) → failed`.
The `customdomains.Manager` scans every 30s and advances each domain:

1. **Add** (`POST /api/v1/zones/{id}/domains`) → `pending_dns` + the exact CNAME
   record to create (target = the zone's `cdn_hostname`). Apex domains are detected
   (public-suffix list) and shown ALIAS/flattening guidance — **never per-edge A
   records** (those bypass geo routing + failover).
2. **Verify (gate before any ACME)** — the manager resolves the domain against a
   **public resolver (1.1.1.1)** and only proceeds if the CNAME chain lands on the
   zone hostname OR the A records hit a known edge IP. This is the rate-limit AND
   abuse gate: **no ACME is ever attempted for a domain not actually routed to
   Brisk.** Pending domains re-check on a mild backoff; `POST /domains/{id}/verify`
   ("check now") forces an immediate pass.
3. **Issue** — lego **HTTP-01** (no Bunny key needed; Brisk doesn't touch the
   customer's DNS). The challenge is answered **centrally**: `acme.ChallengeStore`
   holds `token → keyAuth`, and every edge proxies `:80
   /.well-known/acme-challenge/*` to the control plane over the agent tunnel
   (`http://127.0.0.1:18080`). So **whichever geo-routed edge the CA validates from**
   answers correctly. The cert is stored in `tls_certs` (keyed by the domain), the
   parent zone's `config_version` is bumped, and issuance is **serialized** (one
   ACME job at a time).
4. **Serve** — the agent renders the active custom domain as its **own `server`
   block** (`server_name` = the domain, its own `ssl_certificate`, same zone
   origin/settings); appended as a synthetic agent zone, it flows through the exact
   multi-tenant render + managed-cert path. SNI picks the right cert; many certs,
   same edge IPs.
5. **Renew** — the manager renews within 30 days of expiry and **re-verifies DNS
   first**. If the CNAME was removed it records the error and backs off but **keeps
   serving the old cert until expiry** (never drops TLS early); it does not hammer
   ACME for a domain that left.
6. **Detach** (`DELETE /api/v1/domains/{id}`) — removes the lifecycle row + the
   per-domain cert (no longer fanned out) and bumps `config_version` so edges drop
   the vhost.

**Challenge-proxy nginx detail:** the `:80` server blocks 301-redirect to HTTPS,
but `location ^~ /.well-known/acme-challenge/` (which wins) proxies to the control
plane over **plain HTTP** — excluded from the redirect, or validation breaks. The
Step-1 `default_server` ALSO carries the challenge proxy (so a no-SNI/by-IP CA hit
still validates) while keeping its `/healthz`→200 + `444` health-probe behavior.
When `control_plane_url` is empty (standalone/local) the template falls back to the
legacy LE webroot.

**Staging vs production ACME:** `BRISK_TLS_STAGING` (default `true`) selects the
Let's Encrypt **staging** directory. **Iterate on staging** — production LE limits
include **5 failed validations/hour** and duplicate-cert caps; burning them on
tests can lock issuance for days. The verify-before-issue gate plus the issuance
backoff (≥20 min floor, exponential, capped 12h) keep us well under the limit. Flip
to production (`BRISK_TLS_STAGING=false`) only for the real cutover.

**Rate-limit playbook:** a stuck domain stays `pending`/`failed` with a
human-readable `last_error` surfaced in the dashboard (and `GET /api/v1/domains`
admin list) — never a silent retry loop. Verification (cheap DNS) retries every
~2–15 min; ACME failures back off 20 min → 12 h.

**RBAC:** domain endpoints are tenant-scoped via the identity chokepoint — a
customer manages only its own zones' domains; admin sees all (`GET /domains`).

**Endpoints:** `POST/GET /api/v1/zones/{id}/domains`, `POST
/api/v1/domains/{id}/verify`, `DELETE /api/v1/domains/{id}`, admin `GET
/api/v1/domains`, and the unauthenticated `GET /.well-known/acme-challenge/{token}`
(mounted at the root, outside `/api/v1`).

**Future (NOT built):** DNS **delegated validation** — a customer CNAMEs
`_acme-challenge.theirsite.com → <verify-host>.a2zjav.com` and Brisk answers DNS-01
in its own Bunny zone (the Cloudflare-for-SaaS pattern; enables **wildcard**
customer domains). HTTP-01 is the natural primary for a CDN and covers
bring-your-own subdomains today; delegated DNS-01 is the documented next option.

## Origin Shield — mid-tier cache, per zone (Phase 4 Step 3)

A **shield** is another Brisk PoP (`servers.role = 'shield'`) that sits in front of
the origins. For a **shielded zone**, normal edges proxy that zone's cache-misses to
the shield instead of each edge hitting the origin — so **many edges missing the
same object collapse to ~one origin fetch** (`proxy_cache_lock` at both tiers).
Per-zone opt-in (great for static/video; little benefit for dynamic — an extra hop).

**How the upstream is computed** (control plane, per (edge, zone), in `agentConfig`
→ `shieldUpstreamFor`): shield ON + the target is a `role=shield` server + it's not
this server + the shield is serving (online, not drained, not health-unhealthy) →
this zone's upstream = `shield_host:443`; otherwise → the real origin. The shield
PoP itself, and any edge that is its own shield, always go to the origin (loop
guard). `shield_server_id` NULL falls back to `BRISK_DEFAULT_SHIELD_SERVER_ID`.
The computed `shield_upstream` is folded into the agent-config ETag, so a shield
health/role/config flip triggers a re-pull even without a zone edit.

**Cache-key parity (the #1 correctness rule):** the edge forwards `Host=$host` (the
served hostname) to the shield, and **both tiers key on `$host`** — so the shield
caches under the SAME key the edge uses. If the keys diverged, the shield would MISS
on every edge and you'd get zero offload. The local lab proves this: two edges
missing the same object produce exactly **one** origin fetch (a mismatch would make
the second edge MISS at the shield → two fetches).

**Observability:** the edge adds `X-Brisk-Shield: $upstream_http_x_brisk_cache` —
the SHIELD tier's HIT/MISS (the shield's own `X-Brisk-Cache`, captured before it's
hidden), distinct from the edge's own `X-Brisk-Cache`. Two-tier visibility per
request.

**Graceful shield-failure fallback:** the shield hop has `proxy_connect_timeout 2s`
+ `proxy_intercept_errors on` + `error_page 502 503 504 = @brisk_origin_fallback`.
If the shield is down/unreachable, the edge serves straight from the **origin**
(correct upstream Host; uncached in this degraded path) — never a blackhole — and
resumes shield caching the moment it recovers. Sustained shield death is also caught
by the Phase-3 health system: `shieldUpstreamFor` returns "" for an unhealthy shield,
so edges re-pull origin-direct (with caching restored) within a poll interval.

**Shields are out of DNS:** the geo-routing reconciler skips `role=shield` servers
(they're mid-tier, not user-facing). The health checker still probes them.

**Live-site safety:** enabling/disabling shield bumps `config_version` (a config
change over the poll interval; `nginx -t` before reload), so a zone — incl.
`cdn.a2zjav.com` — keeps serving through an enable/disable. **For Step 3 the live
fleet was NOT touched:** all 3 live edges stay `role=edge`, every zone's shield stays
OFF; the topology was proven entirely in the local lab (`shield-lab/run.sh`).

**Endpoints / config:** `POST /api/v1/zones/{id}/shield {enabled, shield_server_id}`
(tenant-scoped; target must be `role=shield` else 400), `POST
/api/v1/servers/{id}/role {role}` (admin), `BRISK_DEFAULT_SHIELD_SERVER_ID` (network
default; 0 = none).

**Local proof:** `shield-lab/` — an isolated compose (2 origins, 1 shield, 2 edges)
on the real Brisk-nginx image (`Dockerfile.edge` builds nginx.org + headers-more +
brotli + the agent). `bash shield-lab/run.sh` ⇒ 10/10: collapse, concurrency,
video-slice caching, per-zone isolation, shield-death fallback + resume, loop guard,
cache-key parity. **Analytics gap (flagged):** a precise origin-offload metric needs
origin-tier counters the stats schema doesn't record yet — a Phase-4 cleanup step.

## Known follow-ups

Done in Steps 2–3:
- ✅ **Server: Brisk** + **brotli** + **video slicing** — edges moved to nginx.org.
- ✅ Purge + stats fan-out verified across the real fleet.
- ✅ Control-plane-managed DNS-01 wildcard TLS (lego Bunny) — **issued in prod and
  cut over live 2026-06-10**: staging proven first, prod cert issued (issuer YE1),
  new agent rolled to all 3 edges (drain/undrain), zone 6 flipped to `tls_mode=managed`,
  all 3 edges + `cdn.a2zjav.com` verified serving the lego cert, acme.sh cron disabled
  on every edge. Deploy/retire helpers: `tunnels/deploy-agent.sh`, `tunnels/retire-acme.sh`.
- ✅ **Admin auth + tenant-aware RBAC** (Step 3) — session cookie + bearer tokens +
  argon2 + rate-limited login, agent path untouched, cutover runbook above.
  **Phase 3.7 is complete.**
- ✅ **Phase 4 Step 1 — multi-tenant host-based origin routing** (2026-06-10):
  one server block per zone, per-zone origin + upstream Host, `$host` cache
  isolation, `default_server` (health-safe), unique hostname (409). Rolled to all
  3 edges; verified live; `cdn.a2zjav.com` unchanged. See the section above.

- ✅ **Phase 4 Step 2 — custom domains + per-domain auto-TLS** (built + **proven
  live 2026-06-10**): lifecycle state machine (migration 00013), verify-before-issue
  gate, lego HTTP-01 answered via the edges' challenge proxy, per-domain certs
  fanned out + served via SNI, renewal with DNS re-verification, dashboard Custom
  Domains tab + admin list. New agent rolled to all 3 edges (drain/undrain, one at a
  time; `cdn.a2zjav.com` 200 + correct `X-Brisk-Edge` throughout). End-to-end proof
  on `cdn-test.mainakghosh.com` (CNAME → `cdn.a2zjav.com`): `verifying → issuing →
  active` on **staging** (SNI cert on all 3 edges), then **production** ACME
  (`BRISK_CUSTOM_TLS_STAGING=false`) — the prod cert validates against the public
  trust store on all 3 edges. Detach removed the vhost + cert fleet-wide; uniqueness
  409; multi-tenant + by-IP health stayed green throughout. The transient prod
  re-issue 502 (tunnel reconnecting) correctly **kept the old cert serving + backed
  off** — proof the "never drop TLS early" path works.
  - **Operational note:** custom-domain TLS now runs in **production**
    (`BRISK_CUSTOM_TLS_STAGING=false` in `.env`), INDEPENDENT of the wildcard's
    `BRISK_TLS_STAGING`. **Recreating the control-plane container changes its IP →
    `docker restart` the 3 `brisk-tunnels-tunnel-*` containers afterward** or edges
    can't reach it (config-pull + the ACME challenge proxy both 502 until the
    tunnels reconnect).

- ✅ **Phase 4 Step 3 — origin shield (mid-tier cache, per zone)** (built + **proven
  locally 2026-06-11**): server `role` + per-zone `origin_shield_enabled`/
  `shield_server_id` (migration 00014), control-plane per-(edge,zone) upstream with
  loop/role/health guards (ETag-tracked), the agent's two-tier proxy (shield-or-origin,
  same `$host` cache key, `X-Brisk-Shield`, instant `error_page` origin fallback),
  shields excluded from DNS, dashboard zone toggle + shield role badge/setter +
  honest origin-offload note. `shield-lab/run.sh` ⇒ 10/10. **Live fleet untouched**
  (all `role=edge`, all zones shield OFF) — see the section above.

- ✅ **Phase 4 Step 4 — per-zone WAF + rate limiting** (built + **proven locally
  2026-06-11**): a managed **OWASP CRS v4** ruleset + custom rules + Nginx-native
  rate limits, per zone, at a **detect (log-only) or block** mode, with a
  **security-event firewall log**. `waf-lab/run.sh` ⇒ **20/20** (SQLi/XSS block on
  the block zone + pass-through on the off zone, detect-mode would-block logging,
  custom-rule ordering, rate-limit 429, WordPress preset, body-inspect cap,
  fail-open/closed, no regressions). **WAF is OFF by default on every live zone.**
  Schema: migration `00015_waf` (zones `waf_*` cols; `waf_custom_rules`,
  `waf_rate_limits`; `security_events` hypertable). Key decisions:
  - **Engine: OWASP Coraza (pure Go, CRS v4) embedded in `brisk-agent`.** Nginx
    inspects each request via stock **`auth_request`** → the agent's loopback Coraza
    service (`waf_listen`, default `127.0.0.1:9555`), which runs that zone's managed
    CRS + custom rules and returns 200 (allow) / 403 (block). Chosen over
    **coraza-spoa** (HAProxy SPOE only — nginx has no SPOE) and the **Coraza nginx
    C-connector** (heavy cgo build; our agent is already Go + CGO-free). Pure-Go keeps
    the edge a single static binary with CRS rules embedded (no extra files);
    ModSecurity is EOL (2024).
  - **Evaluation order:** custom rules → managed CRS *inside* the WAF service (a
    terminating block/challenge/allow short-circuits); **rate limiting is Nginx
    native** (`limit_req`, preaccess phase, runs *before* `auth_request`). Documented
    split: a custom `allow` does **not** exempt a client from a path's rate limit —
    scope rate limits to specific paths (we do).
  - **Mode:** the engine always runs `SecRuleEngine On`; the agent decides
    block-vs-detect from the interruption + the zone mode, so **detect mode still
    emits the "would-block" event** for tuning. Tuning workflow: enable → start in
    **detect** → review Security events → switch to **block**.
  - **Fail policy (per zone):** on a WAF engine error *or* the WAF service being
    unreachable, **fail open** by default (availability > security; a broken WAF must
    not blackhole a tenant) and log loudly — rendered as `error_page … =
    @waf_failopen` on `/_waf`. `fail_open=false` fails closed (500). Proven both ways.
  - **Body-inspect cap:** `auth_request` forwards **no request body** and Coraza
    `SecRequestBodyLimit` is 128 KB — large media/file/video bodies are **not
    deep-scanned**. URI + query + headers are always inspected; segments
    (`.ts/.m4s/.mp4`) skip the hook entirely.
  - **Rate-limit counters are PER-EDGE + approximate** (per-datacenter, like
    Cloudflare). `requests/period` → an nginx `Nr/m`|`Nr/s` rate; default
    `burst = requests-1 nodelay` so "N per window" lets the first N through and limits
    the (N+1)th. `errors_only` is exposed but edge-approximated (`limit_req` is a
    request limiter, not response-aware).
  - **Security events** ship over the **existing stats pipeline** (bounded,
    drop-oldest → `POST /agent/security-events` → `security_events` hypertable).
    Tenant view: `GET /zones/{id}/security-events`; admin cross-tenant:
    `GET /security-events` + `/security-events/summary`. RBAC via the same `scopeZone`
    chokepoint (a customer manages only its own zones' WAF).
  - **Country rules** need a GeoIP source (future `X-Brisk-WAF-Country`); IP / path /
    method / header / user-agent rules work today. Challenge = block for now (richer
    JS-challenge UX is later, with Lua Step 5).
  - **Propagation:** a WAF change bumps the zone's `config_version` → edges re-pull →
    `nginx -t` then reload → the agent recompiles only the changed zone's CRS
    (fingerprint cache). Live-safe through any enable/disable.

- ✅ **Phase 4 Step 5 — Lua programmable edge + custom cache-rule enforcement**
  (built + **proven locally 2026-06-11**): a Lua layer on the edges enforces the
  per-zone custom cache rules (override-TTL / bypass / force-download / redirect,
  priority first-match) — closing the long-standing "rules stored but not enforced"
  backlog — plus per-zone request/response **header transforms**. `lua-lab/run.sh`
  ⇒ **22/22**. Schema: migration `00016_header_transforms` (cache_rules already
  existed since 00001). Key decisions:
  - **Build choice: Option A — `lua-nginx-module` + `ngx_devel_kit` (NDK) + LuaJIT
    (OpenResty's luajit2) as DYNAMIC modules on the existing nginx.org build**, ABI-
    locked to the nginx version like headers-more/brotli (probe-verified: builds +
    `nginx -t` clean alongside them via `--with-compat`). Chosen over switching the
    edge to OpenResty (would re-validate the whole headers-more/brotli/slice/Coraza/
    TLS/multi-tenant stack). `lua-resty-core`/`lrucache` install to
    `/usr/local/brisk-lua` (`lua_package_path`); the recipe is in `bootstrap.go`
    `EnsureLua` (gated, one edge at a time). The agent renders the Lua directives
    **only when the module is present** (`luaAvailable()` stat-checks the .so), so
    edges WITHOUT it (today's live fleet) render no Lua and stay byte-identical.
  - **Framework:** a small embedded Lua library (`brisk.lua` + `init`/`rewrite`/
    `header_filter` entrypoints) written to `/etc/brisk/lua` on each apply; per-zone
    data rendered to `zones_data.lua` (a Lua table literal: TTLs pre-converted to
    seconds, deny-list, priority-ordered). `init_by_lua` loads it once per reload
    (no per-request disk reads). A zone gets `rewrite_by_lua` + `header_filter_by_lua`
    hooks **only if it has cache rules or header transforms** (else no hooks).
  - **Phase order:** Lua `rewrite` (redirect / bypass / mark-ttl + request header
    transforms) → WAF `auth_request` (access) → cache lookup → upstream → Lua
    `header_filter` (override-ttl Cache-Control / force_download + response header
    transforms). Lua is loaded BEFORE headers-more so its header filter runs AFTER
    it (the override-ttl Cache-Control wins).
  - **Bypass uses a REQUEST HEADER, not a writable nginx var:** a `set` var doesn't
    survive the WAF `auth_request` access phase, so a `bypass_cache` rule sets
    `X-Brisk-Lua-Nocache: 1`, read by `proxy_cache_bypass $http_x_brisk_lua_nocache`.
    (Found + fixed during the lab.)
  - **Skip guard:** the Lua skips internal subrequests (`ngx.req.is_internal()` — the
    WAF `/_waf`, error-page fallbacks) and `/healthz`/`/_waf`, so a `/`-prefix
    redirect rule never hijacks the health probe or the WAF subrequest.
  - **Fail-open:** every per-request Lua path is `pcall`-wrapped — a broken rule
    (e.g. an invalid regex) falls back to default behavior (serve normally) + logs,
    never blackholes a tenant. `nginx -t` runs `init_by_lua` so a bad data/syntax
    error is caught before the reload (rollback).
  - **Managed-header deny-list** (enforced in the API + the Lua + the dashboard UI):
    tenants can't clobber `X-Brisk-*`, `Server`, HSTS, `Host`, or framing headers.
  - **No regressions:** WAF (Coraza), origin shield, custom-domain SNI TLS, slice
    video, Brotli, `$host` cache key, `default_server` /healthz all keep working with
    Lua in the pipeline (lab: a WAF-block zone still 403s SQLi while its redirect /
    transform rules apply). **Country rules / response-aware errors-only rate limits**
    are now feasible with Lua but not yet wired (future).

Still open:
- Fold the apt/`user www-data`/cert-adopt steps fully into `bootstrap.go` so the
  agent self-installs without the manual rollout script.
- **Origin-tier stat counters** (Phase 4 cleanup): record origin-tier requests so the
  dashboard can show a precise origin-offload number for shielded zones (today the
  per-request offload is visible only via the `X-Brisk-Shield` header).
- **GeoIP source for WAF country rules** + a response-aware `errors_only` rate-limit
  path (Lua/OpenResty makes both clean — Step 5).
- **Phase 4 Step 6 (do NOT start until asked):** hardening + cleanup sweep (closes
  Phase 4) — the carried Phase-2/3 backlog (`PUT /rules/{id}` + bulk reorder,
  `GET /zones/{id}/servers`, network-aggregate `/stats`, status-code/geo/top-paths/
  latency + origin-tier counters for the shield offload metric, a real logs API to
  replace the Logs placeholder), a security/perf audit of the whole multi-tenant +
  WAF + TLS + Lua surface, and docs polish. Then Phase 5 (customer portal + billing).
  Also gated: rolling the lua module onto the live fleet (one edge at a time via
  `bootstrap.go` `EnsureLua`, verify `cdn.a2zjav.com` byte-identical); a real shield
  PoP on the live fleet; WAF live-enablement on a real tenant zone (start in detect,
  review events, then block); GeoIP for WAF country rules + response-aware
  errors-only rate limits (now feasible with Lua).

Phase-4 hook (custom domains): the `tls_certs` table + `internal/acme` issuer
already generalize beyond the wildcard — a per-customer custom domain becomes a new
`tls_certs` row (its own SANs, same Bunny DNS-01 issuer) that the agent-config
handler ships to whichever edges serve that zone. No new mechanism needed.

## Phase 4 Step 6 — real logs, analytics depth, GeoIP, errors-only limits, origin lockdown

**Real logs pipeline.** Each edge writes a second, JSON access log
(`/var/log/nginx/brisk.requests.log`, `log_format brisk_json`) with ts, ip, country,
method, host, path, status, bytes, cache status, request/upstream timing, referer, UA,
request-id. The agent's `logship` package tails it (skips history on first read, handles
truncation/rotation) and ships batches via the **same bounded, drop-oldest discipline as
stats** to `POST /agent/logs`. The control plane bulk-inserts (COPY) into the
`request_logs` Timescale hypertable: **7-day retention** + columnstore after 6h (logs are
voluminous — never kept forever). Migration `00017` (verified applies up→down→up).
- **APIs:** `GET /zones/{id}/logs?from&to&status&cache&path&ip&country&limit` (tenant,
  RBAC-scoped via `scopeZone`) + `GET /logs` (admin, optional `zone_id`). The dashboard
  **Logs page** is real, filterable, polled (~5s), recent-first, capped (≤1000 rows).
- **Privacy:** rows hold client IPs/UAs (PII) → 7-day auto-drop, tenant-scoped reads. No
  request bodies logged.

**Real origin-offload + analytics depth.** `GET /zones/{id}/logs/analytics` (+ admin
`/logs/analytics`) aggregate `request_logs` over a window: **origin offload** (HIT vs
origin-fetch, by request **and** bytes), status-code breakdown (2xx/3xx/4xx/5xx), **latency
p50/p95/p99** (`percentile_cont` on `request_time`), top paths, top countries. The
Analytics page renders these (the two honest "not collected" placeholders are gone). The
offload number is now truthful (counted from real per-request cache status), not estimated.

**GeoIP (gated).** `bootstrap.go EnsureGeoIP` builds `ngx_http_geoip2` (needs
`libmaxminddb-dev`) and, if `BRISK_GEOIP_LICENSE_KEY` is set, downloads
`GeoLite2-Country.mmdb` to `/etc/brisk/geoip/`. The agent renders the `geoip2` block +
`$brisk_country` **only when both the module .so and the mmdb exist** (`geoipAvailable()`);
otherwise country is `"-"` (byte-identical). Country flows into logs, analytics, and **WAF
country rules** (the WAF subrequest passes `X-Brisk-WAF-Country $brisk_country`; Coraza
matches `field=country`).

**Errors-only rate limiting (Lua).** nginx `limit_req` can't count by response status, so
a rate limit with `count_mode=errors_only` is enforced by the Lua edge: a `lua_shared_dict
brisk_rl` per-IP counter, bumped in the **log phase** on 401/403 and checked in the
**access phase** (→429). Only zones with such limits get the access/log hooks; needs the
Lua module on the serving edge. `all`-mode limits stay nginx-native. UI: zone Security tab
→ rate limit → "Errors only · 401/403 (Lua)".

**Origin lockdown (the #1 CDN gap).** Set `origin_pull_secret` (+ optional
`origin_pull_header`, default `X-Brisk-Pull-Token`) in the agent config → the edge adds the
secret header to every **origin** request (origin path + shield-down fallback), never the
client. The customer origin rejects requests without it, so traffic must traverse Brisk.
Alternatives (document to tenants): egress-IP allowlist (NY/DE/BLR edge IPs) or mTLS
(future). Never logged; empty = off. Full audit: **`docs/Security_Audit_Phase4.md`**.

**Backlog folded in:** atomic `PUT /zones/{id}/rules/{rid}` + `POST .../rules/reorder`
(no ID churn; dashboard editor uses it), `GET /zones/{id}/servers` (inverse lookup),
network-aggregate `GET /stats/network` (server-side "All PoPs" merge).

### Live-enablement runbooks (all opt-in; never flip prod on without intent)
- **Lua module (gated, Part 1, not done):** one edge at a time — drain in dashboard →
  deploy the final agent → it runs `EnsureLua` (builds NDK + lua-nginx-module + LuaJIT) →
  `nginx -t` → reload → verify `cdn.a2zjav.com` 200, `Server: Brisk`, cache HIT, video,
  TLS, WAF, `/healthz` byte-identical → undrain → next edge. Rollback: redeploy the prior
  agent / remove `load_module` (gated render makes the module's absence byte-identical).
- **GeoIP per edge:** install `libmaxminddb`, run `EnsureGeoIP` (+ licence key for the DB),
  reload; country lights up in logs/analytics; then country WAF rules become enforceable.
- **WAF per tenant zone:** enable in **detect** first, review the firewall log, then switch
  to **block**. Fail-open by default.
- **Origin shield per zone:** toggle in the dashboard once a `role=shield` PoP exists.
- **Origin lockdown:** set `origin_pull_secret`, configure the customer origin to require
  the header (or allowlist the edge IPs), then verify a direct (non-Brisk) origin hit 403s.

## Control-plane refresh — v13 → v17 (lit up Phase-4 features on the live fleet, 2026-06-12)

The live laptop `brisk-control` had been running an old build at **migration v13**, so the
Phase-4 Step-6 features (request logs, analytics depth, cache-rule/WAF/shield definition,
origin-offload) couldn't be used from the live panel even though the edges already ran the
Step-6 agent. A **control-plane-only** rebuild fixed this — **no edge changes, no draining**
(edges serve independently from last-known-good config + local cache, so the live sites
stayed up while the management/ingest layer briefly bounced).

**Procedure (the proven, safe sequence):**
1. **Back up first** — `docker exec <timescaledb> pg_dump -U brisk -Fc -d brisk > backups/brisk-v13-<date>.dump`
   (consistent custom-format logical backup; the circular-FK warnings on `hypertable`/`chunk`/
   `continuous_agg` are standard TimescaleDB catalog advisories, harmless). Also tag the running
   image for rollback: `docker tag <image-id> brisk-control-brisk-control:rollback-v13`.
2. **Rebuild + restart** — `docker compose build brisk-control && docker compose up -d brisk-control`.
   The TimescaleDB **named volume persists** (never wiped). On boot `main.go` runs `migrate.Up`,
   applying **00014 (shield/role) → 00015 (WAF) → 00016 (header_transforms) → 00017 (request_logs)**
   → version 17. Watch the logs: every migration `OK`, no errors. (Migrations are additive — new
   tables/columns only — and the store uses explicit-column SELECTs, so the `rollback-v13` image
   stays fully compatible even against the migrated DB; the dump is the belt-and-suspenders.)
3. **Restart the tunnels** — `cd tunnels && docker compose restart`. **Required**: recreating
   `brisk-control` gives it a **new container IP**, so the autossh reverse-forwards must be
   re-established for agents to reconnect (the documented gotcha). The tunnels attach to
   `brisk-control_default` (external) and resolve `brisk-control:8080` fresh.
4. **Verify reconnect + features** — within ~40s all 3 agents resume heartbeat (`last_seen`
   fresh), config-pull, stats, and **logs** (`POST /agent/logs` now 200; the agents' bounded
   drop-oldest buffers flush their backlog into `request_logs`). Fire a uniquely-tagged request
   through each edge and confirm it lands in `request_logs` within seconds (proves the live
   pipeline, not just the historical flush). Hit `GET /logs`, `GET /zones/{id}/logs/analytics`
   (real offload %, status breakdown, p50/p95/p99, top paths), and save a cache rule on a
   **throwaway zone** (config_version bumps) — then delete it. Confirm the **live zone stays
   opt-in** (waf_enabled=false, origin_shield_enabled=false, 0 rules/transforms/limits).

**Rollback** (if a migration had failed or anything regressed): `docker compose stop brisk-control`,
restore the dump (`timescaledb_pre_restore()` → `pg_restore` → `timescaledb_post_restore()`), and
relaunch the `rollback-v13` image. Not needed here — all four migrations applied cleanly.

**Outcome:** live control plane now runs current code at **v17**; all 3 edges reconnected and
ship logs/stats; the dashboard **Logs / Analytics / cache-rules / WAF / shield / origin-offload**
are live and usable; `cdn.a2zjav.com` served 200 throughout (verified continuously). The
Phase-4 Step-6 backlog is now **actually live**. Backup: `backups/brisk-v13-20260611.dump`.

**Still opt-in.** Every new capability is **available but off by default** — enabling WAF /
origin shield / cache rules / origin-lockdown on a real zone is a separate, deliberate action.
GeoIP country stays `-` until a GeoLite2 mmdb is installed on the edges (module is built, gated
off). Origin-lockdown caveat: only set `origin_pull_secret` once the origin actually checks the
header, or it will reject legitimate traffic.
