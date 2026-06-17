# brisk-control (Phase 2, Step 1)

The Brisk **control plane**: a Go REST API backed by Postgres + TimescaleDB, run in
Docker. Step 1 is the brain + database — server/zone/rule CRUD over `curl`. No agent,
no dashboard, no auth yet (those are later Phase-2 steps).

## Stack
chi v5 (router) · pgx v5 + pgxpool (DB) · goose (embedded migrations) · log/slog
(structured logs) · validator/v10 (request validation) · TimescaleDB
`2.24.0-pg17` (the `stats` table is a hypertable).

> Note: queries are hand-written parameterized `pgx` SQL (isolated in `internal/store`),
> not sqlc. Same intent — plain SQL, no ORM — chosen to stay codegen-free and to keep
> clean API JSON for nullable columns. sqlc can be dropped in later behind the same
> store interface.

## Run
```bash
cp .env.example .env          # set DB_PASSWORD
docker compose up --build -d
curl -s localhost:8080/health
```
Migrations run automatically on startup (idempotent). Data persists in the
`brisk_db` volume across `docker compose restart`.

## API (`/api/v1`, no auth yet)
| Method | Path | Notes |
|---|---|---|
| GET | `/health` | `{status, db, time}` |
| GET/POST | `/api/v1/servers` | list / create |
| GET/DELETE | `/api/v1/servers/{id}` | one / delete |
| GET/POST | `/api/v1/zones` | list / create |
| GET/PUT/DELETE | `/api/v1/zones/{id}` | one (+rules) / update / delete |
| GET/POST | `/api/v1/zones/{id}/rules` | list / add |
| DELETE | `/api/v1/zones/{id}/rules/{rid}` | delete |
| GET | `/api/v1/agent/config` | **501** stub (Step 3) |

Every zone update and rule add/delete **bumps `config_version`** (Step 3 agents
use it to detect changes).

## Project layout
```
cmd/brisk-control/main.go      entrypoint: config -> migrate -> pool -> serve
internal/config                env -> typed config
internal/migrate               embedded goose migrations (internal/migrate/migrations)
internal/store                 pgxpool + servers/zones/rules queries
internal/api                   chi router, middleware (slog), handlers, validation
Dockerfile, docker-compose.yml, .env.example
```

## Forward hooks (ready, not built)
- `agent_tokens` table + no-op `tokenAuth` middleware → Step 2 (auth).
- `GET /api/v1/agent/config` 501 stub → Step 3 (agent pull-config).
- `stats` hypertable → Step 4 (stats ingest).
- `accounts.role` + `account_id` scoping → future customer portal.

## Phase 3 — DNS, Smart Routing & Fast Failover

> **Operations:** running multi-PoP, adding a region, draining for maintenance,
> reading the rotation/health badges, the **live cutover procedure**, and the
> **one-step rollback** all live in **`../docs/Brisk_Phase3_Runbook.md`**.

`brisk-control` owns the `cdn.<zone>` record set on Bunny DNS:

- **Step 2 — auto-registration reconciler:** DNS follows the `servers` table
  (online = enabled A record, off/drained = disabled-but-kept, deleted = removed),
  short TTL, idempotent, only touches `brisk:`-tagged records.
- **Step 3 — Smart-Record routing:** each edge's A record is geo (lat/long) or
  latency (Bunny region) routed, driven by `servers.region` (`BRISK_DNS_ROUTING_MODE`,
  per-server `routing_override`/`routing_weight`). Bunny's update is a partial
  merge, so the reconciler writes full record state (zeros clear stale fields).
- **Step 4 — self-driven health checks + fast failover** (`internal/health`):
  probes every online edge (`/healthz`) on a short interval and flips a dead
  edge's record `Disabled=true` **immediately** — not waiting on Bunny's ~30s
  monitor.

### Failover math & honest caveats (Step 4)

```
detection ≈ check_interval × fail_threshold      (10s × 2 ≈ 20s)
failover  ≈ detection + TTL                       (~20s + ~15s ≈ ~30-35s typical)
```

- **~30s is a TYPICAL target, NOT a guarantee.** End-to-end failover for a given
  user is `detection + their resolver's effective TTL`. Some ISPs/resolvers cache
  past the record TTL, so a subset of users take longer. We can't fix that from
  the authoritative side.
- **Asymmetric thresholds (flap protection):** fail fast (2 consecutive fails →
  unhealthy), recover careful (3 consecutive successes → re-enabled). A single
  blip never pulls an edge; rapid up/down never thrashes DNS.
- **off ≠ delete:** an unhealthy edge's record is **disabled**, never deleted; on
  recovery the *same* record is re-enabled (no churn, no fight with the lock).
- **Write-on-change only:** Bunny is written only when health state *changes*, not
  every probe; probes are staggered (no thundering herd) and rate-limit-safe.
- **All-down blackhole guard:** if every online edge is unhealthy (e.g. the
  checker itself is network-partitioned), records are left **enabled** rather than
  mass-disabled — matching Bunny's own "all offline → return all" behavior. We
  never black-hole the whole CDN on a checker-side blip.
- **Restart resilience:** last-known health is persisted (`servers.health_status`)
  and seeded on startup, so restarting `brisk-control` neither blackholes nor
  thrashes the zone.
- **In-flight viewers (mid-video when a PoP dies)** are recovered by the **HLS
  player's segment retry + re-resolution** (hls.js/native), *not* by DNS. DNS
  failover protects new requests and eventually moves everyone; pair a short TTL
  with a retry-capable player.
- **Anycast** (own IP space + BGP) is the only path to *guaranteed* sub-second
  failover — a future Phase-4+ consideration, explicitly out of scope here.

### Health config

Network-wide (env, see `.env.example`): `BRISK_HEALTH_ENABLED`,
`BRISK_HEALTH_INTERVAL` (10s), `BRISK_HEALTH_TIMEOUT` (3s),
`BRISK_HEALTH_FAIL_THRESHOLD` (2), `BRISK_HEALTH_RISE_THRESHOLD` (3),
`BRISK_HEALTH_PATH` (`/healthz`), `BRISK_HEALTH_SCHEME`, `BRISK_HEALTH_PORT`, and
`BRISK_DNS_TTL` (~15s; clamp 10–120). Per-PoP overrides live on `servers`
(`health_enabled`, `health_interval_seconds`, `health_fail_threshold`,
`health_rise_threshold`) via `POST /api/v1/servers/{id}/health`.

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/health/status` | per-edge health + in-rotation + last probe |
| GET | `/api/v1/health/config` | effective per-PoP health config |
| POST | `/api/v1/servers/{id}/health` | set per-server health overrides |

The external **health probe** is the routing truth and is distinct from the
Step-2 **heartbeat** (`last_seen` = "agent talked to control plane"; probe =
"edge serves users from the outside"). An edge that heartbeats but fails external
probes is still pulled.
