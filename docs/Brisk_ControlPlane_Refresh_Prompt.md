# Brisk CDN — Control-Plane Refresh (light up Phase-4 features on the live fleet)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Control_Plane_Ops.md` + all Phase‑2/3/3.7/4 prompts. **Phases 1–4 are complete and on the live edges** (NY, DE, BLR): multi‑tenant host routing, custom domains + auto‑TLS, origin shield, WAF (Coraza), Lua programmable edge (cache rules + header transforms), structured logs, GeoIP, origin lockdown. **But the live `brisk-control` (on the laptop) is still running an OLD build at migration v13**, so the newer features (request logs, analytics depth, cache‑rule/WAF/shield definition, origin‑offload counters) **can't be used from the live panel yet** — the live brain is behind the live body. The newer migrations (≈ 00014–00017) auto‑apply on a control‑plane restart.

> **Read `CLAUDE.md` and `docs/Control_Plane_Ops.md` first.** This is a **small, low‑risk operational refresh, not a feature build.** Goal: safely rebuild + restart the live (laptop) control plane to the current code so the Phase‑4 features light up on the live fleet — **without dropping the live sites.** The control plane stays on the laptop (no VPS move). Verify, keep rollback ready.

## Goal (one line)
Rebuild + restart the live laptop `brisk-control` so migrations 00014–00017 apply and the **Logs/Analytics/cache‑rules/WAF/shield/origin‑offload** features become usable on the live fleet — edges keep serving `cdn.a2zjav.com` throughout.

## Why this is safe (the key fact)
The **edges serve independently from last‑known‑good config + local cache** (proven repeatedly). So while `brisk-control` rebuilds/restarts, **the live sites keep serving** — the only thing briefly unavailable is the *management/ingest* layer (dashboard writes, config pushes, stats/log ingest), which resumes on reconnect. No edge draining is needed (that pattern is only for edge/agent changes; this is a control‑plane‑only change).

## Steps

### Part 1 — Pre‑flight + backup
- **Back up the database first** (`pg_dump` of the TimescaleDB/Postgres volume) so any migration issue is recoverable. Confirm the backup file exists + is non‑empty before proceeding.
- Note the **current migration version** (should be v13) and the current running image/commit, so rollback is unambiguous.
- Confirm the 3 edges are currently online + serving (baseline): `cdn.a2zjav.com` → 200, `Server: Brisk`, cache HIT, TLS ok, `/healthz` ok.

### Part 2 — Rebuild + restart (migrations auto‑apply)
- Rebuild the `brisk-control` image/binary from current code and restart it (its compose stack: control + TimescaleDB + NATS). **TimescaleDB data persists** (named volume) — don't wipe it.
- On startup, **goose runs migrations 00014 → 00017** (origin‑shield columns, WAF tables, header‑transforms, `request_logs` hypertable + retention, etc.). Watch the logs: every migration applies cleanly, no errors; final version is the latest (≈ v17).
- If a migration **fails**: stop, restore the backup, report — do not leave the DB half‑migrated.
- Re‑establish the autossh tunnels if the control‑plane container IP changed (the known gotcha from Phase‑3.7 Step‑1/2 — documented; restart the tunnels so agents reconnect).

### Part 3 — Verify the live fleet reconnects + features light up
- **Agents reconnect:** all 3 edges resume heartbeating, pulling config, shipping stats + **logs** (the `POST /agent/logs` that was 404ing should now 200 and populate `request_logs`).
- **Logs page:** real requests appear, filterable, with cache status + timing (tenant‑scoped). 
- **Analytics depth:** status‑code breakdown, top paths, latency p50/p95/p99, geo populate (replacing the old placeholders).
- **Cache rules / header transforms:** can now be **saved** for a zone in the dashboard (the v13 brain couldn't store them); a saved rule bumps `config_version` → edges pull → enforced (opt‑in; don't enable on a live zone unless intended).
- **WAF / origin shield:** can be configured per zone (still opt‑in/off by default; don't flip live zones on without intent).
- **Origin‑offload metric:** shows the real number once shield counters flow.
- **Live‑site safety:** confirm `cdn.a2zjav.com` stayed up the whole time (it should never have dropped); edges online + in‑rotation.

### Part 4 — Confirm + document
- Confirm final migration version, all 3 edges healthy + in DNS rotation, dashboard fully functional (logs/analytics/rules/WAF visible + usable).
- Update `docs/Control_Plane_Ops.md`: the live control plane is now at the current version; note the backup taken + the tunnel‑restart‑after‑rebuild step; confirm the backlog from Phase‑4 Step‑6 is now actually live.
- Note clearly: features are **available** now but **opt‑in per zone** — enabling WAF/shield/cache‑rules/origin‑lockdown on a real zone is a deliberate, separate action (with the origin‑lockdown caveat: only enable the `origin_pull_secret` if the origin actually checks it, or it'll reject legit traffic).

## Acceptance (definition of done)
```bash
# 1) DB backed up (pg_dump exists, non-empty) BEFORE restart
# 2) brisk-control rebuilt + restarted; migrations 00014->00017 applied cleanly; version = latest (~v17); TimescaleDB data intact
# 3) Tunnels re-established; all 3 edges reconnect -> heartbeat + config-pull + stats + LOGS now 200 (request_logs populating)
# 4) Logs page shows real live requests (filterable, cache status + timing), tenant-scoped
# 5) Analytics: status codes + top paths + latency p50/p95/p99 + geo populate (placeholders gone)
# 6) A cache rule can be SAVED on a zone -> config_version bumps -> edge pulls + enforces (test on a throwaway zone, not a live one)
# 7) WAF + origin shield configurable per zone (off by default; not flipped on for live zones)
# 8) Origin-offload metric shows a real number
# 9) Live-site safety: cdn.a2zjav.com served 200 throughout; all 3 edges online + in rotation after
# 10) Rollback ready/tested: the pre-restart backup can restore v13 if needed; docs updated
```
**Done when:** the live laptop control plane runs the current code at the latest migration, all 3 edges reconnected and are shipping logs/stats, the dashboard's **Logs / Analytics / cache‑rules / WAF / shield / origin‑offload** are live and usable, and `cdn.a2zjav.com` served continuously throughout — with a DB backup + rollback in hand. The full Brisk feature set is now usable on the live fleet for your own sites.

## Pitfalls (do not skip)
1. **Back up the DB before restarting** — migrations are forward; have a restore point.
2. **Don't wipe the TimescaleDB volume** — persist data; this is a code/migration refresh, not a fresh DB.
3. **Restart the tunnels if the container IP changed** — the known gotcha; agents must reconnect.
4. **Edges keep serving — no draining needed** — this is control‑plane‑only; don't touch the edges.
5. **Features come up opt‑in/off** — don't auto‑enable WAF/shield/cache‑rules/origin‑lockdown on live zones; enabling is a separate deliberate step (mind the origin_pull_secret caveat).
6. **If any migration fails → restore the backup**, don't leave the DB half‑applied.
7. **Verify on a throwaway zone** when testing cache‑rule save/enforce, so a live zone isn't changed unintentionally.

## After this — you're testing the full CDN on your own sites
Once green, the whole Brisk feature set is usable from the live panel. Natural next moves (your pace, not new builds): **onboard your own sites as zones** (origin → CDN hostname → optional custom domain → cache rules), watch logs/analytics, and tune. Moving the control plane off the laptop to a VPS stays an **optional later** step (you've chosen to keep testing locally for now).
