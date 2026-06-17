# Brisk CDN — Phase 2 / Step 1 Build Prompt (Control Plane Skeleton + Database)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑1 prompts. **Phase 1 is complete** — a live single‑node edge (`brisk-agent` + Nginx) is serving HTTPS/TLS 1.3 with caching, HLS, Brotli, BBR, systemd, auto‑renew on a real VPS. The agent already has stub interfaces: `config.Source`, `purge.Purger`, `stats.Reporter`, `client.ControlPlane`.

> **Read `CLAUDE.md` and `docs/Brisk_Phase1_Build_Spec.md` first.** This is **Step 1 of 7 in Phase 2**. Build only what's in scope below, commit in small pieces, and pass the acceptance tests before stopping. Do **not** start Step 2 (auth/add‑server) until Step 1 passes and you've shown me the results.

## Phase 2 at a glance (so you understand where Step 1 fits)
Phase 2 turns one hand‑configured edge into a **fleet managed from a dashboard**. The 7 steps: **(1) control plane skeleton + DB** ← *this step* · (2) auth + add‑server API · (3) agent pull‑config · (4) stats shipping · (5) instant purge · (6) React dashboard · (7) end‑to‑end + deploy. New component naming: **`brisk-control`** (Go API + DB, runs in Docker) and later **`brisk-dashboard`** (React, Docker).

## Step 1 goal (one line)
Stand up **`brisk-control`** — a Go REST API in Docker, backed by **Postgres + TimescaleDB**, with the full core schema (servers, zones, cache rules, stats hypertable, accounts, agent tokens) and basic CRUD for servers + zones, all testable with `curl`. **No agent, no dashboard, no auth yet** — just the brain and its database.

---

## Confirmed stack (current, verified June 2026 — use these)
| Concern | Choice | Notes |
|---|---|---|
| Language | **Go 1.26.x** | same as the agent; single static binary |
| Router | **chi v5** (`github.com/go-chi/chi/v5`) | lightweight, 100% net/http‑compatible, radix‑tree routing, composable middleware (logging, CORS, recoverer, timeout). Preferred over a heavy framework; richer than bare ServeMux. |
| DB driver | **pgx v5** (`github.com/jackc/pgx/v5` + `pgxpool`) | high‑performance pure‑Go Postgres driver + connection pool |
| Queries | **sqlc** | generates type‑safe Go from plain SQL — compile‑time safety, no ORM magic. Preferred over GORM for control + performance. |
| Migrations | **goose** (`github.com/pressly/goose/v3`) | simple SQL migrations with `-- +goose Up/Down`; embed and run on startup |
| Database | **TimescaleDB** (Postgres extension) | Docker image **`timescale/timescaledb:2.24.0-pg17`** — pin the exact tag, do NOT use `latest` (it can jump Postgres major versions). Stats table becomes a **hypertable**. |
| Logging | **`log/slog`** (stdlib) | structured logging; no third‑party logger needed |
| Config | env vars → small typed config struct | 12‑factor; no secrets in code |
| Validation | `github.com/go-playground/validator/v10` | request body validation |

> **Why this stack:** chi + pgx + sqlc + goose + slog is the modern, idiomatic Go API stack in 2026 — minimal dependencies, fast, type‑safe, and close to the standard library, so it stays maintainable as Brisk scales. It's the same shape the big infra companies use (Go control plane + Postgres).

---

## Project structure
```
brisk-control/
├── go.mod
├── cmd/brisk-control/main.go        # entrypoint: load config, connect DB, run migrations, start server
├── internal/
│   ├── config/config.go             # env → typed config
│   ├── api/
│   │   ├── router.go                # chi router + middleware stack
│   │   ├── servers.go               # /servers handlers
│   │   ├── zones.go                 # /zones + nested /rules handlers
│   │   ├── health.go                # /health
│   │   └── respond.go               # JSON helpers (writeJSON, writeError)
│   ├── store/
│   │   ├── store.go                 # pgxpool setup
│   │   ├── queries/                 # *.sql files for sqlc
│   │   └── (sqlc-generated *.go)
│   └── migrate/                     # embedded goose migrations runner
├── migrations/                      # goose .sql files (also embedded)
├── sqlc.yaml
├── Dockerfile
├── docker-compose.yml               # brisk-control + timescaledb
├── .env.example
└── README.md
```

---

## Database schema (full — create all tables now, even ones used in later steps)
Put this in `migrations/00001_init.sql` (goose format). It includes the role‑aware `accounts` table so the **future customer portal** plugs in without a migration rewrite.
```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- accounts: admin now; customers later (multi-tenant ready)
CREATE TABLE accounts (
  id         BIGSERIAL PRIMARY KEY,
  name       TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'admin',          -- admin | customer
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- edge servers (PoPs)
CREATE TABLE servers (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT NOT NULL,
  region        TEXT NOT NULL,                        -- e.g. "IN-DEL", "US-IL"
  ip            INET NOT NULL,
  hostname      TEXT,
  edge_id       TEXT UNIQUE NOT NULL,                 -- shows in X-Brisk-Edge, e.g. DEL1-07
  capacity_mbps INTEGER,                              -- 1000 or 10000
  status        TEXT NOT NULL DEFAULT 'pending',      -- pending|provisioning|online|offline|disabled
  last_seen     TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- zones (sites being accelerated) — this is what agents will pull in Step 3
CREATE TABLE zones (
  id             BIGSERIAL PRIMARY KEY,
  account_id     BIGINT NOT NULL REFERENCES accounts(id) DEFAULT 1,
  name           TEXT NOT NULL,
  cdn_hostname   TEXT UNIQUE NOT NULL,                -- e.g. abcd.brisk-cdn.net
  custom_domain  TEXT,                                -- CNAME custom hostname
  origin_url     TEXT NOT NULL,
  tls_mode       TEXT NOT NULL DEFAULT 'letsencrypt', -- selfsigned|mkcert|letsencrypt
  video          BOOLEAN NOT NULL DEFAULT false,
  profile        TEXT NOT NULL DEFAULT 'vod',         -- vod|live
  playlist_ttl   TEXT NOT NULL DEFAULT '2s',
  segment_ttl    TEXT NOT NULL DEFAULT '12h',
  cors_origin    TEXT NOT NULL DEFAULT '*',
  brotli_level   INTEGER NOT NULL DEFAULT 5,
  status         TEXT NOT NULL DEFAULT 'active',
  config_version BIGINT NOT NULL DEFAULT 1,           -- bumped on change; agents compare to know when to re-pull
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- edge/cache rules per zone (Bunny "Edge Rules" equivalent)
CREATE TABLE cache_rules (
  id           BIGSERIAL PRIMARY KEY,
  zone_id      BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  priority     INTEGER NOT NULL DEFAULT 0,
  match_type   TEXT NOT NULL,                         -- path_prefix|extension|regex
  match_value  TEXT NOT NULL,
  action       TEXT NOT NULL,                         -- override_cache_ttl|bypass_cache|force_download|redirect
  action_value TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- which servers serve which zones (Phase 2 = all; modeled for multi-PoP in Phase 3)
CREATE TABLE server_zones (
  server_id BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  zone_id   BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  PRIMARY KEY (server_id, zone_id)
);

-- agent auth tokens (fleshed out in Step 2; table now). Store a HASH, never plaintext.
CREATE TABLE agent_tokens (
  id         BIGSERIAL PRIMARY KEY,
  server_id  BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ
);

-- time-series stats (shipped by agents in Step 4) -> TimescaleDB hypertable
CREATE TABLE stats (
  time          TIMESTAMPTZ NOT NULL,
  server_id     BIGINT NOT NULL,
  zone_id       BIGINT,
  requests      BIGINT DEFAULT 0,
  hits          BIGINT DEFAULT 0,
  misses        BIGINT DEFAULT 0,
  bytes_sent    BIGINT DEFAULT 0,
  bandwidth_bps BIGINT DEFAULT 0,
  cpu_pct       DOUBLE PRECISION,
  ram_pct       DOUBLE PRECISION,
  disk_pct      DOUBLE PRECISION,
  hit_ratio     DOUBLE PRECISION
);
SELECT create_hypertable('stats', 'time', chunk_time_interval => INTERVAL '1 day');

-- seed the admin account
INSERT INTO accounts (id, name, role) VALUES (1, 'admin', 'admin');

-- +goose Down
DROP TABLE IF EXISTS stats;
DROP TABLE IF EXISTS agent_tokens;
DROP TABLE IF EXISTS server_zones;
DROP TABLE IF EXISTS cache_rules;
DROP TABLE IF EXISTS zones;
DROP TABLE IF EXISTS servers;
DROP TABLE IF EXISTS accounts;
```
> Note: `create_hypertable` must run **after** `CREATE EXTENSION timescaledb`. Later steps will add a TimescaleDB **retention policy** + **compression** on `stats` (not now). Add useful indexes (`servers.status`, `zones.account_id`, `cache_rules.zone_id`, `stats (server_id, time DESC)`).

---

## TimescaleDB + control plane in Docker (`docker-compose.yml`)
```yaml
services:
  timescaledb:
    image: timescale/timescaledb:2.24.0-pg17   # PIN the tag, never :latest
    environment:
      POSTGRES_USER: brisk
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_DB: brisk
    volumes:
      - brisk_db:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U brisk -d brisk"]
      interval: 5s
      timeout: 5s
      retries: 10
    ports:
      - "127.0.0.1:5432:5432"   # bind to localhost only

  brisk-control:
    build: .
    environment:
      DATABASE_URL: "postgres://brisk:${DB_PASSWORD}@timescaledb:5432/brisk?sslmode=disable"
      LISTEN_ADDR: ":8080"
    depends_on:
      timescaledb:
        condition: service_healthy   # wait until DB is actually ready
    ports:
      - "8080:8080"

volumes:
  brisk_db:
```
- The `brisk-control` container runs **goose migrations on startup** (before serving), then starts the API.
- `Dockerfile`: multi‑stage (build static Go binary → small `gcr.io/distroless/static` or `alpine` final image).
- `.env.example` documents `DB_PASSWORD` etc. Never commit a real `.env`.

---

## API (Step 1 endpoints — JSON, versioned under `/api/v1`)
No auth yet (Step 2 adds it). Apply chi middleware: `RequestID`, `RealIP`, `Logger` (wired to slog), `Recoverer`, `Timeout(15s)`, a permissive CORS for dev, and a JSON content‑type setter.
```
GET    /health                         -> {status, db: ok|down, time}
GET    /api/v1/servers                 -> list servers
POST   /api/v1/servers                 -> create (name, region, ip, edge_id, capacity_mbps)
GET    /api/v1/servers/{id}            -> one server
DELETE /api/v1/servers/{id}            -> delete
GET    /api/v1/zones                   -> list zones
POST   /api/v1/zones                   -> create (name, cdn_hostname, origin_url, ...)
GET    /api/v1/zones/{id}              -> one zone (+ its cache_rules)
PUT    /api/v1/zones/{id}              -> update (bump config_version + updated_at)
DELETE /api/v1/zones/{id}              -> delete
GET    /api/v1/zones/{id}/rules        -> list cache rules
POST   /api/v1/zones/{id}/rules        -> add a cache rule
DELETE /api/v1/zones/{id}/rules/{rid}  -> delete a rule
```
- **Important:** every `PUT`/rule change on a zone must **bump `config_version`** and `updated_at` — Step 3's agent pull‑config relies on this to detect changes.
- Validate request bodies (validator). Return proper status codes (201 create, 404 not found, 400 bad input, 409 conflict on unique fields). Consistent JSON error shape `{error: "..."}`.

---

## Config (env)
`DATABASE_URL`, `LISTEN_ADDR` (default `:8080`), `LOG_LEVEL` (default info), `ENV` (dev|prod). Load into a typed struct; fail fast if `DATABASE_URL` missing.

---

## Acceptance tests (Step 1 definition of done)
```bash
docker compose up --build -d
# DB + migrations ran, API is up:
curl -s localhost:8080/health                      # {"status":"ok","db":"ok",...}

# Create + read a server
curl -s -X POST localhost:8080/api/v1/servers -H 'Content-Type: application/json' \
  -d '{"name":"del-edge-1","region":"IN-DEL","ip":"139.59.78.21","edge_id":"DEL1-01","capacity_mbps":1000}'
curl -s localhost:8080/api/v1/servers              # array with the server

# Create + read a zone
curl -s -X POST localhost:8080/api/v1/zones -H 'Content-Type: application/json' \
  -d '{"name":"mainak-site","cdn_hostname":"abcd.brisk-cdn.net","origin_url":"http://127.0.0.1:8000","video":true,"profile":"vod"}'
curl -s localhost:8080/api/v1/zones/1              # zone with config_version=1, empty rules

# Update zone -> config_version bumps to 2
curl -s -X PUT localhost:8080/api/v1/zones/1 -H 'Content-Type: application/json' -d '{"segment_ttl":"24h"}'
curl -s localhost:8080/api/v1/zones/1 | grep config_version    # 2

# Add a cache rule
curl -s -X POST localhost:8080/api/v1/zones/1/rules -H 'Content-Type: application/json' \
  -d '{"match_type":"extension","match_value":"m3u8","action":"override_cache_ttl","action_value":"2s"}'

# Verify the stats hypertable exists
docker compose exec timescaledb psql -U brisk -d brisk -c "SELECT hypertable_name FROM timescaledb_information.hypertables;"   # stats

# Validation works
curl -s -X POST localhost:8080/api/v1/zones -H 'Content-Type: application/json' -d '{}'   # 400 with JSON error
```
**Done when:** `docker compose up` brings up TimescaleDB + `brisk-control`, migrations create all tables and the `stats` **hypertable**, server/zone/rule CRUD works over `curl`, zone updates bump `config_version`, validation returns clean 400s, and the API survives a `docker compose restart` (data persists in the volume).

---

## Forward hooks (leave these ready, don't build yet)
- **Auth (Step 2):** `agent_tokens` table exists; add a token‑auth middleware skeleton (no‑op now) so Step 2 just fills it in.
- **Agent pull (Step 3):** add a `GET /api/v1/agent/config` endpoint **stub** that will (in Step 3) return the zone set + `config_version` for the calling server. Stub returns `501 Not Implemented` now.
- **Stats (Step 4):** the `stats` hypertable is ready; the ingest endpoint comes in Step 4.
- **Role‑aware:** `accounts.role` exists so the customer portal (future) filters by `account_id` — keep all zone queries `account_id`‑scopable even though everything is admin/account 1 now.

## Pitfalls (do not skip)
1. **Pin the TimescaleDB image tag** (`2.24.0-pg17`) — `:latest` can silently jump Postgres major versions and break the volume.
2. **`create_hypertable` after `CREATE EXTENSION timescaledb`**, and the `stats` table must include the partitioning column (`time`) — hypertables can't have a separate single‑column PK that excludes `time`.
3. **Wait for DB health** before the API connects (`depends_on: condition: service_healthy`) or first‑boot migrations race the DB.
4. **Run migrations on startup** idempotently (goose tracks applied versions) — safe across restarts.
5. **Store token hashes, never plaintext** in `agent_tokens` (Step 2 uses it).
6. **Bump `config_version`** on every zone/rule change — Step 3 depends on it.
7. **No secrets in the repo** — `.env` git‑ignored; `.env.example` only.
8. Bind Postgres to `127.0.0.1` (not `0.0.0.0`) — it should never be publicly reachable.

## Next — Step 2 (do NOT start)
Auth (per‑agent API tokens, mTLS‑ready design) + the **"add server"** flow: generate a token when a server is registered, and the endpoints/handshake the agent uses to authenticate. Wait for the user's go‑ahead and a Step 2 prompt.
