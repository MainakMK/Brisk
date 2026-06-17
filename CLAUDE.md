# CLAUDE.md — Brisk CDN

Compact project rules. Read this every session. **Full detail lives in `Brisk_Phase1_Build_Spec.md` — read it before building.**

## What Brisk is
A self‑hosted, fully‑owned CDN (like Bunny/KeyCDN/CDN77) for our large private sites, designed to scale and later be sold. Core content: **HLS video** + static web assets. The recipe: **Nginx (cache) + Go `brisk-agent` (control) + Lua later** — same pattern the big CDNs use, taking only the free, proven pieces.

## Golden rules (non‑negotiable)
1. **Brisk runs forever on our own code (Go + Nginx).** No AI tool or third‑party service in the production runtime. Claude Code builds it; the agent + Nginx run it.
2. **Edges = bare‑metal, no Docker.** Docker is only for local testing and the (separate) control‑plane/dashboard.
3. **Data plane is independent of the control plane.** If the dashboard/control plane is down, edges keep serving from their last saved config. Dashboard down ≠ CDN down.
4. **Never reload Nginx with a bad config.** Always `nginx -t` first; only `nginx -s reload` on success; keep the previous good config to roll back.
5. **Idempotent bootstrap** — safe to re‑run; check before installing.
6. **Lightweight everywhere** — one small static Go binary per edge; minimal deps; the box's resources are for caching.
7. **Brand everything "Brisk".**

## Stack (current versions — June 2026)
- OS: **Ubuntu 24.04 LTS**
- Cache: **Nginx stable 1.30.x** (official nginx.org packages)
- Language: **Go 1.26.x** — single static binary
- TLS: Let's Encrypt **ECDSA P‑256**, **TLS 1.3 + 1.2**, OCSP stapling, HSTS, session resumption
- Compression: **Brotli** (`ngx_brotli` dynamic module) + Gzip fallback
- Header branding: **`headers-more-nginx-module`** (to override `Server: Brisk`)
- Video: Nginx **slice module** (1 MB slices)
- Kernel: **TCP BBR** (`fq` + `bbr`)
- Phase 2+: Postgres + **TimescaleDB** (analytics), **Bunny DNS** (routing)

## Conventions
- Binary: `brisk-agent` → `/usr/local/bin/`
- Config: `/etc/brisk/agent.yaml`; certs: `/etc/brisk/tls/<domain>/`
- Cache: `/var/cache/brisk`
- Headers: `Server: Brisk`, `X-Brisk-Cache` (= `$upstream_cache_status`), `X-Brisk-Edge`, `X-Brisk-Request-Id`
- Code namespace: `brisk-agent/{config,nginx,tls,bootstrap,stats,purge,client,deploy}`

## Load steering #3 + #4 — LIVE (gated off by default)
**Built + rolled out 2026-06-14.** Control plane rebuilt+restarted (`load steer disabled` logged)
and the new agent rolled NY→FRA→BLR one edge at a time with a proven **byte-identical** nginx.conf
gate (per-edge sha256 BEFORE==AFTER). Both features ship **OFF**, so the live fleet is byte-identical
until enabled. All 3 edges serve both live zones (testjim/testmainak.cdn.a2zjav.com) 200/Server:Brisk.
**To enable:** `BRISK_LOAD_STEER_ENABLED=true` on the control plane (#3) / `edge_protect: true` in an
edge's agent.yaml (#4). Rollback: on-edge `brisk-agent.prev-ls` + `nginx.conf.bak-ls`. See
`docs/features/Brisk_Load_Steering.html`.
- **#3 Load-feedback loop (control plane):** `internal/dns/loadsteer.go` `LoadController` samples
  each edge's live pressure (`max(cpu%, bandwidth÷capacity)`) every ~45s, EWMA-smooths +
  deadbands it, and multiplies that edge's Smart-Record weight (the #2 knob) by a factor floored
  at `0.30` (load never fully drains an edge; drain/health own full removal). In-memory factor ⇒
  CP-restart resets to neutral, CP-down freezes weights in Bunny (Golden Rule #3). Gated by
  `BRISK_LOAD_STEER_ENABLED` (+ `_INTERVAL/_LOW/_HIGH/_MIN_FACTOR`); only steers online edges with
  a capacity set. Applied in the one `effectiveWeight()` both apex `Diff` + per-zone `DiffZone` use;
  surfaced in `GET /dns/routing` (`load_steer` + per-edge `load_factor`).
- **#4 Edge self-protection (agent):** agent-LOCAL `edge_protect` (agent.yaml top-level ⇒ survives
  every config poll, works with the CP down). Renders nginx per-IP `limit_conn` (503 on saturation)
  + optional `limit_req` (429) in every server block incl. the default_server. Per-IP keying keeps
  the health-checker's budget intact so `/healthz` stays answerable (existing probe still governs
  DNS). Off ⇒ byte-identical. Fields: `edge_protect`, `edge_max_conn_per_ip` (default 200),
  `edge_req_per_sec_per_ip` (0=off), `edge_req_burst`.

## Project status (Phase 4 — COMPLETE; Lua live on all 3 edges)
Phases 1–3 + **Phase 4 Steps 1–6** are **built, validated, and live**. Phase 4 added the
multi‑tenant, sellable‑CDN surface: host routing, **custom domains + per‑domain
auto‑TLS (SNI)**, **per‑zone origin shield**, **per‑zone WAF** (OWASP Coraza + CRS v4 +
nginx rate limiting), a **Lua programmable edge** (cache rules + header transforms),
and Step 6's finale —
- **Real logs pipeline:** structured JSON edge access log → agent tail/ship (bounded,
  drop‑oldest) → `request_logs` Timescale hypertable (**7‑day retention** + columnstore)
  → filterable, near‑real‑time, tenant‑scoped **Logs page**.
- **Real origin‑offload + analytics depth:** offload % (by request + bytes), status‑code
  breakdown, **latency p50/p95/p99**, top paths, top countries — from `request_logs`,
  wired into Analytics (the old "not collected" placeholders are gone).
- **GeoIP** (ngx_http_geoip2, gated): country in logs/analytics + **WAF country rules**.
- **Errors‑only rate limiting** (Lua: counts only 401/403 for login/OTP).
- **Origin lockdown** (Part 6): `origin_pull_secret` → secret header on origin requests
  so a reachable origin can reject non‑Brisk traffic. See `docs/Security_Audit_Phase4.md`.
- Backlog folded in: atomic `PUT /rules` + bulk reorder, `GET /zones/{id}/servers`,
  network‑aggregate `/stats`.

Everything new is **gated + off by default** (shield/WAF/Lua/GeoIP/lockdown per zone or
per edge), so the live fleet stays byte‑identical until deliberately enabled.

**⚑ LIVE‑SITE UPDATE (2026‑06‑13): `cdn.a2zjav.com` is RETIRED — it is no longer a live
zone.** The fleet now serves a different set of configured zones. **Historical paragraphs
below still mention `cdn.a2zjav.com` as the live site — that is past record, not current
truth.** Operationally, the golden rule is generic: **never drop whatever hostname(s) are
actually live/in‑rotation at the time; never leave a live hostname with zero in‑rotation
PoPs.** Before any rollout, confirm the currently‑serving hostnames from the control plane
and treat THOSE as the byte‑identical target. (The current live zone list is not hardcoded
here on purpose — verify it live.)

**Dashboard follow‑up (2026‑06‑12) — built + control plane on v18:**
- **Logs + Security pages are real** (the "SOON" badges were a stale Vite dev‑server cache
  on the Windows bind‑mount; a dashboard container restart serves fresh source).
- **Zone delete tears the zone down across all PoPs** (`DELETE /zones/{id}` → whole‑zone NATS
  purge to every serving edge + vhost drops on next config pull) with a **type‑the‑hostname
  guard** (HTTP 412 unless `?confirm=<cdn_hostname>`) for any zone serving on live edges —
  enforced server‑side so a stray browser DELETE can't nuke a live zone.
- **Per‑zone Cache Settings** (Bunny‑style: Smart Cache, edge/browser TTL, query‑sort,
  cache‑error, Vary‑by webp/device/country/cookie/header/querystring, strip‑cookies,
  large‑object slice, stale offline/updating) — migration 00018 + store + `PUT
  /zones/{id}/cache-settings` + agent `nginx/cache.go` render + dashboard **Cache** tab.
  **Defaults reproduce current behavior**; agent emits `cache_settings` only when non‑default
  (omitempty) → default zones (incl. cdn.a2zjav.com) stay byte‑identical. **The new agent
  binary still needs the gated NY→DE→BLR rollout** for edge enforcement (query‑sort/whitelist
  need the Lua edge). Live zone 6 was deleted again this session (browser) and restored from
  backup; the new guard now prevents recurrence.

**Part 1 — the LIVE Lua‑module rollout is DONE (2026‑06‑11).** Rolled onto all 3 edges
NY→DE→BLR one at a time (drain→deploy final agent→`EnsureLua` built the module +
`EnsureGeoIP` built geoip2 gated‑off→`nginx -t`→reload→verify→undrain). `cdn.a2zjav.com`
stayed **byte‑identical** at every step (identical asset sha256, 200, `Server: Brisk`,
cache HIT, favicon 302 HIT, TLS 1.3, `/healthz` ok, WAF‑off) and the live site was served
by ≥2 edges throughout. **Enforcement stays opt‑in:** every edge renders only the
http‑block gated Lua (`load_module` + `init_by_lua` + `lua_shared_dict brisk_rl`) with
**zero per‑zone hooks** (`zones_data.lua` = empty `return {}`); custom cache rules / header
transforms / errors‑only limits are now *enforceable* on prod but enabled only when a zone
defines them. Each edge keeps `/usr/local/bin/brisk-agent.prev` for one‑command rollback;
rollout tooling: `tunnels/{deploy-lua,verify-lua}.sh`. Security/perf audit:
`docs/Security_Audit_Phase4.md`. Golden rule still holds: **never drop whatever
hostname(s) are currently live**; never leave a live hostname with zero in‑rotation PoPs.
(`cdn.a2zjav.com` was the live site when this was written; it is **retired** as of
2026‑06‑13 — see the LIVE‑SITE UPDATE near the top of this section.)

The Phase‑1 section below is the original build spec, kept for reference.

## Current phase: PHASE 1 — one edge node, proven
**IN:** `brisk-agent` skeleton; Nginx caching a real site; disk+RAM cache; slice/range for HLS; request coalescing (`proxy_cache_lock`); branded headers; Brotli+Gzip; TLS 1.3/ECDSA (self‑signed local, Let's Encrypt on VPS); TCP BBR; bootstrap + systemd; local end‑to‑end test.
**OUT (Phase 2+):** dashboard, control plane, multi‑server, Bunny DNS, analytics DB, network‑wide purge, Lua. **But** add stub interfaces now: `config.Source`, `purge.Purger`, `stats.Reporter`, `client.ControlPlane`.

### Build order
1. Repo skeleton + `config.go` (load `agent.yaml`)
2. `nginx.go` + templates → HTTP caching → **test MISS then HIT**
3. Video/HLS + coalescing + branded headers → **test 206 + m3u8 BYPASS**
4. `tls.go` self‑signed → **HTTPS / TLS 1.3**
5. Edge tuning + Brotli → **test `Content-Encoding: br`**
6. `bootstrap.go` + systemd → **survives reboot**
7. End‑to‑end local test; then one VPS with real Let's Encrypt + BBR

Work one step at a time; each test must pass before the next.

## Local dev (Windows)
Docker Desktop (installed) or WSL2 Ubuntu as the test "server". Locally testable: caching, headers, slice/HLS, Brotli, agent reload, bootstrap, **self‑signed TLS**. Needs a real VPS: **real Let's Encrypt cert**, public access, real latency, and **BBR's real effect** (host kernel governs BBR inside Docker).

## HLS / cache rules
- `.m3u8` playlists: **never cache** (always fresh) → `X-Brisk-Cache: BYPASS`.
- `.ts`/`.m4s`/`.mp4`: slice 1m, cache, `proxy_cache_key` includes `$slice_range`, validate `200 206`.
- Open‑source Nginx has **no built‑in purge directive** (that's Plus) — the agent purges by deleting cache files (or via `ngx_cache_purge`).

## Don't forget
- Origin IP must never be exposed in production (mTLS / secret pull header) — security phase, but don't design against it.
- ECDSA over RSA always (10k+ vs ~2–3k handshakes/sec).
- 1 Gbps ≈ 125 MB/s; network is the bottleneck; 10 Gbps + NVMe at scale.

## Runbooks (in-repo, portable — travel with the project to any laptop)
- **Changing the CDN base domain** (e.g. `a2zjav.com` → new), keeping Bunny geo-DNS:
  `docs/features/Brisk_CDN_Domain_Migration_Runbook.md` (plain-text, read anywhere) +
  `…_Runbook.html` (browser infographics). It's a CONFIG+DNS+CERT migration,
  no core code change — the base domain is parameterized (`BUNNY_DNS_ZONE`, `BRISK_CDN_RECORD`,
  `BRISK_TLS_DOMAINS`, dashboard `VITE_CDN_BASE_DOMAIN`, per-zone `cdn_hostname`). Process =
  delegate new zone to Bunny → issue new wildcard cert (DNS-01, fan to edges) → point reconciler
  at new zone → dual-host existing zones → tenants re-CNAME → verify → retire old. Dual-run =
  zero downtime; never drop a live hostname with zero PoPs. Bunny Smart-DNS reused as-is.
- **Rolling a `brisk-agent` change to every PoP** (how edge deploys work):
  `docs/features/Brisk_Agent_Rollout_Process.html` (diagram + prose). One static binary
  (`golang:1.26`, CGO off) → rolled **one edge at a time NY→DE→BLR** via `tunnels/deploy-*.sh`
  behind a **byte-identical gate** (per-edge `sha256(nginx.conf)` BEFORE==AFTER, else halt +
  roll back from `.prev`/`.bak`). nginx never stops (serves through the ~6s agent swap);
  `nginx -t` before any reload. Always verify the **effect** (e.g. DB field populated), not just
  the deploy exit code. Tooling also: `tunnels/check-tech.sh` (running-exe vs on-disk sha).
